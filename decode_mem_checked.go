//go:build purego || !(386 || amd64 || arm64 || loong64 || ppc64 || ppc64le || s390x || wasm)

// Checked memory access for purego and other targets.

package brrr

import (
	"encoding/binary"

	"github.com/molecule-man/go-brrr/internal/core"
)

// Use the slice refill in this build.
const checkedDecoder = true

// load32 returns the little-endian uint32 at b[i:].
func load32(b []byte, i int) uint32 {
	return binary.LittleEndian.Uint32(b[i : i+4])
}

// load64 returns the little-endian uint64 at b[i:].
func load64(b []byte, i int) uint64 {
	return binary.LittleEndian.Uint64(b[i : i+8])
}

// The checked decoder reads br.input.
func (br *bitReader) setInputBase(data []byte) {}

// huffmanEntry returns t[offset+i] packed as Bits | Value<<16. It slices with
// the signed offset first, so a negative offset cannot wrap into range.
func huffmanEntry(t []core.HuffmanCode, offset int, i uint64) uint32 {
	e := t[offset:][i]
	return uint32(e.Bits) | uint32(e.Value)<<16
}

func uint64At(t []uint64, i uint64) *uint64 {
	return &t[i]
}

// distExtra returns the extra bit count and the base offset of distance code.
func distExtra(b *metablockBodyArena, code uint) (uint, uint) {
	return uint(b.distExtraBits[code]), b.distOffset[code]
}

// copy16 copies b[src:src+16] to b[dst:dst+16]. The load completes before the
// store, so overlap is safe.
func copy16(b []byte, dst, src int) {
	*(*[16]byte)(b[dst : dst+16]) = [16]byte(b[src : src+16])
}

// checkRingCopy checks the logical ring bounds before the eager store at pos.
func checkRingCopy(size, pos, n int) {
	if pos < 0 || n < 0 || pos >= size || n > size-pos {
		panic("brrr: ring buffer access past the logical end")
	}
}

// cacheLiteralCodes fills only literalCodesOffsets. The checked context batch
// does not read literalCodesPtrs.
func (s *decodeState) cacheLiteralCodes() {
	ctxMap := s.contextMap[s.contextMapSliceIdx:]
	htrees := s.literalHGroup.htrees
	for ctx := range 64 {
		s.literalCodesOffsets[ctx] = htrees[ctxMap[ctx]]
	}
}

// clearScalarFields clears scalar state. The decoder fills bodyArena and
// literalCodesOffsets before use, so this reset preserves them.
func (s *decodeState) clearScalarFields() {
	s.state = 0
	s.loopCounter = 0
	s.bufferLength = 0
	s.buffer = [8]byte{}
	s.pos = 0
	s.maxBackwardDistance = 0
	s.maxDistance = 0
	s.ringbufferSize = 0
	s.ringbufferMask = 0
	s.distRBIdx = 0
	s.distRB = [4]int{}
	s.safeCmdMemento = bitReaderState{}
	s.metaBlockRemainingLen = 0
	s.maxDecodedBytes = 0
	s.declaredTotal = 0
	s.contextMapSliceIdx = 0
	s.distContextMapSliceIdx = 0
	s.trivialLiteralContext = 0
	s.distanceContext = 0
	s.blockLength = [3]uint{}
	s.blockLengthIndex = 0
	s.numBlockTypes = [3]uint{}
	s.blockTypeRB = [6]uint{}
	s.distancePostfixBits = 0
	s.numDirectDistanceCodes = 0
	s.numDistHTrees = 0
	s.numLiteralHTrees = 0
	s.rbRoundtrips = 0
	s.partialPosOut = 0
	s.mtfUpperBound = 0
	s.copyLength = 0
	s.distanceCode = 0
	s.substateMetablockHeader = 0
	s.substateUncompressed = 0
	s.substateDecodeUint8 = 0
	s.substateReadBlockLength = 0
	s.newRingbufferSize = 0
	s.windowBits = 0
	s.sizeNibbles = 0
	s.trivialLiteralContexts = [8]uint32{}
	s.isLastMetablock = false
	s.isUncompressed = false
	s.isMetadata = false
	s.cannyRingbufferAllocation = false
	s.distHTreeIndex = 0
	s.distCodesOffset = 0
	s.distCodesCache = [4]int{}
}

// decodeLiteralsContextBatch decodes n literals using context-dependent Huffman
// trees. Returns updated (p1, p2) context bytes.
func decodeLiteralsContextBatch(
	dst []byte, n int,
	s *decodeState,
	contextLookup []byte, p1, p2 byte,
	br *bitReader,
) (byte, byte) {
	val := br.val
	bitPos := br.bitPos
	brPos := br.pos
	codes := s.literalHGroup.codes
	c1, c2 := uint(p1), uint(p2)

	for j := range n {
		if bitPos < core.HuffmanMaxCodeLength {
			val |= load64(br.input, brPos) << (bitPos & 63)
			brPos += int((63 - bitPos) >> 3)
			bitPos |= 56
		}

		ctx := uint(contextLookup[c1]) | uint(contextLookup[256+c2])
		offset := s.literalCodesOffsets[ctx]

		idx := val & huffmanTableMask
		raw := huffmanEntry(codes, offset, idx)
		drop := uint(raw & 0xFF)
		value := uint(raw >> 16)
		if drop > huffmanTableBits {
			nbits := drop - huffmanTableBits
			idx2 := idx + uint64(value) + ((val >> huffmanTableBits) & bitMask(nbits))
			raw = huffmanEntry(codes, offset, idx2)
			drop = huffmanTableBits + uint(raw&0xFF)
			value = uint(raw >> 16)
		}
		bitPos -= drop
		val >>= drop & 63

		c2 = c1
		c1 = value
		dst[j] = byte(value)
	}

	br.val = val
	br.bitPos = bitPos
	br.pos = brPos
	return byte(c1), byte(c2)
}
