package encoder

import "testing"

func TestCommonPrefixLen(t *testing.T) {
	tests := []struct {
		name  string
		a, b  []byte
		limit int
		want  int
	}{
		{
			name:  "empty slices",
			a:     []byte{},
			b:     []byte{},
			limit: 0,
			want:  0,
		},
		{
			name:  "identical",
			a:     []byte{1, 2, 3, 4},
			b:     []byte{1, 2, 3, 4},
			limit: 4,
			want:  4,
		},
		{
			name:  "differ at start",
			a:     []byte{1, 2, 3},
			b:     []byte{9, 2, 3},
			limit: 3,
			want:  0,
		},
		{
			name:  "differ in middle",
			a:     []byte{1, 2, 3, 4, 5},
			b:     []byte{1, 2, 9, 4, 5},
			limit: 5,
			want:  2,
		},
		{
			name:  "limit shorter than match",
			a:     []byte{1, 2, 3, 4, 5},
			b:     []byte{1, 2, 3, 4, 5},
			limit: 3,
			want:  3,
		},
		{
			name:  "limit zero on non-empty",
			a:     []byte{1, 2, 3},
			b:     []byte{1, 2, 3},
			limit: 0,
			want:  0,
		},
		{
			name:  "differ at last byte",
			a:     []byte{1, 2, 3, 4},
			b:     []byte{1, 2, 3, 9},
			limit: 4,
			want:  3,
		},
		{
			name:  "long match crosses 8-byte boundary",
			a:     []byte("abcdefghijklmnop"),
			b:     []byte("abcdefghijklmnop"),
			limit: 16,
			want:  16,
		},
		{
			name:  "long mismatch after 8-byte boundary",
			a:     []byte("abcdefghijklmnop"),
			b:     []byte("abcdefghijXlmnop"),
			limit: 16,
			want:  10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchLen(tt.a, tt.b, tt.limit)
			if got != tt.want {
				t.Errorf("commonPrefixLen(%v, %v, %d) = %d, want %d",
					tt.a, tt.b, tt.limit, got, tt.want)
			}
		})
	}
}

func matchLenLongFixture(tb testing.TB, limit, mismatchAt int) (data []byte, a, b uint) {
	tb.Helper()
	data = make([]byte, 2*limit+64)
	for i := range data {
		data[i] = byte(i*7 + 1)
	}
	a, b = 0, uint(limit+32)
	copy(data[b:b+uint(limit)], data[a:a+uint(limit)])
	if mismatchAt >= 0 && mismatchAt < limit {
		data[b+uint(mismatchAt)]++
	}
	return data, a, b
}

func TestMatchLenAtLongAgreesWithMatchLenAtAtEveryMismatchPosition(t *testing.T) {
	// Spans either side of the 4096-byte block, and mismatches at block
	// boundaries, just inside them, and past the last whole block.
	for _, limit := range []int{0, 1, 7, 8, 4095, 4096, 4097, 8192, 8193, 12000, 65536} {
		positions := []int{-1, 0, 1, 7, 8, 100, 4095, 4096, 4097, 8191, 8192, limit - 1}
		for _, mismatchAt := range positions {
			if mismatchAt >= limit {
				continue
			}
			data, a, b := matchLenLongFixture(t, limit, mismatchAt)
			want := matchLenAt(data, a, b, limit)
			if got := matchLenAtLong(data, a, b, limit); got != want {
				t.Fatalf("limit=%d mismatchAt=%d: matchLenAtLong=%d, matchLenAt=%d; a wrong "+
					"common-prefix length changes the emitted copy length",
					limit, mismatchAt, got, want)
			}
		}
	}
}
