package brrr

import "testing"

func TestClusterIdenticalHistograms(t *testing.T) {
	// Identical histograms should cluster down to 1.
	const alphabetSize = 8
	const inSize = 4
	in := make([]uint32, inSize*alphabetSize)
	for i := range inSize {
		for j := range alphabetSize {
			in[i*alphabetSize+j] = 10
		}
	}
	out := make([]uint32, inSize*alphabetSize)
	outSize, symbols := clusterHistograms(in, inSize, alphabetSize, 256, out, &q10Bufs{})
	if outSize != 1 {
		t.Errorf("identical histograms: outSize = %d, want 1", outSize)
	}
	for i, s := range symbols {
		if s != 0 {
			t.Errorf("symbols[%d] = %d, want 0", i, s)
		}
	}
}

func TestClusterDifferentHistograms(t *testing.T) {
	// Maximally different histograms should stay separate.
	const alphabetSize = 8
	const inSize = 3
	in := make([]uint32, inSize*alphabetSize)
	// Histogram 0: all weight on symbol 0
	in[0] = 100
	// Histogram 1: all weight on symbol 4
	in[1*alphabetSize+4] = 100
	// Histogram 2: all weight on symbol 7
	in[2*alphabetSize+7] = 100

	out := make([]uint32, inSize*alphabetSize)
	outSize, symbols := clusterHistograms(in, inSize, alphabetSize, 256, out, &q10Bufs{})
	if outSize != 3 {
		t.Errorf("different histograms: outSize = %d, want 3", outSize)
	}
	// Each symbol should map to a different cluster.
	seen := make(map[uint32]bool)
	for i, s := range symbols {
		seen[s] = true
		if s >= uint32(outSize) {
			t.Errorf("symbols[%d] = %d, out of range [0, %d)", i, s, outSize)
		}
	}
	if len(seen) != 3 {
		t.Errorf("expected 3 unique clusters, got %d", len(seen))
	}
}

func TestClusterSymbolsMapCorrectly(t *testing.T) {
	// Verify that symbols correctly maps every input histogram to an output cluster.
	const alphabetSize = 4
	const inSize = 6
	in := make([]uint32, inSize*alphabetSize)
	// Group A (similar): histograms 0, 2, 4
	for _, idx := range []int{0, 2, 4} {
		in[idx*alphabetSize+0] = 50
		in[idx*alphabetSize+1] = 50
	}
	// Group B (similar): histograms 1, 3, 5
	for _, idx := range []int{1, 3, 5} {
		in[idx*alphabetSize+2] = 50
		in[idx*alphabetSize+3] = 50
	}

	out := make([]uint32, inSize*alphabetSize)
	outSize, symbols := clusterHistograms(in, inSize, alphabetSize, 256, out, &q10Bufs{})
	if outSize != 2 {
		t.Errorf("expected 2 clusters, got %d", outSize)
	}

	// All Group A histograms should have the same symbol.
	if symbols[0] != symbols[2] || symbols[0] != symbols[4] {
		t.Errorf("Group A not clustered: symbols = [%d, _, %d, _, %d, _]",
			symbols[0], symbols[2], symbols[4])
	}
	// All Group B histograms should have the same symbol.
	if symbols[1] != symbols[3] || symbols[1] != symbols[5] {
		t.Errorf("Group B not clustered: symbols = [_, %d, _, %d, _, %d]",
			symbols[1], symbols[3], symbols[5])
	}
	// The two groups should be different.
	if symbols[0] == symbols[1] {
		t.Error("Group A and Group B should be in different clusters")
	}
}

func TestClusterStressLargeInput(t *testing.T) {
	// Stress test with 256+ input histograms (exercises the 64-batch boundary).
	const alphabetSize = 4
	const inSize = 300
	in := make([]uint32, inSize*alphabetSize)
	for i := range inSize {
		// All histograms are identical.
		in[i*alphabetSize+0] = 10
		in[i*alphabetSize+1] = 20
	}
	out := make([]uint32, inSize*alphabetSize)
	outSize, symbols := clusterHistograms(in, inSize, alphabetSize, 256, out, &q10Bufs{})
	if outSize != 1 {
		t.Errorf("expected 1 cluster for identical histograms, got %d", outSize)
	}
	for i, s := range symbols {
		if s != 0 {
			t.Errorf("symbols[%d] = %d, want 0", i, s)
		}
	}
}

func TestClusterMaxHistogramsLimit(t *testing.T) {
	// With maxHistograms=2, even different histograms get merged.
	const alphabetSize = 4
	const inSize = 10
	in := make([]uint32, inSize*alphabetSize)
	for i := range inSize {
		// Each histogram has weight on a different symbol.
		in[i*alphabetSize+(i%alphabetSize)] = 100
	}
	out := make([]uint32, inSize*alphabetSize)
	outSize, symbols := clusterHistograms(in, inSize, alphabetSize, 2, out, &q10Bufs{})
	if outSize > 2 {
		t.Errorf("expected at most 2 clusters, got %d", outSize)
	}
	for i, s := range symbols {
		if s >= uint32(outSize) {
			t.Errorf("symbols[%d] = %d, out of range [0, %d)", i, s, outSize)
		}
	}
}

func TestHistogramPairIsLess(t *testing.T) {
	// Lower costDiff is "better" (should be at heap root), so the one with
	// higher costDiff is "less" (ranked below).
	a := histogramPair{idx1: 0, idx2: 1, costDiff: -5.0}
	b := histogramPair{idx1: 0, idx2: 1, costDiff: -3.0}
	if histogramPairIsLess(&a, &b) {
		t.Error("a (costDiff=-5) should not rank below b (costDiff=-3)")
	}
	if !histogramPairIsLess(&b, &a) {
		t.Error("b (costDiff=-3) should rank below a (costDiff=-5)")
	}
}

func TestClusterCostDiff(t *testing.T) {
	// clusterCostDiff(a, b) = a*log2(a) + b*log2(b) - (a+b)*log2(a+b).
	// For equal sizes this should be negative (merging reduces entropy estimate).
	d := clusterCostDiff(10, 10)
	if d >= 0 {
		t.Errorf("clusterCostDiff(10, 10) = %v, want negative", d)
	}
}

func TestClusterSingleHistogram(t *testing.T) {
	// Edge case: single input histogram.
	const alphabetSize = 4
	in := []uint32{10, 20, 30, 40}
	out := make([]uint32, alphabetSize)
	outSize, symbols := clusterHistograms(in, 1, alphabetSize, 256, out, &q10Bufs{})
	if outSize != 1 {
		t.Errorf("single histogram: outSize = %d, want 1", outSize)
	}
	if symbols[0] != 0 {
		t.Errorf("symbols[0] = %d, want 0", symbols[0])
	}
}
