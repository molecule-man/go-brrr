package encoder

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

func clusterBlocksBefore(
	split *blockSplit,
	bufs *q10Bufs,
	data []uint16, blockIDs []byte,
	length, numBlocks, alphabetSize int,
) {
	bufs.cbHistSymbols = growUint32(bufs.cbHistSymbols, numBlocks)
	histogramSymbols := bufs.cbHistSymbols[:numBlocks]
	expectedNumClusters := clustersPerBatch *
		(numBlocks + histogramsPerBatch - 1) / histogramsPerBatch

	bufs.cbAllHistograms = growUint32(bufs.cbAllHistograms, expectedNumClusters*alphabetSize)
	allHistograms := bufs.cbAllHistograms
	allHistogramsSize := 0
	bufs.cbClusterSizes = growUint32(bufs.cbClusterSizes, expectedNumClusters)
	clusterSizes := bufs.cbClusterSizes
	clusterSizesLen := 0
	numClusters := 0

	bufs.cbBatchHist = growUint32(bufs.cbBatchHist, min(numBlocks, histogramsPerBatch)*alphabetSize)
	batchHistograms := bufs.cbBatchHist
	maxNumPairs := histogramsPerBatch * histogramsPerBatch / 2
	bufs.cbPairs = growHistogramPairs(bufs.cbPairs, maxNumPairs+1)
	pairs := bufs.cbPairs
	bufs.cbTmpHist = growUint32(bufs.cbTmpHist, 2*alphabetSize)
	tmpHist := bufs.cbTmpHist

	bufs.cbBatchU32 = growUint32(bufs.cbBatchU32, 4*histogramsPerBatch)
	sizes := bufs.cbBatchU32[0*histogramsPerBatch : 1*histogramsPerBatch]
	newClusters := bufs.cbBatchU32[1*histogramsPerBatch : 2*histogramsPerBatch]
	symbols := bufs.cbBatchU32[2*histogramsPerBatch : 3*histogramsPerBatch]
	remap := bufs.cbBatchU32[3*histogramsPerBatch : 4*histogramsPerBatch]
	bufs.cbBlockLengths = growUint32Clear(bufs.cbBlockLengths, numBlocks)
	blockLengths := bufs.cbBlockLengths[:numBlocks]

	blockIdx := 0
	for i := range length {
		blockLengths[blockIdx]++
		if i+1 == length || blockIDs[i] != blockIDs[i+1] {
			blockIdx++
		}
	}

	bufs.cbBatchFloat = growFloat64(bufs.cbBatchFloat, histogramsPerBatch)
	bufs.cbBatchTotals = growUint32(bufs.cbBatchTotals, histogramsPerBatch)
	pos := 0
	for i := 0; i < numBlocks; i += histogramsPerBatch {
		numToCombine := min(numBlocks-i, histogramsPerBatch)

		for j := range numToCombine {
			bl := int(blockLengths[i+j])
			hist := batchHistograms[j*alphabetSize : (j+1)*alphabetSize]
			clear(hist)
			for range bl {
				hist[data[pos]]++
				pos++
			}
			newClusters[j] = uint32(j)
			symbols[j] = uint32(j)
			sizes[j] = 1
		}

		batchBitCosts := bufs.cbBatchFloat[:numToCombine]
		batchTotalCounts := bufs.cbBatchTotals[:numToCombine]
		for j := range numToCombine {
			hist := batchHistograms[j*alphabetSize : (j+1)*alphabetSize]
			batchBitCosts[j] = populationCost(hist, alphabetSize)
			batchTotalCounts[j] = histogramTotalCount(hist, alphabetSize)
		}

		numNewClusters := histogramCombine(
			batchHistograms, alphabetSize,
			batchBitCosts, batchTotalCounts,
			sizes, symbols[:numToCombine], newClusters[:numToCombine],
			pairs,
			numToCombine, numToCombine, histogramsPerBatch, maxNumPairs,
			tmpHist,
		)

		needed := (allHistogramsSize + numNewClusters) * alphabetSize
		if needed > len(allHistograms) {
			grown := make([]uint32, needed*2)
			copy(grown, allHistograms[:allHistogramsSize*alphabetSize])
			allHistograms = grown
			bufs.cbAllHistograms = allHistograms
		}
		if clusterSizesLen+numNewClusters > len(clusterSizes) {
			grown := make([]uint32, (clusterSizesLen+numNewClusters)*2)
			copy(grown, clusterSizes[:clusterSizesLen])
			clusterSizes = grown
			bufs.cbClusterSizes = clusterSizes
		}

		for j := range numNewClusters {
			srcIdx := int(newClusters[j])
			copy(
				allHistograms[allHistogramsSize*alphabetSize:(allHistogramsSize+1)*alphabetSize],
				batchHistograms[srcIdx*alphabetSize:(srcIdx+1)*alphabetSize],
			)
			allHistogramsSize++
			clusterSizes[clusterSizesLen] = sizes[srcIdx]
			clusterSizesLen++
			remap[srcIdx] = uint32(j)
		}
		for j := range numToCombine {
			histogramSymbols[i+j] = uint32(numClusters) + remap[symbols[j]]
		}
		numClusters += numNewClusters
	}

	maxNumPairsFinal := min(64*numClusters, (numClusters/2)*numClusters)
	if maxNumPairsFinal+1 > len(pairs) {
		bufs.cbPairs = growHistogramPairs(bufs.cbPairs, maxNumPairsFinal+1)
		pairs = bufs.cbPairs
	}

	bufs.cbClusters = growUint32(bufs.cbClusters, numClusters)
	clusters := bufs.cbClusters[:numClusters]
	for i := range numClusters {
		clusters[i] = uint32(i)
	}

	bufs.cbBatchFloat = growFloat64(bufs.cbBatchFloat, numClusters)
	allBitCosts := bufs.cbBatchFloat[:numClusters]
	bufs.cbBatchTotals = growUint32(bufs.cbBatchTotals, numClusters)
	allTotalCounts := bufs.cbBatchTotals[:numClusters]
	for i := range numClusters {
		hist := allHistograms[i*alphabetSize : (i+1)*alphabetSize]
		allBitCosts[i] = populationCost(hist, alphabetSize)
		allTotalCounts[i] = histogramTotalCount(hist, alphabetSize)
	}

	numFinalClusters := histogramCombine(
		allHistograms, alphabetSize,
		allBitCosts, allTotalCounts,
		clusterSizes, histogramSymbols, clusters,
		pairs,
		numClusters, numBlocks, maxNumberOfBlockTypes, maxNumPairsFinal,
		tmpHist,
	)

	const invalidIndex = ^uint32(0)
	bufs.cbNewIndex = growUint32(bufs.cbNewIndex, numClusters)
	newIndex := bufs.cbNewIndex[:numClusters]
	for i := range newIndex {
		newIndex[i] = invalidIndex
	}

	pos = 0
	nextIndex := uint32(0)
	for i := range numBlocks {
		bl := int(blockLengths[i])
		clear(tmpHist[:alphabetSize])
		for range bl {
			tmpHist[data[pos]]++
			pos++
		}

		bestOut := histogramSymbols[0]
		if i != 0 {
			bestOut = histogramSymbols[i-1]
		}
		bestBits := histogramBitCostDistance(
			tmpHist[:alphabetSize],
			allHistograms[int(bestOut)*alphabetSize:(int(bestOut)+1)*alphabetSize],
			tmpHist[alphabetSize:2*alphabetSize],
			alphabetSize,
			allBitCosts[bestOut], allTotalCounts[bestOut],
		)

		for j := range numFinalClusters {
			ci := int(clusters[j])
			curBits := histogramBitCostDistance(
				tmpHist[:alphabetSize],
				allHistograms[ci*alphabetSize:(ci+1)*alphabetSize],
				tmpHist[alphabetSize:2*alphabetSize],
				alphabetSize,
				allBitCosts[clusters[j]], allTotalCounts[clusters[j]],
			)
			if curBits < bestBits {
				bestBits = curBits
				bestOut = clusters[j]
			}
		}
		histogramSymbols[i] = bestOut
		if newIndex[bestOut] == invalidIndex {
			newIndex[bestOut] = nextIndex
			nextIndex++
		}
	}

	split.types = growByte(split.types, numBlocks)[:numBlocks]
	split.lengths = growUint32(split.lengths, numBlocks)[:numBlocks]

	var curLength uint32
	splitIdx := 0
	var maxType byte
	for i := range numBlocks {
		curLength += blockLengths[i]
		if i+1 == numBlocks || histogramSymbols[i] != histogramSymbols[i+1] {
			id := byte(newIndex[histogramSymbols[i]])
			split.types[splitIdx] = id
			split.lengths[splitIdx] = curLength
			if id > maxType {
				maxType = id
			}
			curLength = 0
			splitIdx++
		}
	}
	split.types = split.types[:splitIdx]
	split.lengths = split.lengths[:splitIdx]
	split.numTypes = int(maxType) + 1
}

