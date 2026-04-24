package brrr

import "testing"

func TestPopulationCostEmpty(t *testing.T) {
	// Empty histogram → oneSymbolHistogramCost (12.0).
	hist := make([]uint32, 256)
	got := populationCost(hist, len(hist))
	if got != 12.0 {
		t.Errorf("empty histogram: got %v, want 12.0", got)
	}
}

func TestPopulationCostSingleSymbol(t *testing.T) {
	// Single-symbol histogram → 12.0.
	hist := make([]uint32, 256)
	hist[42] = 100
	got := populationCost(hist, len(hist))
	if got != 12.0 {
		t.Errorf("single-symbol histogram: got %v, want 12.0", got)
	}
}

func TestPopulationCostTwoSymbols(t *testing.T) {
	// Two-symbol histogram → 20.0 + totalCount.
	hist := make([]uint32, 256)
	hist[0] = 10
	hist[1] = 20
	got := populationCost(hist, len(hist))
	want := 20.0 + 30.0 // twoSymbolHistogramCost + totalCount
	if got != want {
		t.Errorf("two-symbol histogram: got %v, want %v", got, want)
	}
}

func TestPopulationCostThreeSymbols(t *testing.T) {
	hist := make([]uint32, 256)
	hist[0] = 5
	hist[10] = 10
	hist[20] = 15
	// threeSymbolHistogramCost + 2*(5+10+15) - max(5,10,15) = 28 + 60 - 15 = 73.
	got := populationCost(hist, len(hist))
	want := 73.0
	if got != want {
		t.Errorf("three-symbol histogram: got %v, want %v", got, want)
	}
}

func TestPopulationCostFourSymbols(t *testing.T) {
	hist := make([]uint32, 256)
	hist[0] = 20
	hist[1] = 15
	hist[2] = 5
	hist[3] = 3
	// Sort descending: 20, 15, 5, 3
	// h23 = 5 + 3 = 8
	// histoMax = max(8, 20) = 20
	// fourSymbolHistogramCost + 3*8 + 2*(20+15) - 20 = 37 + 24 + 70 - 20 = 111.
	got := populationCost(hist, len(hist))
	want := 111.0
	if got != want {
		t.Errorf("four-symbol histogram: got %v, want %v", got, want)
	}
}

func TestPopulationCostGeqBitsEntropy(t *testing.T) {
	// For non-trivial histograms (>4 symbols), populationCost >= bitsEntropy
	// because it adds Huffman tree overhead.
	hist := make([]uint32, 256)
	for i := range 10 {
		hist[i] = uint32(i + 1)
	}
	pc := populationCost(hist, len(hist))
	be := bitsEntropy(hist)
	if pc < be {
		t.Errorf("populationCost (%v) < bitsEntropy (%v)", pc, be)
	}
}

func TestPopulationCostSmallAlphabet(t *testing.T) {
	// Works correctly with small alphabet sizes.
	hist := []uint32{10, 20, 30, 40, 50}
	got := populationCost(hist, len(hist))
	if got <= 0 {
		t.Errorf("small alphabet: got non-positive cost %v", got)
	}
}
