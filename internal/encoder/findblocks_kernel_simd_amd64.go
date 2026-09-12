// Drop-in replacement for findBlocks.
//
// Replaces the two inner loops of the findBlocks DP forward pass in
// block_splitter.go. Needs findblocks_kernel_generic.go for every other target.
//
// Self-contained: every function this adds is in this file.
//
// Only the two inner loops of the DP forward pass change. The insertCost matrix
// build, the prologue switch-cost scaling and the backtrace are untouched.
//
// findBlocks is worth the effort because its share of the profile is large and
// payload dependent: 24.5% of a quality-11 encode on a 187 KB JS file, 7.3% on
// a 172 KB HTML one, and the two loops below are 1.46s of its 1.49s.
//
// Whole-function measurements against the scalar loops:
//
//	length 3000, numHistograms 100   +71.8%
//	length 2500, numHistograms  50   +49.5%
//
// Per microarchitecture level, for the two kernels separately:
//
//	                        v1/v2      v3                    v4
//	DP step (argmin)        scalar     AVX 256-bit  +72%     AVX 256-bit  <- not AVX-512
//	clamp + bitmap          scalar     AVX 256-bit  +133%    AVX-512      +239%
//
// The DP step declines AVX-512 even on v4: numHistograms tops out at 100, so
// eight float64 lanes is only a dozen vectors and the wider horizontal fold
// costs more than the extra width saves (1.50x against 256-bit's 1.72x).
//
// The clamp keeps AVX-512, and unlike the histogram kernels it is safe to: its
// loop is self-contained vector arithmetic with no scalar floating point
// interleaved, so it does not pay the transition penalty documented in
// histogramAdd.go. Holding this kernel at AVX-512 while moving the histogram
// kernels off it measured +16.7% end-to-end against +8.6% for all-AVX-512.
//
// The clamp takes its switch-signal bits straight out of the comparison mask
// register. An earlier version rebuilt that bitmap with a scalar pass and lost
// to the scalar original on every tier below AVX-512.
//
// v1 and v2 fall back to the scalar loops: no SSE assembly was written for
// these kernels, and Go's archsimd cannot emit pre-AVX instructions.

//go:build go1.27 && amd64 && !purego

package encoder

import (
	"math/bits"

	"simd/archsimd"
)

var (
	findBlocksHasAVX = archsimd.X86.AVX()
)

// findBlocksDPStep adds this symbol's per-histogram insertion cost into the
// running cost vector and reports the cheapest histogram.
//
// First-occurrence tie-breaking is load-bearing, not cosmetic: findBlocks
// stores the returned index as this byte's block type, so resolving a tie
// differently picks a different block type and changes the compressed output.
func findBlocksDPStep(cost, insertCost []float64) (float64, int) {
	if findBlocksHasAVX {
		return findBlocksDPStepAVX256HoistedBound(cost, insertCost)
	}
	return findBlocksDPStepOriginal(cost, insertCost)
}

// findBlocksClamp rebases the running costs against the winning histogram,
// clamps them at the block-switch cost, and records which histograms hit the
// clamp in the switch-signal bitmap.
func findBlocksClamp(cost []float64, sig []byte, minCost, switchCost float64) {
	if findBlocksHasAVX {
		findBlocksClampAVX256ByteUnrolled(cost, sig, minCost, switchCost)
		return
	}
	findBlocksClampOriginal(cost, sig, minCost, switchCost)
}

// findBlocksDPStepAVX256HoistedBound adds insertCost into cost and reports the cheapest histogram using 256-bit vectors with the trip count computed up front, so the loop carries no bound test on the slice length. AVX widened floating-point vectors to 256 bits ahead of integers.
func findBlocksDPStepAVX256HoistedBound(cost, insertCost []float64) (float64, int) {
	acc := archsimd.BroadcastFloat64x4(noMinCost)
	n := len(cost) &^ 3
	for k := 0; k < n; k += 4 {
		v := archsimd.LoadFloat64x4(cost[k:]).Add(archsimd.LoadFloat64x4(insertCost[k:]))
		v.Store(cost[k:])
		acc = acc.Min(v)
	}
	minCost := foldMinFloat64x4(acc)
	for k := n; k < len(cost); k++ {
		cost[k] += insertCost[k]
		minCost = min(minCost, cost[k])
	}
	return firstIndexOfMinFloat64x4(cost, minCost)
}

