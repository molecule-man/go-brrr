//go:build amd64 && !purego

package encoder

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

func findBlocksDPScalarReference(data []uint16, insertCost, cost []float64, switchSignal, blockID []byte, blockSwitchBitcost float64) {
	numHistograms := len(cost)
	bitmapLen := (numHistograms + 7) >> 3
	for byteIx, symbol := range data {
		switchCost := blockSwitchBitcost
		if byteIx < 2000 {
			switchCost *= 0.77 + float64(0.07/2000*float64(byteIx))
		}
		row := int(symbol) * numHistograms
		minCost, best := findBlocksDPStepScalarReference(cost, insertCost[row:row+numHistograms])
		if minCost < noMinCost {
			blockID[byteIx] = byte(best)
		}
		findBlocksClampScalarReference(cost, switchSignal[byteIx*bitmapLen:], minCost, switchCost)
	}
}

func findBlocksDPStepScalarReference(cost, insertCost []float64) (float64, int) {
	minCost, best := noMinCost, 0
	for k := range cost {
		cost[k] += insertCost[k]
		if cost[k] < minCost {
			minCost = cost[k]
			best = k
		}
	}
	return minCost, best
}

func findBlocksClampScalarReference(cost []float64, sig []byte, minCost, switchCost float64) {
	for k := range cost {
		cost[k] -= minCost
		if cost[k] >= switchCost {
			cost[k] = switchCost
			sig[k>>3] |= 1 << (k & 7)
		}
	}
}

const findBlocksDPSwitchBitcost = 28.1

type findBlocksDPCase struct {
	name          string
	data          []uint16
	insertCost    []float64
	cost          []float64
	numHistograms int
}

func findBlocksDPCases() []findBlocksDPCase {
	rng := rand.New(rand.NewPCG(7, 11))
	const alphabet = 32
	// Byte 0's switch cost, rounded at run time as the kernel rounds it.
	bitcost := float64(findBlocksDPSwitchBitcost)
	firstSwitchCost := bitcost * 0.77
	switchCostEdges := []float64{0, firstSwitchCost,
		math.Nextafter(firstSwitchCost, 0), math.Nextafter(firstSwitchCost, math.Inf(1))}
	shapes := map[string]func() float64{
		"random": func() float64 { return rng.Float64() * 12 },
		"ties":   func() float64 { return float64(rng.IntN(3)) },
		"zeros":  func() float64 { return 0 },
		"no_minimum": func() float64 {
			if rng.IntN(4) == 0 {
				return 2e99
			}
			return rng.Float64() * 4
		},
		"signed_zeros":      func() float64 { return math.Copysign(0, float64(rng.IntN(2))-0.5) },
		"switch_cost_edges": func() float64 { return switchCostEdges[rng.IntN(len(switchCostEdges))] },
	}
	shapeNames := []string{"random", "ties", "zeros", "no_minimum", "signed_zeros", "switch_cost_edges"}
	histogramCounts := []int{2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89, 90, 91, 92, 93, 94, 95, 96, 97, 98, 99, 100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111, 112, 113, 114, 115, 116, 117, 118, 119, 120, 121, 122, 123, 124, 125, 126, 127, 128, 129, 130, 131, 132, 133, 134, 135, 136, 200, 255, 256}
	lengths := []int{0, 1, 2, 1999, 2000, 2001, 2600}
	cases := make([]findBlocksDPCase, 0, len(shapeNames)*len(histogramCounts)*len(lengths))
	for _, shape := range shapeNames {
		for _, numHistograms := range histogramCounts {
			for _, length := range lengths {
				data := make([]uint16, length)
				for i := range data {
					data[i] = uint16(rng.IntN(alphabet))
				}
				insertCost := make([]float64, alphabet*numHistograms)
				for i := range insertCost {
					insertCost[i] = shapes[shape]()
				}
				cost := make([]float64, numHistograms)
				for i := range cost {
					cost[i] = shapes[shape]()
				}
				if shape == "switch_cost_edges" {
					// A zero minimum and no insert cost make byte 0 compare cost
					// itself against the switch cost, so equality is exact.
					clear(insertCost)
					cost[0] = 0
				}
				cases = append(cases, findBlocksDPCase{
					fmt.Sprintf("%s/histograms=%d/length=%d", shape, numHistograms, length),
					data, insertCost, cost, numHistograms,
				})
			}
		}
	}
	return cases
}

func runFindBlocksDP(c findBlocksDPCase, dp func([]uint16, []float64, []float64, []byte, []byte, float64)) ([]float64, []byte, []byte) {
	cost := slices.Clone(c.cost)
	switchSignal := make([]byte, len(c.data)*((c.numHistograms+7)>>3))
	blockID := make([]byte, len(c.data))
	for i := range blockID {
		blockID[i] = 0xAA
	}
	dp(c.data, c.insertCost, cost, switchSignal, blockID, findBlocksDPSwitchBitcost)
	return cost, switchSignal, blockID
}

