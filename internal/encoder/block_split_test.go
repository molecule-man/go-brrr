package encoder

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

func TestContextBlockSplitterBasic(t *testing.T) {
	// Create a simple context block splitter with 2 contexts and feed it
	// symbols to verify it produces a valid block split.
	var split blockSplit
	var b0 splitBufs
	cs := newContextBlockSplitter(&split, 256, 2, 4, 400.0, 100, &b0)

	// Feed 20 symbols alternating between contexts 0 and 1.
	for i := range 20 {
		cs.addSymbolBefore(i%10, i%2)
	}
	cs.finishBlock(true)

	if split.numTypes < 1 {
		t.Errorf("numTypes = %d, want >= 1", split.numTypes)
	}
	if len(split.types) == 0 {
		t.Error("types is empty")
	}
	if len(split.lengths) == 0 {
		t.Error("lengths is empty")
	}

	// Total block lengths should be at least 20 (the splitter pads the
	// final block to minBlockSize when blockSize < minBlockSize, matching
	// the C reference behavior).
	var total uint32
	for _, l := range split.lengths {
		total += l
	}
	if total < 20 {
		t.Errorf("total block lengths = %d, want >= 20", total)
	}
}

func TestContextBlockSplitterMaxBlockTypes(t *testing.T) {
	// With numContexts=2, maxBlockTypes should be 128 (256/2).
	var split blockSplit
	var b0 splitBufs
	cs := newContextBlockSplitter(&split, 256, 2, 4, 400.0, 100, &b0)
	if cs.maxBlockTypes != 128 {
		t.Errorf("maxBlockTypes = %d, want 128", cs.maxBlockTypes)
	}

	cs2 := newContextBlockSplitter(&split, 256, 13, 4, 400.0, 100, &b0)
	if cs2.maxBlockTypes != 256/13 {
		t.Errorf("maxBlockTypes = %d, want %d", cs2.maxBlockTypes, 256/13)
	}
}

func TestBuildMetaBlockGreedyWithContext(t *testing.T) {
	// Build a metablock with context modeling and verify the output.
	data := []byte("Hello, World! This is a test of context-aware block splitting in brotli. " +
		"The quick brown fox jumps over the lazy dog. Pack my box with five dozen liquor jugs.")
	// Pad to power of 2.
	buf := make([]byte, 256)
	copy(buf, data)
	mask := uint(len(buf) - 1)

	// Create a simple command sequence: one big insert.
	commands := []command{
		{insertLen: uint32(len(data)), copyLen: 0, cmdPrefix: 10},
	}

	var bufs splitBufs
	var mb metaBlockSplit

	buildMetaBlockGreedy(buf, 0, mask, 0, 0,
		2, staticContextMapSimpleUTF8[:],
		commands, len(data), &bufs, &mb)

	if mb.litSplit.numTypes < 1 {
		t.Errorf("litSplit.numTypes = %d, want >= 1", mb.litSplit.numTypes)
	}
	if mb.literalContextMap == nil {
		t.Error("literalContextMap is nil, want non-nil for context modeling")
	}
	if len(mb.literalContextMap) != mb.litSplit.numTypes*(1<<core.LiteralContextBits) {
		t.Errorf("literalContextMap length = %d, want %d",
			len(mb.literalContextMap), mb.litSplit.numTypes*(1<<core.LiteralContextBits))
	}
	// Verify context map entries are valid.
	maxHisto := uint32(mb.litSplit.numTypes * 2) // 2 contexts
	for i, v := range mb.literalContextMap {
		if v >= maxHisto {
			t.Errorf("literalContextMap[%d] = %d, exceeds max %d", i, v, maxHisto-1)
		}
	}
}

func TestBuildMetaBlockGreedySingleContext(t *testing.T) {
	// With numContexts=1, should produce nil context map (same as Q4 path).
	data := []byte("abcdefghijklmnopqrstuvwxyz")
	buf := make([]byte, 32)
	copy(buf, data)
	mask := uint(len(buf) - 1)

	commands := []command{
		{insertLen: uint32(len(data)), copyLen: 0, cmdPrefix: 10},
	}

	var bufs splitBufs
	var mb metaBlockSplit

	buildMetaBlockGreedy(buf, 0, mask, 0, 0,
		1, nil, commands, len(data), &bufs, &mb)

	if mb.literalContextMap != nil {
		t.Error("literalContextMap should be nil for single-context path")
	}
	if mb.litSplit.numTypes < 1 {
		t.Errorf("litSplit.numTypes = %d, want >= 1", mb.litSplit.numTypes)
	}
}

