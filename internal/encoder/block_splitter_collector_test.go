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
	raw       []command
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
	raw := slices.Clone(s.commands)
	buildMetaBlock(s.data, pos, mask, quality, s.prevByte, s.prevByte2, s.commands,
		chooseContextMode(quality, s.data, pos, mask, uint(len(in))), false, &e.mb, &e.q10)
	return recordedMetablock{cmds: slices.Clone(s.commands), raw: raw, data: slices.Clone(s.data), pos: pos, mask: mask, size: len(in)}
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

func recordedMetablockPaths(tb testing.TB) []string {
	tb.Helper()
	corpus, err := filepath.Glob(filepath.Join("..", "..", "brotli-ref", "tests", "testdata", "*.txt"))
	if err != nil {
		tb.Fatal(err)
	}
	return append([]string{
		"../../testdata/github_events_2k.json",
		"../../testdata/github_events_8k.json",
		"../../testdata/gh_172KB.html",
		"../../testdata/reactcore_187KB.js",
	}, corpus...)
}

func TestSplitBlockParallelChoosesTheDistanceParametersAndSplitsAllThreeCategoriesExactlyLikeTheSequentialSplitter(t *testing.T) {
	insertOnly := make([]command, 0, 300)
	for i := range 300 {
		insertOnly = append(insertOnly, newInsertCommand(uint(1+i%37)))
	}

	var before, serial q10Bufs
	parallel := q10Bufs{parallel: true}
	t.Cleanup(parallel.hqCollector.stop)
	t.Cleanup(parallel.hqHelper.stop)
	var tmpHist []uint32
	for _, path := range recordedMetablockPaths(t) {
		m := recordMetablock(t, path, 10)
		for _, c := range []struct {
			name string
			cmds []command
		}{
			{"whole_metablock", m.raw},
			{"no_commands", m.raw[:0]},
			{"fewer_symbols_than_min_split", m.raw[:min(len(m.raw), minLengthForBlockSplitting-1)]},
			{"one_command_below_the_offload_threshold", m.raw[:min(len(m.raw), splitOffloadMinCommands-1)]},
			{"exactly_the_offload_threshold", m.raw[:min(len(m.raw), splitOffloadMinCommands)]},
			{"insert_only_commands_without_distances", insertOnly},
		} {
			for _, quality := range []int{10, 11} {
				wantCmds := slices.Clone(c.cmds)
				wantParams := optimizeDistanceParams(wantCmds, &tmpHist)
				var litWant, cmdWant, distWant blockSplit
				splitBlockBefore(&litWant, &cmdWant, &distWant, &before, wantCmds, m.data, m.pos, m.mask, quality)

				serialCmds := slices.Clone(c.cmds)
				serialParams := optimizeDistanceParams(serialCmds, &serial.bmTmpHist)
				var litSerial, cmdSerial, distSerial blockSplit
				splitBlock(&litSerial, &cmdSerial, &distSerial, &serial, serialCmds, m.data, m.pos, m.mask, quality)

				parallelCmds := slices.Clone(c.cmds)
				var litParallel, cmdParallel, distParallel blockSplit
				parallelParams := splitBlockParallel(&litParallel, &cmdParallel, &distParallel, &parallel,
					parallelCmds, m.data, m.pos, m.mask, quality)

				var errs []error
				for _, got := range []struct {
					mode           string
					params         distanceParams
					cmds           []command
					lit, cmd, dist *blockSplit
				}{
					{"serial", serialParams, serialCmds, &litSerial, &cmdSerial, &distSerial},
					{"parallel", parallelParams, parallelCmds, &litParallel, &cmdParallel, &distParallel},
				} {
					if got.params != wantParams {
						errs = append(errs, fmt.Errorf("%s: distance parameters %+v, want %+v", got.mode, got.params, wantParams))
					}
					if !slices.Equal(got.cmds, wantCmds) {
						errs = append(errs, fmt.Errorf("%s: commands after the distance prefix rewrite differ", got.mode))
					}
					if err := errors.Join(
						blockSplitMismatch("literal", got.lit, &litWant),
						blockSplitMismatch("command", got.cmd, &cmdWant),
						blockSplitMismatch("distance", got.dist, &distWant),
					); err != nil {
						errs = append(errs, fmt.Errorf("%s: %w", got.mode, err))
					}
				}
				if err := errors.Join(errs...); err != nil {
					t.Errorf("%s %s q%d: the distance search, prefix rewrite and three splits must come out exactly as "+
						"the sequential splitter's whether they run on the caller or on the collector and helper "+
						"goroutines, or the metablock stops being byte-identical to the C reference:\n%v",
						filepath.Base(path), c.name, quality, err)
				}
			}
		}
	}
}

