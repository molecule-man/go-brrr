package brrr

import "testing"

// Test vectors generated from the C reference implementation (brotli-ref/c/enc/prefix.h).
func TestPrefixEncodeCopyDistance(t *testing.T) {
	tests := []struct {
		distanceCode   uint
		numDirectCodes uint
		postfixBits    uint
		wantCode       uint16
		wantExtra      uint32
	}{
		// Short codes: distanceCode < 16 + numDirectCodes → pass through.
		{0, 0, 0, 0, 0},
		{15, 0, 0, 15, 0},
		{5, 4, 0, 5, 0},
		{19, 4, 0, 19, 0},
		{0, 0, 1, 0, 0},

		// numDirectCodes=0, postfixBits=0
		{16, 0, 0, 1040, 0},
		{17, 0, 0, 1040, 1},
		{18, 0, 0, 1041, 0},
		{19, 0, 0, 1041, 1},
		{20, 0, 0, 2066, 0},
		{23, 0, 0, 2066, 3},
		{30, 0, 0, 3092, 2},
		{50, 0, 0, 4118, 6},

		// numDirectCodes=4, postfixBits=0
		{20, 4, 0, 1044, 0},
		{21, 4, 0, 1044, 1},
		{24, 4, 0, 2070, 0},

		// numDirectCodes=0, postfixBits=1
		{16, 0, 1, 1040, 0},
		{17, 0, 1, 1041, 0},
		{18, 0, 1, 1040, 1},
		{19, 0, 1, 1041, 1},
		{20, 0, 1, 1042, 0},
		{24, 0, 1, 2068, 0},

		// numDirectCodes=0, postfixBits=2
		{16, 0, 2, 1040, 0},
		{20, 0, 2, 1040, 1},
		{24, 0, 2, 1044, 0},

		// Mixed numDirectCodes and postfixBits
		{20, 4, 1, 1044, 0},
		{24, 4, 2, 1044, 1},

		// Larger distance codes
		{100, 0, 0, 5144, 24},
		{200, 0, 0, 6170, 60},
		{500, 0, 0, 7197, 104},
		{100, 8, 2, 3112, 7},
		{200, 12, 3, 3144, 1},
	}

	for _, tt := range tests {
		code, extra := prefixEncodeCopyDistance(tt.distanceCode, tt.numDirectCodes, tt.postfixBits)
		if code != tt.wantCode || extra != tt.wantExtra {
			t.Errorf("prefixEncodeCopyDistance(%d, %d, %d) = (%d, %d), want (%d, %d)",
				tt.distanceCode, tt.numDirectCodes, tt.postfixBits,
				code, extra, tt.wantCode, tt.wantExtra)
		}
	}
}

// Test vectors generated from the C reference implementation (brotli-ref/c/enc/command.h).
func TestNewCommand(t *testing.T) {
	tests := []struct {
		numDirectCodes uint
		postfixBits    uint
		insertLen      uint
		copyLen        uint
		copyLenDelta   int
		distanceCode   uint
		wantInsertLen  uint32
		wantCopyLen    uint32
		wantDistExtra  uint32
		wantCmdPrefix  uint16
		wantDistPrefix uint16
	}{
		{0, 0, 1, 4, 0, 0, 1, 4, 0, 10, 0},
		{0, 0, 0, 2, 0, 15, 0, 2, 0, 128, 15},
		{0, 0, 10, 5, 0, 16, 10, 5, 0, 259, 1040},
		{0, 0, 100, 20, 0, 50, 100, 20, 6, 379, 4118},
		// Positive copyLenDelta
		{0, 0, 5, 10, 1, 0, 5, 33554442, 0, 104, 0},
		{0, 0, 5, 10, 3, 0, 5, 100663306, 0, 105, 0},
		// Negative copyLenDelta
		{0, 0, 5, 10, -1, 0, 5, 4261412874, 0, 47, 0},
		{0, 0, 5, 10, -3, 0, 5, 4194304010, 0, 45, 0},
		// With numDirectCodes and postfixBits
		{4, 0, 10, 5, 0, 20, 10, 5, 0, 259, 1044},
		{0, 1, 10, 5, 0, 20, 10, 5, 0, 259, 1042},
		{4, 2, 10, 5, 0, 24, 10, 5, 1, 259, 1044},
		// Larger values
		{0, 0, 1000, 500, 0, 200, 1000, 500, 60, 668, 6170},
		{0, 0, 22594, 2118, 0, 500, 22594, 2118, 104, 703, 7197},
	}

	for _, tt := range tests {
		cfg := commandConfig{
			insertLen:      tt.insertLen,
			copyLen:        tt.copyLen,
			copyLenDelta:   tt.copyLenDelta,
			distanceCode:   tt.distanceCode,
			numDirectCodes: tt.numDirectCodes,
			postfixBits:    tt.postfixBits,
		}
		cmd := newCommand(cfg)
		if cmd.insertLen != tt.wantInsertLen || cmd.copyLen != tt.wantCopyLen ||
			cmd.distExtra != tt.wantDistExtra || cmd.cmdPrefix != tt.wantCmdPrefix ||
			cmd.distPrefix != tt.wantDistPrefix {
			t.Errorf("newCommand(%+v) =\n  {%d, %d, %d, %d, %d}, want\n  {%d, %d, %d, %d, %d}",
				cfg,
				cmd.insertLen, cmd.copyLen, cmd.distExtra, cmd.cmdPrefix, cmd.distPrefix,
				tt.wantInsertLen, tt.wantCopyLen, tt.wantDistExtra, tt.wantCmdPrefix, tt.wantDistPrefix)
		}
	}
}