func TestMapStaticContexts(t *testing.T) {
	var mb metaBlockSplit
	mb.litSplit.numTypes = 2

	mapStaticContexts(&mb, 2, staticContextMapSimpleUTF8[:])

	wantLen := 2 * (1 << core.LiteralContextBits) // 2 * 64 = 128
	if len(mb.literalContextMap) != wantLen {
		t.Fatalf("literalContextMap length = %d, want %d", len(mb.literalContextMap), wantLen)
	}

	// For block type 0: entries should be offset 0 + staticContextMap[j].
	// For block type 1: entries should be offset 2 + staticContextMap[j].
	for j := range 64 {
		want0 := staticContextMapSimpleUTF8[j]
		got0 := mb.literalContextMap[j]
		if got0 != want0 {
			t.Errorf("literalContextMap[0<<6 + %d] = %d, want %d", j, got0, want0)
		}

		want1 := 2 + staticContextMapSimpleUTF8[j]
		got1 := mb.literalContextMap[64+j]
		if got1 != want1 {
			t.Errorf("literalContextMap[1<<6 + %d] = %d, want %d", j, got1, want1)
		}
	}
}

func (cs *contextBlockSplitter) addSymbolBefore(symbol, context int) {
	idx := (cs.currHistogramIdx + context) * cs.alphabetSize
	cs.histograms[idx+symbol]++
	cs.blockSize++
	if cs.blockSize == cs.targetBlockSize {
		cs.finishBlock(false)
	}
}

func buildMetaBlockGreedyBefore(
	ringbuffer []byte, pos, mask uint,
	prevByte, prevByte2 byte,
	numContexts uint, staticContextMap []uint32,
	commands []command,
	bufs *splitBufs, mb *metaBlockSplit,
) {
	var numLiterals int
	for i := range commands {
		numLiterals += int(commands[i].insertLen)
	}

	var cmdSplitter, distSplitter blockSplitter
	cmdSplitter, bufs.cmdHistograms, bufs.cmdTypes, bufs.cmdLengths =
		newBlockSplitter(&mb.cmdSplit, core.AlphabetSizeInsertAndCopyLength, 1024, 500.0,
			len(commands), bufs.cmdHistograms, bufs.cmdTypes, bufs.cmdLengths)
	distSplitter, bufs.distHistograms, bufs.distTypes, bufs.distLengths =
		newBlockSplitter(&mb.distSplit, 64, 512, 100.0,
			len(commands), bufs.distHistograms, bufs.distTypes, bufs.distLengths)

	if numContexts <= 1 {
		mb.literalContextMap = mb.literalContextMap[:0]

		var litSplitter blockSplitter
		litSplitter, bufs.litHistograms, bufs.litTypes, bufs.litLengths =
			newBlockSplitter(&mb.litSplit, core.AlphabetSizeLiteral, 512, 400.0,
				numLiterals, bufs.litHistograms, bufs.litTypes, bufs.litLengths)

		for i := range commands {
			cmd := commands[i]
			cmdSplitter.addSymbol(int(cmd.cmdPrefix))
			for j := cmd.insertLen; j != 0; j-- {
				litSplitter.addSymbol(int(ringbuffer[pos&mask]))
				pos++
			}
			copyLen := cmd.copyLength()
			pos += uint(copyLen)
			if copyLen != 0 && cmd.cmdPrefix >= 128 {
				distSplitter.addSymbol(int(cmd.distPrefixCode()))
			}
		}

		litSplitter.finishBlock(true)
		mb.litHistograms = litSplitter.histograms[:litSplitter.histogramsSize*core.AlphabetSizeLiteral]
	} else {
		ctxSplitter := newContextBlockSplitter(
			&mb.litSplit, core.AlphabetSizeLiteral, int(numContexts), 512, 400.0, numLiterals, bufs)
		utf8LUT := uint(core.ContextUTF8) << 9

		for i := range commands {
			cmd := commands[i]
			cmdSplitter.addSymbol(int(cmd.cmdPrefix))
			for j := cmd.insertLen; j != 0; j-- {
				literal := ringbuffer[pos&mask]
				context := staticContextMap[core.ContextLookupTable[utf8LUT+uint(prevByte)]|core.ContextLookupTable[utf8LUT+256+uint(prevByte2)]]
				ctxSplitter.addSymbolBefore(int(literal), int(context))
				prevByte2 = prevByte
				prevByte = literal
				pos++
			}
			copyLen := cmd.copyLength()
			pos += uint(copyLen)
			if copyLen != 0 {
				prevByte2 = ringbuffer[(pos-2)&mask]
				prevByte = ringbuffer[(pos-1)&mask]
				if cmd.cmdPrefix >= 128 {
					distSplitter.addSymbol(int(cmd.distPrefixCode()))
				}
			}
		}

		ctxSplitter.finishBlock(true)
		mb.litHistograms = ctxSplitter.histograms[:ctxSplitter.histogramsSize*core.AlphabetSizeLiteral]

		mapStaticContexts(mb, numContexts, staticContextMap)
	}

	cmdSplitter.finishBlock(true)
	distSplitter.finishBlock(true)

	mb.cmdHistograms = cmdSplitter.histograms[:cmdSplitter.histogramsSize*core.AlphabetSizeInsertAndCopyLength]
	mb.distHistograms = distSplitter.histograms[:distSplitter.histogramsSize*64]
}

