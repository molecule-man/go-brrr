//go:build amd64 && !purego

package encoder

// findBlocksDPStep adds insertCost into cost and returns the minimum together
// with the index of its first occurrence.
//
// First-occurrence tie-breaking is load-bearing: findBlocks stores the index as
// the block type for this byte, so resolving a tie differently changes the
// compressed output. The kernel gets it by not fusing the two jobs: one pass
// computes the minimum, a second finds the lowest index comparing equal to it,
// so no lane ordering inside a vector can win a tie.
//
// minCost is noMinCost exactly when the scalar loop would have left its index
// unwritten, so the caller must keep the guard.
//
// Implemented in findblocks_kernel_simd_amd64.s. SSE2 is baseline on amd64, so
// this needs no CPU feature check.
//
//go:noescape
func findBlocksDPStep(cost, insertCost []float64) (minCost float64, best int)

// findBlocksClamp rebases cost against minCost, clamps it at switchCost and
// sets one bit in sig per clamped histogram.
//
// The caller passes switchSignal[ix:], this position's bitmap row, so the
// kernel's sig[k>>3] is the same byte as switchSignal[ix+(k>>3)]. The kernel
// never touches sig[(len(cost)+7)>>3] or beyond: on the last row that byte is
// past the end of switchSignal.
//
// Implemented in findblocks_kernel_simd_amd64.s.
//
//go:noescape
func findBlocksClamp(cost []float64, sig []byte, minCost, switchCost float64)
