// Parity and benchmarks for the findBlocks DP kernels. Vector and scalar live
// in one binary so the comparison carries no run-to-run drift.

//go:build go1.27 && amd64 && !purego

package encoder

import (
	"math"
	"testing"
)

var (
	findBlocksMinSink  float64
	findBlocksBestSink int
)

func findBlocksKernelFixture(tb testing.TB, numHistograms int) (cost, insertCost []float64) {
	tb.Helper()
	cost = make([]float64, numHistograms)
	insertCost = make([]float64, numHistograms)
	for i := range cost {
		cost[i] = float64((i*7919)%1000) / 13
		insertCost[i] = float64((i*104729)%997) / 7
	}
	return cost, insertCost
}

func TestFindBlocksDPStepVectorMatchesScalarIncludingTieBreak(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 33, 100} {
		costV, insert := findBlocksKernelFixture(t, n)
		costS := append([]float64(nil), costV...)

		gotMin, gotBest := findBlocksDPStepAVX256HoistedBound(costV, insert)
		wantMin, wantBest := findBlocksDPStepOriginal(costS, insert)

		if gotMin != wantMin || gotBest != wantBest {
			t.Errorf("n=%d: AVX256 DP step returned (min=%v, best=%d), scalar returned "+
				"(min=%v, best=%d); the index becomes this byte's block type, so any "+
				"difference changes the compressed output", n, gotMin, gotBest, wantMin, wantBest)
		}
		for i := range costS {
			if costV[i] != costS[i] {
				t.Fatalf("n=%d index %d: AVX256 left cost %v, scalar left %v", n, i, costV[i], costS[i])
			}
		}
	}
}

func TestFindBlocksDPStepTieGoesToTheFirstOccurrence(t *testing.T) {
	for _, n := range []int{4, 8, 16, 100} {
		cost := make([]float64, n)
		insert := make([]float64, n)
		scalarCost := make([]float64, n)

		_, gotBest := findBlocksDPStepAVX256HoistedBound(cost, insert)
		_, wantBest := findBlocksDPStepOriginal(scalarCost, insert)
		if gotBest != wantBest || gotBest != 0 {
			t.Errorf("n=%d: with every cost equal the AVX256 step chose index %d and scalar "+
				"chose %d; both must choose the first occurrence (0) or block types shift",
				n, gotBest, wantBest)
		}
	}
}

func TestFindBlocksClampVectorMatchesScalarAndSetsIdenticalSignalBits(t *testing.T) {
	for _, n := range []int{1, 4, 7, 8, 9, 16, 17, 63, 100} {
		costV, _ := findBlocksKernelFixture(t, n)
		costS := append([]float64(nil), costV...)
		sigV := make([]byte, (n+7)/8+4)
		sigS := make([]byte, len(sigV))

		const minCost, switchCost = 3.5, 20.0
		findBlocksClampAVX256ByteUnrolled(costV, sigV, minCost, switchCost)
		findBlocksClampOriginal(costS, sigS, minCost, switchCost)

		for i := range costS {
			if math.Abs(costV[i]-costS[i]) > 0 {
				t.Fatalf("n=%d index %d: vector clamp left cost %v, scalar left %v",
					n, i, costV[i], costS[i])
			}
		}
		for i := range sigS {
			if sigV[i] != sigS[i] {
				t.Fatalf("n=%d signal byte %d: vector set %08b, scalar set %08b; the bitmap "+
					"drives block-switch decisions, so a wrong bit changes the output",
					n, i, sigV[i], sigS[i])
			}
		}
	}
}

func BenchmarkFindBlocksDPStep100Histograms(b *testing.B) {
	b.Run("impl=scalar", func(b *testing.B) {
		cost, insert := findBlocksKernelFixture(b, 100)
		b.ReportAllocs()
		for range b.N {
			findBlocksMinSink, findBlocksBestSink = findBlocksDPStepOriginal(cost, insert)
		}
	})
	b.Run("impl=avx256_v3", func(b *testing.B) {
		cost, insert := findBlocksKernelFixture(b, 100)
		b.ReportAllocs()
		for range b.N {
			findBlocksMinSink, findBlocksBestSink = findBlocksDPStepAVX256HoistedBound(cost, insert)
		}
	})
}

func BenchmarkFindBlocksClamp100Histograms(b *testing.B) {
	const minCost, switchCost = 3.5, 20.0

	b.Run("impl=scalar", func(b *testing.B) {
		cost, _ := findBlocksKernelFixture(b, 100)
		sig := make([]byte, 100/8+4)
		b.ReportAllocs()
		for range b.N {
			findBlocksClampOriginal(cost, sig, minCost, switchCost)
		}
	})
	b.Run("impl=avx256_v3", func(b *testing.B) {
		cost, _ := findBlocksKernelFixture(b, 100)
		sig := make([]byte, 100/8+4)
		b.ReportAllocs()
		for range b.N {
			findBlocksClampAVX256ByteUnrolled(cost, sig, minCost, switchCost)
		}
	})
}