// distanceCode round-trips with prefixEncodeCopyDistance.
func TestDistanceCode(t *testing.T) {
	tests := []struct {
		distanceCode   uint
		numDirectCodes uint
		postfixBits    uint
	}{
		// Short codes: pass through unchanged.
		{0, 0, 0},
		{5, 0, 0},
		{15, 0, 0},
		{5, 4, 0},
		{19, 4, 0},
		{0, 0, 1},

		// Long-range codes, various parameter combinations.
		{16, 0, 0},
		{17, 0, 0},
		{20, 0, 0},
		{50, 0, 0},
		{100, 0, 0},
		{200, 0, 0},
		{500, 0, 0},
		{20, 4, 0},
		{16, 0, 1},
		{24, 0, 1},
		{16, 0, 2},
		{24, 0, 2},
		{20, 4, 1},
		{24, 4, 2},
		{100, 8, 2},
		{200, 12, 3},
	}

	for _, tt := range tests {
		code, extra := prefixEncodeCopyDistance(tt.distanceCode, tt.numDirectCodes, tt.postfixBits)
		cmd := command{distPrefix: code, distExtra: extra}
		got := cmd.distanceCode(tt.numDirectCodes, tt.postfixBits)
		if got != uint32(tt.distanceCode) {
			t.Errorf("distanceCode(ndc=%d, pb=%d) after encode(%d) = %d, want %d",
				tt.numDirectCodes, tt.postfixBits, tt.distanceCode, got, tt.distanceCode)
		}
	}
}

// Test vectors generated from the C reference implementation (brotli-ref/c/enc/command.h).
func TestNewInsertCommand(t *testing.T) {
	tests := []struct {
		insertLen      uint
		wantInsertLen  uint32
		wantCopyLen    uint32
		wantDistExtra  uint32
		wantCmdPrefix  uint16
		wantDistPrefix uint16
	}{
		{0, 0, 134217728, 0, 130, 16},
		{1, 1, 134217728, 0, 138, 16},
		{5, 5, 134217728, 0, 170, 16},
		{6, 6, 134217728, 0, 178, 16},
		{10, 10, 134217728, 0, 258, 16},
		{100, 100, 134217728, 0, 314, 16},
		{1000, 1000, 134217728, 0, 474, 16},
		{22594, 22594, 134217728, 0, 506, 16},
		{16799809, 16799809, 134217728, 0, 506, 16},
	}

	for _, tt := range tests {
		cmd := newInsertCommand(tt.insertLen)
		if cmd.insertLen != tt.wantInsertLen || cmd.copyLen != tt.wantCopyLen ||
			cmd.distExtra != tt.wantDistExtra || cmd.cmdPrefix != tt.wantCmdPrefix ||
			cmd.distPrefix != tt.wantDistPrefix {
			t.Errorf("newInsertCommand(%d) =\n  {%d, %d, %d, %d, %d}, want\n  {%d, %d, %d, %d, %d}",
				tt.insertLen,
				cmd.insertLen, cmd.copyLen, cmd.distExtra, cmd.cmdPrefix, cmd.distPrefix,
				tt.wantInsertLen, tt.wantCopyLen, tt.wantDistExtra, tt.wantCmdPrefix, tt.wantDistPrefix)
		}
	}
}

func TestCommandDistanceContext(t *testing.T) {
	// The distance context depends on cmdPrefix bits [7:6] (r) and [2:0] (c).
	// Context 0-2 when r∈{0,2,4,7} and c<=2; otherwise context 3.
	tests := []struct {
		cmdPrefix uint16
		want      uint32
	}{
		// r=0 (cmdPrefix 0-63), c=0,1,2 → context 0,1,2
		{0, 0}, {1, 1}, {2, 2},
		// r=0, c=3..7 → context 3
		{3, 3}, {7, 3},
		// r=1 (cmdPrefix 64-127) → always context 3 regardless of c
		{64, 3}, {65, 3}, {66, 3},
		// r=2 (cmdPrefix 128-191), c=0,1,2 → context 0,1,2
		{128, 0}, {129, 1}, {130, 2},
		// r=2, c=3 → context 3
		{131, 3},
		// r=3 (cmdPrefix 192-255) → always context 3
		{192, 3}, {193, 3},
		// r=4 (cmdPrefix 256-319), c=0,1,2 → context 0,1,2
		{256, 0}, {257, 1}, {258, 2},
		// r=4, c=3 → context 3
		{259, 3},
		// r=5 (cmdPrefix 320-383) → always context 3
		{320, 3}, {321, 3},
		// r=6 (cmdPrefix 384-447) → always context 3
		{384, 3}, {385, 3},
		// r=7 (cmdPrefix 448-511), c=0,1,2 → context 0,1,2
		{448, 0}, {449, 1}, {450, 2},
		// r=7, c=3 → context 3
		{451, 3},
		// Higher prefixes (r=8+)
		{512, 3}, {600, 3}, {703, 3},
	}
	for _, tt := range tests {
		cmd := command{cmdPrefix: tt.cmdPrefix}
		got := cmd.distanceContext()
		if got != tt.want {
			t.Errorf("command{cmdPrefix: %d}.distanceContext() = %d, want %d",
				tt.cmdPrefix, got, tt.want)
		}
	}
}

func TestCommandDistanceContextRange(t *testing.T) {
	// All possible cmdPrefix values should produce context 0-3.
	for prefix := range uint16(704) {
		cmd := command{cmdPrefix: prefix}
		ctx := cmd.distanceContext()
		if ctx > 3 {
			t.Fatalf("command{cmdPrefix: %d}.distanceContext() = %d, exceeds 3", prefix, ctx)
		}
	}
}
