package encoder

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

func preallocHistogramPairsBefore(s []histogramPair, n int) []histogramPair {
	if cap(s) < n {
		return make([]histogramPair, 0, n)
	}
	return s
}

func (b *q10Bufs) preallocQ10Before(blockSizeArg int) {
	blockSize := 2 * blockSizeArg
	const (
		maxLitHist     = 100
		maxCmdHist     = 50
		maxDistHist    = 50
		maxAlpha       = core.AlphabetSizeInsertAndCopyLength
		litAlpha       = core.AlphabetSizeLiteral
		distAlpha      = core.NumHistogramDistanceSymbols
		hpb            = 64
		cpb            = 16
		maxPairs       = hpb*hpb/2 + 1
		maxBlockTypes  = maxNumberOfBlockTypes
		litContextMul  = 64
		distContextMul = 4
	)

	estBlocks := min(blockSize/64+1, 4096)
	estClusters := cpb * (estBlocks + hpb - 1) / hpb

	b.svHistograms = preallocUint32(b.svHistograms, (maxLitHist+1)*maxAlpha)
	b.svBlockIDs = preallocByte(b.svBlockIDs, blockSize)
	b.svFloat = preallocFloat64(b.svFloat, maxAlpha*maxLitHist+maxLitHist)
	bitmapLen := (maxLitHist + 7) >> 3
	b.svSwitchSig = preallocByte(b.svSwitchSig, blockSize*bitmapLen)
	b.svNewID = preallocUint16(b.svNewID, maxLitHist)

	b.cbHistSymbols = preallocUint32(b.cbHistSymbols, estBlocks)
	b.cbAllHistograms = preallocUint32(b.cbAllHistograms, estClusters*maxAlpha)
	b.cbClusterSizes = preallocUint32(b.cbClusterSizes, estClusters)
	b.cbBatchHist = preallocUint32(b.cbBatchHist, hpb*maxAlpha)
	b.cbPairs = preallocHistogramPairsBefore(b.cbPairs, maxPairs)
	b.cbTmpHist = preallocUint32(b.cbTmpHist, 2*maxAlpha)
	b.cbBatchU32 = preallocUint32(b.cbBatchU32, 4*hpb)
	b.cbBlockLengths = preallocUint32(b.cbBlockLengths, estBlocks)
	b.cbBatchFloat = preallocFloat64(b.cbBatchFloat, max(hpb, estClusters))
	b.cbBatchTotals = preallocUint32(b.cbBatchTotals, max(hpb, estClusters))
	b.cbClusters = preallocUint32(b.cbClusters, estClusters)
	b.cbNewIndex = preallocUint32(b.cbNewIndex, estClusters)

	b.sbLiteralBytes = preallocByte(b.sbLiteralBytes, blockSize)
	b.sbUint16 = preallocUint16(b.sbUint16, blockSize)

	b.bmTmpHist = preallocUint32(b.bmTmpHist, maxAlpha)
	b.bmContextModes = preallocByte(b.bmContextModes, maxBlockTypes)
	litHistSize := maxBlockTypes * litContextMul
	b.bmLitHist = preallocUint32(b.bmLitHist, litHistSize*litAlpha)
	distHistSize := maxBlockTypes * distContextMul
	b.bmDistHist = preallocUint32(b.bmDistHist, distHistSize*distAlpha)
	b.bmLitOutHist = preallocUint32(b.bmLitOutHist, litHistSize*litAlpha)
	b.bmDistOutHist = preallocUint32(b.bmDistOutHist, distHistSize*distAlpha)

	maxInSize := litHistSize
	b.chClusterSize = preallocUint32(b.chClusterSize, maxInSize)
	b.chClusters = preallocUint32(b.chClusters, maxInSize)
	b.chBitCosts = preallocFloat64(b.chBitCosts, maxInSize)
	b.chTotalCounts = preallocUint32(b.chTotalCounts, maxInSize)
	b.chSymbols = preallocUint32(b.chSymbols, maxInSize)
	b.chTmpHist = preallocUint32(b.chTmpHist, maxAlpha)
	b.chPairs = preallocHistogramPairsBefore(b.chPairs, maxPairs)

	b.hrNewIndex = preallocUint32(b.hrNewIndex, maxInSize)
	b.hrTmpData = preallocUint32(b.hrTmpData, maxBlockTypes*maxAlpha)
	b.hrTmpBitCosts = preallocFloat64(b.hrTmpBitCosts, maxBlockTypes)
	b.hrTmpTotals = preallocUint32(b.hrTmpTotals, maxBlockTypes)

	b.zNodes = preallocZopfliNodes(b.zNodes, blockSize+1)
	b.zMatches = preallocBackwardMatches(b.zMatches, 2*(h10MaxNumMatches+64))
	if cap(b.zCostModel.literalCosts) < blockSize+2 {
		b.zCostModel.literalCosts = make([]float32, 0, blockSize+2)
	}
	if cap(b.zCostModel.costDist) < 64 {
		b.zCostModel.costDist = make([]float32, 0, 64)
	}

	b.hqNumMatchesArr = preallocUint32(b.hqNumMatchesArr, blockSize)
	b.hqMatches = preallocBackwardMatches(b.hqMatches, 4*blockSize)
}

