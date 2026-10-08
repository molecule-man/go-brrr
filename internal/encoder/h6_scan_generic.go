//go:build !amd64 || purego

package encoder

import (
	"math/bits"
	"unsafe"
)

// h6FindInBucket runs phase 2 of h6 findLongestMatch. See the amd64 version.
// This target does not prefetch next.
func h6FindInBucket(h *h6, data unsafe.Pointer, keyTag, curMasked, mask,
	minPrev, maxLength, bestLen, bestScore uint,
	_ *h6Block,
) (length, distance, score uint) {
	load32 := func(i uint) uint32 { return *(*uint32)(unsafe.Add(data, i)) }
	key, tag := keyTag>>8&(h6BucketSize-1), uint8(keyTag)
	block := &h.blocks[key]
	n := uint(h.num[key])

	// Bit j of matches: slot (head+j) has the same tag. Bit 0 is the newest entry.
	head := (n + 1) & h6BlockMask
	splat := uint64(tag) * 0x0101010101010101
	matches := uint32(bits.RotateLeft16(uint16(tagEqualMask8(loadU64LE(block.tags[:], 0)^splat)|
		tagEqualMask8(loadU64LE(block.tags[:], 8)^splat)<<8), -int(head)))
	// Drop the slots that hold no entry yet.
	if stored := 0xFFFF - n; stored < h6BlockSize {
		matches &= uint32(1)<<stored - 1
	}

	score = bestScore
	if curMasked+bestLen > mask {
		return length, distance, score
	}
	first4 := load32(curMasked)
	curProbe := load32(curMasked + bestLen - 3)
	for ; matches != 0; matches &= matches - 1 {
		prevRaw := uint(block.pos[(head+uint(bits.TrailingZeros32(matches)))&h6BlockMask])
		if prevRaw < minPrev {
			break
		}
		prevMasked := prevRaw & mask
		if curProbe != load32(prevMasked+bestLen-3) || prevMasked+bestLen > mask {
			continue
		}
		if first4 != load32(prevMasked) {
			continue
		}
		ml := 4 + uint(matchLenUnsafe(data, prevMasked+4, curMasked+4, int(maxLength)-4))
		// backward < window, so the mask gives cur-prevRaw.
		backward := (curMasked - prevMasked) & mask
		if s := backwardReferenceScore(ml, backward); s > score {
			score, bestLen, length, distance = s, ml, ml, backward
			if curMasked+bestLen > mask {
				break
			}
			curProbe = load32(curMasked + bestLen - 3)
		}
	}
	return length, distance, score
}

// matchLenUnsafe returns the number of equal bytes at data+a and data+b, at
// most limit.
func matchLenUnsafe(data unsafe.Pointer, a, b uint, limit int) int {
	i := 0
	for ; i <= limit-8; i += 8 {
		xor := *(*uint64)(unsafe.Add(data, a+uint(i))) ^ *(*uint64)(unsafe.Add(data, b+uint(i)))
		if xor != 0 {
			return i + bits.TrailingZeros64(xor)/8
		}
	}
	for ; i < limit && *(*byte)(unsafe.Add(data, a+uint(i))) == *(*byte)(unsafe.Add(data, b+uint(i))); i++ {
	}
	return i
}