func splitByteVectorBefore(
	split *blockSplit,
	bufs *q10Bufs,
	data []uint16, length int,
	p splitVecParams,
) {
	numHistograms := min(length/p.symbolsPerHistogram+1, p.maxHistograms)

	if length == 0 {
		split.numTypes = 1
		return
	}

	if length < minLengthForBlockSplitting {
		split.types = append(split.types, 0)
		split.lengths = append(split.lengths, uint32(length))
		split.numTypes = 1
		return
	}

	bufs.svHistograms = growUint32Clear(bufs.svHistograms, numHistograms*p.alphabetSize)
	histograms := bufs.svHistograms[:numHistograms*p.alphabetSize]

	initialEntropyCodes(data, histograms, length, p.samplingStride, numHistograms, p.alphabetSize)
	refineEntropyCodes(data, histograms, length, p.samplingStride, numHistograms, p.alphabetSize)

	bufs.svBlockIDs = growByte(bufs.svBlockIDs, length)
	blockIDs := bufs.svBlockIDs[:length]
	bitmapLen := (numHistograms + 7) >> 3
	floatNeeded := p.alphabetSize*numHistograms + numHistograms
	bufs.svFloat = growFloat64(bufs.svFloat, floatNeeded)
	insertCost := bufs.svFloat[:p.alphabetSize*numHistograms]
	cost := bufs.svFloat[p.alphabetSize*numHistograms : floatNeeded]
	bufs.svSwitchSig = growByte(bufs.svSwitchSig, length*bitmapLen)
	switchSignal := bufs.svSwitchSig[:length*bitmapLen]
	bufs.svNewID = growUint16(bufs.svNewID, numHistograms)
	newID := bufs.svNewID[:numHistograms]

	iters := 3
	if p.quality >= hqZopflificationQuality {
		iters = 10
	}

	var numBlocks int
	for range iters {
		numBlocks = findBlocks(data,
			histograms, insertCost, cost, switchSignal, blockIDs,
			length, numHistograms, p.alphabetSize, p.blockSwitchCost)
		numHistograms = remapBlockIDs(blockIDs, length, newID, numHistograms)
		buildBlockHistograms(data, blockIDs, histograms, length, numHistograms, p.alphabetSize)
	}

	clusterBlocksBefore(split, bufs, data, blockIDs, length, numBlocks, p.alphabetSize)
}

