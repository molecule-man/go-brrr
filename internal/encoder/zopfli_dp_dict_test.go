package encoder

import (
	"math/bits"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

type dpDictInput struct {
	name string
	data []byte
}

func dpDictCorpus(tb testing.TB, limit int) []dpDictInput {
	tb.Helper()
	paths := []string{
		"../../testdata/github_events_2k.json",
		"../../testdata/github_events_5k.json",
		"../../testdata/github_events_8k.json",
		"../../testdata/gh_172KB.html",
		"../../testdata/reactcore_187KB.js",
		"../../brotli-ref/tests/testdata/plrabn12.txt",
		"../../brotli-ref/tests/testdata/lcet10.txt",
		"../../brotli-ref/tests/testdata/mapsdatazrh",
	}
	inputs := make([]dpDictInput, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			tb.Fatal(err)
		}
		inputs = append(inputs, dpDictInput{filepath.Base(path), data[:min(len(data), limit)]})
	}
	return inputs
}

func dpDictRing(data []byte) ([]byte, uint) {
	rb := make([]byte, 1<<bits.Len(uint(len(data))))
	copy(rb, data)
	return rb, uint(len(rb) - 1)
}

func dpDictCollect(tb testing.TB, rb []byte, mask, numBytes uint, lgwin int, skipDict bool) *q10Bufs {
	tb.Helper()
	const quality = 10
	h := &h10{lgwin: lgwin, quality: quality, skipDict: skipDict}
	h.reset(true, numBytes, nil)
	storeEnd := uint(0)
	if numBytes >= h10MaxTreeCompLength {
		storeEnd = numBytes - h10MaxTreeCompLength + 1
	}
	bufs := &q10Bufs{
		hqNumMatchesArr: make([]uint32, numBytes),
		hqMatches:       make([]backwardMatch, hqMatchesPerByte*numBytes),
	}
	bufs.hqCollector = hqCollector{
		bufs: bufs, hasher: h, ringbuffer: rb, numBytes: numBytes, ringBufferMask: mask,
		maxBackwardLimit: uint(1)<<lgwin - core.WindowGap, storeEnd: storeEnd, quality: quality,
	}
	bufs.hqCollector.collect()
	return bufs
}

func dpDictNodes(rb []byte, mask, numBytes uint, model *zopfliCostModel) []zopfliNode {
	nodes := make([]zopfliNode, numBytes+1)
	initZopfliNodes(nodes)
	model.init(64, numBytes)
	model.setFromLiteralCosts(0, rb, mask)
	return nodes
}

// dictHandoffsAt records handoff positions. The first transfers the dictionary search to the DP.
func dictHandoffsAt(at ...uint) *dictOwnership {
	d := new(dictOwnership)
	for k, pos := range at {
		d.at[k] = uint64(pos)
	}
	d.count.Store(uint32(len(at)))
	return d
}

// handedOffMatches includes dictionary matches only where the collector owns the search.
func handedOffMatches(withDict, lzOnly *q10Bufs, n uint, at []uint) ([]uint32, []backwardMatch) {
	num := make([]uint32, n)
	var out []backwardMatch
	var wi, li uint
	dpOwns, next := false, 0
	for i := range n {
		for next < len(at) && at[next] <= i {
			dpOwns = !dpOwns
			next++
		}
		w, l := uint(withDict.hqNumMatchesArr[i]), uint(lzOnly.hqNumMatchesArr[i])
		if dpOwns {
			num[i] = uint32(l)
			out = append(out, lzOnly.hqMatches[li:li+l]...)
		} else {
			num[i] = uint32(w)
			out = append(out, withDict.hqMatches[wi:wi+w]...)
		}
		wi += w
		li += l
	}
	return num, append(out, make([]backwardMatch, h10MaxNumMatches)...)
}

func TestZopfliIterateWithTheDictionaryHandedBackAndForthProducesTheNodesOfACollectorThatSearchesItForEveryHandoffPattern(t *testing.T) {
	for _, in := range dpDictCorpus(t, 64<<10) {
		const quality, lgwin = 10, 22
		rb, mask := dpDictRing(in.data)
		n := uint(len(in.data))
		withDict := dpDictCollect(t, rb, mask, n, lgwin, false)
		lzOnly := dpDictCollect(t, rb, mask, n, lgwin, true)
		var wantModel zopfliCostModel
		wantNodes := dpDictNodes(rb, mask, n, &wantModel)
		want := zopfliIterate(wantNodes, rb, []int{4, 11, 15, 16}, &wantModel,
			withDict.hqNumMatchesArr, withDict.hqMatches, n, 0, mask, 0, nil, quality, lgwin, nil, nil)
		for _, at := range [][]uint{
			{},
			{0},
			{1},
			{dictHandoffMinPos},
			{n / 2},
			{n - 1},
			{n},
			{n / 4, n / 2},
			{0, 1},
			{n / 5, n / 3, n / 2},
			{dictHandoffMinPos, dictHandoffMinPos + 1, n / 2, n/2 + dictReclaimLead},
		} {
			num, matches := handedOffMatches(withDict, lzOnly, n, at)
			var model zopfliCostModel
			nodes := dpDictNodes(rb, mask, n, &model)
			got := zopfliIterate(nodes, rb, []int{4, 11, 15, 16}, &model,
				num, matches, n, 0, mask, 0, nil, quality, lgwin, nil, dictHandoffsAt(at...))
			if got != want || !slices.Equal(nodes, wantNodes) {
				t.Errorf("%s handoffs at %v of %d: DP result %d, want %d, nodes equal = %t",
					in.name, at, n, got, want, slices.Equal(nodes, wantNodes))
			}
		}
	}
}

