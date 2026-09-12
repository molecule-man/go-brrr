//go:build amd64 && !purego

package encoder

import "testing"

func histogramAddScalarReference(dst, src []uint32, alphabetSize int) {
	for i := range alphabetSize {
		dst[i] += src[i]
	}
}

func histogramAddFixture(tb testing.TB, n int) (dst, src []uint32) {
	tb.Helper()
	dst = make([]uint32, n)
	src = make([]uint32, n)
	for i := range dst {
		dst[i] = uint32(i*2654435761) % 50000
		src[i] = uint32(i*40503) % 50000
	}
	return dst, src
}

func TestHistogramAddSSE2MatchesScalarAtEveryTailLength(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 256, 704} {
		dstV, src := histogramAddFixture(t, n)
		dstS := append([]uint32(nil), dstV...)

		histogramAddAsm(dstV, src, n)
		histogramAddScalarReference(dstS, src, n)

		for i := range dstS {
			if dstV[i] != dstS[i] {
				t.Fatalf("n=%d index %d: SSE2 8x-unrolled add produced %d, scalar produced "+
					"%d; a wrong tail corrupts the merged histogram and changes output",
					n, i, dstV[i], dstS[i])
			}
		}
	}
}

func TestHistogramAddSSE2LeavesEntriesPastAlphabetSizeUntouched(t *testing.T) {
	const n = 11
	dst := make([]uint32, 64)
	src := make([]uint32, 64)
	for i := range src {
		src[i] = 7
	}
	histogramAddAsm(dst, src, n)
	for i := range len(dst) {
		if dst[i] != 0 {
			t.Fatalf("index %d past alphabetSize was modified to %d; the histogram arena is "+
				"contiguous, so writing past n corrupts the next histogram", i, dst[i])
		}
	}
}

func BenchmarkHistogramAdd704Symbols(b *testing.B) {
	b.Run("impl=scalar_loop", func(b *testing.B) {
		dst, src := histogramAddFixture(b, 704)
		b.ReportAllocs()
		for range b.N {
			histogramAddScalarReference(dst, src, 704)
		}
	})
	b.Run("impl=sse2_asm", func(b *testing.B) {
		dst, src := histogramAddFixture(b, 704)
		b.ReportAllocs()
		for range b.N {
			histogramAddAsm(dst, src, 704)
		}
	})
}
