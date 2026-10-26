//go:build !purego && (386 || amd64 || arm64 || loong64 || ppc64 || ppc64le || s390x || wasm)

// Native loads need unaligned access. le32 and le64 handle big-endian targets.

package brrr

import (
	"unsafe"

	"github.com/molecule-man/go-brrr/internal/core"
)

// Keep the refill in processCommands to preserve the input load order.
const checkedDecoder = false

// load32 returns the little-endian uint32 at b[i:].
func load32(b []byte, i int) uint32 {
	return le32(*(*uint32)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(b)), i)))
}

// load64 returns the little-endian uint64 at b[i:].
func load64(b []byte, i int) uint64 {
	return le64(*(*uint64)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(b)), i)))
}

// huffmanEntry returns t[offset+i] packed as Bits | Value<<16.
func huffmanEntry(t []core.HuffmanCode, offset int, i uint64) uint32 {
	tableBase := unsafe.Add(
		unsafe.Pointer(unsafe.SliceData(t)),
		uintptr(offset)*unsafe.Sizeof(core.HuffmanCode{}),
	)
	return *(*uint32)(unsafe.Add(tableBase, i*4))
}

func uint64At(t []uint64, i uint64) *uint64 {
	return (*uint64)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(t)), i*8))
}

// distExtra returns the extra bit count and the base offset of distance code.
func distExtra(b *metablockBodyArena, code uint) (uint, uint) {
	nExtra := uint(*(*byte)(unsafe.Add(unsafe.Pointer(&b.distExtraBits[0]), uintptr(code))))
	offset := *(*uint)(unsafe.Add(unsafe.Pointer(&b.distOffset[0]), uintptr(code)*unsafe.Sizeof(uint(0))))
	return nExtra, offset
}

// copy16 copies b[src:src+16] to b[dst:dst+16]. The load completes before the
// store, so overlap is safe.
func copy16(b []byte, dst, src int) {
	base := unsafe.Pointer(unsafe.SliceData(b))
	*(*[16]byte)(unsafe.Add(base, dst)) = *(*[16]byte)(unsafe.Add(base, src))
}

// checkRingCopy is a no-op. The checked build checks the logical ring bounds.
func checkRingCopy(size, pos, n int) {}

// clearScalarFields clears scalar state. The decoder fills bodyArena and
// literalCodesOffsets before use, so this reset preserves them.
func (s *decodeState) clearScalarFields() {
	start := unsafe.Pointer(&s.state)
	size := uintptr(unsafe.Pointer(&s.distCodesCache)) - uintptr(start) + unsafe.Sizeof(s.distCodesCache)
	clear(unsafe.Slice((*byte)(start), size))
}

// decodeLiteralsContextBatch decodes n literals using context-dependent Huffman
// trees. It hoists bitReader state into locals and uses unsafe pointer arithmetic
// to avoid per-symbol bounds checks. Returns updated (p1, p2) context bytes.
func decodeLiteralsContextBatch(
	dst []byte, n int,
	s *decodeState,
	contextLookup []byte, p1, p2 byte,
	br *bitReader,
) (byte, byte) {
	val := br.val
	bitPos := br.bitPos
	brPos := br.pos
	inputBase := br.inputBase
	ctxBase := unsafe.Pointer(unsafe.SliceData(contextLookup))
	// Use an incrementing pointer instead of an index to avoid spilling the loop
	// counter onto the stack - on x86-64 the shift in fillBitWindow requires CX,
	// which would otherwise evict j every iteration.
	dstPtr := unsafe.Pointer(unsafe.SliceData(dst))
	ptrsBase := unsafe.Pointer(&s.literalCodesPtrs)
	c1, c2 := uint(p1), uint(p2)

	for n > 0 {
		n--
		if bitPos < core.HuffmanMaxCodeLength {
			val |= le64(*(*uint64)(unsafe.Add(inputBase, brPos))) << (bitPos & 63)
			brPos += int((63 - bitPos) >> 3)
			bitPos |= 56
		}

		ctx := uint(*(*byte)(unsafe.Add(ctxBase, c1))) | uint(*(*byte)(unsafe.Add(ctxBase, 256+c2)))
		tableBase := *(*unsafe.Pointer)(unsafe.Add(ptrsBase, uintptr(ctx)*unsafe.Sizeof(unsafe.Pointer(nil))))

		// decodeSymbol inline
		idx := val & huffmanTableMask
		raw := *(*uint32)(unsafe.Add(tableBase, idx*4))
		drop := uint(raw & 0xFF)
		value := uint(raw >> 16)
		if drop > huffmanTableBits {
			nbits := drop - huffmanTableBits
			idx2 := idx + uint64(value) + ((val >> huffmanTableBits) & bitMask(nbits))
			raw = *(*uint32)(unsafe.Add(tableBase, idx2*4))
			drop = huffmanTableBits + uint(raw&0xFF)
			value = uint(raw >> 16)
		}
		// dropBits inline
		bitPos -= drop
		val >>= drop & 63

		c2 = c1
		c1 = value
		*(*byte)(dstPtr) = byte(value)
		dstPtr = unsafe.Add(dstPtr, 1)
	}

	br.val = val
	br.bitPos = bitPos
	br.pos = brPos
	return byte(c1), byte(c2)
}

