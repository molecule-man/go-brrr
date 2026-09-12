// Parity and benchmarks for the AVX2 symbol-redirect kernel. Vector and scalar
// live in one binary so the comparison carries no run-to-run drift.

//go:build go1.27 && amd64 && !purego

package encoder

import (
	"testing"

	"simd/archsimd"
)

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
	for _, n := range []int{0, 1, 2, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 65, 95, 96, 97, 1000, 16384} {
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

// histogramCombineRedirectAVX2Single is the pre-unroll kernel, kept as the
// benchmark baseline so both run in one binary.
func histogramCombineRedirectAVX2Single(s []uint32, old, replacement uint32) {
	o := archsimd.BroadcastUint32x8(old)
	r := archsimd.BroadcastUint32x8(replacement)
	for len(s) >= 8 {
		v := archsimd.LoadUint32x8(s)
		r.IfElse(v.Equal(o), v).Store(s)
		s = s[8:]
	}
	if len(s) > 0 {
		v, _ := archsimd.LoadUint32x8Part(s)
		r.IfElse(v.Equal(o), v).StorePart(s)
	}
}

func TestHistogramCombineRedirectUnrollMatchesSingle(t *testing.T) {
	const old, replacement = 7, 999
	for _, n := range []int{0, 1, 7, 8, 31, 32, 33, 63, 64, 65, 95, 96, 97, 1000, 16384} {
		got := histogramCombineRedirectFixture(t, n, old)
		want := append([]uint32(nil), got...)
		histogramCombineRedirectAVX2MaskedTail(got, old, replacement)
		histogramCombineRedirectAVX2Single(want, old, replacement)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("n=%d index %d: unrolled kernel wrote %d, single-vector kernel "+
					"wrote %d; the redirect drives cluster assignment so any difference "+
					"changes the compressed output", n, i, got[i], want[i])
			}
		}
	}
}

func BenchmarkHistogramCombineRedirectUnroll16384Symbols(b *testing.B) {
	const v uint32 = 7
	b.Run("impl=OLD_avx2_1x", func(b *testing.B) {
		s := histogramCombineRedirectFixture(b, 16384, v)
		b.ReportAllocs()
		for range b.N {
			histogramCombineRedirectAVX2Single(s, v, v)
		}
	})
	b.Run("impl=NEW_avx2_unroll4", func(b *testing.B) {
		s := histogramCombineRedirectFixture(b, 16384, v)
		b.ReportAllocs()
		for range b.N {
			histogramCombineRedirectAVX2MaskedTail(s, v, v)
		}
	})
}