func (e *encoderSplit) resetBefore(quality, lgwin int, sizeHint uint) {
	e.encodeState.reset(quality, lgwin, sizeHint)

	e.mb.distanceContextMap = e.mb.distanceContextMap[:0]
	e.mb.literalContextMap = e.mb.literalContextMap[:0]

	if quality < 10 {
		if e.hasher != nil {
			e.prevHasher = e.hasher
			e.hasher = nil
		}
	} else if e.hasher != nil {
		e.resetHasher()
	}

	if quality >= 10 {
		if e.hasher == nil {
			h := poolH10.Get().(*h10)
			h.lgwin = lgwin
			h.quality = quality
			h.bufs = &e.q10
			e.hasher = h
			e.resetHasher()
		}
		blockSize := 1 << e.lgblock
		e.q10.preallocQ10Before(blockSize)

		const (
			maxTypes       = 256
			litContextMul  = 64
			distContextMul = 4
		)
		e.mb.literalContextMap = preallocUint32(e.mb.literalContextMap, maxTypes*litContextMul)
		e.mb.distanceContextMap = preallocUint32(e.mb.distanceContextMap, maxTypes*distContextMul)
		e.mb.cmdHistograms = preallocUint32(e.mb.cmdHistograms, maxTypes*core.AlphabetSizeInsertAndCopyLength)

		e.mb.litSplit.types = preallocByte(e.mb.litSplit.types, maxTypes)
		e.mb.litSplit.lengths = preallocUint32(e.mb.litSplit.lengths, maxTypes)
		e.mb.cmdSplit.types = preallocByte(e.mb.cmdSplit.types, maxTypes)
		e.mb.cmdSplit.lengths = preallocUint32(e.mb.cmdSplit.lengths, maxTypes)
		e.mb.distSplit.types = preallocByte(e.mb.distSplit.types, maxTypes)
		e.mb.distSplit.lengths = preallocUint32(e.mb.distSplit.lengths, maxTypes)

		e.litDepths = preallocByte(e.litDepths, maxTypes*litContextMul*core.AlphabetSizeLiteral)
		e.litBits = preallocUint16(e.litBits, maxTypes*litContextMul*core.AlphabetSizeLiteral)
		e.cmdDepths = preallocByte(e.cmdDepths, maxTypes*core.AlphabetSizeInsertAndCopyLength)
		e.cmdBits = preallocUint16(e.cmdBits, maxTypes*core.AlphabetSizeInsertAndCopyLength)
		e.distDepths = preallocByte(e.distDepths, maxTypes*distContextMul*core.NumHistogramDistanceSymbols)
		e.distBits = preallocUint16(e.distBits, maxTypes*distContextMul*core.NumHistogramDistanceSymbols)
		e.rleSymBuf = preallocUint32(e.rleSymBuf, maxTypes*litContextMul)
		if cap(e.goodForRLE) < core.AlphabetSizeInsertAndCopyLength {
			e.goodForRLE = make([]bool, 0, core.AlphabetSizeInsertAndCopyLength)
		}
	}
}

