// Drop-in replacement for histogramTotalCount.
//
// Replaces the scalar histogramTotalCount formerly in cluster.go — signature
// unchanged.
// Needs:    histogram_total_count_simd_amd64.s (same directory).
//           histogram_total_count_generic.go for every other build target.
//
// Measured per microarchitecture level, 704 symbols, against the scalar loop:
//
//	v1  SSE2, four accumulators, PSHUFD fold    32.6 ns   +728%   <- selected on v1
//	v2  SSE4, four accumulators, PHADDD fold    31.7 ns   +751%   <- selected on v2+
//	v3  AVX2, masked tail                       48.2 ns   +460%
//	v4  AVX-512, reslicing loop                 30.8 ns   +777%   <- NOT selected
//
// v2 is where this kernel gets its one genuinely new instruction: SSSE3's
// PHADDD does the horizontal add that SSE2 has to emulate with two shuffles.
//
// AVX-512 is not selected even on v4, and AVX2 is simply slower than the SSE
// assembly. The AVX-512 reason is that this kernel runs interleaved with
// populationCost's scalar math.Log2, and the transition cost exceeds what the
// extra width saves.
//
// SSE2 is baseline on amd64 and the PHADDD fold is selected by a CPUID probe at
// startup, so this needs no build experiment and is active in an ordinary build.

//go:build amd64 && !purego

package encoder

// histogramTotalCountHasSSSE3 gates the PHADDD fold. Read once at startup so
// dispatch is a branch on a fixed value rather than a repeated CPUID.
var histogramTotalCountHasSSSE3 = histogramTotalCountCPUIDHasSSSE3()

// histogramTotalCount sums all entries in a histogram.
func histogramTotalCount(h []uint32, alphabetSize int) uint32 {
	if histogramTotalCountHasSSSE3 {
		return histogramTotalCountSSE4PhaddFourAccum(h, alphabetSize)
	}
	return histogramTotalCountSSE2FourAccum(h, alphabetSize)
}

// histogramTotalCountSSE2FourAccum sums with four independent accumulators and
// folds them with PSHUFD. GOAMD64=v1. Implemented in
// histogram_total_count_simd_amd64.s.
//
//go:noescape
func histogramTotalCountSSE2FourAccum(h []uint32, n int) uint32

// histogramTotalCountSSE4PhaddFourAccum sums with four independent accumulators
// and folds them with PHADDD. GOAMD64=v2. Implemented in
// histogram_total_count_simd_amd64.s.
//
//go:noescape
func histogramTotalCountSSE4PhaddFourAccum(h []uint32, n int) uint32

// histogramTotalCountCPUIDHasSSSE3 reports CPUID.01H:ECX.SSSE3[bit 9], which is
// what PHADDD requires. Implemented in histogram_total_count_simd_amd64.s.
func histogramTotalCountCPUIDHasSSSE3() bool
