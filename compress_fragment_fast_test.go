package brrr

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// newTestArena creates a onePassArena initialized with default command prefix
// codes, ready for compression.
func newTestArena() *onePassArena {
	var s onePassArena
	s.initCommandPrefixCodes()
	return &s
}

// compressForTest is a helper that compresses input with compressFragmentFast
// and returns the bitstream result.
func compressForTest(t *testing.T, input []byte, tableBits uint) ([]byte, uint) {
	t.Helper()
	s := newTestArena()
	tableSize := 1 << tableBits
	table := make([]uint32, tableSize)
	// Allocate generous buffer: worst case is uncompressed metablock.
	bufSize := len(input)*2 + 1024
	buf := make([]byte, bufSize)
	b := bitWriter{buf: buf}
	compressFragmentFast(s, input, true, table, &b)
	n := (b.bitOffset + 7) / 8
	return buf[:n], b.bitOffset
}

func TestCompressFragmentFast(t *testing.T) {
	t.Run("empty_last", func(t *testing.T) {
		buf, bits := compressForTest(t, nil, 9)
		snapshotBitstream(t, buf, bits)
	})

	t.Run("hello_world", func(t *testing.T) {
		buf, bits := compressForTest(t, []byte("Hello, World!"), 9)
		snapshotBitstream(t, buf, bits)
	})

	t.Run("repeated_a", func(t *testing.T) {
		input := bytes.Repeat([]byte("a"), 1000)
		buf, bits := compressForTest(t, input, 11)
		snapshotBitstream(t, buf, bits)
	})

	t.Run("repeated_pattern", func(t *testing.T) {
		input := bytes.Repeat([]byte("abcdefghij"), 200)
		buf, bits := compressForTest(t, input, 11)
		snapshotBitstream(t, buf, bits)
	})

	t.Run("sequential_bytes", func(t *testing.T) {
		input := make([]byte, 512)
		for i := range input {
			input[i] = byte(i % 256)
		}
		buf, bits := compressForTest(t, input, 11)
		snapshotBitstream(t, buf, bits)
	})

	t.Run("pseudo_random", func(t *testing.T) {
		rng := rand.New(rand.NewPCG(42, 0))
		input := make([]byte, 2048)
		for i := range input {
			input[i] = byte(rng.IntN(256))
		}
		buf, bits := compressForTest(t, input, 13)
		snapshotBitstream(t, buf, bits)
	})

	t.Run("large_table_bits_15", func(t *testing.T) {
		input := bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog. "), 100)
		buf, bits := compressForTest(t, input, 15)
		snapshotBitstream(t, buf, bits)
	})
}

func TestCompressFragmentFastMultiBlock(t *testing.T) {
	// Input larger than firstBlockSize (3<<15 = 98304) to test multi-block.
	t.Run("multi_block_merge", func(t *testing.T) {
		input := bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz"), 5000)
		buf, bits := compressForTest(t, input, 13)
		snapshotBitstream(t, buf, bits)
	})
}

func TestCompressFragmentFastNotLast(t *testing.T) {
	// Test non-last block: arena should be updated for the next call.
	s := newTestArena()
	tableSize := 1 << 11
	table := make([]uint32, tableSize)
	input := bytes.Repeat([]byte("test data "), 100)
	buf := make([]byte, len(input)*2+1024)
	b := bitWriter{buf: buf}

	compressFragmentFast(s, input, false, table, &b)

	// Verify that cmdCodeNumBits was updated (non-zero means prefix codes
	// were recomputed for the next block).
	if s.cmdCodeNumBits == 0 {
		t.Error("cmdCodeNumBits should be non-zero after non-last block")
	}
}

func TestHashFragment(t *testing.T) {
	// Basic sanity: same 5 bytes produce the same hash.
	data := []byte("abcdefghijklmnop") // need at least 8 bytes for Uint64
	h1 := hashFragment(data, 0, 55)
	h2 := hashFragment(data, 0, 55)
	if h1 != h2 {
		t.Errorf("same input produced different hashes: %d vs %d", h1, h2)
	}

	// Different 5 bytes should (very likely) produce different hashes.
	data2 := []byte("xyzwvutsrqponmlk")
	h3 := hashFragment(data2, 0, 55)
	if h1 == h3 {
		t.Logf("unlikely collision: hash=%d for both inputs", h1)
	}
}

func TestIsMatch(t *testing.T) {
	// Layout: "abcde___" at 0, "abcde123" at 8, "abcdx___" at 16.
	buf := []byte("abcde___abcde123abcdx___")
	if !isMatch(buf, 0, 8) {
		t.Error("expected match for first 5 bytes")
	}
	if isMatch(buf, 0, 16) {
		t.Error("expected no match when 5th byte differs")
	}
}

