package encoder

import "testing"

// findBlocksSink keeps the benchmarked calls from being elided.
var findBlocksSink int

// findBlocksNoMinCost mirrors the sentinel so the replica below stays
// self-contained and compiles against a tree that has no noMinCost const.
const findBlocksNoMinCost = 1e99

// findBlocksBefore is findBlocks with the two inner loops as they were
// before the SSE2 kernels replaced them, so both run in one binary.
func findBlocksBefore(
	data []uint16,
	histograms []uint32,
	insertCost []float64,
	cost []float64,
	switchSignal []byte,
	blockID []byte,
	length, numHistograms, alphabetSize int,
	blockSwitchBitcost float64,
) int {
	bitmapLen := (numHistograms + 7) >> 3
	numBlocks := 1

	if numHistograms <= 1 {
		clear(blockID[:length])
		return 1
	}

	// Fill the per-symbol, per-histogram insertion costs.
	// For each histogram j and symbol i:
	//   insertCost[i * numHistograms + j] = log2(totalCount_j) - symbolBitCost(count_j_i)
	//
	// This represents the cost in bits to encode symbol i using histogram j.
	// The reverse loop below writes every element before any read, so no
	// pre-zeroing is needed.
	for j := range numHistograms {
		hist := histograms[j*alphabetSize:]
		var totalCount uint32
		for i := range alphabetSize {
			totalCount += hist[i]
		}
		insertCost[j] = fastLog2(int(totalCount))
	}
	for i := alphabetSize - 1; i >= 0; i-- {
		for j := range numHistograms {
			insertCost[i*numHistograms+j] =
				insertCost[j] - symbolBitCost(int(histograms[j*alphabetSize+i]))
		}
	}

	// DP forward pass: track the minimum cost path through histograms.
	clear(cost[:numHistograms])
	clear(switchSignal[:length*bitmapLen])

	const prologueLength = 2000
	const prologueMultiplier = 0.07 / 2000

	for byteIx := range length {
		ix := byteIx * bitmapLen
		symbol := int(data[byteIx])
		insertCostIx := symbol * numHistograms
		switchCost := blockSwitchBitcost

		minCost := findBlocksNoMinCost
		for k := range numHistograms {
			cost[k] += insertCost[insertCostIx+k]
			if cost[k] < minCost {
				minCost = cost[k]
				blockID[byteIx] = byte(k)
			}
		}

		// Reduce switch cost in the prologue to encourage early splits.
		if byteIx < prologueLength {
			switchCost *= 0.77 + prologueMultiplier*float64(byteIx)
		}

		for k := range numHistograms {
			cost[k] -= minCost
			if cost[k] >= switchCost {
				cost[k] = switchCost
				switchSignal[ix+(k>>3)] |= 1 << (k & 7)
			}
		}
	}

	// Backtrace from the last position to determine block boundaries.
	byteIx := length - 1
	ix := byteIx * bitmapLen
	curID := blockID[byteIx]
	for byteIx > 0 {
		mask := byte(1 << (curID & 7))
		byteIx--
		ix -= bitmapLen
		if switchSignal[ix+int(curID>>3)]&mask != 0 {
			if curID != blockID[byteIx] {
				curID = blockID[byteIx]
				numBlocks++
			}
		}
		blockID[byteIx] = curID
	}

	return numBlocks
}

func findBlocksFixture(tb testing.TB, length, numHistograms, alphabetSize int) (
	data []uint16, histograms []uint32, insertCost, cost []float64, sig, blockID []byte,
) {
	tb.Helper()
	data = make([]uint16, length)
	for i := range data {
		data[i] = uint16((i * 7919) % alphabetSize)
	}
	histograms = make([]uint32, numHistograms*alphabetSize)
	for i := range histograms {
		histograms[i] = uint32((i*2654435761)%97) + 1
	}
	insertCost = make([]float64, alphabetSize*numHistograms)
	cost = make([]float64, numHistograms)
	sig = make([]byte, length*((numHistograms+7)>>3))
	blockID = make([]byte, length)
	return
}

func benchmarkFindBlocks(b *testing.B, length, numHistograms int) {
	const alphabetSize = 256

	b.Run("impl=before_scalar_loops", func(b *testing.B) {
		data, hist, ins, cost, sig, id := findBlocksFixture(b, length, numHistograms, alphabetSize)
		b.ReportAllocs()
		for range b.N {
			findBlocksSink = findBlocksBefore(data, hist, ins, cost, sig, id,
				length, numHistograms, alphabetSize, 28.1)
		}
	})
	b.Run("impl=after_sse2_kernels", func(b *testing.B) {
		data, hist, ins, cost, sig, id := findBlocksFixture(b, length, numHistograms, alphabetSize)
		b.ReportAllocs()
		for range b.N {
			findBlocksSink = findBlocks(data, hist, ins, cost, sig, id,
				length, numHistograms, alphabetSize, 28.1)
		}
	})
}

func BenchmarkFindBlocks3000Bytes100Histograms(b *testing.B) { benchmarkFindBlocks(b, 3000, 100) }
func BenchmarkFindBlocks2500Bytes50Histograms(b *testing.B)  { benchmarkFindBlocks(b, 2500, 50) }
