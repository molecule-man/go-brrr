//go:build !(go1.27 && amd64) || purego

package encoder

// findBlocksClamp rebases the running costs against the winning histogram,
// clamps them at the block-switch cost, and records which histograms hit the
// clamp in the switch-signal bitmap.
func findBlocksClamp(cost []float64, sig []byte, minCost, switchCost float64) {
	for k := range cost {
		cost[k] -= minCost
		if cost[k] >= switchCost {
			cost[k] = switchCost
			sig[k>>3] |= 1 << (k & 7)
		}
	}
}
