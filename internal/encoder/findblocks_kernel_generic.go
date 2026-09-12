//go:build !amd64 || purego

package encoder

// findBlocksDPStep adds insertCost into cost and returns the minimum together
// with the index of its first occurrence.
func findBlocksDPStep(cost, insertCost []float64) (minCost float64, best int) {
	minCost = noMinCost
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
