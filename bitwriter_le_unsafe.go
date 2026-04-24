// writeBits: unsafe fast path for little-endian platforms.

//go:build !purego && (amd64 || 386 || arm || arm64 || loong64 || mips64le || mipsle || ppc64le || riscv64 || wasm)

package brrr

import "unsafe"

// writeBits packs value into the bitstream and advances the bit position.
// Up to 56 bits may be written at a time.
func (b *bitWriter) writeBits(nbits uint, value uint64) {
	bytePos := b.bitOffset >> 3
	bitOff := b.bitOffset & 7
	p := (*uint64)(unsafe.Pointer(&b.buf[bytePos]))
	*p = uint64(*(*byte)(unsafe.Pointer(p))) | value<<bitOff
	b.bitOffset += nbits
}

func (b *bitWriter) writeLiteralBits(input []byte, depths *[256]byte, bits *[256]uint16) {
	buf := b.buf
	bitOffset := b.bitOffset
	for _, lit := range input {
		bytePos := bitOffset >> 3
		bitOff := bitOffset & 7
		p := (*uint64)(unsafe.Pointer(&buf[bytePos]))
		*p = uint64(*(*byte)(unsafe.Pointer(p))) | uint64(bits[lit])<<bitOff
		bitOffset += uint(depths[lit])
	}
	b.bitOffset = bitOffset
}