func splitBlockBefore(
	litSplit, cmdSplit, distSplit *blockSplit,
	bufs *q10Bufs,
	cmds []command,
	data []byte, pos, mask uint,
	quality int,
) {
	bufs.sbLiteralBytes = copyLiteralsToByteArrayBuf(cmds, data, pos, mask, bufs.sbLiteralBytes)
	numLiterals := len(bufs.sbLiteralBytes)
	bufs.sbUint16 = growUint16(bufs.sbUint16, numLiterals)
	symbols := bufs.sbUint16[:numLiterals]
	for i, b := range bufs.sbLiteralBytes {
		symbols[i] = uint16(b)
	}
	splitByteVectorBefore(litSplit, bufs, symbols, numLiterals, splitVecParams{
		symbolsPerHistogram: symbolsPerLiteralHistogram,
		maxHistograms:       maxLiteralHistograms,
		samplingStride:      literalStrideLength,
		blockSwitchCost:     literalBlockSwitchCost,
		quality:             quality,
		alphabetSize:        core.AlphabetSizeLiteral,
	})

	bufs.sbUint16 = growUint16(bufs.sbUint16, len(cmds))
	symbols = bufs.sbUint16[:len(cmds)]
	for i := range cmds {
		symbols[i] = cmds[i].cmdPrefix
	}
	splitByteVectorBefore(cmdSplit, bufs, symbols, len(cmds), splitVecParams{
		symbolsPerHistogram: symbolsPerCommandHistogram,
		maxHistograms:       maxCommandHistograms,
		samplingStride:      commandStrideLength,
		blockSwitchCost:     commandBlockSwitchCost,
		quality:             quality,
		alphabetSize:        core.AlphabetSizeInsertAndCopyLength,
	})

	bufs.sbUint16 = growUint16(bufs.sbUint16, len(cmds))
	symbols = bufs.sbUint16[:len(cmds)]
	j := 0
	for i := range cmds {
		cmd := &cmds[i]
		if cmd.copyLength() != 0 && cmd.cmdPrefix >= 128 {
			symbols[j] = cmd.distPrefix & 0x3FF
			j++
		}
	}
	splitByteVectorBefore(distSplit, bufs, symbols[:j], j, splitVecParams{
		symbolsPerHistogram: symbolsPerDistanceHistogram,
		maxHistograms:       maxCommandHistograms,
		samplingStride:      distanceStrideLength,
		blockSwitchCost:     distanceBlockSwitchCost,
		quality:             quality,
		alphabetSize:        core.NumHistogramDistanceSymbols,
	})
}

