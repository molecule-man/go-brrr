package brrr

import (
	"math/bits"
)

// Byte-level prefix matching for LZ77.

// matchLen returns the number of bytes common to the start of a and b,
// examining at most limit bytes. Both slices must be at least limit bytes long.
func matchLen(a, b []byte, limit int) int {
	i := 0
	for ; i <= limit-8; i += 8 {
		xor := loadU64LE(a, uint(i)) ^ loadU64LE(b, uint(i))
		if xor != 0 {
			return i + bits.TrailingZeros64(xor)/8
		}
	}

	for ; i < limit && a[i] == b[i]; i++ {
	}

	return i
}

// matchLenAt compares data[a:] against data[b:] for up to limit bytes using
// unsafe loads, avoiding sub-slice creation and its bounds checks.
func matchLenAt(data []byte, a, b uint, limit int) int {
	i := 0
	for ; i <= limit-8; i += 8 {
		xor := loadU64LE(data, a+uint(i)) ^ loadU64LE(data, b+uint(i))
		if xor != 0 {
			return i + bits.TrailingZeros64(xor)/8
		}
	}

	for ; i < limit && data[a+uint(i)] == data[b+uint(i)]; i++ {
	}

	return i
}

// matchLenAtNoInline compares data[a:] against data[b:] for up to limit bytes.
// Equivalent to matchLen(data[a:], data[b:], limit) but avoids sub-slice
// creation at the call site, reducing caller code size.
//
// The tail loop uses loadByte (unsafe) to avoid bounds-check calls, keeping
// this a leaf function and eliminating frame-pointer save/restore overhead.
//
//go:noinline
func matchLenAtNoInline(data []byte, a, b uint, limit int) int {
	i := 0
	for ; i <= limit-8; i += 8 {
		xor := loadU64LE(data, a+uint(i)) ^ loadU64LE(data, b+uint(i))
		if xor != 0 {
			return i + bits.TrailingZeros64(xor)/8
		}
	}

	for ; i < limit && loadByte(data, a+uint(i)) == loadByte(data, b+uint(i)); i++ {
	}

	return i
}
