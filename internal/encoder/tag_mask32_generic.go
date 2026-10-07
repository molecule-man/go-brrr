//go:build !amd64 || purego

package encoder

import "unsafe"

// tagMask32 sets bit k when tags[k] equals tag. This target ignores the prefetch addresses.
func tagMask32(tags *[32]uint8, tag uint8, _, _ unsafe.Pointer) uint32 {
	splat := uint64(tag) * 0x0101010101010101
	return tagEqualMask8(loadU64LE(tags[:], 0)^splat) |
		tagEqualMask8(loadU64LE(tags[:], 8)^splat)<<8 |
		tagEqualMask8(loadU64LE(tags[:], 16)^splat)<<16 |
		tagEqualMask8(loadU64LE(tags[:], 24)^splat)<<24
}