type dpDictRun struct {
	hasher        *h10
	bufs          *q10Bufs
	commands      []command
	distCache     [4]int
	lastInsertLen uint
	numLiterals   uint
}

func runDPDictEntry(tb testing.TB, parallel bool, data []byte, shrinkMatches bool) *dpDictRun {
	tb.Helper()
	const quality, lgwin = 10, 22
	rb, mask := dpDictRing(data)
	bufs := &q10Bufs{parallel: parallel}
	tb.Cleanup(bufs.hqCollector.stop)
	if shrinkMatches {
		bufs.hqMatches = make([]backwardMatch, 0, 16)
	}
	r := &dpDictRun{hasher: &h10{lgwin: lgwin, quality: quality, bufs: bufs}, bufs: bufs, distCache: [4]int{4, 11, 15, 16}}
	n := uint(len(data))
	r.hasher.reset(true, n, nil)
	r.hasher.stitchToPreviousBlock(n, 0, rb, mask)
	createHqZopfliBackwardReferences(n, 0, rb, mask, quality, lgwin, 0, nil, r.distCache[:], r.hasher, &r.lastInsertLen, &r.commands, &r.numLiterals, bufs)
	return r
}

func compareDPDictRuns(t *testing.T, got, want *dpDictRun) {
	t.Helper()
	if !slices.Equal(got.commands, want.commands) {
		t.Errorf("%d commands differ from the %d of the serial run; the block would encode to different bytes",
			len(got.commands), len(want.commands))
	}
	if got.distCache != want.distCache || got.lastInsertLen != want.lastInsertLen || got.numLiterals != want.numLiterals {
		t.Errorf("carried state distCache %v lastInsertLen %d numLiterals %d, want %v %d %d; the next block starts from it",
			got.distCache, got.lastInsertLen, got.numLiterals, want.distCache, want.lastInsertLen, want.numLiterals)
	}
	if !slices.Equal(got.hasher.forest, want.hasher.forest) || got.hasher.buckets != want.hasher.buckets {
		t.Error("the hasher tree differs after the block; the next block would search a different forest")
	}
	if got.hasher.skipDict {
		t.Error("skipDict is still set after the block; the next findAllMatches caller outside the q10 collector " +
			"would silently lose every static-dictionary match")
	}
}

func dpDictDivergingInput(tb testing.TB) []byte {
	tb.Helper()
	text, err := os.ReadFile("../../testdata/gh_172KB.html")
	if err != nil {
		tb.Fatal(err)
	}
	r := rand.New(rand.NewPCG(3, 4))
	random := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(r.Uint32())
		}
		return b
	}
	const tail = 22000
	x := random(40000)
	in := slices.Clone(text[:24<<10])
	in = append(in, x...)
	in = append(in, x[:20000]...)
	in = append(in, random(500)...)
	in = append(in, x[tail:tail+300]...)
	in = append(in, random(tail-20000-800)...)
	in = append(in, x[tail:tail+17000]...)
	return append(in, text[24<<10:32<<10]...)
}

func TestParallelCreateHqZopfliBackwardReferencesEmitsTheSerialCommandsWhenTheBlockDivergesOrTheFeedAborts(t *testing.T) {
	t.Run("diverged_block_redone_serially", func(t *testing.T) {
		in := dpDictDivergingInput(t)
		want := runDPDictEntry(t, false, in, false)
		got := runDPDictEntry(t, true, in, false)
		if cap(got.bufs.zMatches) == 0 {
			t.Fatal("the input must make the DP skip a long copy the collector did not, so the block is redone by " +
				"the serial fallback; without that this case never checks the fallback keeps its dictionary matches")
		}
		compareDPDictRuns(t, got, want)
	})

	t.Run("aborted_feed_rerun", func(t *testing.T) {
		saved := hqMatchesPerByte
		hqMatchesPerByte = 0
		t.Cleanup(func() { hqMatchesPerByte = saved })
		in := dpDictCorpus(t, 16<<10)[4].data
		want := runDPDictEntry(t, false, in, true)
		got := runDPDictEntry(t, true, in, true)
		if !got.bufs.hqFeed.aborted.Load() {
			t.Fatal("the shrunk match buffer must force the collector to reallocate and abort the feed, " +
				"otherwise this case never checks the rerun keeps searching the dictionary on the DP")
		}
		compareDPDictRuns(t, got, want)
	})
}
