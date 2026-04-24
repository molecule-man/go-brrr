// Tests for decodeState tail-buffer helpers (stashTail / useStashedTail).

package brrr

import (
	"testing"
)

func TestStashTailBasic(t *testing.T) {
	var s decodeState
	// 10 bytes of input; consume the first 4, stash the remaining 6.
	input := []byte{0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80, 0x90, 0xA0}
	s.br.setInput(input)
	s.br.pos = 4 // simulate having consumed 4 bytes

	s.stashTail()

	if s.bufferLength != 6 {
		t.Fatalf("bufferLength = %d, want 6", s.bufferLength)
	}
	want := input[4:]
	for i := range len(want) {
		if s.buffer[i] != want[i] {
			t.Fatalf("buffer[%d] = %#x, want %#x", i, s.buffer[i], want[i])
		}
	}

	// Repoint bitReader and verify reads match.
	s.useStashedTail()
	if s.br.availIn() != 6 {
		t.Fatalf("availIn after useStashedTail = %d, want 6", s.br.availIn())
	}
	for i, b := range want {
		if !s.br.pullByte() {
			t.Fatalf("pullByte returned false at byte %d", i)
		}
		got := s.br.takeBits(8)
		if byte(got) != b {
			t.Fatalf("byte %d: got %#x, want %#x", i, got, b)
		}
	}
}

func TestStashTailWithPartialAccumulator(t *testing.T) {
	var s decodeState
	input := []byte{0xAB, 0xCD, 0xEF, 0x12, 0x34, 0x56}
	s.br.setInput(input)

	// Pull two bytes into the accumulator, consume 4 bits.
	s.br.pullByte()
	s.br.pullByte()
	nibble := s.br.takeBits(4) // consume low 4 bits of 0xAB = 0xB
	if nibble != 0xB {
		t.Fatalf("nibble = %#x, want 0xB", nibble)
	}
	// Accumulator now holds 12 bits (bits 4..15 of the two bytes).
	// pos=2, 4 unconsumed input bytes remain.

	s.stashTail()

	// unload should push the 12 remaining accumulator bits (1 whole byte)
	// back, so the stashed tail should be 5 bytes (pos backed up by 1).
	if s.bufferLength != 5 {
		t.Fatalf("bufferLength = %d, want 5", s.bufferLength)
	}

	// Verify buffer contents: the unloaded byte plus the 4 unconsumed bytes.
	// After unload, the accumulator keeps 4 bits (12 - 8 = 4), and pos
	// backs up by 1 to include the partially-consumed byte.
	wantBuf := []byte{0xCD, 0xEF, 0x12, 0x34, 0x56}
	for i := range len(wantBuf) {
		if s.buffer[i] != wantBuf[i] {
			t.Fatalf("buffer[%d] = %#x, want %#x", i, s.buffer[i], wantBuf[i])
		}
	}

	// Repoint and verify we can read the stashed data.
	s.useStashedTail()

	// The accumulator still has 4 bits from the unload. Pull the first
	// stashed byte to get a full byte of data.
	s.br.pullByte()
	got := s.br.takeBits(8)
	// The 4 remaining bits after takeBits(4) were the high nibble of 0xAB = 0xA.
	// After unload pushed 1 byte back, bitPos=4, val holds those 4 bits.
	// pullByte loads 0xCD on top: val = 0xCD<<4 | 0xA = 0xCDA, bitPos=12.
	// takeBits(8) returns low 8 bits = 0xDA.
	if byte(got) != 0xDA {
		t.Fatalf("first byte after repoint = %#x, want 0xDA", got)
	}
}

func TestStashTailEmpty(t *testing.T) {
	var s decodeState
	input := []byte{0x01, 0x02}
	s.br.setInput(input)
	s.br.pos = 2 // all consumed

	s.stashTail()

	if s.bufferLength != 0 {
		t.Fatalf("bufferLength = %d, want 0", s.bufferLength)
	}
}

func TestStashTailMaxBuffer(t *testing.T) {
	var s decodeState
	// 12 bytes of input, none consumed — only 8 should be stashed.
	input := make([]byte, 12)
	for i := range input {
		input[i] = byte(i + 1)
	}
	s.br.setInput(input)

	s.stashTail()

	if s.bufferLength != 8 {
		t.Fatalf("bufferLength = %d, want 8", s.bufferLength)
	}
	for i := range 8 {
		if s.buffer[i] != byte(i+1) {
			t.Fatalf("buffer[%d] = %d, want %d", i, s.buffer[i], i+1)
		}
	}
}
