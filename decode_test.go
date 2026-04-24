// Unit tests for decoder edge cases and malformed-stream handling.

package brrr

import (
	"bytes"
	"testing"
)

func TestProcessCommandsRejectsZeroLengthStaticDictionaryTransform(t *testing.T) {
	const transformIdx = 54
	if got := transformTriplets[transformIdx*3+1]; got != transformOmitFirst9 {
		t.Fatalf("transform %d = %d, want omitFirst9", transformIdx, got)
	}

	var s decodeState
	s.state = decoderStateCommandPostDecodeLiterals
	s.ringbufferSize = 64
	s.ringbufferMask = s.ringbufferSize - 1
	s.ringbuffer = make([]byte, s.ringbufferSize+ringBufferWriteAheadSlack)
	s.pos = 2
	s.maxDistance = s.pos
	s.maxBackwardDistance = s.pos
	s.copyLength = dictMinWordLength
	s.metaBlockRemainingLen = 7
	s.distanceCode = 1
	s.distRBIdx = 1
	s.distRB[0] = s.maxDistance + 1 + (transformIdx << dictSizeBitsByLength[dictMinWordLength])

	result := s.processCommands()
	if result != decoderResultError {
		t.Fatalf("processCommands() result = %v, want decoderResultError", result)
	}
	if got, want := s.err.Error(), "brotli: invalid dictionary transform"; got != want {
		t.Fatalf("processCommands() err = %q, want %q", got, want)
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