func TestFindBlocksDPWritesNothingPastCostSwitchSignalOrBlockID(t *testing.T) {
	// The histogram counts cover the small path, the 8-wide loop and every tail
	// length. The last row's bitmap byte is the one next to the padding.
	const length, pad = 3, 8
	for _, c := range findBlocksDPCases() {
		if len(c.data) != 2 || !strings.HasPrefix(c.name, "random/") {
			continue
		}
		data := append(slices.Clone(c.data), c.data[0])
		bitmapLen := (c.numHistograms + 7) >> 3
		cost := make([]float64, c.numHistograms+pad)
		copy(cost, c.cost)
		switchSignal := make([]byte, length*bitmapLen+pad)
		blockID := make([]byte, length+pad)
		for i := c.numHistograms; i < len(cost); i++ {
			cost[i] = 12345.75
		}
		for i := length * bitmapLen; i < len(switchSignal); i++ {
			switchSignal[i] = 0x5A
		}
		for i := length; i < len(blockID); i++ {
			blockID[i] = 0x5A
		}

		findBlocksDP(data, c.insertCost, cost[:c.numHistograms], switchSignal[:length*bitmapLen],
			blockID[:length], findBlocksDPSwitchBitcost)

		for i := c.numHistograms; i < len(cost); i++ {
			if cost[i] != 12345.75 {
				t.Errorf("%s: cost[%d] past numHistograms became %v; findBlocks slices cost from a shared arena",
					c.name, i, cost[i])
			}
		}
		for i := length * bitmapLen; i < len(switchSignal); i++ {
			if switchSignal[i] != 0x5A {
				t.Errorf("%s: switchSignal[%d] past length*bitmapLen became %#02x", c.name, i, switchSignal[i])
			}
		}
		for i := length; i < len(blockID); i++ {
			if blockID[i] != 0x5A {
				t.Errorf("%s: blockID[%d] past length became %#02x", c.name, i, blockID[i])
			}
		}
	}
}

func TestFindBlocksDPSSE2MatchesTheScalarLoopBitForBitForTwoTo256HistogramsOnTiesZerosSignedZerosAndPositionsWithoutAMinimum(t *testing.T) {
	var errs []error
	for _, c := range findBlocksDPCases() {
		gotCost, gotSig, gotID := runFindBlocksDP(c, findBlocksDP)
		wantCost, wantSig, wantID := runFindBlocksDP(c, findBlocksDPScalarReference)
		for i := range wantCost {
			if math.Float64bits(gotCost[i]) != math.Float64bits(wantCost[i]) {
				errs = append(errs, fmt.Errorf("%s: cost[%d] bits %#016x, scalar loop %#016x; cost is the DP state every later "+
					"byte builds on", c.name, i, math.Float64bits(gotCost[i]), math.Float64bits(wantCost[i])))
				break
			}
		}
		if !slices.Equal(gotSig, wantSig) {
			errs = append(errs, fmt.Errorf("%s: switchSignal differs from the scalar loop; the bitmap drives the backtrace, "+
				"so a wrong bit moves block boundaries", c.name))
		}
		if !slices.Equal(gotID, wantID) {
			errs = append(errs, fmt.Errorf("%s: blockID differs from the scalar loop, including positions without a minimum "+
				"that must keep their previous value", c.name))
		}
	}
	if err := errors.Join(errs...); err != nil {
		t.Error(err)
	}
}

func BenchmarkFindBlocksDP2600Bytes100Histograms(b *testing.B) {
	for _, c := range findBlocksDPCases() {
		if c.name != "random/histograms=100/length=2600" {
			continue
		}
		cost := make([]float64, c.numHistograms)
		switchSignal := make([]byte, len(c.data)*((c.numHistograms+7)>>3))
		blockID := make([]byte, len(c.data))
		b.ReportAllocs()
		b.SetBytes(int64(len(c.data)))
		for range b.N {
			copy(cost, c.cost)
			findBlocksDP(c.data, c.insertCost, cost, switchSignal, blockID, findBlocksDPSwitchBitcost)
		}
	}
}

func BenchmarkFindBlocksDP2600BytesPerHistogramCount(b *testing.B) {
	cases := map[string]findBlocksDPCase{}
	for _, c := range findBlocksDPCases() {
		cases[c.name] = c
	}
	for _, n := range []int{2, 3, 4, 6, 8, 9, 12, 16, 17, 24, 32, 48, 50, 64, 96, 100, 128, 200, 256} {
		c, ok := cases[fmt.Sprintf("random/histograms=%d/length=2600", n)]
		if !ok {
			b.Fatalf("no random 2600-byte fixture with %d histograms", n)
		}
		b.Run(fmt.Sprintf("histograms=%d", n), func(b *testing.B) {
			cost := make([]float64, c.numHistograms)
			switchSignal := make([]byte, len(c.data)*((c.numHistograms+7)>>3))
			blockID := make([]byte, len(c.data))
			b.ReportAllocs()
			b.SetBytes(int64(len(c.data)))
			b.ResetTimer()
			for range b.N {
				copy(cost, c.cost)
				findBlocksDP(c.data, c.insertCost, cost, switchSignal, blockID, findBlocksDPSwitchBitcost)
			}
		})
	}
}
