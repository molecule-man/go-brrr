package brrr

import (
	"bytes"
	"testing"
)

// compressTwoPassForTest is a helper that compresses input with
// compressFragmentTwoPass and returns the bitstream result (including
// the brotli stream header for window size 18).
func compressTwoPassForTest(t *testing.T, input []byte, tableBits uint) []byte {
	t.Helper()
	var s twoPassArena
	tableSize := 1 << tableBits
	table := make([]uint32, tableSize)
	commandBuf := make([]uint32, twoPassBlockSize)
	literalBuf := make([]byte, twoPassBlockSize)
	bufSize := len(input)*2 + 1024
	buf := make([]byte, bufSize)
	b := bitWriter{buf: buf}

	// Write the brotli stream header for window size 18.
	b.writeBits(4, 3)

	compressFragmentTwoPass(&s, input, true, commandBuf, literalBuf, table, &b)
	n := (b.bitOffset + 7) / 8
	return buf[:n]
}

func TestCompressFragmentTwoPassRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		input     []byte
		tableBits uint
	}{
		{
			name:      "hello_world",
			input:     []byte("Hello, World!"),
			tableBits: 9,
		},
		{
			name:      "repeated_a_1000",
			input:     bytes.Repeat([]byte("a"), 1000),
			tableBits: 11,
		},
		{
			name:      "repeated_pattern_2000",
			input:     bytes.Repeat([]byte("abcdefghij"), 200),
			tableBits: 11,
		},
		{
			name:      "sequential_512",
			input:     sequentialBytesRT(512),
			tableBits: 11,
		},
		{
			name:      "pseudo_random_2048",
			input:     pseudoRandomBytesRT(2048, 42),
			tableBits: 13,
		},
		{
			name:      "fox_sentence_100x",
			input:     bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog. "), 100),
			tableBits: 15,
		},
		{
			name:      "single_byte",
			input:     []byte("X"),
			tableBits: 9,
		},
		{
			name:      "five_bytes_exact",
			input:     []byte("exact"),
			tableBits: 9,
		},
		{
			name:      "table_bits_8",
			input:     bytes.Repeat([]byte("test8 "), 100),
			tableBits: 8,
		},
		{
			name:      "table_bits_10",
			input:     bytes.Repeat([]byte("test10 "), 100),
			tableBits: 10,
		},
		{
			name:      "table_bits_12",
			input:     bytes.Repeat([]byte("test12 "), 100),
			tableBits: 12,
		},
		{
			name:      "table_bits_14",
			input:     bytes.Repeat([]byte("test14 "), 100),
			tableBits: 14,
		},
		{
			name:      "table_bits_16",
			input:     bytes.Repeat([]byte("test16 "), 500),
			tableBits: 16,
		},
		{
			name:      "table_bits_17",
			input:     bytes.Repeat([]byte("test17 "), 500),
			tableBits: 17,
		},
		{
			name:      "large_block",
			input:     bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz"), 6000),
			tableBits: 13,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed := compressTwoPassForTest(t, tt.input, tt.tableBits)
			decompressed := brotliDecompress(t, compressed)
			if !bytes.Equal(decompressed, tt.input) {
				t.Errorf("round-trip mismatch: got %d bytes, want %d bytes",
					len(decompressed), len(tt.input))
				if len(decompressed) < 200 && len(tt.input) < 200 {
					t.Errorf("got  %q", decompressed)
					t.Errorf("want %q", tt.input)
				}
			}
		})
	}
}
