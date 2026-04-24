package brrr

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
