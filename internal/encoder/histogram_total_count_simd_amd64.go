// Drop-in replacement for histogramTotalCount.
//
// Replaces the scalar histogramTotalCount formerly in cluster.go — signature
// unchanged.
// Needs:    histogram_total_count_simd_amd64.s (same directory).
//           histogram_total_count_generic.go for every other build target.
//
// Measured per microarchitecture level, 704 symbols, against the scalar loop:
//
//	v1  SSE2, four accumulators, PSHUFD fold    33.3 ns   +739%   <- selected
//	v2  SSE4, four accumulators, PHADDD fold    33.5 ns   +734%
//	v3  AVX2, masked tail                       48.2 ns   +460%
//	v4  AVX-512, reslicing loop                 30.8 ns   +777%
//
// Only the v1 fold ships. PHADDD measured -0.6% against PSHUFD, inside noise,
// so the SSSE3 dispatch was not worth a CPUID probe. AVX-512 does win in
// isolation, but this kernel runs interleaved with populationCost's scalar
// math.Log2 and the transition cost exceeds what the extra width saves.
//
// SSE2 is baseline on amd64, so this needs no feature check and is active in an
// ordinary build at every GOAMD64 level.

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
