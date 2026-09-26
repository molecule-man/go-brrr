package encoder

import (
	"fmt"
	"math/bits"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

func (h *h10) findAllMatchesBefore(
	data []byte, ringBufferMask, curIx, maxLength, maxBackward, dictionaryDistance uint,
	quality int, matches []backwardMatch,
) uint {
	curIxMasked := curIx & ringBufferMask
	bestLen := uint(1)
	nMatches := 0

	shortMatchMaxBackward := uint(16)
	if quality == 11 {
		shortMatchMaxBackward = 64
	}

	stop := uint(0)
	if curIx > shortMatchMaxBackward {
		stop = curIx - shortMatchMaxBackward
	}

	if prefix2Mask64Available && shortMatchMaxBackward == 64 &&
		curIxMasked >= 64 && curIx > 64 && maxBackward >= 63 {
		mask := prefix2Mask64(&data[curIxMasked-64], data[curIxMasked], data[curIxMasked+1])
		mask &^= 1
		for mask != 0 && bestLen <= 2 {
			j := uint(63 - bits.LeadingZeros64(mask))
			mask &^= 1 << j
			backward := 64 - j
			prevIxMasked := curIxMasked - backward
			length := uint(matchLenAt(data, prevIxMasked, curIxMasked, int(maxLength)))
			if length > bestLen {
				bestLen = length
				matches[nMatches] = newBackwardMatch(backward, length)
				nMatches++
			}
		}
	} else {
		for i := curIx - 1; i > stop && bestLen <= 2; i-- {
			backward := curIx - i
			if backward > maxBackward {
				break
			}
			prevIxMasked := i & ringBufferMask
			if data[curIxMasked] != data[prevIxMasked] ||
				data[curIxMasked+1] != data[prevIxMasked+1] {
				continue
			}
			length := uint(matchLenAt(data, prevIxMasked, curIxMasked, int(maxLength)))
			if length > bestLen {
				bestLen = length
				matches[nMatches] = newBackwardMatch(backward, length)
				nMatches++
			}
		}
	}

	if bestLen < maxLength {
		if maxLength >= h10MaxTreeCompLength {
			nMatches += h.storeAndFindMatches(
				data, curIx, ringBufferMask, maxLength, maxBackward,
				&bestLen, matches[nMatches:],
			)
		} else {
			nMatches += h.findMatchesNoStore(
				data, curIx, ringBufferMask, maxLength, maxBackward,
				&bestLen, matches[nMatches:],
			)
		}
	}

	minLen := max(uint(4), bestLen+1)
	maxLen := min(uint(maxStaticDictMatchLen), maxLength)
	if minLen <= maxLen {
		var dictMatches [maxStaticDictMatchLen + 1]uint32
		for i := range dictMatches {
			dictMatches[i] = invalidMatch
		}
		if findAllStaticDictionaryMatches(data[curIxMasked:], minLen, maxLength, dictMatches[:]) {
			for l := minLen; l <= maxLen; l++ {
				dictID := dictMatches[l]
				if dictID < invalidMatch {
					distance := dictionaryDistance + uint(dictID>>5) + 1
					if distance <= maxBackwardDistance {
						matches[nMatches] = newDictionaryBackwardMatch(distance, l, uint(dictID&31))
						nMatches++
					}
				}
			}
		}
	}

	return uint(nMatches)
}

func zopfliIterateBefore(nodes []zopfliNode, ringbuffer []byte, distCache []int, model *zopfliCostModel, numMatches []uint32, matches []backwardMatch, numBytes, position, ringBufferMask, gap uint, compound *compoundDictionary, quality, lgwin int, feed *matchFeed) uint {
	maxBackwardLimit := (uint(1) << lgwin) - core.WindowGap
	maxZopfli := maxZopfliLen(quality)
	var queue startPosQueue
	curMatchPos := uint(0)

	nodes[0].length = 0
	nodes[0].setCost(0)

	for i := uint(0); i+3 < numBytes; i++ {
		if !feed.wait(i) {
			return zopfliIterateAborted
		}
		skip := updateNodes(nodes, ringbuffer, distCache,
			matches[curMatchPos:], model, &queue,
			numBytes, position, i, ringBufferMask, maxBackwardLimit, gap, compound, uint(numMatches[i]), quality)
		if skip < longCopyQuickStep {
			skip = 0
		} else if quality < hqZopflificationQuality &&
			(numMatches[i] != 1 || matches[curMatchPos].matchLength() < skip) {
			return zopfliIterateDiverged
		}
		curMatchPos += uint(numMatches[i])
		if numMatches[i] == 1 && matches[curMatchPos-1].matchLength() > maxZopfli {
			skip = max(matches[curMatchPos-1].matchLength(), skip)
		}
		if skip > 1 {
			skip--
			for skip > 0 {
				i++
				if i+3 >= numBytes {
					break
				}
				if !feed.wait(i) {
					return zopfliIterateAborted
				}
				evaluateNode(nodes, i, position, maxBackwardLimit, gap, distCache, model, &queue)
				curMatchPos += uint(numMatches[i])
				skip--
			}
		}
	}
	return computeShortestPathFromNodes(nodes, numBytes)
}

func createHqZopfliBackwardReferencesBefore(numBytes, position uint, ringbuffer []byte, ringBufferMask uint, quality, lgwin int, gap uint, compound *compoundDictionary, distCache []int, hasher *h10, lastInsertLen *uint, commands *[]command, numLiterals *uint, bufs *q10Bufs) {
	maxBackwardLimit := (uint(1) << lgwin) - core.WindowGap
	hasCompound := compound != nil && compound.numChunks > 0
	shadowMatches := uint(0)
	if hasCompound {
		shadowMatches = h10MaxNumMatches + 128
	}
	matchesPerByte := uint(4)
	if bufs.parallel {
		matchesPerByte = hqMatchesPerByte
	}
	matchesSize := matchesPerByte*numBytes + shadowMatches
	storeEnd := position
	if numBytes >= h10MaxTreeCompLength {
		storeEnd = position + numBytes - h10MaxTreeCompLength + 1
	}
	if cap(bufs.hqNumMatchesArr) < int(numBytes) {
		bufs.hqNumMatchesArr = make([]uint32, numBytes)
	} else {
		bufs.hqNumMatchesArr = bufs.hqNumMatchesArr[:numBytes]
		clear(bufs.hqNumMatchesArr)
	}
	numMatchesArr := bufs.hqNumMatchesArr

	if cap(bufs.hqMatches) < int(matchesSize) {
		bufs.hqMatches = make([]backwardMatch, matchesSize)
	} else {
		bufs.hqMatches = bufs.hqMatches[:matchesSize]
	}
	matches := bufs.hqMatches[:cap(bufs.hqMatches)]

	needed := int(numBytes + 1)
	if cap(bufs.zNodes) < needed {
		bufs.zNodes = make([]zopfliNode, needed)
	} else {
		bufs.zNodes = bufs.zNodes[:needed]
	}
	nodes := bufs.zNodes
	model := &bufs.zCostModel
	model.init(64, numBytes)

	passes := 2
	forestUsed := 0
	if quality < hqZopflificationQuality {
		passes = 1
		if numBytes >= longCopyQuickStep {
			forestUsed = min(len(hasher.forest), 2*int(position+numBytes))
			n := forestUsed + len(hasher.buckets)
			if cap(bufs.hqHasherSnap) < n {
				bufs.hqHasherSnap = make([]uint32, len(hasher.forest)+len(hasher.buckets))
			}
			bufs.hqHasherSnap = bufs.hqHasherSnap[:n]
			copy(bufs.hqHasherSnap, hasher.forest[:forestUsed])
			copy(bufs.hqHasherSnap[forestUsed:], hasher.buckets[:])
		}
	}

	feed := &bufs.hqFeed
	feed.reset()
	col := &bufs.hqCollector
	col.bufs = bufs
	col.hasher = hasher
	col.compound = compound
	col.ringbuffer = ringbuffer
	col.numBytes = numBytes
	col.position = position
	col.ringBufferMask = ringBufferMask
	col.maxBackwardLimit = maxBackwardLimit
	col.gap = gap
	col.storeEnd = storeEnd
	col.shadowMatches = shadowMatches
	col.quality = quality
	var firstPassFeed *matchFeed
	if bufs.parallel {
		col.begin(hqJobCollect)
		firstPassFeed = feed
	} else {
		col.collect()
		matches = bufs.hqMatches
	}

	origNumLiterals := *numLiterals
	origLastInsertLen := *lastInsertLen
	origDistCache := [4]int{distCache[0], distCache[1], distCache[2], distCache[3]}
	origNumCommands := len(*commands)
	restore := func() {
		*commands = (*commands)[:origNumCommands]
		*numLiterals = origNumLiterals
		*lastInsertLen = origLastInsertLen
		distCache[0] = origDistCache[0]
		distCache[1] = origDistCache[1]
		distCache[2] = origDistCache[2]
		distCache[3] = origDistCache[3]
	}

	for pass := range passes {
		initZopfliNodes(nodes)
		var f *matchFeed
		if pass == 0 {
			model.setFromLiteralCosts(position, ringbuffer, ringBufferMask)
			f = firstPassFeed
		} else {
			col.wait()
			passCommands := (*commands)[origNumCommands:]
			model.setFromCommands(position, ringbuffer, ringBufferMask,
				passCommands, origLastInsertLen)
		}
		restore()

		result := zopfliIterateBefore(nodes, ringbuffer, distCache, model, numMatchesArr, matches,
			numBytes, position, ringBufferMask, gap, compound, quality, lgwin, f)
		if result == zopfliIterateAborted {
			col.wait()
			matches = bufs.hqMatches
			initZopfliNodes(nodes)
			restore()
			result = zopfliIterateBefore(nodes, ringbuffer, distCache, model, numMatchesArr, matches,
				numBytes, position, ringBufferMask, gap, compound, quality, lgwin, nil)
		}
		if result == zopfliIterateDiverged {
			col.wait()
			copy(hasher.forest[:forestUsed], bufs.hqHasherSnap)
			copy(hasher.buckets[:], bufs.hqHasherSnap[forestUsed:])
			restore()
			createZopfliBackwardReferences(numBytes, position, ringbuffer, ringBufferMask, quality, lgwin, gap, compound, distCache, hasher, lastInsertLen, commands, numLiterals, bufs)
			return
		}

		zopfliCreateCommands(nodes, numBytes, position, maxBackwardLimit, gap, distCache, lastInsertLen, commands, numLiterals)
	}
	col.wait()
}

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

func dpDictCollect(tb testing.TB, rb []byte, mask, numBytes uint, quality, lgwin int, skipDict bool) *q10Bufs {
	tb.Helper()
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

func TestFindAllMatchesWithoutItsDictionaryPhasePlusStaticDictBackwardMatchesReturnsTheBeforeMatchesAtEveryPosition(t *testing.T) {
	for _, in := range dpDictCorpus(t, 32<<10) {
		for _, cfg := range []struct{ quality, lgwin int }{{10, 16}, {10, 22}, {11, 22}} {
			rb, mask := dpDictRing(in.data)
			n := uint(len(in.data))
			before := &h10{lgwin: cfg.lgwin, quality: cfg.quality}
			full := &h10{lgwin: cfg.lgwin, quality: cfg.quality}
			lzOnly := &h10{lgwin: cfg.lgwin, quality: cfg.quality, skipDict: true}
			hashers := []*h10{before, full, lzOnly}
			for _, h := range hashers {
				h.reset(true, n, nil)
			}
			storeEnd := uint(0)
			if n >= h10MaxTreeCompLength {
				storeEnd = n - h10MaxTreeCompLength + 1
			}
			maxBackwardLimit := uint(1)<<cfg.lgwin - core.WindowGap
			var want, got, split [h10MaxNumMatches]backwardMatch
			for i := uint(0); i+h10HashTypeLength-1 < n; i++ {
				maxDistance := min(i, maxBackwardLimit)
				numWant := before.findAllMatchesBefore(rb, mask, i, n-i, maxDistance, maxDistance, cfg.quality, want[:])
				numGot := full.findAllMatches(rb, mask, i, n-i, maxDistance, maxDistance, cfg.quality, got[:])
				numSplit := lzOnly.findAllMatches(rb, mask, i, n-i, maxDistance, maxDistance, cfg.quality, split[:])
				bestLen := uint(1)
				if numSplit > 0 {
					bestLen = split[numSplit-1].matchLength()
				}
				numSplit += uint(staticDictBackwardMatches(rb, i&mask, bestLen, n-i, maxDistance, split[numSplit:]))
				if !slices.Equal(got[:numGot], want[:numWant]) || !slices.Equal(split[:numSplit], want[:numWant]) {
					t.Errorf("%s q%d lgwin %d position %d: before %v, with dictionary %v, LZ then dictionary %v; "+
						"the collector and the DP must see the same match list or the q10 output changes",
						in.name, cfg.quality, cfg.lgwin, i, want[:numWant], got[:numGot], split[:numSplit])
					break
				}
				if numWant > 0 && want[numWant-1].matchLength() > maxZopfliLen(cfg.quality) {
					l := want[numWant-1].matchLength()
					for _, h := range hashers {
						h.storeRange(rb, mask, i+1, min(i+l, storeEnd))
					}
					i += l - 1
				}
			}
		}
	}
}

func TestZopfliIterateSearchingTheDictionaryItselfProducesTheBeforeNodesFromACollectorThatNoLongerSearchesIt(t *testing.T) {
	for _, in := range dpDictCorpus(t, 64<<10) {
		for _, lgwin := range []int{16, 22} {
			const quality = 10
			rb, mask := dpDictRing(in.data)
			n := uint(len(in.data))
			withDict := dpDictCollect(t, rb, mask, n, quality, lgwin, false)
			lzOnly := dpDictCollect(t, rb, mask, n, quality, lgwin, true)

			var wantModel, dpModel, keptModel zopfliCostModel
			wantNodes := dpDictNodes(rb, mask, n, &wantModel)
			want := zopfliIterateBefore(wantNodes, rb, []int{4, 11, 15, 16}, &wantModel,
				withDict.hqNumMatchesArr, withDict.hqMatches, n, 0, mask, 0, nil, quality, lgwin, nil)
			dpNodes := dpDictNodes(rb, mask, n, &dpModel)
			dp := zopfliIterate(dpNodes, rb, []int{4, 11, 15, 16}, &dpModel,
				lzOnly.hqNumMatchesArr, lzOnly.hqMatches, n, 0, mask, 0, nil, quality, lgwin, nil, true)
			keptNodes := dpDictNodes(rb, mask, n, &keptModel)
			kept := zopfliIterate(keptNodes, rb, []int{4, 11, 15, 16}, &keptModel,
				withDict.hqNumMatchesArr, withDict.hqMatches, n, 0, mask, 0, nil, quality, lgwin, nil, false)

			if dp != want || !slices.Equal(dpNodes, wantNodes) {
				t.Errorf("%s lgwin %d: the DP appending its own dictionary matches to the LZ-only feed returned %d, "+
					"want %d, and nodes equal = %t; it must price exactly the matches the collector used to hand it",
					in.name, lgwin, dp, want, slices.Equal(dpNodes, wantNodes))
			}
			if kept != want || !slices.Equal(keptNodes, wantNodes) {
				t.Errorf("%s lgwin %d: with the dictionary search left in the collector the DP returned %d, want %d, "+
					"and nodes equal = %t; q11 and compound blocks take this path and must not change",
					in.name, lgwin, kept, want, slices.Equal(keptNodes, wantNodes))
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

func runDPDictEntry(tb testing.TB, entry func(uint, uint, []byte, uint, int, int, uint, *compoundDictionary, []int, *h10, *uint, *[]command, *uint, *q10Bufs),
	data []byte, block, quality, lgwin int, compound *compoundDictionary, shrinkMatches bool,
) *dpDictRun {
	tb.Helper()
	rb, mask := dpDictRing(data)
	bufs := &q10Bufs{parallel: true}
	tb.Cleanup(bufs.hqCollector.stop)
	if shrinkMatches {
		bufs.hqMatches = make([]backwardMatch, 0, 16)
	}
	gap := uint(0)
	if compound != nil {
		gap = compound.totalSize
	}
	r := &dpDictRun{hasher: &h10{lgwin: lgwin, quality: quality, bufs: bufs}, bufs: bufs, distCache: [4]int{4, 11, 15, 16}}
	r.hasher.reset(true, uint(len(data)), nil)
	for start := 0; start < len(data); start += block {
		n, position := uint(min(block, len(data)-start)), uint(start)
		r.hasher.stitchToPreviousBlock(n, position, rb, mask)
		entry(n, position, rb, mask, quality, lgwin, gap, compound, r.distCache[:], r.hasher, &r.lastInsertLen, &r.commands, &r.numLiterals, bufs)
	}
	return r
}

func compareDPDictRuns(t *testing.T, got, want *dpDictRun) {
	t.Helper()
	if !slices.Equal(got.commands, want.commands) {
		t.Errorf("%d commands differ from the %d before the move; the block would encode to different bytes",
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

func TestCreateHqZopfliBackwardReferencesWithTheDictionarySearchOnTheDPEmitsTheBeforeCommands(t *testing.T) {
	type entryCase struct {
		name     string
		data     []byte
		block    int
		quality  int
		lgwin    int
		compound *compoundDictionary
	}
	corpus := dpDictCorpus(t, 96<<10)
	cases := make([]entryCase, 0, 5*len(corpus)+9)
	for _, in := range corpus {
		for _, cfg := range []struct{ quality, lgwin, limit, block int }{
			{10, 16, 64 << 10, 64 << 10}, {10, 22, 64 << 10, 64 << 10}, {11, 22, 16 << 10, 16 << 10},
			{10, 16, 96 << 10, 32 << 10}, {10, 22, 96 << 10, 32 << 10},
		} {
			cases = append(cases, entryCase{fmt.Sprintf("%s_q%d_lgwin%d_blocks_of_%d", in.name, cfg.quality, cfg.lgwin, cfg.block),
				in.data[:min(len(in.data), cfg.limit)], cfg.block, cfg.quality, cfg.lgwin, nil})
		}
	}
	for _, n := range []int{1, 4, 5, 36, 37, 38, 41} {
		cases = append(cases, entryCase{fmt.Sprintf("html_prefix_%d", n), corpus[3].data[:n], n, 10, 22, nil})
	}
	var cd compoundDictionary
	if err := cd.attach(newPreparedDictionary(corpus[3].data[:32<<10])); err != nil {
		t.Fatal(err)
	}
	cases = append(cases, entryCase{"compound_dictionary", corpus[3].data[32<<10 : 64<<10], 32 << 10, 10, 22, &cd})
	diverging := dpDictDivergingInput(t)
	cases = append(cases, entryCase{"diverged_block_redone_serially", diverging, len(diverging), 10, 22, nil})

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := runDPDictEntry(t, createHqZopfliBackwardReferencesBefore, c.data, c.block, c.quality, c.lgwin, c.compound, false)
			got := runDPDictEntry(t, createHqZopfliBackwardReferences, c.data, c.block, c.quality, c.lgwin, c.compound, false)
			if c.name == "diverged_block_redone_serially" && (cap(want.bufs.zMatches) == 0 || cap(got.bufs.zMatches) == 0) {
				t.Fatal("the input must make the DP skip a long copy the collector did not, so the block is redone by " +
					"the serial fallback; without that this case never checks the fallback keeps its dictionary matches")
			}
			compareDPDictRuns(t, got, want)
		})
	}

	t.Run("aborted_feed_rerun", func(t *testing.T) {
		saved := hqMatchesPerByte
		hqMatchesPerByte = 0
		t.Cleanup(func() { hqMatchesPerByte = saved })
		in := corpus[4].data[:16<<10]
		want := runDPDictEntry(t, createHqZopfliBackwardReferencesBefore, in, len(in), 10, 22, nil, true)
		got := runDPDictEntry(t, createHqZopfliBackwardReferences, in, len(in), 10, 22, nil, true)
		if !want.bufs.hqFeed.aborted.Load() || !got.bufs.hqFeed.aborted.Load() {
			t.Fatal("the shrunk match buffer must force the collector to reallocate and abort the feed, " +
				"otherwise this case never checks the rerun keeps searching the dictionary on the DP")
		}
		compareDPDictRuns(t, got, want)
	})
}
