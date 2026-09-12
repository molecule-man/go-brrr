// Parity and benchmarks for the AVX2 symbol-redirect kernel. Vector and scalar
// live in one binary so the comparison carries no run-to-run drift.

//go:build go1.27 && amd64 && !purego

package encoder

import "testing"

func histogramCombineRedirectFixture(tb testing.TB, n int, old uint32) []uint32 {
	tb.Helper()
	s := make([]uint32, n)
	for i := range s {
		if i%3 == 0 {
			s[i] = old
		} else {
			s[i] = uint32(i%17) + 100
		}
	}
	return s
}

func TestHistogramCombineRedirectAVX2MatchesScalarIncludingMaskedTail(t *testing.T) {
	const old, replacement = 7, 999
	for _, n := range []int{0, 1, 2, 7, 8, 9, 15, 16, 17, 31, 64, 1000, 16384} {
		vec := histogramCombineRedirectFixture(t, n, old)
		scalar := append([]uint32(nil), vec...)

		histogramCombineRedirectAVX2MaskedTail(vec, old, replacement)
		histogramCombineRedirectScalar(scalar, old, replacement)

		for i := range scalar {
			if vec[i] != scalar[i] {
				t.Fatalf("n=%d index %d: AVX2 masked tail wrote %d, scalar wrote %d; "+
					"a mismatched tail silently mis-clusters symbols and changes output",
					n, i, vec[i], scalar[i])
			}
		}
	}
}

func TestHistogramCombineRedirectAVX2LeavesBytesPastTheSliceUntouched(t *testing.T) {
	const old, replacement = 7, 999
	backing := make([]uint32, 40)
	for i := range backing {
		backing[i] = old
	}
	histogramCombineRedirectAVX2MaskedTail(backing[:11], old, replacement)
	for i := 11; i < len(backing); i++ {
		if backing[i] != old {
			t.Fatalf("index %d past the slice was rewritten to %d; StorePart must not "+
				"write beyond len(s) or it corrupts the neighbouring symbols",
				i, backing[i])
		}
	}
}

func BenchmarkHistogramCombineRedirect16384Symbols(b *testing.B) {
	const old, replacement = 7, 999

	b.Run("impl=scalar", func(b *testing.B) {
		s := histogramCombineRedirectFixture(b, 16384, old)
		b.ReportAllocs()
		for range b.N {
			histogramCombineRedirectScalar(s, old, replacement)
		}
	})
	b.Run("impl=avx2_v3", func(b *testing.B) {
		s := histogramCombineRedirectFixture(b, 16384, old)
		b.ReportAllocs()
		for range b.N {
			histogramCombineRedirectAVX2MaskedTail(s, old, replacement)
		}
	})
}
