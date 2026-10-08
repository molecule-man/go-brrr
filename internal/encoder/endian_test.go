package encoder

import (
	"bytes"
	"testing"
	"unsafe"
)

func TestMatchLenSIMDMismatchPosition(t *testing.T) {
	const n = 48
	for limit := 0; limit <= 40; limit++ {
		for mismatch := 0; mismatch <= limit; mismatch++ {
			data := make([]byte, 2*n)
			for i := range n {
				data[i] = byte(i*7 + 1)
				data[n+i] = data[i]
			}
			if mismatch < n {
				data[n+mismatch] ^= 0x5A
			}
			want := min(mismatch, limit)
			got := matchLenSIMD(unsafe.Pointer(&data[0]), 0, n, limit)
			if got != want {
				t.Fatalf("limit %d, mismatch at %d: got %d, want %d", limit, mismatch, got, want)
			}
		}
	}
}

func TestCompressFragmentTwoPassBytes(t *testing.T) {
	tests := []struct {
		name      string
		input     []byte
		tableBits uint
	}{
		{"hello_world", []byte("Hello, World!"), 9},
		{"repeated_pattern_2000", bytes.Repeat([]byte("abcdefghij"), 200), 11},
		{"pseudo_random_2048", pseudoRandomBytesRT(2048, 42), 13},
		{"fox_sentence_100x", bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog. "), 100), 15},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := compressTwoPassForTest(t, tc.input, tc.tableBits)
			snapshotBitstream(t, out, uint(len(out))*8)
		})
	}
}