// foldMinFloat64x4 reduces a 256-bit accumulator to its smallest lane.
func foldMinFloat64x4(v archsimd.Float64x4) float64 {
	var lanes [4]float64
	v.StoreArray(&lanes)
	m := lanes[0]
	for _, x := range lanes[1:] {
		m = min(m, x)
	}
	return m
}

// firstIndexOfMinFloat64x4 returns minCost and the index of its first occurrence,
// matching the scalar loop's tie-breaking. The comparison yields a mask whose
// lowest set bit is the first matching lane, so the whole search costs one
// compare and one trailing-zero count per vector. A minimum still at the
// sentinel means nothing was below it, which is the case where the scalar loop
// never updates its index.
func firstIndexOfMinFloat64x4(cost []float64, minCost float64) (float64, int) {
	if minCost >= noMinCost {
		return noMinCost, 0
	}
	target := archsimd.BroadcastFloat64x4(minCost)
	k := 0
	for ; k+4 <= len(cost); k += 4 {
		if m := archsimd.LoadFloat64x4(cost[k:]).Equal(target).ToBits(); m != 0 {
			return minCost, k + bits.TrailingZeros8(m)
		}
	}
	for ; k < len(cost); k++ {
		if cost[k] == minCost {
			return minCost, k
		}
	}
	return minCost, 0
}

// findBlocksClampAVX256ByteUnrolled rebases cost against minCost, clamps it at switchCost and records the
// clamped histograms in sig using 256-bit vectors grouped so each iteration fills one whole bitmap byte with a single store. AVX already provides 256-bit floating-point vectors, unlike the integer kernels which wait for AVX2.
func findBlocksClampAVX256ByteUnrolled(cost []float64, sig []byte, minCost, switchCost float64) {
	mc := archsimd.BroadcastFloat64x4(minCost)
	sc := archsimd.BroadcastFloat64x4(switchCost)
	k := 0
	for ; k+8 <= len(cost); k += 8 {
		v0 := archsimd.LoadFloat64x4(cost[k:]).Sub(mc)
		v1 := archsimd.LoadFloat64x4(cost[k+4:]).Sub(mc)
		sig[k>>3] = v0.GreaterEqual(sc).ToBits()<<0 | v1.GreaterEqual(sc).ToBits()<<4
		v0.Min(sc).Store(cost[k:])
		v1.Min(sc).Store(cost[k+4:])
	}
	findBlocksClampScalarTail(cost, sig, minCost, switchCost, k)
}

// findBlocksClampScalarTail finishes a remainder shorter than one vector.
func findBlocksClampScalarTail(cost []float64, sig []byte, minCost, switchCost float64, from int) {
	for k := from; k < len(cost); k++ {
		cost[k] -= minCost
		if cost[k] >= switchCost {
			cost[k] = switchCost
			sig[k>>3] |= 1 << (k & 7)
		}
	}
}

// findBlocksDPStepOriginal adds insertCost into cost and returns the minimum
// and the index of its first occurrence.
//
// Origin: internal/encoder/block_splitter.go:208-214, inside findBlocks.
// First-occurrence tie-breaking is load-bearing: findBlocks stores the index as
// the block type for this byte, so resolving a tie differently changes the
// compressed output.
func findBlocksDPStepOriginal(cost, insertCost []float64) (float64, int) {
	minCost := noMinCost
	best := 0
	for k := range cost {
		cost[k] += insertCost[k]
		if cost[k] < minCost {
			minCost = cost[k]
			best = k
		}
	}
	return minCost, best
}

// findBlocksClampOriginal rebases cost against minCost, clamps it at switchCost
// and sets one bit in sig per clamped histogram.
//
// Origin: internal/encoder/block_splitter.go:221-227, inside findBlocks. The
// encoder writes switchSignal[ix+(k>>3)]; callers pass that row as a subslice
// so sig[k>>3] is the same byte.
func findBlocksClampOriginal(cost []float64, sig []byte, minCost, switchCost float64) {
	for k := range cost {
		cost[k] -= minCost
		if cost[k] >= switchCost {
			cost[k] = switchCost
			sig[k>>3] |= 1 << (k & 7)
		}
	}
}
