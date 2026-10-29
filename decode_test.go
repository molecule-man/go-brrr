// Unit tests for decoder edge cases and malformed-stream handling.

package brrr

import (
	"bytes"
	"encoding/hex"
	"io"
	"testing"
	"testing/iotest"

	"github.com/molecule-man/go-brrr/internal/core"
)

// A 22-byte stream that requests a block switch with one block type.
// C reference v1.2.1 rejects it with BROTLI_DECODER_ERROR_FORMAT_BLOCK_SWITCH
// (CVE-2026-111497).
func TestDecodeRejectsBlockSwitchWithOneBlockType(t *testing.T) {
	payload, err := hex.DecodeString("200010414141fcffff07002228f00b60f7f41f000000")
	if err != nil {
		t.Fatal(err)
	}
	const want = "brotli: invalid block switch"

	t.Run("oneshot", func(t *testing.T) {
		_, err := Decompress(payload)
		if err == nil {
			t.Fatal("Decompress accepted the malformed stream")
		}
		if err.Error() != want {
			t.Fatalf("Decompress err = %q, want %q", err, want)
		}
	})

	t.Run("streaming", func(t *testing.T) {
		r := NewReader(bytes.NewReader(payload))
		_, err := io.Copy(io.Discard, r)
		if err == nil {
			t.Fatal("Reader accepted the malformed stream")
		}
		if err.Error() != want {
			t.Fatalf("Reader err = %q, want %q", err, want)
		}
	})
}

func TestDecompressZeroLengthStaticDictionaryTransform(t *testing.T) {
	// Transform 64 (OmitLast9) turns "links" into an empty string, then
	// a literal command emits "A". Verified with Google's C decoder.
	compressed := []byte{
		0x02, 0x00, 0x00, 0x00, 0x44, 0x50, 0x0d,
		0xa2, 0x48, 0xb0, 0xae, 0x00, 0x01,
	}
	t.Run("Decompress", func(t *testing.T) {
		got, err := Decompress(compressed)
		if err != nil {
			t.Fatalf("Decompress: %v", err)
		}
		if string(got) != "A" {
			t.Fatalf("Decompress = %q, want %q", got, "A")
		}
	})
	t.Run("Reader", func(t *testing.T) {
		r := NewReader(iotest.OneByteReader(bytes.NewReader(compressed)))
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if string(got) != "A" {
			t.Fatalf("ReadAll = %q, want %q", got, "A")
		}
	})
}

func TestDecompressZeroBitSymbolCodeLengthsAtEndOfInput(t *testing.T) {
	compressed := []byte("\xa188A01\x12\" \x00\x00\x00\x00")
	want := bytes.Repeat([]byte("#"), 9992)
	t.Run("Decompress", func(t *testing.T) {
		got, err := Decompress(compressed)
		if err != nil {
			t.Fatalf("Decompress: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("Decompress returned %d bytes, want %d '#' bytes", len(got), len(want))
		}
	})
	t.Run("Reader", func(t *testing.T) {
		r := NewReader(iotest.OneByteReader(bytes.NewReader(compressed)))
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("ReadAll returned %d bytes, want %d '#' bytes", len(got), len(want))
		}
	})
}

func TestProcessCommandsRejectsInvalidStaticDictionaryTransform(t *testing.T) {
	const transformIdx = core.NumTransforms
	var s decodeState
	s.state = decoderStateCommandPostDecodeLiterals
	s.ringbufferSize = 64
	s.ringbufferMask = s.ringbufferSize - 1
	s.ringbuffer = make([]byte, s.ringbufferSize+ringBufferWriteAheadSlack)
	s.pos = 2
	s.maxDistance = s.pos
	s.maxBackwardDistance = s.pos
	s.copyLength = core.DictMinWordLength
	s.metaBlockRemainingLen = 7
	s.distanceCode = 1
	s.distRBIdx = 1
	s.distRB[0] = s.maxDistance + 1 + (transformIdx << core.DictSizeBitsByLength[core.DictMinWordLength])

	result := s.processCommands()
	if result != decoderResultError {
		t.Fatalf("processCommands() result = %v, want decoderResultError", result)
	}
	if s.err == nil {
		t.Fatal("processCommands() returned no error")
	}
	if got, want := s.err.Error(), "brotli: invalid dictionary transform"; got != want {
		t.Fatalf("processCommands() err = %q, want %q", got, want)
	}
}