type recordedMetablock struct {
	cmds      []command
	data      []byte
	pos, mask uint
	size      int
}

func recordMetablock(tb testing.TB, path string, quality int) recordedMetablock {
	tb.Helper()
	in, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	e := NewCompressor(quality, 22, uint(len(in)), false).(*encoderSplit)
	defer e.Release()
	if _, err := e.Write(io.Discard, in); err != nil {
		tb.Fatal(err)
	}
	if _, _, ready := e.prepareMetaBlock(true, false); !ready {
		tb.Fatalf("%s: the final q%d metablock was not ready after the whole input was written", path, quality)
	}
	s := &e.encodeState
	if got := s.inputPos - s.lastFlushPos; got != uint64(len(in)) {
		tb.Fatalf("%s: the recorded q%d metablock covers %d of %d bytes; the fixture must be the whole file as one metablock",
			path, quality, got, len(in))
	}
	pos := wrapPosition(s.lastFlushPos)
	mask := uint(s.mask)
	buildMetaBlock(s.data, pos, mask, quality, s.prevByte, s.prevByte2, s.commands,
		chooseContextMode(quality, s.data, pos, mask, uint(len(in))), false, &e.mb, &e.q10)
	return recordedMetablock{cmds: slices.Clone(s.commands), data: slices.Clone(s.data), pos: pos, mask: mask, size: len(in)}
}

