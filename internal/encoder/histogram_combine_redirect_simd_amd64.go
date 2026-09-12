// AVX2 symbol-redirect loop for histogramCombine.
//
// Replaces the inline loop formerly in histogramCombine — it repoints every
// symbol of a merged-away cluster at the cluster it was merged into, runs once
// per merge over every input histogram, and reaches 16384 entries for literals
// carrying all 64 contexts.
//
// Measured per microarchitecture level, 16384 symbols, against the scalar loop:
//
//	v1  no assembly written for this kernel   6027 ns    baseline
//	v2  no assembly written for this kernel   6027 ns    baseline
//	v3  AVX2, masked tail                     1313 ns    +359%   <- selected on v3+
//	v4  AVX-512, masked tail                   736 ns    +719%   <- see note
//
// v4 is deliberately left on the v3 kernel. AVX-512 is 1.8x faster in isolation,
// but the sibling histogram kernels showed a large penalty from interleaving
// 512-bit code with populationCost's scalar math.Log2 — 36.5 ms against 13.1 ms
// for byte-identical output — and this kernel sits in the same loop. Its
// exposure is far lower, since it runs once per merge rather than thousands of
// times, so AVX-512 may well win here; it is not selected because that has not
// been measured in place.
//
// v1 and v2 fall back to the scalar loop: no SSE assembly was written for this
// kernel, and archsimd cannot emit pre-AVX instructions.

//go:build go1.27 && amd64 && !purego

package encoder

import "simd/archsimd"

// histogramCombineHasAVX2 is read once at startup. Dispatch is a branch on a
// fixed value rather than a function pointer so the selected kernel stays a
// direct call.
var histogramCombineHasAVX2 = archsimd.X86.AVX2()

// histogramCombineRedirect repoints symbols from one cluster to another.
func histogramCombineRedirect(s []uint32, old, replacement uint32) {
	if histogramCombineHasAVX2 {
		histogramCombineRedirectAVX2MaskedTail(s, old, replacement)
		return
	}
	histogramCombineRedirectScalar(s, old, replacement)
}

// histogramCombineRedirectAVX2MaskedTail rewrites every occurrence of old using
// 256-bit vectors and a masked tail instead of a scalar remainder.
func histogramCombineRedirectAVX2MaskedTail(s []uint32, old, replacement uint32) {
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

// histogramCombineRedirectScalar rewrites every occurrence of old to
// replacement. This is the original inline loop, kept as the pre-AVX2 path.
func histogramCombineRedirectScalar(s []uint32, old, replacement uint32) {
	for i := range s {
		if s[i] == old {
			s[i] = replacement
		}
	}
}
