// Parity and benchmarks for the SSE2 histogramTotalCount kernel. The kernel and
// the scalar reference live in one binary so the comparison carries no
// run-to-run drift.

//go:build amd64 && !purego

package encoder

import "testing"

var histogramTotalCountSink uint32

func histogramTotalCountScalarReference(h []uint32, alphabetSize int) uint32 {
	var total uint32
	for i := range alphabetSize {
		total += h[i]
	}
	return total
}

func histogramTotalCountFixture(tb testing.TB, n int) []uint32 {
	tb.Helper()
	h := make([]uint32, n)
	for i := range h {
		h[i] = uint32(i*2654435761) % 100000
	}
	return h
}

func TestHistogramTotalCountSSE2MatchesScalarAtEveryTailLength(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 31, 63, 64, 256, 704} {
		h := histogramTotalCountFixture(t, n)
		want := histogramTotalCountScalarReference(h, n)
		if got := histogramTotalCountSSE2FourAccum(h, n); got != want {
			t.Errorf("n=%d: PSHUFD fold summed to %d but the scalar loop gives %d; "+
				"a wrong tail here silently corrupts every cost estimate", n, got, want)
		}
		if got := histogramTotalCount(h, n); got != want {
			t.Errorf("n=%d: histogramTotalCount summed to %d but the scalar loop gives "+
				"%d, so the drop-in replacement is not equivalent", n, got, want)
		}
	}
}

func BenchmarkHistogramTotalCount704Symbols(b *testing.B) {
	h := histogramTotalCountFixture(b, 704)

	b.Run("impl=OLD_scalar_loop", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			histogramTotalCountSink = histogramTotalCountScalarReference(h, 704)
		}
	})
	b.Run("impl=NEW_sse2_pshufd", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			histogramTotalCountSink = histogramTotalCountSSE2FourAccum(h, 704)
		}
	})
}