func TestBuildMetaBlockInParallelModeGivesExactlyTheSequentialSplitsHistogramsContextMapsAndDistanceParameters(t *testing.T) {
	parallel := q10Bufs{parallel: true}
	t.Cleanup(parallel.hqCollector.stop)
	t.Cleanup(parallel.hqHelper.stop)
	for _, path := range recordedMetablockPaths(t) {
		for _, quality := range []int{10, 11} {
			m := recordMetablock(t, path, quality)
			for _, disableContextModeling := range []bool{false, true} {
				contextMode := chooseContextMode(quality, m.data, m.pos, m.mask, uint(m.size))
				var serial q10Bufs
				var mbWant, mbGot metaBlockSplit
				wantCmds, gotCmds := slices.Clone(m.raw), slices.Clone(m.raw)
				wantParams := buildMetaBlock(m.data, m.pos, m.mask, quality, 0, 0, wantCmds,
					contextMode, disableContextModeling, &mbWant, &serial)
				gotParams := buildMetaBlock(m.data, m.pos, m.mask, quality, 0, 0, gotCmds,
					contextMode, disableContextModeling, &mbGot, &parallel)

				var errs []error
				if gotParams != wantParams {
					errs = append(errs, fmt.Errorf("distance parameters %+v, want %+v", gotParams, wantParams))
				}
				if !slices.Equal(gotCmds, wantCmds) {
					errs = append(errs, errors.New("commands after the distance prefix rewrite differ"))
				}
				errs = append(errs,
					blockSplitMismatch("literal", &mbGot.litSplit, &mbWant.litSplit),
					blockSplitMismatch("command", &mbGot.cmdSplit, &mbWant.cmdSplit),
					blockSplitMismatch("distance", &mbGot.distSplit, &mbWant.distSplit))
				for _, f := range []struct {
					name      string
					got, want []uint32
				}{
					{"literal histograms", mbGot.litHistograms, mbWant.litHistograms},
					{"command histograms", mbGot.cmdHistograms, mbWant.cmdHistograms},
					{"distance histograms", mbGot.distHistograms, mbWant.distHistograms},
					{"literal context map", mbGot.literalContextMap, mbWant.literalContextMap},
					{"distance context map", mbGot.distanceContextMap, mbWant.distanceContextMap},
				} {
					if !slices.Equal(f.got, f.want) {
						errs = append(errs, fmt.Errorf("%s differ", f.name))
					}
				}
				if err := errors.Join(errs...); err != nil {
					t.Errorf("%s q%d contextModeling=%v: buildMetaBlock with the splits and the distance clustering on "+
						"worker goroutines must produce exactly the sequential metablock, or the output stops being "+
						"byte-identical to the C reference:\n%v",
						filepath.Base(path), quality, !disableContextModeling, err)
				}
			}
		}
	}
}

func BenchmarkBuildMetaBlockOnRecordedCommands(b *testing.B) {
	for _, name := range []string{"plrabn12.txt", "lcet10.txt", "mapsdatazrh"} {
		for _, quality := range []int{10, 11} {
			m := recordMetablock(b, filepath.Join("..", "..", "brotli-ref", "tests", "testdata", name), quality)
			contextMode := chooseContextMode(quality, m.data, m.pos, m.mask, uint(m.size))
			for _, impl := range []string{"serial", "parallel"} {
				b.Run(fmt.Sprintf("q%d_%s/impl=%s", quality, name, impl), func(b *testing.B) {
					bufs := q10Bufs{parallel: impl == "parallel"}
					defer bufs.hqCollector.stop()
					defer bufs.hqHelper.stop()
					var mb metaBlockSplit
					work := make([]command, len(m.raw))
					b.SetBytes(int64(m.size))
					b.ReportAllocs()
					for range b.N {
						copy(work, m.raw)
						buildMetaBlock(m.data, m.pos, m.mask, quality, 0, 0, work, contextMode, false, &mb, &bufs)
					}
				})
			}
		}
	}
}
