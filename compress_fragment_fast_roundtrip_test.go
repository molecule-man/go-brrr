package brrr

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// TestCompressFragmentFastRoundTrip verifies that compression output can be
// decompressed back to the original input using the reference C decoder.
func TestCompressFragmentFastRoundTrip(t *testing.T) {
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
			name:      "multi_block_130000",
			input:     bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz"), 5000),
			tableBits: 13,
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
			name:      "table_bits_9",
			input:     bytes.Repeat([]byte("test9 "), 100),
			tableBits: 9,
		},
		{
			name:      "table_bits_11",
			input:     bytes.Repeat([]byte("test11 "), 100),
			tableBits: 11,
		},
		{
			name:      "table_bits_13",
			input:     bytes.Repeat([]byte("test13 "), 100),
			tableBits: 13,
		},
		{
			name:      "table_bits_15",
			input:     bytes.Repeat([]byte("test15 "), 100),
			tableBits: 15,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestArena()
			tableSize := 1 << tt.tableBits
			table := make([]uint32, tableSize)
			bufSize := len(tt.input)*2 + 1024
			buf := make([]byte, bufSize)
			b := bitWriter{buf: buf}

			// Write the brotli stream header for window size 18.
			// For lgwin=18 (>17): value = ((18-17)<<1)|1 = 3, bits = 4.
			b.writeBits(4, 3)

			compressFragmentFast(s, tt.input, true, table, &b)
			compressed := buf[:(b.bitOffset+7)/8]

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

func sequentialBytesRT(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 256)
	}
	return b
}

func pseudoRandomBytesRT(n int, seed uint64) []byte {
	rng := rand.New(rand.NewPCG(seed, 0))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.IntN(256))
	}
	return b
}
