//go:build amd64 && !purego

package encoder

// histogramTotalCount sums all entries in a histogram.
func histogramTotalCount(h []uint32, alphabetSize int) uint32 {
	return histogramTotalCountSSE2FourAccum(h, alphabetSize)
}

// histogramTotalCountSSE2FourAccum sums with four independent accumulators and
// folds them with PSHUFD. Implemented in histogram_total_count_simd_amd64.s.
//
//go:noescape
func histogramTotalCountSSE2FourAccum(h []uint32, n int) uint32