type preallocTrimInput struct {
	name string
	data []byte
}

func preallocTrimTestdata(tb testing.TB) []preallocTrimInput {
	tb.Helper()
	names := []string{
		"github_events_2k.json", "github_events_5k.json", "github_events_8k.json",
		"gh_172KB.html", "reactcore_187KB.js",
	}
	inputs := make([]preallocTrimInput, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join("../../testdata", name))
		if err != nil {
			tb.Fatal(err)
		}
		inputs = append(inputs, preallocTrimInput{name, data})
	}
	return inputs
}

func preallocTrimCorpus(tb testing.TB) []preallocTrimInput {
	tb.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "brotli-ref", "tests", "testdata", "*.txt"))
	if err != nil {
		tb.Fatal(err)
	}
	inputs := make([]preallocTrimInput, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			tb.Fatal(err)
		}
		inputs = append(inputs, preallocTrimInput{filepath.Base(path), data})
	}
	return inputs
}

func preallocTrimEdgeCases(text []byte, inputBlockSize int) []preallocTrimInput {
	rng := rand.New(rand.NewPCG(10, 11))
	random := make([]byte, 64<<10)
	for i := range random {
		random[i] = byte(rng.Uint32())
	}
	return []preallocTrimInput{
		{"empty", nil},
		{"one_byte", text[:1]},
		{"below_the_block_split_minimum", text[:minLengthForBlockSplitting-1]},
		{"exactly_one_input_block", text[:inputBlockSize]},
		{"one_byte_past_one_input_block", text[:inputBlockSize+1]},
		{"incompressible_random", random},
		{"one_repeated_byte", bytes.Repeat([]byte{'a'}, 300<<10)},
	}
}