// copyOverlappingPattern repeats the dist bytes before pos across n bytes.
// Its 16-byte stores can use up to 15 bytes of ring-buffer slack.
func copyOverlappingPattern(rb []byte, pos, dist, n int) {
	base := unsafe.Pointer(unsafe.SliceData(rb))
	p, end := pos, pos+n
	for dist < 16 && p < end {
		*(*[16]byte)(unsafe.Add(base, p)) = *(*[16]byte)(unsafe.Add(base, p-dist))
		p += dist
		dist += dist
	}
	for p < end {
		*(*[16]byte)(unsafe.Add(base, p)) = *(*[16]byte)(unsafe.Add(base, p-dist))
		p += 16
	}
}

// distanceSymbolEntryFast loads the first-level distance Huffman table entry.
func distanceSymbolEntryFast(bits uint64, table []core.HuffmanCode, offset int) uint32 {
	tableBase := unsafe.Add(
		unsafe.Pointer(unsafe.SliceData(table)),
		uintptr(offset)*unsafe.Sizeof(core.HuffmanCode{}),
	)
	idx := bits & huffmanTableMask
	return *(*uint32)(unsafe.Add(tableBase, idx*4))
}

// decodeDistanceSymbolSecondLevel decodes a second-level distance Huffman entry.
func decodeDistanceSymbolSecondLevel(bits uint64, table []core.HuffmanCode, offset int, code, drop uint) (uint, uint) {
	tableBase := unsafe.Add(
		unsafe.Pointer(unsafe.SliceData(table)),
		uintptr(offset)*unsafe.Sizeof(core.HuffmanCode{}),
	)
	nbits := drop - huffmanTableBits
	idx := (bits & huffmanTableMask) + uint64(code) + ((bits >> huffmanTableBits) & bitMask(nbits))
	raw := *(*uint32)(unsafe.Add(tableBase, idx*4))
	return uint(raw >> 16), huffmanTableBits + uint(raw&0xFF)
}

// decodeLiteralsBatch decodes n literals with local bit-reader state.
func decodeLiteralsBatch(dst []byte, n int, table []core.HuffmanCode, br *bitReader) {
	val := br.val
	bitPos := br.bitPos
	brPos := br.pos
	inputBase := br.inputBase
	tableBase := unsafe.Pointer(unsafe.SliceData(table))

	for j := range n {
		if bitPos < core.HuffmanMaxCodeLength {
			val |= le64(*(*uint64)(unsafe.Add(inputBase, brPos))) << (bitPos & 63)
			brPos += int((63 - bitPos) >> 3)
			bitPos |= 56
		}

		idx := val & huffmanTableMask
		raw := *(*uint32)(unsafe.Add(tableBase, idx*4))
		drop := uint(raw & 0xFF)
		value := uint(raw >> 16)
		if drop > huffmanTableBits {
			nbits := drop - huffmanTableBits
			idx2 := idx + uint64(value) + ((val >> huffmanTableBits) & bitMask(nbits))
			raw = *(*uint32)(unsafe.Add(tableBase, idx2*4))
			drop = huffmanTableBits + uint(raw&0xFF)
			value = uint(raw >> 16)
		}
		bitPos -= drop
		val >>= drop & 63
		dst[j] = byte(value)
	}

	br.val = val
	br.bitPos = bitPos
	br.pos = brPos
}
