//go:build go1.27 && amd64 && !purego

package encoder

import "simd/archsimd"

// findBlocksHasAVX512 is read once at startup so dispatch is a branch on a
// fixed value rather than a repeated feature query.
var findBlocksHasAVX512 = archsimd.X86.AVX512()

// findBlocksClamp rebases the running costs against the winning histogram,
// clamps them at the block-switch cost, and records which histograms hit the
// clamp in the switch-signal bitmap.
func findBlocksClamp(cost []float64, sig []byte, minCost, switchCost float64) {
	if findBlocksHasAVX512 {
		findBlocksClampAVX512ByteUnrolled(cost, sig, minCost, switchCost)
		return
	}
	findBlocksClampOriginal(cost, sig, minCost, switchCost)
}

// findBlocksClampAVX512ByteUnrolled processes eight float64 per iteration,
// which is exactly one bitmap byte, so sig is written with one store per group.
func findBlocksClampAVX512ByteUnrolled(cost []float64, sig []byte, minCost, switchCost float64) {
	mc := archsimd.BroadcastFloat64x8(minCost)
	sc := archsimd.BroadcastFloat64x8(switchCost)
	k := 0
	for ; k+8 <= len(cost); k += 8 {
		v0 := archsimd.LoadFloat64x8(cost[k:]).Sub(mc)
		sig[k>>3] = v0.GreaterEqual(sc).ToBits()
		v0.Min(sc).Store(cost[k:])
	}
	findBlocksClampScalarTail(cost, sig, minCost, switchCost, k)
}

// findBlocksClampScalarTail finishes the elements past the last full group.
func findBlocksClampScalarTail(cost []float64, sig []byte, minCost, switchCost float64, from int) {
	for k := from; k < len(cost); k++ {
		cost[k] -= minCost
		if cost[k] >= switchCost {
			cost[k] = switchCost
			sig[k>>3] |= 1 << (k & 7)
		}
	}
}

// findBlocksClampOriginal is the original inline loop, kept as the pre-AVX-512
// path.
func findBlocksClampOriginal(cost []float64, sig []byte, minCost, switchCost float64) {
	for k := range cost {
		cost[k] -= minCost
		if cost[k] >= switchCost {
			cost[k] = switchCost
			sig[k>>3] |= 1 << (k & 7)
		}
	}
}
