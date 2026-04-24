package brrr

import "testing"

func TestDistanceAlphabetSize(t *testing.T) {
	tests := []struct {
		npostfix, ndirect, maxnbits uint32
		want                        uint32
	}{
		// NPOSTFIX=0, NDIRECT=0, MAXNBITS=24 → 16 + 0 + (24 << 1) = 64
		{0, 0, 24, 64},
		// NPOSTFIX=1, NDIRECT=0, MAXNBITS=24 → 16 + 0 + (24 << 2) = 112
		{1, 0, 24, 112},
		// NPOSTFIX=0, NDIRECT=15, MAXNBITS=24 → 16 + 15 + (24 << 1) = 79
		{0, 15, 24, 79},
		// NPOSTFIX=3, NDIRECT=120, MAXNBITS=24 → 16 + 120 + (24 << 4) = 520
		{3, 120, 24, 520},
	}
	for _, tt := range tests {
		got := distanceAlphabetSize(tt.npostfix, tt.ndirect, tt.maxnbits)
		if got != tt.want {
			t.Errorf("distanceAlphabetSize(%d, %d, %d) = %d, want %d",
				tt.npostfix, tt.ndirect, tt.maxnbits, got, tt.want)
		}
	}
}

func TestInitDistanceParams(t *testing.T) {
	tests := []struct {
		npostfix, ndirect uint32
		wantAlphabetSize  uint32
	}{
		{0, 0, 64},
		{1, 0, 112},
		{0, 15, 79},
		{3, 120, 520},
	}
	for _, tt := range tests {
		dp := initDistanceParams(tt.npostfix, tt.ndirect)
		if dp.postfixBits != tt.npostfix {
			t.Errorf("postfixBits = %d, want %d", dp.postfixBits, tt.npostfix)
		}
		if dp.numDirectCodes != tt.ndirect {
			t.Errorf("numDirectCodes = %d, want %d", dp.numDirectCodes, tt.ndirect)
		}
		if dp.alphabetSizeMax != tt.wantAlphabetSize {
			t.Errorf("alphabetSizeMax = %d, want %d", dp.alphabetSizeMax, tt.wantAlphabetSize)
		}
		if dp.alphabetSizeLimit != tt.wantAlphabetSize {
			t.Errorf("alphabetSizeLimit = %d, want %d", dp.alphabetSizeLimit, tt.wantAlphabetSize)
		}
	}
}

func TestInitDistanceParamsMaxDistance(t *testing.T) {
	// For NPOSTFIX=0, NDIRECT=0:
	// maxDistance = 0 + (1 << (24+0+2)) - (1 << (0+2)) = (1<<26) - (1<<2) = 67108860
	dp := initDistanceParams(0, 0)
	want := uint32((1 << 26) - (1 << 2))
	if dp.maxDistance != want {
		t.Errorf("maxDistance = %d, want %d", dp.maxDistance, want)
	}
}

func TestRecomputeDistancePrefixes(t *testing.T) {
	// Create commands with known distance encoding, then re-encode
	// with different params and verify round-trip.
	origParams := initDistanceParams(0, 0)

	// Build a few commands with actual distances.
	cmds := make([]command, 3)
	for i, dist := range []uint{20, 100, 1000} {
		cmds[i] = newCommand(commandConfig{
			insertLen:      5,
			copyLen:        4,
			distanceCode:   dist + numDistanceShortCodes - 1,
			numDirectCodes: 0,
			postfixBits:    0,
		})
	}

	newParams := initDistanceParams(1, 0)
	recomputeDistancePrefixes(cmds, origParams, newParams)

	// Verify: reconstruct distance codes and re-encode directly.
	for i, dist := range []uint{20, 100, 1000} {
		distCode := dist + numDistanceShortCodes - 1
		wantPrefix, wantExtra := prefixEncodeCopyDistance(distCode, 0, 1)
		if cmds[i].distPrefix != wantPrefix || cmds[i].distExtra != wantExtra {
			t.Errorf("cmd[%d] after recompute: distPrefix=%d distExtra=%d, want %d %d",
				i, cmds[i].distPrefix, cmds[i].distExtra, wantPrefix, wantExtra)
		}
	}
}

func TestRecomputeDistancePrefixesSameParams(t *testing.T) {
	// Same params should be a no-op.
	params := initDistanceParams(0, 0)
	cmds := []command{
		newCommand(commandConfig{
			insertLen:      5,
			copyLen:        4,
			distanceCode:   20 + numDistanceShortCodes - 1,
			numDirectCodes: 0,
			postfixBits:    0,
		}),
	}
	origPrefix := cmds[0].distPrefix
	origExtra := cmds[0].distExtra
	recomputeDistancePrefixes(cmds, params, params)
	if cmds[0].distPrefix != origPrefix || cmds[0].distExtra != origExtra {
		t.Error("same params should not modify commands")
	}
}

func TestComputeDistanceCost(t *testing.T) {
	params := initDistanceParams(0, 0)
	cmds := []command{
		newCommand(commandConfig{
			insertLen:      5,
			copyLen:        4,
			distanceCode:   20 + numDistanceShortCodes - 1,
			numDirectCodes: 0,
			postfixBits:    0,
		}),
	}

	tmpHist := make([]uint32, params.alphabetSizeMax)
	cost, ok := computeDistanceCost(cmds, params, params, tmpHist)
	if !ok {
		t.Fatal("computeDistanceCost returned ok=false")
	}
	if cost <= 0 {
		t.Errorf("expected positive cost, got %v", cost)
	}
}

func TestComputeDistanceCostExceedsMax(t *testing.T) {
	// origParams with NPOSTFIX=3 allows very large distances.
	// newParams with NPOSTFIX=0 has a smaller maxDistance.
	origParams := initDistanceParams(3, 0)
	newParams := initDistanceParams(0, 0)

	// Pick a distance that fits origParams but exceeds newParams.maxDistance.
	dist := uint(newParams.maxDistance + 1000)
	distCode := dist + numDistanceShortCodes - 1
	cmds := []command{
		newCommand(commandConfig{
			insertLen:      5,
			copyLen:        4,
			distanceCode:   distCode,
			numDirectCodes: 0,
			postfixBits:    3,
		}),
	}

	tmpHist := make([]uint32, max(origParams.alphabetSizeMax, newParams.alphabetSizeMax))
	_, ok := computeDistanceCost(cmds, origParams, newParams, tmpHist)
	if ok {
		t.Error("expected ok=false for distance exceeding newParams.maxDistance")
	}
}
