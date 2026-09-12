// Drop-in replacement for histogramAdd.
//
// Replaces the scalar histogramAdd formerly in cluster.go — signature unchanged.
// Needs:    histogram_add_simd_amd64.s (same directory).
//           histogram_add_generic.go for every other build target.
//
// SSE2 is baseline on amd64, so this needs neither a CPU feature check nor a
// build experiment: it is active in an ordinary build.
//
// Measured per microarchitecture level, 704 symbols, against the scalar loop:
//
//	v1  SSE2 assembly, 8x unrolled   77.5 ns   +430%   <- selected at every level
//	v2  SSE4 LDDQU, 8x unrolled      78.0 ns   +427%
//	v3  AVX2 reslicing loop         131.4 ns   +213%
//	v4  AVX-512 reslicing loop       42.3 ns   +873%   <- NOT selected, see below
//
// The SSE2 assembly is selected on every level including v4, which looks wrong
// until you measure it in place rather than in isolation. histogramAdd is
// called from compareAndPushToQueue, which calls populationCost immediately
// afterwards — scalar floating point dominated by math.Log2. Interleaving
// 512-bit vector code with scalar FP that tightly costs far more than the width
// saves: histogramCombine measured 36.5 ms with the AVX-512 kernel against
// 13.1 ms with this one, for byte-identical output, and the whole encoder
// gained +8.6% with AVX-512 against +16.7% without it at quality 10.
//
// AVX2 is not selected either, for the simpler reason that it is slower here
// than the SSE2 assembly.

//go:build amd64 && !purego

package encoder

// histogramAdd adds src histogram into dst histogram element-wise.
func histogramAdd(dst, src []uint32, alphabetSize int) {
	histogramAddSSE2Unrolled8x(dst, src, alphabetSize)
}

// histogramAddSSE2Unrolled8x adds src into dst thirty-two uint32 per iteration
// across eight XMM register pairs. SSE2 is baseline on amd64, so this needs no
// CPU feature check. Implemented in histogramAdd.s.
//
//go:noescape
func histogramAddSSE2Unrolled8x(dst, src []uint32, n int)
