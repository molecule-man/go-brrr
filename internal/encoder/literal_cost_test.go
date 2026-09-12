package encoder

import "testing"

// literalCostSink keeps the benchmarked calls from being elided.
// literalCostBenchBytes is the payload size both literal-cost benchmarks use.
const literalCostBenchBytes = 256 << 10

var literalCostSink float32

// The *Before functions recompute fastLog2 of the window count on every
// byte, as both paths did before the value was cached.
func estimateBitCostsForLiteralsRawBefore(data []byte, pos, length, mask uint, histogram []uint, cost []float32) {
	windowHalf := uint(2000)
	inWindow := min(windowHalf, length)

	clear(histogram[:256])

	// Bootstrap histogram.
	for i := range inWindow {
		histogram[data[(pos+i)&mask]]++
	}

	// Compute bit costs with sliding window.
	for i := range length {
		if i >= windowHalf {
			histogram[data[(pos+i-windowHalf)&mask]]--
			inWindow--
		}
		if i+windowHalf < length {
			histogram[data[(pos+i+windowHalf)&mask]]++
			inWindow++
		}
		histo := histogram[data[(pos+i)&mask]]
		if histo == 0 {
			histo = 1
		}
		litCost := fastLog2(int(inWindow)) - fastLog2(int(histo))
		litCost += 0.029
		if litCost < 1.0 {
			litCost = litCost*0.5 + 0.5
		}
		cost[i] = float32(litCost)
	}
}

func estimateBitCostsForLiteralsUTF8Before(data []byte, pos, length, mask uint, histogram []uint, cost []float32) {
	maxUTF8 := decideMultiByteStatsLevel(data, pos, length, mask)
	windowHalf := uint(495)
	inWindow := min(windowHalf, length)
	var inWindowUTF8 [3]uint

	// Clear histograms: 3 * 256 entries.
	clear(histogram[:3*256])

	// Bootstrap histograms from the initial window.
	lastC := uint(0)
	utf8Pos := uint(0)
	for i := range inWindow {
		c := uint(data[(pos+i)&mask])
		histogram[256*utf8Pos+c]++
		inWindowUTF8[utf8Pos]++
		utf8Pos = utf8Position(lastC, c, maxUTF8)
		lastC = c
	}

	// Compute bit costs with sliding window.
	for i := range length {
		if i >= windowHalf {
			// Remove a byte in the past.
			var c, lc uint
			if i >= windowHalf+1 {
				c = uint(data[(pos+i-windowHalf-1)&mask])
			}
			if i >= windowHalf+2 {
				lc = uint(data[(pos+i-windowHalf-2)&mask])
			}
			utf8Pos2 := utf8Position(lc, c, maxUTF8)
			histogram[256*utf8Pos2+uint(data[(pos+i-windowHalf)&mask])]--
			inWindowUTF8[utf8Pos2]--
		}
		if i+windowHalf < length {
			// Add a byte in the future.
			c := uint(data[(pos+i+windowHalf-1)&mask])
			lc := uint(data[(pos+i+windowHalf-2)&mask])
			utf8Pos2 := utf8Position(lc, c, maxUTF8)
			histogram[256*utf8Pos2+uint(data[(pos+i+windowHalf)&mask])]++
			inWindowUTF8[utf8Pos2]++
		}

		var c uint
		if i >= 1 {
			c = uint(data[(pos+i-1)&mask])
		}
		var lc uint
		if i >= 2 {
			lc = uint(data[(pos+i-2)&mask])
		}
		curUTF8Pos := utf8Position(lc, c, maxUTF8)
		maskedPos := (pos + i) & mask
		histo := histogram[256*curUTF8Pos+uint(data[maskedPos])]
		if histo == 0 {
			histo = 1
		}
		litCost := fastLog2(int(inWindowUTF8[curUTF8Pos])) - fastLog2(int(histo))
		litCost += 0.02905
		if litCost < 1.0 {
			litCost = litCost*0.5 + 0.5
		}
		// Make the first bytes more expensive to account for the statistical
		// anomaly at the beginning of the data.
		const prologueLength = 2000
		const multiplier = 0.35 / prologueLength
		if i < prologueLength {
			litCost += 0.35 + multiplier*float64(i)
		}
		cost[i] = float32(litCost)
	}
}

func literalCostFixture(tb testing.TB, text bool) (data []byte, histogram []uint, cost []float32) {
	tb.Helper()
	const n = literalCostBenchBytes
	data = make([]byte, n)
	for i := range data {
		if text {
			data[i] = byte(32 + (i*7)%90)
		} else {
			data[i] = byte(i * 251)
		}
	}
	histogram = make([]uint, 3*256)
	cost = make([]float32, n)
	return
}

func BenchmarkEstimateBitCostsForLiteralsRaw256KiB(b *testing.B) {
	const n = literalCostBenchBytes
	data, histogram, cost := literalCostFixture(b, false)
	b.Run("impl=before_recompute_log", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(n))
		for range b.N {
			estimateBitCostsForLiteralsRawBefore(data, 0, n, uint(n-1), histogram, cost)
		}
		literalCostSink = cost[0]
	})
	b.Run("impl=after_cached_log", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(n))
		for range b.N {
			estimateBitCostsForLiteralsRaw(data, 0, n, uint(n-1), histogram, cost)
		}
		literalCostSink = cost[0]
	})
}

func BenchmarkEstimateBitCostsForLiteralsUTF8256KiB(b *testing.B) {
	const n = literalCostBenchBytes
	data, histogram, cost := literalCostFixture(b, true)
	b.Run("impl=before_recompute_log", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(n))
		for range b.N {
			estimateBitCostsForLiteralsUTF8Before(data, 0, n, uint(n-1), histogram, cost)
		}
		literalCostSink = cost[0]
	})
	b.Run("impl=after_cached_log", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(n))
		for range b.N {
			estimateBitCostsForLiteralsUTF8(data, 0, n, uint(n-1), histogram, cost)
		}
		literalCostSink = cost[0]
	})
}
