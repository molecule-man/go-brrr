//go:build !purego

// Decoder memory access for the default build. The purego build uses checked slices.

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

func (br *bitReader) setInputBase(data []byte) {
	br.inputBase = unsafe.Pointer(unsafe.SliceData(data))
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

// checkRingCopy is a no-op. The purego build checks the logical ring bounds.
func checkRingCopy(size, pos, n int) {}

func (s *decodeState) cacheLiteralCodes() {
	ctxMap := s.contextMap[s.contextMapSliceIdx:]
	htrees := s.literalHGroup.htrees
	codesBase := unsafe.Pointer(unsafe.SliceData(s.literalHGroup.codes))
	for ctx := range 64 {
		offset := htrees[ctxMap[ctx]]
		s.literalCodesOffsets[ctx] = offset
		s.literalCodesPtrs[ctx] = unsafe.Add(codesBase, uintptr(offset)*4)
	}
}

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
	inputBase := unsafe.Pointer(unsafe.SliceData(br.input))
	ctxBase := unsafe.Pointer(unsafe.SliceData(contextLookup))
	// Use an incrementing pointer instead of an index to avoid spilling the
	// loop counter onto the stack — on x86-64 the shift in fillBitWindow
	// requires CX, which would otherwise evict j every iteration.
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