// A block switch with one block type is the CVE-2026-111497 state.
// Fixed C returns BROTLI_DECODER_ERROR_FORMAT_BLOCK_SWITCH for it.
func TestProcessCommandsRejectsBlockSwitchWithOneBlockType(t *testing.T) {
	tests := []struct {
		name                  string
		treeType              int
		state                 decoderState
		trivialLiteralContext int
	}{
		{"command", 1, decoderStateCommandBegin, 0},
		{"literal trivial context", 0, decoderStateCommandInner, 1},
		{"literal context", 0, decoderStateCommandInner, 0},
		{"distance", 2, decoderStateCommandPostDecodeLiterals, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s decodeState
			s.state = tc.state
			s.ringbufferSize = 64
			s.ringbufferMask = s.ringbufferSize - 1
			s.ringbuffer = make([]byte, s.ringbufferSize+ringBufferWriteAheadSlack)
			s.pos = 2
			s.loopCounter = 1
			s.metaBlockRemainingLen = 7
			s.distanceCode = -1
			s.trivialLiteralContext = tc.trivialLiteralContext
			s.numBlockTypes = [3]uint{1, 1, 1}
			s.blockLength = [3]uint{blockSizeCap, blockSizeCap, blockSizeCap}
			s.blockLength[tc.treeType] = 0

			result := s.processCommands()
			if result != decoderResultError {
				t.Fatalf("processCommands() result = %v, want decoderResultError", result)
			}
			if s.err == nil {
				t.Fatal("processCommands() returned no error")
			}
			if got, want := s.err.Error(), "brotli: invalid block switch"; got != want {
				t.Fatalf("processCommands() err = %q, want %q", got, want)
			}
		})
	}
}

// TestDecompressMetadataBlock verifies that metadata blocks are skipped and
// produce no output. The streams are hand-crafted to exercise skipMetadataBlock.
//
// Stream layout (LSB-first bit ordering):
//
//	Byte 0 (0x2C = 0b00101100):
//	  bit 0:   0  → window bits = 16
//	  bit 1:   0  → ISLAST = 0
//	  bits 2-3: 11 → MNIBBLES = 3 (metadata block)
//	  bit 4:   0  → RESERVED = 0
//	  bits 5-6: 01 → MSKIPBYTES = 1 (one size byte follows)
//	  bit 7:   0  → LSB of metadata size field
//	Byte 1: remaining 7 bits of the 8-bit metadata size, plus 1 padding bit
//	  (all zero → size = 0+1 = 1 or 2+1 = 3 depending on the test case)
//	Byte(s): the metadata payload (skipped by the decoder)
//	Last byte (0x03): ISLAST=1, ISLASTEMPTY=1 → terminates the stream
func TestDecompressMetadataBlock(t *testing.T) {
	tests := []struct {
		name   string
		stream []byte
	}{
		{
			// 1-byte metadata payload: size field byte = 0x00 → length = 1.
			// b1 = 0x00: bits 0-6 = 0 (size = 0), bit 7 = 0 (padding).
			name:   "1-byte metadata",
			stream: []byte{0x2C, 0x00, 0xAB, 0x03},
		},
		{
			// 3-byte metadata payload: size field byte encodes 2 → length = 3.
			// b1 = 0x01: bits 0-6 = 0b0000001 (assembles to value 2 with bit7 of b0=0),
			// bit 7 = 0 (padding). Followed by 3 metadata bytes then last block.
			name:   "3-byte metadata",
			stream: []byte{0x2C, 0x01, 0xDE, 0xAD, 0xBE, 0x03},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decompress(tc.stream)
			if err != nil {
				t.Fatalf("Decompress: %v", err)
			}
			if !bytes.Equal(got, []byte{}) && len(got) != 0 {
				t.Fatalf("got %d bytes, want empty output", len(got))
			}
		})
	}
}