func TestUpdateBits(t *testing.T) {
	// Write some bits, then overwrite a portion.
	buf := make([]byte, 8)
	buf[0] = 0xFF
	buf[1] = 0xFF
	buf[2] = 0xFF

	// Overwrite 8 bits starting at bit 4 with 0xAB.
	updateBits(buf, 8, 0xAB, 4)

	// Bits 0-3 of byte 0 should be unchanged (0xF).
	// Bits 4-7 of byte 0 should be low nibble of 0xAB = 0xB.
	// Bits 0-3 of byte 1 should be high nibble of 0xAB = 0xA.
	// Bits 4-7 of byte 1 should be unchanged (0xF).
	wantByte0 := byte(0xBF) // 0xF | (0xB << 4)
	wantByte1 := byte(0xFA) // 0xA | (0xF << 4)
	if buf[0] != wantByte0 {
		t.Errorf("byte 0: got 0x%02x, want 0x%02x", buf[0], wantByte0)
	}
	if buf[1] != wantByte1 {
		t.Errorf("byte 1: got 0x%02x, want 0x%02x", buf[1], wantByte1)
	}
}

func TestStoreMetaBlockHeader(t *testing.T) {
	tests := []struct {
		name         string
		length       uint
		uncompressed bool
		wantBits     uint
		wantBytes    []byte
	}{
		{"small_compressed", 100, false, 20, []byte{0x18, 0x03, 0x00}},
		{"small_uncompressed", 100, true, 20, []byte{0x18, 0x03, 0x08}},
		{"medium_compressed", 100000, false, 24, []byte{0xfa, 0x34, 0x0c}},
		{"large_compressed", 1000000, false, 24, []byte{0xfa, 0x11, 0x7a}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 64)
			b := bitWriter{buf: buf}
			b.writeMetaBlockHeader(int(tt.length), false, tt.uncompressed)
			n := (b.bitOffset + 7) / 8
			assertBitstream(t, buf[:n], b.bitOffset, tt.wantBytes, tt.wantBits)
		})
	}
}

func TestWriteInsertLen(t *testing.T) {
	tests := []struct {
		name      string
		insertLen uint
		wantBits  uint
		wantBytes []byte
	}{
		{"zero", 0, 0, nil},
		{"small_5", 5, 5, []byte{0x15}},
		{"medium_64", 64, 14, []byte{0xdf, 0x3b}},
		{"large_1000", 1000, 19, []byte{0x7f, 0x9b, 0x06}},
		{"max_before_long_6209", 6209, 22, []byte{0xff, 0xfe, 0x3f}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 64)
			b := bitWriter{buf: buf}
			s := newTestArena()
			c := &fragmentCompressor{arena: s, b: &b}
			c.writeInsertLen(tt.insertLen)
			n := (b.bitOffset + 7) / 8
			assertBitstream(t, buf[:n], b.bitOffset, tt.wantBytes, tt.wantBits)
		})
	}
}

func TestWriteLongInsertLen(t *testing.T) {
	tests := []struct {
		name      string
		insertLen uint
		wantBits  uint
		wantBytes []byte
	}{
		{"min_6210", 6210, 24, []byte{0xff, 0x01, 0x00}},
		{"medium_15000", 15000, 24, []byte{0xff, 0x59, 0x89}},
		{"boundary_22593", 22593, 24, []byte{0xff, 0xfd, 0xff}},
		{"boundary_22594", 22594, 34, []byte{0xff, 0x03, 0x00, 0x00, 0x00}},
		{"large_100000", 100000, 34, []byte{0xff, 0x7b, 0xb9, 0x04, 0x00}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 64)
			b := bitWriter{buf: buf}
			s := newTestArena()
			c := &fragmentCompressor{arena: s, b: &b}
			c.writeLongInsertLen(tt.insertLen)
			n := (b.bitOffset + 7) / 8
			assertBitstream(t, buf[:n], b.bitOffset, tt.wantBytes, tt.wantBits)
		})
	}
}

func TestWriteCopyLen(t *testing.T) {
	tests := []struct {
		name      string
		copyLen   uint
		wantBits  uint
		wantBytes []byte
	}{
		{"min_5", 5, 4, []byte{0x04}},
		{"small_9", 9, 4, []byte{0x06}},
		{"medium_50", 50, 11, []byte{0x57, 0x06}},
		{"large_1000", 1000, 19, []byte{0xbf, 0x8a, 0x06}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 64)
			b := bitWriter{buf: buf}
			s := newTestArena()
			c := &fragmentCompressor{arena: s, b: &b}
			c.writeCopyLen(tt.copyLen)
			n := (b.bitOffset + 7) / 8
			assertBitstream(t, buf[:n], b.bitOffset, tt.wantBytes, tt.wantBits)
		})
	}
}

func TestWriteDistance(t *testing.T) {
	tests := []struct {
		name      string
		distance  uint
		wantBits  uint
		wantBytes []byte
	}{
		{"small_1", 1, 7, []byte{0x1b}},
		{"medium_100", 100, 10, []byte{0xe9, 0x00}},
		{"large_10000", 10000, 17, []byte{0x63, 0xe2, 0x00}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 64)
			b := bitWriter{buf: buf}
			s := newTestArena()
			c := &fragmentCompressor{arena: s, b: &b}
			c.writeDistance(tt.distance)
			n := (b.bitOffset + 7) / 8
			assertBitstream(t, buf[:n], b.bitOffset, tt.wantBytes, tt.wantBits)
		})
	}
}
