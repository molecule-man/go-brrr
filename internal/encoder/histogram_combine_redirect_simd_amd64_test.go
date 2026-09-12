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

func TestHistogramCombineRedirectAVX512MatchesScalar(t *testing.T) {
	if !histogramCombineHasAVX512 {
		t.Skip("AVX-512 not available on this CPU")
	}
	const old, replacement = 7, 999
	for _, n := range []int{0, 1, 2, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 65, 1000, 16384} {
		vec := histogramCombineRedirectFixture(t, n, old)
		scalar := append([]uint32(nil), vec...)

		histogramCombineRedirectAVX512Masked(vec, old, replacement)
		histogramCombineRedirectScalar(scalar, old, replacement)

		for i := range scalar {
			if vec[i] != scalar[i] {
				t.Fatalf("n=%d index %d: AVX-512 masked store wrote %d, scalar wrote %d; the "+
					"redirect drives cluster assignment so any difference changes output",
					n, i, vec[i], scalar[i])
			}
		}
	}
}

func TestHistogramCombineRedirectAVX512LeavesBytesPastTheSliceUntouched(t *testing.T) {
	if !histogramCombineHasAVX512 {
		t.Skip("AVX-512 not available on this CPU")
	}
	const old, replacement = 7, 999
	backing := make([]uint32, 64)
	for i := range backing {
		backing[i] = old
	}
	histogramCombineRedirectAVX512Masked(backing[:11], old, replacement)
	for i := 11; i < len(backing); i++ {
		if backing[i] != old {
			t.Fatalf("index %d past the slice was rewritten to %d; the masked store must not "+
				"write beyond len(s) or it corrupts neighbouring symbols", i, backing[i])
		}
	}
}

func BenchmarkHistogramCombineRedirect16384Symbols(b *testing.B) {
	const v uint32 = 7
	b.Run("impl=OLD_scalar", func(b *testing.B) {
		s := histogramCombineRedirectFixture(b, 16384, v)
		b.ReportAllocs()
		for range b.N {
			histogramCombineRedirectScalar(s, v, v)
		}
	})
	b.Run("impl=NEW_avx512_masked", func(b *testing.B) {
		if !histogramCombineHasAVX512 {
			b.Skip("AVX-512 not available")
		}
		s := histogramCombineRedirectFixture(b, 16384, v)
		b.ReportAllocs()
		for range b.N {
			histogramCombineRedirectAVX512Masked(s, v, v)
		}
	})
}