type greedyMetaBlockInput struct {
	ringbuffer          []byte
	pos, mask           uint
	prevByte, prevByte2 byte
	numContexts         uint
	staticContextMap    []uint32
	commands            []command
	numLiterals         int
}

func (in *greedyMetaBlockInput) buildBefore(bufs *splitBufs, mb *metaBlockSplit) {
	buildMetaBlockGreedyBefore(in.ringbuffer, in.pos, in.mask, in.prevByte, in.prevByte2,
		in.numContexts, in.staticContextMap, in.commands, bufs, mb)
}

func (in *greedyMetaBlockInput) buildAfter(bufs *splitBufs, mb *metaBlockSplit) {
	buildMetaBlockGreedy(in.ringbuffer, in.pos, in.mask, in.prevByte, in.prevByte2,
		in.numContexts, in.staticContextMap, in.commands, in.numLiterals, bufs, mb)
}

type greedyMetaBlockRecorder struct {
	*encoderSplit
	inputs []greedyMetaBlockInput
}

func (r *greedyMetaBlockRecorder) encodeData(isLast, forceFlush bool) []byte {
	e := r.encoderSplit
	if e.hasher == nil {
		e.chooseHasher(isLast)
	}
	metablockSize, earlyResult, ready := e.prepareMetaBlock(isLast, forceFlush)
	if !ready {
		return earlyResult
	}
	s := &e.encodeState
	in := greedyMetaBlockInput{
		ringbuffer:  bytes.Clone(s.data),
		pos:         wrapPosition(s.lastFlushPos),
		mask:        uint(s.mask),
		prevByte:    s.prevByte,
		prevByte2:   s.prevByte2,
		numContexts: 1,
		commands:    slices.Clone(s.commands),
		numLiterals: int(s.numLiterals),
	}
	if s.quality >= 5 {
		in.numContexts, in.staticContextMap = decideOverLiteralContextModeling(
			s.data, in.pos, in.mask, uint(metablockSize), s.quality, s.sizeHint)
	}
	r.inputs = append(r.inputs, in)
	e.initMetaBlockOutput(metablockSize)
	e.writeMetaBlockInternal(int(metablockSize), int(e.numLiterals), int(e.numCommands), isLast)
	return e.finishMetaBlock()
}

func recordGreedyMetaBlockInputs(tb testing.TB, data []byte, quality, lgwin int) []greedyMetaBlockInput {
	tb.Helper()
	r := &greedyMetaBlockRecorder{encoderSplit: NewCompressor(quality, lgwin, uint(len(data)), false).(*encoderSplit)}
	defer r.Release()
	if _, err := streamWrite(r, io.Discard, data); err != nil {
		tb.Fatal(err)
	}
	if err := streamClose(r, io.Discard); err != nil {
		tb.Fatal(err)
	}
	return r.inputs
}

func diffMetaBlockSplit(got, want *metaBlockSplit) error {
	var errs []error
	for _, c := range []struct {
		name      string
		got, want []uint32
	}{
		{"litHistograms", got.litHistograms, want.litHistograms},
		{"cmdHistograms", got.cmdHistograms, want.cmdHistograms},
		{"distHistograms", got.distHistograms, want.distHistograms},
		{"literalContextMap", got.literalContextMap, want.literalContextMap},
		{"litSplit.lengths", got.litSplit.lengths, want.litSplit.lengths},
		{"cmdSplit.lengths", got.cmdSplit.lengths, want.cmdSplit.lengths},
		{"distSplit.lengths", got.distSplit.lengths, want.distSplit.lengths},
	} {
		if !slices.Equal(c.got, c.want) {
			errs = append(errs, fmt.Errorf("%s differs (%d vs %d entries)", c.name, len(c.got), len(c.want)))
		}
	}
	for _, c := range []struct {
		name      string
		got, want *blockSplit
	}{
		{"litSplit", &got.litSplit, &want.litSplit},
		{"cmdSplit", &got.cmdSplit, &want.cmdSplit},
		{"distSplit", &got.distSplit, &want.distSplit},
	} {
		if !bytes.Equal(c.got.types, c.want.types) {
			errs = append(errs, fmt.Errorf("%s.types differs (%d vs %d blocks)", c.name, len(c.got.types), len(c.want.types)))
		}
		if c.got.numTypes != c.want.numTypes {
			errs = append(errs, fmt.Errorf("%s.numTypes = %d, want %d", c.name, c.got.numTypes, c.want.numTypes))
		}
	}
	return errors.Join(errs...)
}

