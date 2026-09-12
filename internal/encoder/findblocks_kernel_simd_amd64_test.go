//go:build amd64 && !purego

package encoder

import (
	"math"
	"math/rand/v2"
	"testing"
)

const findBlocksTestSwitchCost = 20.0

var findBlocksKernelSizes = []int{1, 2, 3, 7, 8, 15, 16, 17, 63, 64, 65, 100}

var findBlocksKernelShapes = []string{
	"mixed", "all_equal", "tie_at_end", "tie_across_vectors", "min_in_tail",
	"exact_switch_cost", "straddle_switch_cost", "zeros", "signed_zeros",
	"huge", "tiny",
}

// findBlocksDPStepShapes adds NaN, which pins MINPD's operand order. The clamp
// is excluded: it diverges on NaN by design, and NaN cannot reach it because
// insertCost is finite.
var findBlocksDPStepShapes = append(findBlocksKernelShapes, "nan")

var (
	findBlocksMinSink  float64
	findBlocksBestSink int
)

func findBlocksDPStepScalarReference(cost, insertCost []float64) (float64, int) {
	minCost, best := noMinCost, 0
	for k := range cost {
		cost[k] += insertCost[k]
		if cost[k] < minCost {
			minCost = cost[k]
			best = k
		}
	}
	return minCost, best
}

func findBlocksClampScalarReference(cost []float64, sig []byte, minCost, switchCost float64) {
	for k := range cost {
		cost[k] -= minCost
		if cost[k] >= switchCost {
			cost[k] = switchCost
			sig[k>>3] |= 1 << (k & 7)
		}
	}
}

func findBlocksKernelFixture(tb testing.TB, n int, shape string) (cost, insertCost []float64) {
	tb.Helper()
	cost = make([]float64, n)
	insertCost = make([]float64, n)
	sc := float64(findBlocksTestSwitchCost)
	for i := range n {
		switch shape {
		case "mixed":
			cost[i] = float64((i*7919)%1000) / 13
			insertCost[i] = float64((i*104729)%997) / 7
		case "all_equal":
			cost[i], insertCost[i] = 4.25, 1.5
		case "tie_at_end":
			cost[i] = float64(n - i)
			if i >= n-2 {
				cost[i] = -1
			}
		case "tie_across_vectors":
			cost[i] = 10
			if i == 1 || i == 2 || i == 8 || i == 9 {
				cost[i] = -3
			}
		case "min_in_tail":
			cost[i] = float64(n - i)
		case "exact_switch_cost":
			if i%3 == 0 {
				cost[i] = sc
			} else {
				cost[i] = float64(i%5) - 2
			}
		case "straddle_switch_cost":
			if i%2 == 0 {
				cost[i] = math.Nextafter(sc, 0)
			} else {
				cost[i] = math.Nextafter(sc, math.Inf(1))
			}
		case "zeros":
			// leave both at +0.0
		case "signed_zeros":
			// -0.0 + (+0.0) is +0.0, so insertCost has to carry the sign too or
			// the shape is gone before the accumulator ever sees it.
			if i%2 == 1 {
				cost[i] = math.Copysign(0, -1)
				insertCost[i] = math.Copysign(0, -1)
			}
		case "huge":
			switch i % 3 {
			case 0:
				cost[i] = 1e308
			case 1:
				cost[i] = -1e308
			default:
				cost[i] = math.MaxFloat64
			}
		case "nan":
			if i%4 == 1 {
				cost[i] = math.NaN()
			} else {
				cost[i] = float64(i) - 3
			}
		case "tiny":
			if i%2 == 0 {
				cost[i] = 5e-324
			} else {
				cost[i] = -1e-300
			}
		}
	}
	return cost, insertCost
}

func TestFindBlocksDPStepSSE2MatchesScalarBitForBitAtEveryLengthAndShape(t *testing.T) {
	for _, shape := range findBlocksDPStepShapes {
		for _, n := range findBlocksKernelSizes {
			costV, insertCost := findBlocksKernelFixture(t, n, shape)
			costS := append([]float64(nil), costV...)

			gotMin, gotBest := findBlocksDPStep(costV, insertCost)
			wantMin, wantBest := findBlocksDPStepScalarReference(costS, insertCost)

			if math.Float64bits(gotMin) != math.Float64bits(wantMin) {
				t.Errorf("shape=%s n=%d: minCost bits %#016x, scalar %#016x; minCost feeds the "+
					"clamp's subtraction, so differing bits change the compressed output",
					shape, n, math.Float64bits(gotMin), math.Float64bits(wantMin))
			}
			if gotBest != wantBest {
				t.Errorf("shape=%s n=%d: best=%d, scalar=%d; this index becomes the byte's "+
					"block type, so a different tie resolution changes the compressed output",
					shape, n, gotBest, wantBest)
			}
			for i := range costS {
				if math.Float64bits(costV[i]) != math.Float64bits(costS[i]) {
					t.Fatalf("shape=%s n=%d index %d: cost bits %#016x, scalar %#016x",
						shape, n, i, math.Float64bits(costV[i]), math.Float64bits(costS[i]))
				}
			}
		}
	}
}

