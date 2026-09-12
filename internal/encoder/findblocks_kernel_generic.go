// Scalar findBlocks DP kernels for every target the AVX version does not cover:
// non-amd64, purego builds, and Go before 1.27. These are the original inline
// loops from findBlocks, unchanged.

//go:build !(go1.27 && amd64) || purego

package encoder

// findBlocksDPStep adds insertCost into cost and returns the minimum and the
// index of its first occurrence.
//
// First-occurrence tie-breaking is load-bearing: findBlocks stores the index as
// the block type for this byte, so resolving a tie differently changes the
// compressed output.
func findBlocksDPStep(cost, insertCost []float64) (float64, int) {
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

// findBlocksClamp rebases cost against minCost, clamps it at switchCost and
// sets one bit in sig per clamped histogram.
func findBlocksClamp(cost []float64, sig []byte, minCost, switchCost float64) {
	for k := range cost {
		cost[k] -= minCost
		if cost[k] >= switchCost {
			cost[k] = switchCost
			sig[k>>3] |= 1 << (k & 7)
		}
	}
}