func compareGreedyBuilds(inputs []greedyMetaBlockInput, got, want *metaBlockSplit) error {
	var gotBufs, wantBufs splitBufs
	var errs []error
	for i := range inputs {
		in := &inputs[i]
		literals := 0
		for _, cmd := range in.commands {
			literals += int(cmd.insertLen)
		}
		if in.numLiterals != literals {
			errs = append(errs, fmt.Errorf("metablock %d: the encoder counted %d literals but its commands insert %d, "+
				"and that count sizes the literal splitter's histograms and block arrays", i, in.numLiterals, literals))
		}
		in.buildBefore(&wantBufs, want)
		in.buildAfter(&gotBufs, got)
		if err := diffMetaBlockSplit(got, want); err != nil {
			errs = append(errs, fmt.Errorf("metablock %d (%d commands, %d literals, %d contexts): %w",
				i, len(in.commands), literals, in.numContexts, err))
		}
	}
	return errors.Join(errs...)
}

func greedyEquivalenceFiles(tb testing.TB) []string {
	tb.Helper()
	var paths []string
	for _, pattern := range []string{"../../testdata/*.html", "../../testdata/*.js", "../../testdata/*.json", "../../brotli-ref/tests/testdata/*.txt"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			tb.Fatal(err)
		}
		paths = append(paths, matches...)
	}
	return paths
}

func TestBuildMetaBlockGreedyCountsEveryLiteralRunExactlyLikeThePerLiteralSplitterOnRecordedEncoderMetaBlocks(t *testing.T) {
	for _, path := range greedyEquivalenceFiles(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for quality := 4; quality <= 9; quality++ {
			for _, lgwin := range []int{16, 22} {
				t.Run(fmt.Sprintf("%s/q%d/lgwin%d", filepath.Base(path), quality, lgwin), func(t *testing.T) {
					var got, want metaBlockSplit
					if err := compareGreedyBuilds(recordGreedyMetaBlockInputs(t, data, quality, lgwin), &got, &want); err != nil {
						t.Fatalf("the batched literal loop must feed the splitters the same histograms and hit finishBlock "+
							"at the same symbol counts as one addSymbol per literal, or the block split and the "+
							"encoded bytes change:\n%v", err)
					}
				})
			}
		}
	}
}

func syntheticGreedyCommands(rng *rand.Rand, count int) []command {
	commands := make([]command, count)
	for i := range commands {
		var insertLen uint
		switch r := rng.IntN(20); {
		case r < 4:
			insertLen = 0
		case r < 12:
			insertLen = 1 + uint(rng.IntN(8))
		case r < 17:
			insertLen = 9 + uint(rng.IntN(100))
		case r < 19:
			insertLen = 109 + uint(rng.IntN(1500))
		default:
			insertLen = 1609 + uint(rng.IntN(5000))
		}
		if rng.IntN(10) == 0 {
			commands[i] = newInsertCommand(insertLen)
		} else {
			commands[i] = newCommandSimpleDist(insertLen, 2+uint(rng.IntN(300)), 0, uint(rng.IntN(1<<16)))
		}
	}
	return commands
}

func greedyInputFromCommands(ringbuffer []byte, pos uint, prevByte, prevByte2 byte, commands []command) greedyMetaBlockInput {
	literals := 0
	for _, cmd := range commands {
		literals += int(cmd.insertLen)
	}
	return greedyMetaBlockInput{
		ringbuffer:  ringbuffer,
		pos:         pos,
		mask:        uint(len(ringbuffer) - 1),
		prevByte:    prevByte,
		prevByte2:   prevByte2,
		commands:    commands,
		numLiterals: literals,
	}
}

