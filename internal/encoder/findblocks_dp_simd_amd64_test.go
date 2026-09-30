//go:build amd64 && !purego

package encoder

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
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
		"signed_zeros": func() float64 { return math.Copysign(0, float64(rng.IntN(2))-0.5) },
	}
	shapeNames := []string{"random", "ties", "zeros", "no_minimum", "signed_zeros"}
	histogramCounts := []int{2, 3, 7, 8, 9, 15, 16, 17, 31, 32, 33, 64, 100, 255, 256}
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
	dp(c.data, c.insertCost, cost, switchSignal, blockID, 28.1)
	return cost, switchSignal, blockID
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
			findBlocksDP(c.data, c.insertCost, cost, switchSignal, blockID, 28.1)
		}
	}
}
