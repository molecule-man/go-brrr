package encoder

import (
	"bytes"
	"errors"
	"fmt"
	"math/bits"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

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
		skip := updateNodesBefore(nodes, ringbuffer, distCache,
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

func zopfliComputeShortestPathBefore(numBytes, position uint, ringbuffer []byte, ringBufferMask uint, quality, lgwin int, gap uint, compound *compoundDictionary, distCache []int, hasher *h10, nodes []zopfliNode, bufs *q10Bufs) uint {
	maxBackwardLimit := (uint(1) << lgwin) - core.WindowGap
	maxZopfli := maxZopfliLen(quality)
	var queue startPosQueue
	hasCompound := compound != nil && compound.numChunks > 0
	lzOff := uint(0)
	if hasCompound {
		lzOff = h10MaxNumMatches + 128
	}
	matchesNeeded := 2*(h10MaxNumMatches+64) + int(lzOff)
	if cap(bufs.zMatches) < matchesNeeded {
		bufs.zMatches = make([]backwardMatch, matchesNeeded)
	} else {
		bufs.zMatches = bufs.zMatches[:matchesNeeded]
	}
	matches := bufs.zMatches
	storeEnd := position
	if numBytes >= h10MaxTreeCompLength {
		storeEnd = position + numBytes - h10MaxTreeCompLength + 1
	}

	model := &bufs.zCostModel
	model.init(64, numBytes)

	nodes[0].length = 0
	nodes[0].setCost(0)
	model.setFromLiteralCosts(position, ringbuffer, ringBufferMask)

	for i := uint(0); i+h10HashTypeLength-1 < numBytes; i++ {
		pos := position + i
		maxDistance := min(pos, maxBackwardLimit)
		maxLength := numBytes - i

		numFound := hasher.findAllMatches(
			ringbuffer, ringBufferMask, pos, maxLength, maxDistance,
			maxDistance+gap, quality, matches[lzOff:])

		if hasCompound {
			cdMatches := compound.lookupAllMatches(
				ringbuffer, ringBufferMask, pos, 3, maxLength,
				maxDistance, maxBackwardDistance,
				matches[lzOff-64:lzOff],
			)
			if cdMatches > 0 {
				mergeMatches(matches,
					matches[lzOff-64:lzOff-64+cdMatches],
					matches[lzOff:lzOff+numFound])
				numFound += cdMatches
			} else {
				copy(matches, matches[lzOff:lzOff+numFound])
			}
		}

		if numFound > 0 && matches[numFound-1].matchLength() > maxZopfli {
			matches[0] = matches[numFound-1]
			numFound = 1
		}

		skip := updateNodesBefore(nodes, ringbuffer, distCache,
			matches[:numFound], model, &queue,
			numBytes, position, i, ringBufferMask, maxBackwardLimit, gap, compound, numFound, quality)
		if skip < longCopyQuickStep {
			skip = 0
		}
		if numFound == 1 && matches[0].matchLength() > maxZopfli {
			skip = max(matches[0].matchLength(), skip)
		}
		if skip > 1 {
			hasher.storeRange(ringbuffer, ringBufferMask, pos+1, min(pos+skip, storeEnd))
			skip--
			for skip > 0 {
				i++
				if i+h10HashTypeLength-1 >= numBytes {
					break
				}
				evaluateNode(nodes, i, position, maxBackwardLimit, gap, distCache, model, &queue)
				skip--
			}
		}
	}

	return computeShortestPathFromNodes(nodes, numBytes)
}

const zopfliBenchBlock = 256 << 10

type zopfliInput struct {
	name string
	data []byte
}

func zopfliCorpus(tb testing.TB) []zopfliInput {
	tb.Helper()
	names := []string{"plrabn12.txt", "lcet10.txt", "mapsdatazrh"}
	in := make([]zopfliInput, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join("..", "..", "brotli-ref", "tests", "testdata", name))
		if err != nil {
			tb.Fatal(err)
		}
		in = append(in, zopfliInput{name, data})
	}
	return in
}

func newZopfliStream(in, dict []byte, quality, lgwin int) (*encodeState, *h10, error) {
	s := new(encodeState)
	s.reset(quality, lgwin, uint(len(in)))
	if dict != nil {
		if err := s.attachDictionary(newPreparedDictionary(dict)); err != nil {
			return nil, nil, fmt.Errorf("attaching a %d-byte compound dictionary: %w", len(dict), err)
		}
	}
	h := &h10{lgwin: lgwin, quality: quality}
	h.reset(true, uint(len(in)), nil)
	return s, h, nil
}

func loadZopfliBlock(s *encodeState, h *h10, chunk []byte, position uint) {
	s.copyInputToRingBuffer(chunk)
	h.stitchToPreviousBlock(uint(len(chunk)), position, s.data, uint(s.mask))
}

func collectZopfliMatches(s *encodeState, h *h10, position, numBytes uint) ([]uint32, []backwardMatch) {
	bufs := new(q10Bufs)
	shadowMatches := uint(0)
	if s.compound.numChunks > 0 {
		shadowMatches = h10MaxNumMatches + 128
	}
	storeEnd := position
	if numBytes >= h10MaxTreeCompLength {
		storeEnd = position + numBytes - h10MaxTreeCompLength + 1
	}
	bufs.hqNumMatchesArr = make([]uint32, numBytes)
	bufs.hqMatches = make([]backwardMatch, hqMatchesPerByte*numBytes+shadowMatches)
	col := &bufs.hqCollector
	col.bufs = bufs
	col.hasher = h
	col.compound = &s.compound
	col.ringbuffer = s.data
	col.numBytes = numBytes
	col.position = position
	col.ringBufferMask = uint(s.mask)
	col.maxBackwardLimit = uint(1)<<s.lgwin - core.WindowGap
	col.gap = s.compound.totalSize
	col.storeEnd = storeEnd
	col.shadowMatches = shadowMatches
	col.quality = s.quality
	col.collect()
	return bufs.hqNumMatchesArr, bufs.hqMatches
}

func cloneH10(h *h10) *h10 {
	c := *h
	c.forest = slices.Clone(h.forest)
	return &c
}

func compareZopfliNodes(what string, got, want uint, after, before []zopfliNode) error {
	var errs []error
	if got != want {
		errs = append(errs, fmt.Errorf("%s returned %d, scanning every queue entry returned %d, so the command count or the skip-ahead differs", what, got, want))
	}
	differ := 0
	for i := range after {
		if after[i] == before[i] {
			continue
		}
		if differ == 0 {
			errs = append(errs, fmt.Errorf("%s left nodes[%d] = %+v, scanning every queue entry left %+v; a different node is a different command, so q10/q11 output would stop matching the C reference", what, i, after[i], before[i]))
		}
		differ++
	}
	if differ > 1 {
		errs = append(errs, fmt.Errorf("%s: %d of %d nodes differ in total", what, differ, len(after)))
	}
	return errors.Join(errs...)
}

func zopfliIterateBothWays(s *encodeState, distCache []int, model *zopfliCostModel, numMatches []uint32, matches []backwardMatch, position, numBytes uint) ([]zopfliNode, uint, error) {
	after := make([]zopfliNode, numBytes+1)
	before := make([]zopfliNode, numBytes+1)
	initZopfliNodes(after)
	initZopfliNodes(before)
	mask, gap := uint(s.mask), s.compound.totalSize
	got := zopfliIterate(after, s.data, distCache, model, numMatches, matches, numBytes, position, mask, gap, &s.compound, s.quality, s.lgwin, nil)
	want := zopfliIterateBefore(before, s.data, distCache, model, numMatches, matches, numBytes, position, mask, gap, &s.compound, s.quality, s.lgwin, nil)
	return after, got, compareZopfliNodes("zopfliIterate", got, want, after, before)
}

func zopfliComputeShortestPathBothWays(s *encodeState, distCache []int, hAfter, hBefore *h10, position, numBytes uint) ([]zopfliNode, error) {
	after := make([]zopfliNode, numBytes+1)
	before := make([]zopfliNode, numBytes+1)
	initZopfliNodes(after)
	initZopfliNodes(before)
	mask, gap := uint(s.mask), s.compound.totalSize
	got := zopfliComputeShortestPath(numBytes, position, s.data, mask, s.quality, s.lgwin, gap, &s.compound, distCache, hAfter, after, new(q10Bufs))
	want := zopfliComputeShortestPathBefore(numBytes, position, s.data, mask, s.quality, s.lgwin, gap, &s.compound, distCache, hBefore, before, new(q10Bufs))
	err := compareZopfliNodes("zopfliComputeShortestPath", got, want, after, before)
	if !slices.Equal(hAfter.forest, hBefore.forest) || hAfter.buckets != hBefore.buckets {
		err = errors.Join(err, errors.New("zopfliComputeShortestPath left a different H10 tree than scanning every queue entry, so the skip-ahead stored different positions and later blocks would find different matches"))
	}
	return after, err
}

func compareZopfliStream(in, dict []byte, quality, lgwin int) error {
	s, h, err := newZopfliStream(in, dict, quality, lgwin)
	if err != nil {
		return err
	}
	defer s.releaseRingBuffer()
	mask, gap := uint(s.mask), s.compound.totalSize
	maxBackwardLimit := uint(1)<<lgwin - core.WindowGap
	distCache := []int{4, 11, 15, 16}
	lastInsertLen := uint(0)
	var errs []error
	for start := 0; start < len(in); start += 1 << s.lgblock {
		chunk := in[start:min(start+1<<s.lgblock, len(in))]
		position, numBytes := uint(start), uint(len(chunk))
		loadZopfliBlock(s, h, chunk, position)
		var hAfter, hBefore *h10
		if quality < hqZopflificationQuality {
			hAfter, hBefore = cloneH10(h), cloneH10(h)
		}
		numMatches, matches := collectZopfliMatches(s, h, position, numBytes)
		model := new(zopfliCostModel)
		model.init(64, numBytes)
		model.setFromLiteralCosts(position, s.data, mask)
		nodes, result, err := zopfliIterateBothWays(s, distCache, model, numMatches, matches, position, numBytes)
		if err != nil {
			errs = append(errs, fmt.Errorf("block at %d, first pass: %w", start, err))
		}
		if quality >= hqZopflificationQuality {
			passDistCache, passInsertLen := slices.Clone(distCache), lastInsertLen
			var passCommands []command
			var passLiterals uint
			zopfliCreateCommands(nodes, numBytes, position, maxBackwardLimit, gap, passDistCache, &passInsertLen, &passCommands, &passLiterals)
			model.setFromCommands(position, s.data, mask, passCommands, lastInsertLen)
			nodes, _, err = zopfliIterateBothWays(s, distCache, model, numMatches, matches, position, numBytes)
			if err != nil {
				errs = append(errs, fmt.Errorf("block at %d, second pass: %w", start, err))
			}
		} else {
			fallback, err := zopfliComputeShortestPathBothWays(s, distCache, hAfter, hBefore, position, numBytes)
			if err != nil {
				errs = append(errs, fmt.Errorf("block at %d, single-pass fallback: %w", start, err))
			}
			if result == zopfliIterateDiverged {
				nodes, h = fallback, hAfter
			}
		}
		var commands []command
		var numLiterals uint
		zopfliCreateCommands(nodes, numBytes, position, maxBackwardLimit, gap, distCache, &lastInsertLen, &commands, &numLiterals)
	}
	return errors.Join(errs...)
}

func TestZopfliScanningEachDistinctDistanceCacheOnceLeavesTheSameNodesAsScanningEveryQueueEntry(t *testing.T) {
	type zopfliCase struct {
		name  string
		data  []byte
		dict  []byte
		lgwin int
	}
	var cases []zopfliCase
	testdata := map[string][]byte{}
	for _, name := range []string{"gh_172KB.html", "reactcore_187KB.js", "github_events_8k.json"} {
		data, err := os.ReadFile(filepath.Join("../../testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		testdata[name] = data
		for _, lgwin := range []int{10, 16, 22} {
			lgblock := min(18, max(16, lgwin))
			cases = append(cases, zopfliCase{fmt.Sprintf("%s lgwin %d, %d KiB blocks in a %d KiB ring", name, lgwin, 1<<lgblock>>10, 2<<max(lgwin, lgblock)>>10), data, nil, lgwin})
		}
	}
	react := testdata["reactcore_187KB.js"]
	rnd := rand.New(rand.NewSource(1))
	random := make([]byte, 100000)
	rnd.Read(random)
	var drift bytes.Buffer
	for drift.Len() < 200000 {
		drift.WriteString("the quick brown fox jumps over the lazy dog")
		drift.Write(bytes.Repeat([]byte{' '}, rnd.Intn(7)))
	}
	cases = append(cases,
		zopfliCase{"reactcore_187KB.js after its first 48 KiB with the first 64 KiB as a compound dictionary, so cached distances reach into the dictionary", react[48<<10:], react[:64<<10], 16},
		zopfliCase{"zeros, where every queue entry carries the same distance cache and copies run past the quick-step length", make([]byte, 300000), nil, 16},
		zopfliCase{"random bytes, where the queue entries keep the initial distance cache and no short code matches", random, nil, 22},
		zopfliCase{"sentence with 0-6 random spaces, so the -3..+3 short codes around the last two distances win", drift.Bytes(), nil, 16},
	)
	if !testing.Short() {
		for _, in := range zopfliCorpus(t) {
			cases = append(cases, zopfliCase{in.name + " lgwin 22", in.data, nil, 22})
		}
	}
	for _, c := range cases {
		for _, quality := range []int{10, 11} {
			t.Run(fmt.Sprintf("q%d %s", quality, c.name), func(t *testing.T) {
				t.Parallel()
				if err := compareZopfliStream(c.data, c.dict, quality, c.lgwin); err != nil {
					t.Error(err)
				}
			})
		}
	}
	for _, quality := range []int{10, 11} {
		t.Run(fmt.Sprintf("q%d inputs of 1 to 40 bytes, shorter than the four bytes a position needs and just past it", quality), func(t *testing.T) {
			t.Parallel()
			var errs []error
			for n := 1; n <= 40; n++ {
				if err := compareZopfliStream(bytes.Repeat([]byte("abcab"), 8)[:n], nil, quality, 10); err != nil {
					errs = append(errs, fmt.Errorf("%d bytes: %w", n, err))
				}
			}
			if err := errors.Join(errs...); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestUpdateNodesRescansAQueueEntryWhoseDistanceCacheDiffersFromAnEarlierOneOnlyInAnOlderDistance(t *testing.T) {
	const pos, numBytes = 2048, 4096
	rnd := rand.New(rand.NewSource(1))
	data := make([]byte, numBytes)
	rnd.Read(data)
	copy(data[pos:pos+80], data[pos-700:])
	copy(data[pos-300:pos-260], data[pos-700:])
	model := new(zopfliCostModel)
	model.init(64, numBytes)
	model.setFromLiteralCosts(0, data, numBytes-1)
	var errs []error
	for _, caches := range [][2][4]int{
		{{11, 22, 33, 44}, {11, 22, 300, 44}},
		{{11, 22, 33, 44}, {11, 22, 33, 700}},
	} {
		var queue startPosQueue
		for k, dc := range caches {
			queue.push(&posData{pos: pos - 1 - uint(k), distanceCache: dc, costdiff: float32(k)})
		}
		afterQueue, beforeQueue := queue, queue
		after := make([]zopfliNode, numBytes+1)
		before := make([]zopfliNode, numBytes+1)
		initZopfliNodes(after)
		initZopfliNodes(before)
		var sc dcScratch
		got := updateNodes(after, data, []int{4, 11, 15, 16}, nil, model, &afterQueue, numBytes, 0, pos, numBytes-1, 1<<22-core.WindowGap, 0, nil, 0, 11, &sc)
		want := updateNodesBefore(before, data, []int{4, 11, 15, 16}, nil, model, &beforeQueue, numBytes, 0, pos, numBytes-1, 1<<22-core.WindowGap, 0, nil, 0, 11)
		if err := compareZopfliNodes("updateNodes", got, want, after, before); err != nil {
			errs = append(errs, fmt.Errorf("queue caches %v, where only the second entry's older distance matches 40 or 80 bytes, so reusing the first entry's empty scan loses those copies: %w", caches, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		t.Error(err)
	}
}

func BenchmarkZopfliIterate256KiB(b *testing.B) {
	corpus := zopfliCorpus(b)
	impls := []struct {
		name    string
		iterate func([]zopfliNode, []byte, []int, *zopfliCostModel, []uint32, []backwardMatch, uint, uint, uint, uint, *compoundDictionary, int, int, *matchFeed) uint
	}{{"before", zopfliIterateBefore}, {"after", zopfliIterate}}
	for _, in := range corpus {
		if len(in.data) < zopfliBenchBlock {
			b.Fatalf("%s holds %d bytes, the benchmark name promises a %d-byte block", in.name, len(in.data), zopfliBenchBlock)
		}
		block := in.data[:zopfliBenchBlock]
		for _, quality := range []int{10, 11} {
			s, h, err := newZopfliStream(block, nil, quality, 22)
			if err != nil {
				b.Fatal(err)
			}
			loadZopfliBlock(s, h, block, 0)
			numMatches, matches := collectZopfliMatches(s, h, 0, zopfliBenchBlock)
			model := new(zopfliCostModel)
			model.init(64, zopfliBenchBlock)
			model.setFromLiteralCosts(0, s.data, uint(s.mask))
			for _, impl := range impls {
				b.Run(fmt.Sprintf("%s_q%d/impl=%s", in.name, quality, impl.name), func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(zopfliBenchBlock)
					nodes := make([]zopfliNode, zopfliBenchBlock+1)
					distCache := []int{4, 11, 15, 16}
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						initZopfliNodes(nodes)
						impl.iterate(nodes, s.data, distCache, model, numMatches, matches, zopfliBenchBlock, 0, uint(s.mask), 0, &s.compound, quality, 22, nil)
					}
				})
			}
		}
	}
}

type zopfliTestInput struct {
	name string
	data []byte
}

func zopfliCorpusInputs(tb testing.TB) []zopfliTestInput {
	tb.Helper()
	var paths []string
	for _, pattern := range []string{"../../testdata/*", "../../brotli-ref/tests/testdata/*.txt"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			tb.Fatalf("glob %s: %v", pattern, err)
		}
		paths = append(paths, matches...)
	}
	var inputs []zopfliTestInput
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			tb.Fatalf("stat %s: %v", p, err)
		}
		if fi.IsDir() {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			tb.Fatalf("read %s: %v", p, err)
		}
		inputs = append(inputs, zopfliTestInput{filepath.Base(p), data})
	}
	return inputs
}

func zopfliEdgeInputs() []zopfliTestInput {
	r := rand.New(rand.NewSource(1))
	random := make([]byte, 200000)
	for i := range random {
		random[i] = byte(r.Intn(256))
	}
	words := make([]byte, 0, 300016)
	for len(words) < 300000 {
		words = append(words, random[r.Intn(4096):][:3+r.Intn(12)]...)
		words = append(words, ' ')
	}
	return []zopfliTestInput{
		{"zeros_200000", make([]byte, 200000)},
		{"random_200000", random},
		{"period2_200000", bytes.Repeat([]byte("ab"), 100000)},
		{"period3_199998", bytes.Repeat([]byte("abc"), 66666)},
		{"period5_200000", bytes.Repeat([]byte("abcde"), 40000)},
		{"random_words_300000", words},
		{"short_17", []byte("abcabcabcabcabcab")},
	}
}

type zopfliTestBlock struct {
	position, numBytes uint
	distCache          [4]int
	lastInsertLen      uint
	numMatches         []uint32
	matches            []backwardMatch
}

func zopfliStreamBlocks(tb testing.TB, data []byte, quality, lgwin int, visit func(ringbuffer []byte, ringBufferMask uint, blk zopfliTestBlock)) {
	tb.Helper()
	var s encodeState
	s.reset(quality, lgwin, 0)
	bufs := &q10Bufs{parallel: true}
	defer bufs.hqCollector.stop()
	h := &h10{lgwin: lgwin, quality: quality, bufs: bufs}
	h.reset(false, 0, nil)
	blockSize := 1 << s.lgblock
	for off := 0; off < len(data); off += blockSize {
		block := data[off:min(off+blockSize, len(data))]
		s.copyInputToRingBuffer(block)
		blk := zopfliTestBlock{position: uint(off), numBytes: uint(len(block)), lastInsertLen: s.lastInsertLen}
		for i, d := range s.distCache {
			blk.distCache[i] = int(d)
		}
		h.stitchToPreviousBlock(blk.numBytes, blk.position, s.data, uint(s.mask))
		h.createBackwardReferences(&s, uint32(len(block)), uint32(off))
		blk.numMatches = bufs.hqNumMatchesArr[:len(block)]
		blk.matches = bufs.hqMatches[:cap(bufs.hqMatches)]
		visit(s.data, uint(s.mask), blk)
	}
}

func compareZopfliPassesWithPreChangeCopy(ringbuffer []byte, ringBufferMask uint, quality, lgwin int, blk zopfliTestBlock) error {
	maxBackwardLimit := (uint(1) << lgwin) - core.WindowGap
	var model zopfliCostModel
	model.init(64, blk.numBytes)
	nodesAfter := make([]zopfliNode, blk.numBytes+1)
	nodesBefore := make([]zopfliNode, blk.numBytes+1)
	passes := 1
	if quality >= hqZopflificationQuality {
		passes = 2
	}
	var commands []command
	var errs []error
	for pass := range passes {
		if pass == 0 {
			model.setFromLiteralCosts(blk.position, ringbuffer, ringBufferMask)
		} else {
			model.setFromCommands(blk.position, ringbuffer, ringBufferMask, commands, blk.lastInsertLen)
		}
		initZopfliNodes(nodesAfter)
		initZopfliNodes(nodesBefore)
		after := zopfliIterate(nodesAfter, ringbuffer, blk.distCache[:], &model, blk.numMatches, blk.matches,
			blk.numBytes, blk.position, ringBufferMask, 0, nil, quality, lgwin, nil)
		before := zopfliIterateBefore(nodesBefore, ringbuffer, blk.distCache[:], &model, blk.numMatches, blk.matches,
			blk.numBytes, blk.position, ringBufferMask, 0, nil, quality, lgwin, nil)
		if after != before {
			errs = append(errs, fmt.Errorf("block at %d, pass %d: zopfliIterate returned %d, the pre-change pass returned %d", blk.position, pass, after, before))
		}
		for i := range nodesAfter {
			if nodesAfter[i] != nodesBefore[i] {
				errs = append(errs, fmt.Errorf("block at %d, pass %d: node %d is %+v, the pre-change pass left %+v", blk.position, pass, i, nodesAfter[i], nodesBefore[i]))
				break
			}
		}
		if after >= zopfliIterateDiverged {
			break
		}
		distCache := blk.distCache
		lastInsertLen := blk.lastInsertLen
		var numLiterals uint
		commands = commands[:0]
		zopfliCreateCommands(nodesAfter, blk.numBytes, blk.position, maxBackwardLimit, 0, distCache[:], &lastInsertLen, &commands, &numLiterals)
	}
	return errors.Join(errs...)
}

func TestUpdateNodesDistanceCacheFilterLeavesEveryZopfliNodeIdenticalToThePreChangeCopyOnCorpusAndEdgeStreams(t *testing.T) {
	for _, in := range append(zopfliCorpusInputs(t), zopfliEdgeInputs()...) {
		t.Run(in.name, func(t *testing.T) {
			t.Parallel()
			for _, quality := range []int{10, 11} {
				for _, lgwin := range []int{10, 16, 22} {
					var errs []error
					zopfliStreamBlocks(t, in.data, quality, lgwin, func(ringbuffer []byte, ringBufferMask uint, blk zopfliTestBlock) {
						errs = append(errs, compareZopfliPassesWithPreChangeCopy(ringbuffer, ringBufferMask, quality, lgwin, blk))
					})
					if err := errors.Join(errs...); err != nil {
						t.Errorf("q=%d lgwin=%d: the SWAR pre-filter may only skip distance-cache candidates whose continuation byte differs, so every DP node must match the scalar loop:\n%v", quality, lgwin, err)
					}
				}
			}
		})
	}
}

func shortCodeFilterScalar(ringbuffer []byte, curIxMasked, bestLen, ringBufferMask, maxDistance uint, dc *[4]int) (want uint16, regular bool) {
	d0, d1 := dc[0], dc[1]
	back := [core.NumDistanceShortCodes]uint{
		uint(d0), uint(d1), uint(dc[2]), uint(dc[3]),
		uint(d0 - 1), uint(d0 + 1), uint(d0 - 2), uint(d0 + 2), uint(d0 - 3), uint(d0 + 3),
		uint(d1 - 1), uint(d1 + 1), uint(d1 - 2), uint(d1 + 2), uint(d1 - 3), uint(d1 + 3),
	}
	continuation := ringbuffer[curIxMasked+bestLen]
	regular = true
	for j, backward := range back {
		if backward == 0 || backward > maxDistance {
			regular = false
			continue
		}
		prevIxMasked := (curIxMasked - backward) & ringBufferMask
		if prevIxMasked+bestLen <= ringBufferMask && ringbuffer[prevIxMasked+bestLen] == continuation {
			want |= 1 << j
		}
	}
	return want, regular
}

func TestShortCodeCandidatesEqualsTheScalarContinuationFilterForEveryLanePatternOfBothWindowsAndBothSingleDistances(t *testing.T) {
	const mask = 4095
	mismatch := [4]byte{0x01, 0x80, 0x7F, 0xFF}
	geometries := []struct{ cur, bestLen, d0, d1, d2, d3 uint }{
		{2000, 5, 100, 300, 700, 1000},
		{2000, 1, 300, 100, 1000, 700},
		{2000, 9, 100, 107, 50, 60},
		{2000, 2, 100, 103, 101, 97},
		{3000, 64, 4, 2000, 1, 2003},
	}
	var errs []error
	for _, g := range geometries {
		rb := make([]byte, mask+1)
		const cont = 0xA5
		rb[g.cur+g.bestLen] = cont
		dc := [4]int{int(g.d0), int(g.d1), int(g.d2), int(g.d3)}
		for a := range 128 {
			for b := range 128 {
				for c := range 4 {
					for w, d := range []uint{g.d0, g.d1} {
						lanes := [2]int{a, b}[w]
						base := g.cur - d - 4 + g.bestLen
						rb[base] = cont
						for k := range 7 {
							rb[base+1+uint(k)] = cont ^ mismatch[(a+b+k)&3]
							if lanes>>k&1 != 0 {
								rb[base+1+uint(k)] = cont
							}
						}
					}
					for i, d := range []uint{g.d2, g.d3} {
						rb[g.cur-d+g.bestLen] = cont ^ mismatch[(a+i)&3]
						if c>>i&1 != 0 {
							rb[g.cur-d+g.bestLen] = cont
						}
					}
					rb[g.cur+g.bestLen] = cont
					want, regular := shortCodeFilterScalar(rb, g.cur, g.bestLen, mask, 3000, &dc)
					got := shortCodeCandidates(rb, g.cur, g.bestLen, mask, 3000, &dc, cont)
					if !regular || got != want {
						errs = append(errs, fmt.Errorf("geometry %+v lanes d0=%07b d1=%07b singles=%02b: got %016b, scalar filter %016b (regular=%v)", g, a, b, c, got, want, regular))
					}
				}
			}
		}
	}
	if len(errs) > 0 {
		t.Errorf("the lane tables must map byte k+1 of the 8-byte window loaded at cur-d-4+bestLen to the RFC 7932 short code of distance d+3-k; %d of %d patterns differ, first ones:\n%v", len(errs), len(geometries)*128*128*4, errors.Join(errs[:min(len(errs), 8)]...))
	}
}

func TestShortCodeCandidatesKeepsAllSixteenCandidatesWhenACandidateIsIrregularOrAWindowWrapsAndFiltersExactlyOtherwise(t *testing.T) {
	const mask = 4095
	r := rand.New(rand.NewSource(7))
	rb := make([]byte, mask+1)
	for i := range rb {
		rb[i] = "ab"[r.Intn(2)]
	}
	var errs []error
	checked, filtered := 0, 0
	for _, maxDistance := range []uint{8, 12, 3000} {
		ds := []int{0, 1, 2, 3, 4, 5, 6, 7, 11, 60, int(maxDistance) - 4, int(maxDistance) - 3, int(maxDistance) - 2, int(maxDistance), int(maxDistance) + 1}
		for _, cur := range []uint{0, 3, 8, 11, 12, 13, 100, 2000, 4000, 4094} {
			for _, bestLen := range []uint{1, 2, 7, 64} {
				if cur+bestLen > mask {
					continue
				}
				for _, d0 := range ds {
					for _, d1 := range ds {
						for _, d2 := range []int{0, 1, 5, int(maxDistance), int(maxDistance) + 1} {
							for _, d3 := range []int{0, 2, int(maxDistance), int(maxDistance) + 1} {
								dc := [4]int{d0, d1, d2, d3}
								want, regular := shortCodeFilterScalar(rb, cur, bestLen, mask, maxDistance, &dc)
								wraps := (cur-uint(d0)-4)&mask+bestLen+7 > mask || (cur-uint(d1)-4)&mask+bestLen+7 > mask
								if regular && !wraps {
									filtered++
								} else {
									want = 0xFFFF
								}
								checked++
								if got := shortCodeCandidates(rb, cur, bestLen, mask, maxDistance, &dc, rb[cur+bestLen]); got != want {
									errs = append(errs, fmt.Errorf("cur=%d bestLen=%d maxDistance=%d dc=%v: got %016b, want %016b (regular=%v wraps=%v)", cur, bestLen, maxDistance, dc, got, want, regular, wraps))
								}
							}
						}
					}
				}
			}
		}
	}
	if len(errs) > 0 {
		t.Errorf("a compound or gray-area candidate is invisible to the ring-buffer bytes and a wrapped window is not contiguous, so both must disable the filter, while an eligible geometry must filter exactly; %d of %d cases differ, first ones:\n%v", len(errs), checked, errors.Join(errs[:min(len(errs), 8)]...))
	}
	if filtered == 0 || filtered == checked {
		t.Errorf("%d of %d geometries were eligible for filtering; the grid must exercise both the filtered path and the keep-everything fallback", filtered, checked)
	}
}

func BenchmarkZopfliIterateDistanceCacheCandidates(b *testing.B) {
	const lgwin = 22
	for _, in := range zopfliCorpusInputs(b) {
		if len(in.data) < 128<<10 {
			continue
		}
		for _, quality := range []int{10, 11} {
			b.Run(fmt.Sprintf("%s/q=%d", in.name, quality), func(b *testing.B) {
				var ringbuffer []byte
				var ringBufferMask uint
				var blocks []zopfliTestBlock
				var models []zopfliCostModel
				maxNumBytes := uint(0)
				zopfliStreamBlocks(b, in.data, quality, lgwin, func(rb []byte, mask uint, blk zopfliTestBlock) {
					ringbuffer, ringBufferMask = rb, mask
					maxNumBytes = max(maxNumBytes, blk.numBytes)
					used := 0
					for _, n := range blk.numMatches {
						used += int(n)
					}
					blk.numMatches = append([]uint32(nil), blk.numMatches...)
					blk.matches = append([]backwardMatch(nil), blk.matches[:used]...)
					blocks = append(blocks, blk)
					var model zopfliCostModel
					model.init(64, blk.numBytes)
					model.setFromLiteralCosts(blk.position, rb, mask)
					models = append(models, model)
				})
				if uint(len(in.data)) > ringBufferMask+1 {
					b.Fatalf("%s is larger than the lgwin %d ring buffer, so early blocks were overwritten before the replay", in.name, lgwin)
				}
				for _, impl := range []struct {
					name    string
					iterate func([]zopfliNode, []byte, []int, *zopfliCostModel, []uint32, []backwardMatch, uint, uint, uint, uint, *compoundDictionary, int, int, *matchFeed) uint
				}{{"before", zopfliIterateBefore}, {"after", zopfliIterate}} {
					b.Run("impl="+impl.name, func(b *testing.B) {
						nodes := make([]zopfliNode, maxNumBytes+1)
						b.ReportAllocs()
						b.SetBytes(int64(len(in.data)))
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							for k := range blocks {
								blk := &blocks[k]
								n := nodes[:blk.numBytes+1]
								initZopfliNodes(n)
								impl.iterate(n, ringbuffer, blk.distCache[:], &models[k], blk.numMatches, blk.matches,
									blk.numBytes, blk.position, ringBufferMask, 0, nil, quality, lgwin, nil)
							}
						}
					})
				}
			})
		}
	}
}

type zopfliDPInput struct {
	name string
	data []byte
	dict []byte
}

func loadZopfliCorpus(tb testing.TB) []zopfliDPInput {
	tb.Helper()
	names := []string{"plrabn12.txt", "lcet10.txt", "mapsdatazrh"}
	ins := make([]zopfliDPInput, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join("..", "..", "brotli-ref", "tests", "testdata", name))
		if err != nil {
			tb.Fatal(err)
		}
		ins = append(ins, zopfliDPInput{name: name, data: data})
	}
	return ins
}

func loadZopfliInputs(tb testing.TB) []zopfliDPInput {
	tb.Helper()
	var ins []zopfliDPInput
	for _, name := range []string{"gh_172KB.html", "github_events_2k.json", "github_events_5k.json", "github_events_8k.json", "reactcore_187KB.js"} {
		data, err := os.ReadFile(filepath.Join("../../testdata", name))
		if err != nil {
			tb.Fatal(err)
		}
		ins = append(ins,
			zopfliDPInput{name: name, data: data},
			zopfliDPInput{name: name + "_last_90pct_with_first_20pct_as_compound_dictionary", data: data[len(data)/10:], dict: data[:len(data)/5]},
		)
	}
	for _, in := range loadZopfliCorpus(tb) {
		ins = append(ins, zopfliDPInput{name: in.name + "_first_256KiB_like_one_input_block", data: in.data[:min(len(in.data), 1<<18)]})
	}
	text := bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog. "), 2000)
	for _, n := range []int{4, 5, 40, 151, 326, 4096} {
		ins = append(ins, zopfliDPInput{name: fmt.Sprintf("text_%d", n), data: text[:n]})
	}
	return append(ins,
		zopfliDPInput{name: "zeros_100000", data: make([]byte, 100000)},
		zopfliDPInput{name: "period7_100000", data: bytes.Repeat([]byte("abcdefg"), 100000/7)},
		zopfliDPInput{name: "random_100000", data: pseudoRandomBytesRT(100000, 7)},
	)
}

type zopfliDPFixture struct {
	ringbuffer []byte
	mask       uint
	numBytes   uint
	lgwin      int
	quality    int
	compound   *compoundDictionary
	numMatches []uint32
	matches    []backwardMatch
}

func newZopfliDPFixture(tb testing.TB, in zopfliDPInput, lgwin, quality int) *zopfliDPFixture {
	tb.Helper()
	numBytes := uint(len(in.data))
	mask := uint(1)<<bits.Len(numBytes) - 1
	ringbuffer := make([]byte, mask+1+7)
	copy(ringbuffer, in.data)
	compound := &compoundDictionary{}
	shadowMatches := uint(0)
	if in.dict != nil {
		if err := compound.attach(newPreparedDictionary(in.dict)); err != nil {
			tb.Fatal(err)
		}
		shadowMatches = h10MaxNumMatches + 128
	}
	hasher := &h10{lgwin: lgwin, quality: quality}
	hasher.reset(true, numBytes, nil)
	bufs := &q10Bufs{hqNumMatchesArr: make([]uint32, numBytes)}
	storeEnd := uint(0)
	if numBytes >= h10MaxTreeCompLength {
		storeEnd = numBytes - h10MaxTreeCompLength + 1
	}
	col := &hqCollector{
		bufs:             bufs,
		hasher:           hasher,
		compound:         compound,
		ringbuffer:       ringbuffer,
		numBytes:         numBytes,
		ringBufferMask:   mask,
		maxBackwardLimit: (uint(1) << lgwin) - core.WindowGap,
		gap:              compound.totalSize,
		storeEnd:         storeEnd,
		shadowMatches:    shadowMatches,
		quality:          quality,
	}
	col.collect()
	return &zopfliDPFixture{ringbuffer, mask, numBytes, lgwin, quality, compound, bufs.hqNumMatchesArr, bufs.hqMatches}
}

func nodeDivergence(nodes [][]zopfliNode, i uint) error {
	for r := 1; r < len(nodes); r++ {
		if nodes[r][i] != nodes[0][i] {
			return fmt.Errorf("node %d is %+v after the rewritten updateNodes but %+v after the original when the DP reaches it; "+
				"the node is final at that point, so the two implementations chose a different cheapest command into it", i, nodes[r][i], nodes[0][i])
		}
	}
	return nil
}

func zopfliIterateLockstep(f *zopfliDPFixture, model *zopfliCostModel, distCache []int, impls []updateNodesFunc, nodes [][]zopfliNode) error {
	maxBackwardLimit := (uint(1) << f.lgwin) - core.WindowGap
	maxZopfli := maxZopfliLen(f.quality)
	var queues [2]startPosQueue
	var skips [2]uint
	for r := range impls {
		initZopfliNodes(nodes[r])
		nodes[r][0].length = 0
		nodes[r][0].setCost(0)
	}
	curMatchPos := uint(0)
	for i := uint(0); i+3 < f.numBytes; i++ {
		if err := nodeDivergence(nodes, i); err != nil {
			return err
		}
		for r, update := range impls {
			skips[r] = update(nodes[r], f.ringbuffer, distCache, f.matches[curMatchPos:], model, &queues[r],
				f.numBytes, 0, i, f.mask, maxBackwardLimit, f.compound.totalSize, f.compound, uint(f.numMatches[i]), f.quality)
		}
		if len(impls) > 1 && skips[1] != skips[0] {
			return fmt.Errorf("position %d: the rewritten updateNodes returned longest update %d, the original %d; "+
				"the DP skip-ahead and the q10 divergence check read this value", i, skips[1], skips[0])
		}
		skip := skips[0]
		if skip < longCopyQuickStep {
			skip = 0
		}
		curMatchPos += uint(f.numMatches[i])
		if f.numMatches[i] == 1 && f.matches[curMatchPos-1].matchLength() > maxZopfli {
			skip = max(f.matches[curMatchPos-1].matchLength(), skip)
		}
		if skip > 1 {
			skip--
			for skip > 0 {
				i++
				if i+3 >= f.numBytes {
					break
				}
				if err := nodeDivergence(nodes, i); err != nil {
					return err
				}
				for r := range impls {
					evaluateNode(nodes[r], i, 0, maxBackwardLimit, f.compound.totalSize, distCache, model, &queues[r])
				}
				curMatchPos += uint(f.numMatches[i])
				skip--
			}
		}
	}
	for r := 1; r < len(impls); r++ {
		if !slices.Equal(nodes[r], nodes[0]) {
			return fmt.Errorf("the final node arrays differ, so the backtrace would emit different commands")
		}
	}
	return nil
}

func TestUpdateNodesWithFloatCopyExtraPointerNodeWritesHoistedShortCodesAndSplitDictionaryLoopMatchesTheOriginalAtEveryDPStep(t *testing.T) {
	for _, in := range loadZopfliInputs(t) {
		for _, lgwin := range []int{16, 22} {
			for _, quality := range []int{10, 11} {
				t.Run(fmt.Sprintf("%s/lgwin=%d/q=%d", in.name, lgwin, quality), func(t *testing.T) {
					t.Parallel()
					impls := []updateNodesFunc{updateNodesBefore, updateNodesWithScratch(new(dcScratch))}
					f := newZopfliDPFixture(t, in, lgwin, quality)
					nodes := [][]zopfliNode{make([]zopfliNode, f.numBytes+1), make([]zopfliNode, f.numBytes+1)}
					distCache := []int{4, 11, 15, 16}
					var model zopfliCostModel
					model.init(64, f.numBytes)
					model.setFromLiteralCosts(0, f.ringbuffer, f.mask)
					if err := zopfliIterateLockstep(f, &model, distCache, impls, nodes); err != nil {
						t.Fatalf("first pass (literal-cost model): %v", err)
					}
					if quality < hqZopflificationQuality {
						return
					}
					computeShortestPathFromNodes(nodes[0], f.numBytes)
					var commands []command
					var lastInsertLen, numLiterals uint
					scratchDistCache := slices.Clone(distCache)
					zopfliCreateCommands(nodes[0], f.numBytes, 0, (uint(1)<<lgwin)-core.WindowGap, f.compound.totalSize, scratchDistCache, &lastInsertLen, &commands, &numLiterals)
					model.setFromCommands(0, f.ringbuffer, f.mask, commands, 0)
					if err := zopfliIterateLockstep(f, &model, distCache, impls, nodes); err != nil {
						t.Fatalf("second pass (cost model from the first pass's %d commands): %v", len(commands), err)
					}
				})
			}
		}
	}
}

func BenchmarkUpdateNodesDP(b *testing.B) {
	impls := []struct {
		name string
		fn   updateNodesFunc
	}{
		{"before", updateNodesBefore},
		{"after", updateNodesWithScratch(new(dcScratch))},
	}
	corpus := loadZopfliCorpus(b)
	for _, in := range corpus {
		for _, quality := range []int{10, 11} {
			f := newZopfliDPFixture(b, in, 22, quality)
			nodes := [][]zopfliNode{make([]zopfliNode, f.numBytes+1)}
			var model zopfliCostModel
			model.init(64, f.numBytes)
			model.setFromLiteralCosts(0, f.ringbuffer, f.mask)
			distCache := []int{4, 11, 15, 16}
			for _, impl := range impls {
				b.Run(fmt.Sprintf("q%d_%s/impl=%s", quality, in.name, impl.name), func(b *testing.B) {
					fn := []updateNodesFunc{impl.fn}
					b.ReportAllocs()
					b.SetBytes(int64(len(in.data)))
					for i := 0; i < b.N; i++ {
						if err := zopfliIterateLockstep(f, &model, distCache, fn, nodes); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}

var copyExtraBefore = [24]uint32{
	0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 2, 2,
	3, 3, 4, 4, 5, 5, 6, 7, 8, 9, 10, 24,
}

func updateZopfliNodeBefore(nodes []zopfliNode, pos, startPos, length, lenCode, dist, shortCode uint, cost float32) {
	next := &nodes[pos+length]
	next.length = uint32(length | ((length + 9 - lenCode) << 25))
	next.distance = uint32(dist)
	next.dcodeInsertLength = uint32((shortCode << 27) | (pos - startPos))
	next.setCost(cost)
}

func updateNodesBefore(nodes []zopfliNode, ringbuffer []byte, startingDistCache []int, matches []backwardMatch, model *zopfliCostModel, queue *startPosQueue, numBytes, blockStart, pos, ringBufferMask, maxBackwardLimit, gap uint, compound *compoundDictionary, numMatches uint, quality int) uint {
	curIx := blockStart + pos
	curIxMasked := curIx & ringBufferMask
	maxDistance := min(curIx, maxBackwardLimit)
	maxDistanceGap := maxDistance + gap
	hasCompound := compound != nil && compound.numChunks > 0
	maxLen := numBytes - pos
	maxZopfli := maxZopfliLen(quality)
	maxIters := maxZopfliCandidates(quality)

	_ = ringbuffer[ringBufferMask]
	_ = nodes[numBytes]

	evaluateNode(nodes, pos, blockStart, maxBackwardLimit, gap, startingDistCache, model, queue)

	var minLen uint
	{
		pd := queue.at(0)
		minCost := pd.cost + model.getMinCostCmd() +
			model.getLiteralCosts(pd.pos, pos)
		minLen = computeMinimumCopyLength(nodes, pos, numBytes, minCost)
	}

	result := uint(0)

	nodesAtPos := nodes[pos:]

	for k := uint(0); k < maxIters && k < queue.size(); k++ {
		pd := queue.at(k)
		start := pd.pos
		insCode := getInsertLenCode(pos - start)
		startCostdiff := pd.costdiff
		baseCost := startCostdiff + float32(insertExtra[insCode]) +
			model.getLiteralCosts(0, pos)

		dc := &pd.distanceCache
		d0, d1 := dc[0], dc[1]
		back := [core.NumDistanceShortCodes]uint{
			uint(d0), uint(d1), uint(dc[2]), uint(dc[3]),
			uint(d0 - 1), uint(d0 + 1), uint(d0 - 2), uint(d0 + 2), uint(d0 - 3), uint(d0 + 3),
			uint(d1 - 1), uint(d1 + 1), uint(d1 - 2), uint(d1 + 2), uint(d1 - 3), uint(d1 + 3),
		}
		bestLen := minLen - 1
		if curIxMasked+bestLen <= ringBufferMask {
			continuation := ringbuffer[curIxMasked+bestLen]
			for j := uint(0); j < core.NumDistanceShortCodes && bestLen < maxLen; j++ {
				backward := back[j]
				if backward == 0 || backward > maxDistanceGap {
					continue
				}
				var length uint
				switch {
				case backward <= maxDistance:
					prevIxMasked := (curIxMasked - backward) & ringBufferMask
					if prevIxMasked+bestLen > ringBufferMask ||
						continuation != ringbuffer[prevIxMasked+bestLen] {
						continue
					}
					length = uint(matchLenAt(ringbuffer, prevIxMasked, curIxMasked, int(maxLen)))
				case hasCompound:
					d := 0
					offset := maxDistance + 1 + compound.totalSize - 1
					for offset >= backward+compound.chunkOffsets[d+1] {
						d++
					}
					source := compound.chunkSource[d]
					offset = offset - compound.chunkOffsets[d] - backward
					limit := min(compound.chunkOffsets[d+1]-compound.chunkOffsets[d]-offset, maxLen)
					if bestLen >= limit || continuation != source[offset+bestLen] {
						continue
					}
					length = uint(matchLen(
						source[offset:],
						ringbuffer[curIxMasked:],
						int(limit),
					))
				default:
					continue
				}

				distCost := baseCost + model.distanceCost(j)
				_ = nodesAtPos[length]
				for l := bestLen + 1; l <= length; l++ {
					copyCode := getCopyLenCode(l)
					cmdCode := combineLengthCodes(insCode, copyCode, j == 0)
					cost := baseCost
					if cmdCode >= 128 {
						cost = distCost
					}
					cost = (cost + float32(copyExtraBefore[copyCode])) + model.commandCost(cmdCode)
					if cost < nodesAtPos[l].cost() {
						updateZopfliNodeBefore(nodes, pos, start, l, l, backward, j+1, cost)
						if l > result {
							result = l
						}
					}
				}
				if length > bestLen {
					bestLen = length
					if curIxMasked+bestLen > ringBufferMask {
						break
					}
					continuation = ringbuffer[curIxMasked+bestLen]
				}
			}
		}

		if k >= 2 {
			continue
		}

		matchLen := minLen
		for j := range numMatches {
			match := matches[j]
			dist := uint(match.distance)
			isDictionaryMatch := dist > maxDistanceGap
			distCode := dist + core.NumDistanceShortCodes - 1
			distSymbol, distExtra := prefixEncodeSimpleDistance(distCode)
			distNumExtra := distSymbol >> 10
			distCost := baseCost + float32(distNumExtra) +
				model.distanceCost(uint(distSymbol&0x3FF))

			maxMatchLen := match.matchLength()
			if matchLen < maxMatchLen && (isDictionaryMatch || maxMatchLen > maxZopfli) {
				matchLen = maxMatchLen
			}
			_ = nodesAtPos[maxMatchLen]
			for ; matchLen <= maxMatchLen; matchLen++ {
				lenCode := matchLen
				if isDictionaryMatch {
					lenCode = match.matchLengthCode()
				}
				copyCode := getCopyLenCode(lenCode)
				cmdCode := combineLengthCodes(insCode, copyCode, false)
				cost := distCost + float32(copyExtraBefore[copyCode]) +
					model.commandCost(cmdCode)
				if cost < nodesAtPos[matchLen].cost() {
					updateZopfliNodeBefore(nodes, pos, start, matchLen, lenCode, dist, 0, cost)
					if matchLen > result {
						result = matchLen
					}
				}
			}
			_ = distExtra
		}
	}
	return result
}