func TestBuildMetaBlockGreedyCountsEveryLiteralRunExactlyLikeThePerLiteralSplitterAcrossRingWrapsBlockEdgesAndTypeLimits(t *testing.T) {
	html, err := os.ReadFile("../../testdata/gh_172KB.html")
	if err != nil {
		t.Fatal(err)
	}
	js, err := os.ReadFile("../../testdata/reactcore_187KB.js")
	if err != nil {
		t.Fatal(err)
	}

	contextMaps := []struct {
		name        string
		numContexts uint
		contextMap  []uint32
	}{
		{"1ctx", 1, nil},
		{"2ctx", 2, staticContextMapSimpleUTF8[:]},
		{"3ctx", 3, staticContextMapContinuation[:]},
		{"13ctx", 13, staticContextMapComplexUTF8[:]},
	}
	check := func(name string, in greedyMetaBlockInput, hitsTypeLimit bool) {
		for _, cm := range contextMaps {
			in.numContexts = cm.numContexts
			in.staticContextMap = cm.contextMap
			t.Run(name+"/"+cm.name, func(t *testing.T) {
				var got, want metaBlockSplit
				if err := compareGreedyBuilds([]greedyMetaBlockInput{in, in}, &got, &want); err != nil {
					t.Fatalf("the batched literal loop must split runs at the ring end and at every block boundary "+
						"without changing which histogram each literal lands in:\n%v", err)
				}
				if limit := maxNumberOfBlockTypes / int(cm.numContexts); hitsTypeLimit && want.litSplit.numTypes != limit {
					t.Fatalf("fixture produced %d literal block types; it exists to drive the splitter into its %d-type "+
						"limit, so the capped branch is no longer covered", want.litSplit.numTypes, limit)
				}
			})
		}
	}

	check("first_insert_fills_the_block_exactly_at_the_ring_end", greedyInputFromCommands(html[:1024], 512, 'a', 'b', []command{
		newInsertCommand(512),
		newCommandSimpleDist(700, 4, 0, 40),
		newInsertCommand(0),
		newInsertCommand(3),
		newCommandSimpleDist(1, 9, 0, 0),
		newCommandSimpleDist(2000, 5, 0, 900),
	}), false)
	check("one_insert_wraps_the_ring_many_times", greedyInputFromCommands(js[:4096], 4000, 0xe2, 0x82, []command{
		newInsertCommand(50000),
		newCommandSimpleDist(7, 3, 0, 12),
	}), false)

	newSymbolSetEvery512 := make([]byte, 1<<18)
	for i := range newSymbolSetEvery512 {
		block := i / 512
		newSymbolSetEvery512[i] = byte(block*7 + (i%3)*(1+block%5))
	}
	check("a_new_symbol_set_every_512_literals_exhausts_the_block_types", greedyInputFromCommands(newSymbolSetEvery512, 0, 0, 0, []command{
		newCommandSimpleDist(300*512+17, 2, 0, 1),
		newInsertCommand(512),
	}), true)

	for seed := range uint64(4) {
		rng := rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))
		src := html
		if seed%2 == 1 {
			src = js
		}
		for _, size := range []int{1 << 12, 1 << 16} {
			off := rng.IntN(len(src) - size)
			check(fmt.Sprintf("random_commands_seed%d_ring%d", seed, size),
				greedyInputFromCommands(src[off:off+size], uint(rng.IntN(1<<20)), byte(rng.IntN(256)), byte(rng.IntN(256)),
					syntheticGreedyCommands(rng, 600)), false)
		}
	}
}

func BenchmarkBuildMetaBlockGreedy(b *testing.B) {
	for _, bc := range []struct {
		name    string
		file    string
		quality int
	}{
		{"q4_plrabn12", "plrabn12.txt", 4},
		{"q5_plrabn12", "plrabn12.txt", 5},
		{"q4_mapsdatazrh", "mapsdatazrh", 4},
		{"q5_mapsdatazrh", "mapsdatazrh", 5},
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", "brotli-ref", "tests", "testdata", bc.file))
		if err != nil {
			b.Fatal(err)
		}
		inputs := recordGreedyMetaBlockInputs(b, data, bc.quality, 22)
		for _, impl := range []struct {
			name  string
			build func(*greedyMetaBlockInput, *splitBufs, *metaBlockSplit)
		}{
			{"before", (*greedyMetaBlockInput).buildBefore},
			{"after", (*greedyMetaBlockInput).buildAfter},
		} {
			b.Run(bc.name+"/impl="+impl.name, func(b *testing.B) {
				var bufs splitBufs
				var mb metaBlockSplit
				b.ReportAllocs()
				b.SetBytes(int64(len(data)))
				for i := 0; i < b.N; i++ {
					for j := range inputs {
						impl.build(&inputs[j], &bufs, &mb)
					}
				}
			})
		}
	}
}