func TestFindBlocksDPStepResolvesTiesToTheFirstOccurrence(t *testing.T) {
	for _, shape := range []string{"all_equal", "tie_across_vectors", "tie_at_end", "zeros"} {
		for _, n := range []int{1, 2, 3, 8, 9, 16, 17, 100} {
			cost, insertCost := findBlocksKernelFixture(t, n, shape)
			sums := make([]float64, n)
			for i := range n {
				sums[i] = cost[i] + insertCost[i]
			}
			want := 0
			for i := 1; i < n; i++ {
				if sums[i] < sums[want] {
					want = i
				}
			}
			if _, got := findBlocksDPStep(cost, insertCost); got != want {
				t.Errorf("shape=%s n=%d: best=%d but the lowest index holding the minimum is %d",
					shape, n, got, want)
			}
		}
	}
}

func TestFindBlocksDPStepReturnsSentinelWhenNothingBeatsIt(t *testing.T) {
	for _, n := range findBlocksKernelSizes {
		// NaN is the case that pins MINPD's accumulator operand order: a stray
		// NaN is flushed back out by the horizontal fold, so it only survives to
		// the result when every element is one.
		for _, fill := range []float64{noMinCost, math.Inf(1), math.NaN()} {
			cost := make([]float64, n)
			insertCost := make([]float64, n)
			for i := range n {
				cost[i] = fill
			}
			gotMin, gotBest := findBlocksDPStep(cost, insertCost)
			costS := make([]float64, n)
			for i := range n {
				costS[i] = fill
			}
			wantMin, wantBest := findBlocksDPStepScalarReference(costS, insertCost)
			if math.Float64bits(gotMin) != math.Float64bits(wantMin) || gotBest != wantBest {
				t.Errorf("n=%d fill=%v: got (%v,%d), scalar (%v,%d); when nothing beats the "+
					"sentinel the caller must not write a block type",
					n, fill, gotMin, gotBest, wantMin, wantBest)
			}
		}
	}
}

func TestFindBlocksClampSSE2MatchesScalarAndSetsIdenticalSignalBits(t *testing.T) {
	for _, shape := range findBlocksKernelShapes {
		for _, n := range findBlocksKernelSizes {
			for _, minCost := range []float64{0.0, 3.5, -1.5} {
				costV, _ := findBlocksKernelFixture(t, n, shape)
				costS := append([]float64(nil), costV...)
				bitmapLen := (n + 7) >> 3
				sigV := make([]byte, bitmapLen)
				sigS := make([]byte, bitmapLen)
				for i := range sigV {
					// Pre-fill so a MOV implementation fails where |= passes.
					sigV[i], sigS[i] = 0xA5, 0xA5
				}

				findBlocksClamp(costV, sigV, minCost, findBlocksTestSwitchCost)
				findBlocksClampScalarReference(costS, sigS, minCost, findBlocksTestSwitchCost)

				for i := range costS {
					if math.Float64bits(costV[i]) != math.Float64bits(costS[i]) {
						t.Fatalf("shape=%s n=%d minCost=%v index %d: cost bits %#016x, scalar %#016x",
							shape, n, minCost, i, math.Float64bits(costV[i]), math.Float64bits(costS[i]))
					}
				}
				for i := range sigS {
					if sigV[i] != sigS[i] {
						t.Fatalf("shape=%s n=%d minCost=%v sig[%d]=%#02x, scalar %#02x; the switch "+
							"bitmap drives the backtrace, so a wrong bit changes block boundaries",
							shape, n, minCost, i, sigV[i], sigS[i])
					}
				}
			}
		}
	}
}

