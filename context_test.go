package brrr

import "testing"

func TestContextLookupTableSize(t *testing.T) {
	if len(contextLookupTable) != 2048 {
		t.Errorf("contextLookupTable has %d bytes, want 2048", len(contextLookupTable))
	}
}

func TestContextLookupLSB6(t *testing.T) {
	// LSB6 mode: context = p1 & 0x3F, p2 ignored.
	tests := []struct {
		p1, p2 byte
		want   byte
	}{
		{0, 0, 0},
		{1, 0, 1},
		{63, 0, 63},
		{64, 0, 0},   // wraps: 64 & 0x3F = 0
		{127, 0, 63}, // 127 & 0x3F = 63
		{255, 100, 63},
		{42, 200, 42},
	}
	for _, tt := range tests {
		got := contextLookup(contextLSB6, tt.p1, tt.p2)
		if got != tt.want {
			t.Errorf("contextLookup(LSB6, %d, %d) = %d, want %d", tt.p1, tt.p2, got, tt.want)
		}
	}
}

func TestContextLookupMSB6(t *testing.T) {
	// MSB6 mode: context = p1 >> 2, p2 ignored.
	tests := []struct {
		p1, p2 byte
		want   byte
	}{
		{0, 0, 0},
		{4, 0, 1},
		{252, 0, 63},
		{255, 0, 63},
		{100, 50, 25},
	}
	for _, tt := range tests {
		got := contextLookup(contextMSB6, tt.p1, tt.p2)
		if got != tt.want {
			t.Errorf("contextLookup(MSB6, %d, %d) = %d, want %d", tt.p1, tt.p2, got, tt.want)
		}
	}
}

func TestContextLookupUTF8(t *testing.T) {
	// UTF8 mode: context depends on character class of p1 and p2.
	tests := []struct {
		name   string
		p1, p2 byte
		want   byte
	}{
		{"space+control", ' ', 0, 8},          // space=class 2, control=class 0 → 8+0=8
		{"lower_a+space", 'a', ' ', 56},       // lower vowel=14*4=56, space=0 → 56
		{"lower_b+lower_a", 'b', 'a', 60 + 3}, // lower cons=15*4=60, lower=3 → 63
		{"digit+digit", '5', '3', 44 + 2},     // digit=11*4=44, digit=2 → 46
		{"upper_A+punct", 'A', '!', 48 + 1},   // upper vowel=12*4=48, punct=1 → 49
	}
	for _, tt := range tests {
		got := contextLookup(contextUTF8, tt.p1, tt.p2)
		if got != tt.want {
			t.Errorf("contextLookup(UTF8, %q, %q) [%s] = %d, want %d",
				tt.p1, tt.p2, tt.name, got, tt.want)
		}
	}
}

func TestContextLookupSigned(t *testing.T) {
	// Signed mode: 8 magnitude buckets for p1, 8 for p2.
	tests := []struct {
		p1, p2 byte
		want   byte
	}{
		{0, 0, 0},     // bucket 0, bucket 0
		{1, 0, 8 + 0}, // bucket 1 (8), bucket 0 (0)
		{16, 0, 16},   // bucket 2
		{64, 0, 24},   // bucket 3
		{128, 0, 32},  // bucket 4
		{192, 0, 40},  // bucket 5
		{240, 0, 48},  // bucket 6
		{255, 0, 56},  // bucket 7
	}
	for _, tt := range tests {
		got := contextLookup(contextSigned, tt.p1, tt.p2)
		if got != tt.want {
			t.Errorf("contextLookup(Signed, %d, %d) = %d, want %d", tt.p1, tt.p2, got, tt.want)
		}
	}
}

func TestContextLookupRange(t *testing.T) {
	// All outputs should be in [0, 63] for all modes and inputs.
	for mode := range uint(4) {
		for p1 := range 256 {
			for p2 := range 256 {
				c := contextLookup(mode, byte(p1), byte(p2))
				if c > 63 {
					t.Fatalf("contextLookup(%d, %d, %d) = %d, exceeds 63", mode, p1, p2, c)
				}
			}
		}
	}
}
