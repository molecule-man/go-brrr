//go:build go1.27 && amd64 && !purego

package encoder

import "simd/archsimd"

// histogramCombineHasAVX512 is read once at startup so dispatch is a branch on
// a fixed value rather than a repeated feature query.
var histogramCombineHasAVX512 = archsimd.X86.AVX512()

// histogramCombineRedirect repoints symbols from one cluster to another.
func histogramCombineRedirect(s []uint32, old, replacement uint32) {
	if histogramCombineHasAVX512 {
		histogramCombineRedirectAVX512Masked(s, old, replacement)
		return
	}
	histogramCombineRedirectScalar(s, old, replacement)
}

// histogramCombineRedirectAVX512Masked rewrites every occurrence of old using
// 512-bit vectors, writing only the matching lanes via an opmask so no blend is
// needed. Implemented in histogram_combine_redirect_avx512_amd64.s.
//
//go:noescape
func histogramCombineRedirectAVX512Masked(s []uint32, old, replacement uint32)

// histogramCombineRedirectScalar rewrites every occurrence of old to
// replacement. This is the original inline loop, kept as the pre-AVX-512 path.
func histogramCombineRedirectScalar(s []uint32, old, replacement uint32) {
	for i := range s {
		if s[i] == old {
			s[i] = replacement
		}
	}
}
