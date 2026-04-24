package brrr

import "testing"

func TestLgBlockComputation(t *testing.T) {
	tests := []struct {
		quality   int
		lgwin     int
		wantBlock int
	}{
		// quality < 4: lgblock = 14
		{2, 18, 14},
		{3, 18, 14},
		{3, 22, 14},

		// quality 4–8: lgblock = 16
		{4, 18, 16},
		{4, 22, 16},
		{5, 17, 16},
		{5, 18, 16},
		{5, 22, 16},
		{5, 24, 16},
		{6, 18, 16},
		{6, 22, 16},

		// quality >= 9: lgblock = min(18, max(16, lgwin))
		{9, 10, 16},
		{9, 16, 16},
		{9, 17, 17},
		{9, 18, 18},
		{9, 20, 18},
		{9, 22, 18},
		{9, 24, 18},
	}

	for _, tt := range tests {
		var s encodeState
		s.reset(tt.quality, tt.lgwin, 0)
		if s.lgblock != tt.wantBlock {
			t.Errorf("reset(%d, %d): lgblock = %d, want %d",
				tt.quality, tt.lgwin, s.lgblock, tt.wantBlock)
		}
	}
}