func TestFindBlocksClampLeavesCostAndSignalPastLengthUntouched(t *testing.T) {
	// n = 8, 16, 64 are the n%8 == 0 cases where an unguarded final byte write
	// would land on sig[bitmapLen], which on the last row is past switchSignal.
	for _, n := range append([]int{8, 16, 64}, findBlocksKernelSizes...) {
		bitmapLen := (n + 7) >> 3
		cost := make([]float64, n+8)
		for i := n; i < len(cost); i++ {
			cost[i] = 12345.75
		}
		sig := make([]byte, bitmapLen+4)
		for i := bitmapLen; i < len(sig); i++ {
			sig[i] = 0x5A
		}

		findBlocksClamp(cost[:n], sig, 1.0, findBlocksTestSwitchCost)

		for i := n; i < len(cost); i++ {
			if cost[i] != 12345.75 {
				t.Errorf("n=%d: cost[%d] past the length was modified to %v; findBlocks slices a "+
					"shared arena, so writing past n corrupts the next position", n, i, cost[i])
			}
		}
		for i := bitmapLen; i < len(sig); i++ {
			if sig[i] != 0x5A {
				t.Errorf("n=%d: sig[%d] past bitmapLen was modified to %#02x; on the last row that "+
					"byte is past the end of switchSignal", n, i, sig[i])
			}
		}
	}
}

func TestFindBlocksClampAtExactlySwitchCostClampsAndSignals(t *testing.T) {
	// One lane in the 8-wide loop, one in the 2-wide tail, one in the odd lane.
	const n = 11
	cost := make([]float64, n)
	for i := range cost {
		cost[i] = -100
	}
	for _, k := range []int{3, 9, 10} {
		cost[k] = findBlocksTestSwitchCost + 1.0
	}
	sig := make([]byte, (n+7)>>3)

	findBlocksClamp(cost, sig, 1.0, findBlocksTestSwitchCost)

	for _, k := range []int{3, 9, 10} {
		if cost[k] != findBlocksTestSwitchCost {
			t.Errorf("index %d: cost=%v, want exactly switchCost; >= must fire at equality",
				k, cost[k])
		}
		if sig[k>>3]&(1<<(k&7)) == 0 {
			t.Errorf("index %d: signal bit not set although the cost was clamped", k)
		}
	}
}

func TestFindBlocksKernelsMatchScalarOverTheRealCallSequence(t *testing.T) {
	const n = 100
	rng := rand.New(rand.NewPCG(1, 2))
	costV := make([]float64, n)
	costS := make([]float64, n)
	insertCost := make([]float64, n)
	sigV := make([]byte, (n+7)>>3)
	sigS := make([]byte, (n+7)>>3)

	for iter := range 200 {
		for i := range insertCost {
			insertCost[i] = rng.Float64() * 12
		}
		clear(sigV)
		clear(sigS)

		minV, bestV := findBlocksDPStep(costV, insertCost)
		minS, bestS := findBlocksDPStepScalarReference(costS, insertCost)
		if bestV != bestS || math.Float64bits(minV) != math.Float64bits(minS) {
			t.Fatalf("iter %d: DP step (%v,%d) vs scalar (%v,%d)", iter, minV, bestV, minS, bestS)
		}

		findBlocksClamp(costV, sigV, minV, findBlocksTestSwitchCost)
		findBlocksClampScalarReference(costS, sigS, minS, findBlocksTestSwitchCost)
		for i := range costS {
			if math.Float64bits(costV[i]) != math.Float64bits(costS[i]) {
				t.Fatalf("iter %d index %d: cost drifted from scalar", iter, i)
			}
		}
		for i := range sigS {
			if sigV[i] != sigS[i] {
				t.Fatalf("iter %d: sig[%d] drifted from scalar", iter, i)
			}
		}
	}
}

func benchmarkFindBlocksDPStep(b *testing.B, n int) {
	cost, insertCost := findBlocksKernelFixture(b, n, "mixed")
	b.Run("impl=before_scalar_loop", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			findBlocksMinSink, findBlocksBestSink = findBlocksDPStepScalarReference(cost, insertCost)
		}
	})
	b.Run("impl=after_sse2_asm", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			findBlocksMinSink, findBlocksBestSink = findBlocksDPStep(cost, insertCost)
		}
	})
}

func benchmarkFindBlocksClamp(b *testing.B, n int) {
	cost, _ := findBlocksKernelFixture(b, n, "mixed")
	sig := make([]byte, (n+7)>>3)
	b.Run("impl=before_scalar_loop", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			findBlocksClampScalarReference(cost, sig, 0.5, findBlocksTestSwitchCost)
		}
	})
	b.Run("impl=after_sse2_asm", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			findBlocksClamp(cost, sig, 0.5, findBlocksTestSwitchCost)
		}
	})
}

func BenchmarkFindBlocksDPStep100Histograms(b *testing.B) { benchmarkFindBlocksDPStep(b, 100) }
func BenchmarkFindBlocksDPStep50Histograms(b *testing.B)  { benchmarkFindBlocksDPStep(b, 50) }
func BenchmarkFindBlocksClamp100Histograms(b *testing.B)  { benchmarkFindBlocksClamp(b, 100) }
func BenchmarkFindBlocksClamp50Histograms(b *testing.B)   { benchmarkFindBlocksClamp(b, 50) }