func blockSplitMismatch(category string, got, want *blockSplit) error {
	if got.numTypes == want.numTypes && slices.Equal(got.types, want.types) && slices.Equal(got.lengths, want.lengths) {
		return nil
	}
	i := 0
	for i < min(len(got.lengths), len(want.lengths)) && got.types[i] == want.types[i] && got.lengths[i] == want.lengths[i] {
		i++
	}
	return fmt.Errorf("%s split: got %d types over %d blocks, want %d types over %d blocks, first differing block %d",
		category, got.numTypes, len(got.lengths), want.numTypes, len(want.lengths), i)
}

func TestSplitBlockWithCommandAndDistanceSplitsOnTheCollectorMatchesTheSequentialSplitter(t *testing.T) {
	corpus, err := filepath.Glob(filepath.Join("..", "..", "brotli-ref", "tests", "testdata", "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	paths := append([]string{
		"../../testdata/github_events_2k.json",
		"../../testdata/github_events_8k.json",
		"../../testdata/gh_172KB.html",
		"../../testdata/reactcore_187KB.js",
	}, corpus...)

	insertOnly := make([]command, 0, 300)
	for i := range 300 {
		insertOnly = append(insertOnly, newInsertCommand(uint(1+i%37)))
	}

	var before, serial q10Bufs
	parallel := q10Bufs{parallel: true}
	t.Cleanup(parallel.hqCollector.stop)
	for _, path := range paths {
		m := recordMetablock(t, path, 10)
		for _, c := range []struct {
			name string
			cmds []command
		}{
			{"whole_metablock", m.cmds},
			{"no_commands", m.cmds[:0]},
			{"fewer_symbols_than_min_split", m.cmds[:min(len(m.cmds), minLengthForBlockSplitting-1)]},
			{"insert_only_commands_without_distances", insertOnly},
		} {
			for _, quality := range []int{10, 11} {
				var litWant, cmdWant, distWant blockSplit
				splitBlockBefore(&litWant, &cmdWant, &distWant, &before, c.cmds, m.data, m.pos, m.mask, quality)
				for _, after := range []*q10Bufs{&serial, &parallel} {
					var litGot, cmdGot, distGot blockSplit
					splitBlock(&litGot, &cmdGot, &distGot, after, c.cmds, m.data, m.pos, m.mask, quality)
					if err := errors.Join(
						blockSplitMismatch("literal", &litGot, &litWant),
						blockSplitMismatch("command", &cmdGot, &cmdWant),
						blockSplitMismatch("distance", &distGot, &distWant),
					); err != nil {
						t.Errorf("%s %s q%d parallel=%v: splitting commands and distances on the collector goroutine "+
							"(parallel) or after the literals on the caller (serial) must give exactly the sequential "+
							"splits, or the metablock stops being byte-identical to the C reference:\n%v",
							filepath.Base(path), c.name, quality, after.parallel, err)
					}
				}
			}
		}
	}
}

func BenchmarkSplitBlockOnRecordedCommands(b *testing.B) {
	for _, name := range []string{"plrabn12.txt", "lcet10.txt", "mapsdatazrh"} {
		for _, quality := range []int{10, 11} {
			m := recordMetablock(b, filepath.Join("..", "..", "brotli-ref", "tests", "testdata", name), quality)
			var lit, cmd, dist blockSplit
			b.Run(fmt.Sprintf("q%d_%s/impl=before", quality, name), func(b *testing.B) {
				var bufs q10Bufs
				b.SetBytes(int64(m.size))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					lit.reset()
					cmd.reset()
					dist.reset()
					splitBlockBefore(&lit, &cmd, &dist, &bufs, m.cmds, m.data, m.pos, m.mask, quality)
				}
			})
			b.Run(fmt.Sprintf("q%d_%s/impl=after", quality, name), func(b *testing.B) {
				bufs := q10Bufs{parallel: true}
				defer bufs.hqCollector.stop()
				b.SetBytes(int64(m.size))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					lit.reset()
					cmd.reset()
					dist.reset()
					splitBlock(&lit, &cmd, &dist, &bufs, m.cmds, m.data, m.pos, m.mask, quality)
				}
			})
		}
	}
}
