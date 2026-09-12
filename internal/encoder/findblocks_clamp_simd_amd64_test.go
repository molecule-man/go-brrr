//go:build go1.27 && amd64 && !purego

package encoder

import "testing"

func findBlocksClampFixture(tb testing.TB, n int) []float64 {
	tb.Helper()
	cost := make([]float64, n)
	for i := range cost {
		cost[i] = float64((i*7919)%1000) / 13
	}
	return cost
}

func TestFindBlocksClampAVX512MatchesScalarAndSetsIdenticalSignalBits(t *testing.T) {
	if !findBlocksHasAVX512 {
		t.Skip("AVX-512 not available on this CPU")
	}
	const minCost, switchCost = 3.5, 20.0
	for _, n := range []int{0, 1, 4, 7, 8, 9, 15, 16, 17, 63, 64, 100} {
		costV := findBlocksClampFixture(t, n)
		costS := append([]float64(nil), costV...)
		sigV := make([]byte, (n+7)/8+4)
		sigS := make([]byte, len(sigV))

		findBlocksClampAVX512ByteUnrolled(costV, sigV, minCost, switchCost)
		findBlocksClampOriginal(costS, sigS, minCost, switchCost)

		for i := range costS {
			if costV[i] != costS[i] {
				t.Fatalf("n=%d index %d: AVX-512 left cost %v, scalar left %v",
					n, i, costV[i], costS[i])
			}
		}
		for i := range sigS {
			if sigV[i] != sigS[i] {
				t.Fatalf("n=%d signal byte %d: AVX-512 set %08b, scalar set %08b; the bitmap "+
					"drives block-switch decisions, so a wrong bit changes the compressed output",
					n, i, sigV[i], sigS[i])
			}
		}
	}
}

func TestFindBlocksClampAVX512LeavesSignalBytesPastTheRangeUntouched(t *testing.T) {
	if !findBlocksHasAVX512 {
		t.Skip("AVX-512 not available on this CPU")
	}
	const n = 12
	cost := findBlocksClampFixture(t, n)
	sig := make([]byte, 8)
	for i := range sig {
		sig[i] = 0xAA
	}
	findBlocksClampAVX512ByteUnrolled(cost, sig, 3.5, 20.0)
	for i := (n + 7) / 8; i < len(sig); i++ {
		if sig[i] != 0xAA {
			t.Fatalf("signal byte %d past the range was overwritten to %08b; the bitmap row "+
				"is a subslice of switchSignal, so writing past it corrupts the next position",
				i, sig[i])
		}
	}
}

func BenchmarkFindBlocksClamp100Histograms(b *testing.B) {
	const minCost, switchCost = 3.5, 20.0

	b.Run("impl=OLD_scalar", func(b *testing.B) {
		cost := findBlocksClampFixture(b, 100)
		sig := make([]byte, 100/8+4)
		b.ReportAllocs()
		for range b.N {
			findBlocksClampOriginal(cost, sig, minCost, switchCost)
		}
	})
	b.Run("impl=NEW_avx512_masked", func(b *testing.B) {
		if !findBlocksHasAVX512 {
			b.Skip("AVX-512 not available on this CPU")
		}
		cost := findBlocksClampFixture(b, 100)
		sig := make([]byte, 100/8+4)
		b.ReportAllocs()
		for range b.N {
			findBlocksClampAVX512ByteUnrolled(cost, sig, minCost, switchCost)
		}
	})
}