func preallocTrimStream(
	e *encoderSplit, reset func(e *encoderSplit, quality, lgwin int, sizeHint uint),
	in []byte, quality, lgwin int, flushChunks []int,
) ([]byte, error) {
	var out bytes.Buffer
	if len(flushChunks) == 0 {
		reset(e, quality, lgwin, uint(len(in)))
		if _, err := e.Write(&out, in); err != nil {
			return nil, err
		}
	} else {
		reset(e, quality, lgwin, 0)
		for i := 0; len(in) > 0; i++ {
			n := min(flushChunks[i%len(flushChunks)], len(in))
			if _, err := e.Write(&out, in[:n]); err != nil {
				return nil, err
			}
			if err := e.Flush(&out); err != nil {
				return nil, err
			}
			in = in[n:]
		}
	}
	if err := e.Close(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func comparePreallocTrimStreams(
	t *testing.T, before, after *encoderSplit, in preallocTrimInput, quality, lgwin int, flushChunks []int,
) {
	t.Helper()
	want, errBefore := preallocTrimStream(before, (*encoderSplit).resetBefore, in.data, quality, lgwin, flushChunks)
	got, errAfter := preallocTrimStream(after, (*encoderSplit).reset, in.data, quality, lgwin, flushChunks)
	stream := fmt.Sprintf("%s at q%d lgwin %d written in one call", in.name, quality, lgwin)
	if len(flushChunks) > 0 {
		stream = fmt.Sprintf("%s at q%d lgwin %d written in chunks of %v bytes, each flushed", in.name, quality, lgwin, flushChunks)
	}
	if err := errors.Join(errBefore, errAfter); err != nil {
		t.Fatalf("%s: %v", stream, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s: the encoder that grows its metablock scratch on demand emitted %d bytes that differ from "+
			"the %d bytes of the encoder that preallocated the worst case; only buffer capacities may change, so "+
			"a difference means a consumer read scratch it did not write first", stream, len(got), len(want))
	}
}

func TestQ10AndQ11EncodersThatGrowScratchOnDemandEmitExactlyTheBytesOfTheWorstCasePreallocatingEncoder(t *testing.T) {
	testdata := preallocTrimTestdata(t)
	events8k, html, js := testdata[2], testdata[3], testdata[4]
	text := append(bytes.Clone(html.data), js.data...)
	for _, quality := range []int{10, 11} {
		for _, lgwin := range []int{16, 22} {
			for _, mode := range []struct {
				name        string
				flushChunks []int
			}{
				{"one_write", nil},
				{"flush_after_writes_of_1_4093_70001_bytes", []int{1, 4093, 70001}},
			} {
				t.Run(fmt.Sprintf("reused_encoders/q%d/lgwin%d/%s", quality, lgwin, mode.name), func(t *testing.T) {
					t.Parallel()
					before, after := new(encoderSplit), new(encoderSplit)
					t.Cleanup(before.releaseBuffers)
					t.Cleanup(after.releaseBuffers)
					after.reset(quality, lgwin, 0)
					for _, in := range append(preallocTrimEdgeCases(text, int(after.inputBlockSize())), testdata...) {
						comparePreallocTrimStreams(t, before, after, in, quality, lgwin, mode.flushChunks)
					}
				})
			}
		}
	}
	t.Run("reused_encoders_across_qualities/lgwin22", func(t *testing.T) {
		t.Parallel()
		before, after := new(encoderSplit), new(encoderSplit)
		t.Cleanup(before.releaseBuffers)
		t.Cleanup(after.releaseBuffers)
		inputs := []preallocTrimInput{html, events8k, js}
		for i, quality := range []int{5, 10, 9, 11, 4, 10, 11} {
			comparePreallocTrimStreams(t, before, after, inputs[i%len(inputs)], quality, 22, nil)
		}
	})
	for _, in := range preallocTrimCorpus(t) {
		for _, quality := range []int{10, 11} {
			t.Run(fmt.Sprintf("fresh_encoders/%s/q%d/lgwin22", in.name, quality), func(t *testing.T) {
				t.Parallel()
				before, after := new(encoderSplit), new(encoderSplit)
				t.Cleanup(before.releaseBuffers)
				t.Cleanup(after.releaseBuffers)
				comparePreallocTrimStreams(t, before, after, in, quality, 22, nil)
			})
		}
	}
}

func preallocTrimColdStreamBytes(
	tb testing.TB, in []byte, quality int, reset func(e *encoderSplit, quality, lgwin int, sizeHint uint),
) uint64 {
	tb.Helper()
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	runtime.GC()
	runtime.GC()
	var start, end runtime.MemStats
	runtime.ReadMemStats(&start)
	e := new(encoderSplit)
	reset(e, quality, 22, uint(len(in)))
	_, writeErr := e.Write(io.Discard, in)
	closeErr := e.Close(io.Discard)
	runtime.ReadMemStats(&end)
	e.releaseBuffers()
	if err := errors.Join(writeErr, closeErr); err != nil {
		tb.Fatal(err)
	}
	return end.TotalAlloc - start.TotalAlloc
}

func TestColdQ10AndQ11EncodersNoLongerAllocateTheWorstCaseMetablockScratchUpFront(t *testing.T) {
	in, err := os.ReadFile("../../testdata/github_events_8k.json")
	if err != nil {
		t.Fatal(err)
	}
	const minSaving = 64 << 20
	for _, quality := range []int{10, 11} {
		before := preallocTrimColdStreamBytes(t, in, quality, (*encoderSplit).resetBefore)
		after := preallocTrimColdStreamBytes(t, in, quality, (*encoderSplit).reset)
		if after+minSaving > before {
			t.Errorf("a cold q%d encoder compressing %d bytes at lgwin 22 allocated %d bytes, only %d fewer than "+
				"the %d bytes of the encoder that preallocated for 256 block types x 64 contexts; the metablock "+
				"histograms, switch signal, entropy-code tables and zopfli nodes must grow to what the stream "+
				"needs, which saves at least %d bytes here", quality, len(in), after, int64(before)-int64(after),
				before, minSaving)
		}
	}
}
