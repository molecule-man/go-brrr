package brrr

import "testing"

func TestBlockHistogramsTally(t *testing.T) {
	// input = "abracadabra\x00\x00\x00\x00\x00" (16 bytes, mask 0xF).
	input := []byte("abracadabra\x00\x00\x00\x00\x00")
	mask := uint(len(input) - 1)

	commands := []command{
		// insert 3 literals, copy 1, uses last distance (cmdPrefix < 128)
		{insertLen: 3, copyLen: 1, cmdPrefix: 50, distPrefix: 5},
		// insert 2 literals, copy 2, explicit distance (cmdPrefix >= 128)
		{insertLen: 2, copyLen: 2, cmdPrefix: 200, distPrefix: 0x0407},
		// insert 1 literal, no copy
		{insertLen: 1, copyLen: 0, cmdPrefix: 10, distPrefix: 0},
	}
	// Trace:
	//   input indices: a=0, b=1, r=2, a=3, c=4, a=5, d=6, a=7, b=8, r=9, a=10
	//   cmd 0: insert input[0]='a', [1]='b', [2]='r'; pos=3; +copy 1 → pos=4
	//   cmd 1: insert input[4]='c', [5]='a'; pos=6; +copy 2 → pos=8
	//   cmd 2: insert input[8]='b'; pos=9

	var litHist [alphabetSizeLiteral]uint32
	var cmdHist [alphabetSizeInsertAndCopyLength]uint32
	var distHist [64]uint32
	hist := blockHistograms{lit: litHist[:], cmd: cmdHist[:], dist: distHist[:]}

	pos := uint(0)
	var distTotal uint
	for i := range commands {
		cmd := commands[i]
		posDelta, distDelta := hist.tally(input, pos, mask, cmd)
		pos += posDelta
		distTotal += distDelta
	}

	if pos != 9 {
		t.Errorf("final pos = %d, want 9", pos)
	}

	// Command histogram.
	for _, tc := range []struct {
		prefix uint16
		want   uint32
	}{
		{10, 1}, {50, 1}, {200, 1},
	} {
		if got := cmdHist[tc.prefix]; got != tc.want {
			t.Errorf("cmdHist[%d] = %d, want %d", tc.prefix, got, tc.want)
		}
	}

	// Literal histogram: a×2, b×2, r×1, c×1.
	for _, tc := range []struct {
		ch   byte
		want uint32
	}{
		{'a', 2}, {'b', 2}, {'r', 1}, {'c', 1}, {'d', 0},
	} {
		if got := litHist[tc.ch]; got != tc.want {
			t.Errorf("litHist[%q] = %d, want %d", tc.ch, got, tc.want)
		}
	}

	// Distance histogram: only cmd 1 records a distance.
	// distPrefix 0x0407 → distPrefixCode() = 0x0407 & 0x3FF = 7.
	if distHist[7] != 1 {
		t.Errorf("distHist[7] = %d, want 1", distHist[7])
	}
	// cmd 0 uses last distance (cmdPrefix 50 < 128), no distance entry.
	if distHist[5] != 0 {
		t.Errorf("distHist[5] = %d, want 0 (uses last distance)", distHist[5])
	}

	// distTotal: only cmd 1 recorded a distance.
	if distTotal != 1 {
		t.Errorf("distTotal = %d, want 1", distTotal)
	}
}

func TestBlockHistogramsTallyEmpty(t *testing.T) {
	var litHist [alphabetSizeLiteral]uint32
	var cmdHist [alphabetSizeInsertAndCopyLength]uint32
	var distHist [64]uint32
	hist := blockHistograms{lit: litHist[:], cmd: cmdHist[:], dist: distHist[:]}

	posDelta, distDelta := hist.tally(nil, 0, 0, command{cmdPrefix: 42})

	if posDelta != 0 {
		t.Errorf("posDelta = %d, want 0", posDelta)
	}
	if distDelta != 0 {
		t.Errorf("distDelta = %d, want 0", distDelta)
	}
	if cmdHist[42] != 1 {
		t.Errorf("cmdHist[42] = %d, want 1", cmdHist[42])
	}
	for i, v := range litHist {
		if v != 0 {
			t.Fatalf("litHist[%d] = %d, want 0", i, v)
		}
	}
}

func TestBlockHistogramsTallyZeroCopyNoDistance(t *testing.T) {
	input := []byte("hello\x00\x00\x00")

	var litHist [alphabetSizeLiteral]uint32
	var cmdHist [alphabetSizeInsertAndCopyLength]uint32
	var distHist [64]uint32
	hist := blockHistograms{lit: litHist[:], cmd: cmdHist[:], dist: distHist[:]}

	// Zero copy length must not record a distance, even with cmdPrefix >= 128.
	cmd := command{insertLen: 2, copyLen: 0, cmdPrefix: 200, distPrefix: 0x0003}
	posDelta, distDelta := hist.tally(input, 0, uint(len(input)-1), cmd)

	if posDelta != 2 {
		t.Errorf("posDelta = %d, want 2", posDelta)
	}
	if distDelta != 0 {
		t.Errorf("distDelta = %d, want 0 (zero copy length)", distDelta)
	}
	if distHist[3] != 0 {
		t.Errorf("distHist[3] = %d, want 0 (zero copy length)", distHist[3])
	}
	if litHist['h'] != 1 || litHist['e'] != 1 {
		t.Errorf("litHist: ['h']=%d ['e']=%d, want 1,1", litHist['h'], litHist['e'])
	}
}
