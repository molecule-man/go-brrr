// RFC 7932 transforms for static dictionary words.

package brrr

import "unsafe"

// Transform types from RFC 7932.
const (
	transformIdentity       = iota // 0
	transformOmitLast1             // 1
	transformOmitLast2             // 2
	transformOmitLast3             // 3
	transformOmitLast4             // 4
	transformOmitLast5             // 5
	transformOmitLast6             // 6
	transformOmitLast7             // 7
	transformOmitLast8             // 8
	transformOmitLast9             // 9
	transformUppercaseFirst        // 10
	transformUppercaseAll          // 11
	transformOmitFirst1            // 12
	transformOmitFirst2            // 13
	transformOmitFirst3            // 14
	transformOmitFirst4            // 15
	transformOmitFirst5            // 16
	transformOmitFirst6            // 17
	transformOmitFirst7            // 18
	transformOmitFirst8            // 19
	transformOmitFirst9            // 20
	transformShiftFirst            // 21
	transformShiftAll              // 22
)

const numTransforms = 121

// Prefix and suffix strings used by transforms. Indices are referenced by
// transformTriplets.
var transformPrefixSuffix = [50]string{
	" ",        // 0
	", ",       // 1
	" of the ", // 2
	" of ",     // 3
	"s ",       // 4
	".",        // 5
	" and ",    // 6
	" in ",     // 7
	"\"",       // 8
	" to ",     // 9
	"\">",      // 10
	"\n",       // 11
	". ",       // 12
	"]",        // 13
	" for ",    // 14
	" a ",      // 15
	" that ",   // 16
	"'",        // 17
	" with ",   // 18
	" from ",   // 19
	" by ",     // 20
	"(",        // 21
	". The ",   // 22
	" on ",     // 23
	" as ",     // 24
	" is ",     // 25
	"ing ",     // 26
	"\n\t",     // 27
	":",        // 28
	"ed ",      // 29
	"=\"",      // 30
	" at ",     // 31
	"ly ",      // 32
	",",        // 33
	"='",       // 34
	".com/",    // 35
	". This ",  // 36
	" not ",    // 37
	"er ",      // 38
	"al ",      // 39
	"ful ",     // 40
	"ive ",     // 41
	"less ",    // 42
	"est ",     // 43
	"ize ",     // 44
	"\xc2\xa0", // 45 (U+00A0 non-breaking space)
	"ous ",     // 46
	" the ",    // 47
	"e ",       // 48
	"",         // 49
}

// Transform triplets: [prefix_id, transform_type, suffix_id] for each of the
// 121 RFC 7932 transforms.
var transformTriplets = [numTransforms * 3]byte{
	49, transformIdentity, 49, // 0
	49, transformIdentity, 0, // 1
	0, transformIdentity, 0, // 2
	49, transformOmitFirst1, 49, // 3
	49, transformUppercaseFirst, 0, // 4
	49, transformIdentity, 47, // 5
	0, transformIdentity, 49, // 6
	4, transformIdentity, 0, // 7
	49, transformIdentity, 3, // 8
	49, transformUppercaseFirst, 49, // 9
	49, transformIdentity, 6, // 10
	49, transformOmitFirst2, 49, // 11
	49, transformOmitLast1, 49, // 12
	1, transformIdentity, 0, // 13
	49, transformIdentity, 1, // 14
	0, transformUppercaseFirst, 0, // 15
	49, transformIdentity, 7, // 16
	49, transformIdentity, 9, // 17
	48, transformIdentity, 0, // 18
	49, transformIdentity, 8, // 19
	49, transformIdentity, 5, // 20
	49, transformIdentity, 10, // 21
	49, transformIdentity, 11, // 22
	49, transformOmitLast3, 49, // 23
	49, transformIdentity, 13, // 24
	49, transformIdentity, 14, // 25
	49, transformOmitFirst3, 49, // 26
	49, transformOmitLast2, 49, // 27
	49, transformIdentity, 15, // 28
	49, transformIdentity, 16, // 29
	0, transformUppercaseFirst, 49, // 30
	49, transformIdentity, 12, // 31
	5, transformIdentity, 49, // 32
	0, transformIdentity, 1, // 33
	49, transformOmitFirst4, 49, // 34
	49, transformIdentity, 18, // 35
	49, transformIdentity, 17, // 36
	49, transformIdentity, 19, // 37
	49, transformIdentity, 20, // 38
	49, transformOmitFirst5, 49, // 39
	49, transformOmitFirst6, 49, // 40
	47, transformIdentity, 49, // 41
	49, transformOmitLast4, 49, // 42
	49, transformIdentity, 22, // 43
	49, transformUppercaseAll, 49, // 44
	49, transformIdentity, 23, // 45
	49, transformIdentity, 24, // 46
	49, transformIdentity, 25, // 47
	49, transformOmitLast7, 49, // 48
	49, transformOmitLast1, 26, // 49
	49, transformIdentity, 27, // 50
	49, transformIdentity, 28, // 51
	0, transformIdentity, 12, // 52
	49, transformIdentity, 29, // 53
	49, transformOmitFirst9, 49, // 54
	49, transformOmitFirst7, 49, // 55
	49, transformOmitLast6, 49, // 56
	49, transformIdentity, 21, // 57
	49, transformUppercaseFirst, 1, // 58
	49, transformOmitLast8, 49, // 59
	49, transformIdentity, 31, // 60
	49, transformIdentity, 32, // 61
	47, transformIdentity, 3, // 62
	49, transformOmitLast5, 49, // 63
	49, transformOmitLast9, 49, // 64
	0, transformUppercaseFirst, 1, // 65
	49, transformUppercaseFirst, 8, // 66
	5, transformIdentity, 21, // 67
	49, transformUppercaseAll, 0, // 68
	49, transformUppercaseFirst, 10, // 69
	49, transformIdentity, 30, // 70
	0, transformIdentity, 5, // 71
	35, transformIdentity, 49, // 72
	47, transformIdentity, 2, // 73
	49, transformUppercaseFirst, 17, // 74
	49, transformIdentity, 36, // 75
	49, transformIdentity, 33, // 76
	5, transformIdentity, 0, // 77
	49, transformUppercaseFirst, 21, // 78
	49, transformUppercaseFirst, 5, // 79
	49, transformIdentity, 37, // 80
	0, transformIdentity, 30, // 81
	49, transformIdentity, 38, // 82
	0, transformUppercaseAll, 0, // 83
	49, transformIdentity, 39, // 84
	0, transformUppercaseAll, 49, // 85
	49, transformIdentity, 34, // 86
	49, transformUppercaseAll, 8, // 87
	49, transformUppercaseFirst, 12, // 88
	0, transformIdentity, 21, // 89
	49, transformIdentity, 40, // 90
	0, transformUppercaseFirst, 12, // 91
	49, transformIdentity, 41, // 92
	49, transformIdentity, 42, // 93
	49, transformUppercaseAll, 17, // 94
	49, transformIdentity, 43, // 95
	0, transformUppercaseFirst, 5, // 96
	49, transformUppercaseAll, 10, // 97
	0, transformIdentity, 34, // 98
	49, transformUppercaseFirst, 33, // 99
	49, transformIdentity, 44, // 100
	49, transformUppercaseAll, 5, // 101
	45, transformIdentity, 49, // 102
	0, transformIdentity, 33, // 103
	49, transformUppercaseFirst, 30, // 104
	49, transformUppercaseAll, 30, // 105
	49, transformIdentity, 46, // 106
	49, transformUppercaseAll, 1, // 107
	49, transformUppercaseFirst, 34, // 108
	0, transformUppercaseFirst, 33, // 109
	0, transformUppercaseAll, 30, // 110
	0, transformUppercaseAll, 1, // 111
	49, transformUppercaseAll, 33, // 112
	49, transformUppercaseAll, 21, // 113
	49, transformUppercaseAll, 12, // 114
	0, transformUppercaseAll, 5, // 115
	49, transformUppercaseAll, 34, // 116
	0, transformUppercaseAll, 12, // 117
	0, transformUppercaseFirst, 30, // 118
	0, transformUppercaseAll, 34, // 119
	0, transformUppercaseFirst, 34, // 120
}

// Indices of transforms ["", omit-last-N, ""] for N=0..9, where N=0 means
// identity. Used by the encoder for fast dictionary matching.
var transformCutOffs = [10]int16{0, 12, 27, 23, 42, 63, 56, 48, 59, 64}

// transformDictionaryWord applies transform transformIdx to word, writing the
// result into dst. Returns the number of bytes written. dst must be large
// enough to hold the result (word length + longest prefix + longest suffix).
func transformDictionaryWord(dst []byte, word string, transformIdx int) int {
	prefix := transformPrefixSuffix[transformTriplets[transformIdx*3]]
	t := transformTriplets[transformIdx*3+1]
	suffix := transformPrefixSuffix[transformTriplets[transformIdx*3+2]]

	// Use unsafe copies to avoid runtime.memmove overhead for small data.
	// Safety: dst is backed by the ring buffer which has ≥542 bytes of
	// slack, and word comes from the static dictionary (≥120 KB). All
	// reads and writes within 32 bytes of the starting offsets are in
	// bounds.
	dstBase := unsafe.Pointer(unsafe.SliceData(dst))

	idx := len(prefix)
	if idx > 0 {
		*(*[8]byte)(dstBase) = *(*[8]byte)(unsafe.Pointer(unsafe.StringData(prefix)))
	}

	wordLen := len(word)
	wordStart := 0
	if t <= transformOmitLast9 {
		wordLen -= int(t)
		if wordLen < 0 {
			wordLen = 0
		}
	} else if t >= transformOmitFirst1 && t <= transformOmitFirst9 {
		skip := min(int(t)-(transformOmitFirst1-1), wordLen)
		wordStart = skip
		wordLen -= skip
	}

	if wordLen > 0 {
		wordSrc := unsafe.Pointer(unsafe.StringData(word))
		dstWord := unsafe.Add(dstBase, idx)
		*(*[16]byte)(dstWord) = *(*[16]byte)(unsafe.Add(wordSrc, wordStart))
		if wordLen > 16 {
			*(*[16]byte)(unsafe.Add(dstWord, 16)) = *(*[16]byte)(unsafe.Add(wordSrc, wordStart+16))
		}
	}
	idx += wordLen

	switch t {
	case transformUppercaseFirst:
		toUpperCase(dst[idx-wordLen:])
	case transformUppercaseAll:
		p := dst[idx-wordLen:]
		remaining := wordLen
		for remaining > 0 {
			step := toUpperCase(p)
			p = p[step:]
			remaining -= step
		}
	}

	suffixLen := len(suffix)
	if suffixLen > 0 {
		*(*[8]byte)(unsafe.Add(dstBase, idx)) = *(*[8]byte)(unsafe.Pointer(unsafe.StringData(suffix)))
	}
	idx += suffixLen
	return idx
}

// toUpperCase uppercases the first UTF-8 character in p using the simplified
// model from RFC 7932. Returns the byte length of the character processed.
func toUpperCase(p []byte) int {
	if p[0] < 0xC0 {
		if p[0] >= 'a' && p[0] <= 'z' {
			p[0] ^= 32
		}
		return 1
	}
	// Simplified 2-byte UTF-8 uppercase: flip bit 5 of the second byte.
	if p[0] < 0xE0 {
		p[1] ^= 32
		return 2
	}
	// Arbitrary transform for 3-byte UTF-8: XOR the third byte with 5.
	p[2] ^= 5
	return 3
}

// shiftUTF8 applies a scalar shift to the first UTF-8 character in word.
// Returns the byte length of the character. Used by SHIFT_FIRST and SHIFT_ALL
// transforms, which are not present in the 121 static RFC 7932 transforms but
// are part of the spec.
func shiftUTF8(word []byte, wordLen int, param uint16) int {
	// Limited sign extension: scalar < (1 << 24).
	scalar := uint32(param&0x7FFF) + (0x1000000 - uint32(param&0x8000))
	switch {
	case word[0] < 0x80:
		// 1-byte / ASCII / 7-bit scalar.
		scalar += uint32(word[0])
		word[0] = byte(scalar & 0x7F)
		return 1
	case word[0] < 0xC0:
		// Continuation byte.
		return 1
	case word[0] < 0xE0:
		// 2-byte / 11-bit scalar.
		if wordLen < 2 {
			return 1
		}
		scalar += uint32(word[1]&0x3F) | uint32(word[0]&0x1F)<<6
		word[0] = byte(0xC0 | ((scalar >> 6) & 0x1F))
		word[1] = byte(uint32(word[1]&0xC0) | (scalar & 0x3F))
		return 2
	case word[0] < 0xF0:
		// 3-byte / 16-bit scalar.
		if wordLen < 3 {
			return wordLen
		}
		scalar += uint32(word[2]&0x3F) | uint32(word[1]&0x3F)<<6 | uint32(word[0]&0x0F)<<12
		word[0] = byte(0xE0 | ((scalar >> 12) & 0x0F))
		word[1] = byte(uint32(word[1]&0xC0) | ((scalar >> 6) & 0x3F))
		word[2] = byte(uint32(word[2]&0xC0) | (scalar & 0x3F))
		return 3
	case word[0] < 0xF8:
		// 4-byte / 21-bit scalar.
		if wordLen < 4 {
			return wordLen
		}
		scalar += uint32(word[3]&0x3F) | uint32(word[2]&0x3F)<<6 |
			uint32(word[1]&0x3F)<<12 | uint32(word[0]&0x07)<<18
		word[0] = byte(0xF0 | ((scalar >> 18) & 0x07))
		word[1] = byte(uint32(word[1]&0xC0) | ((scalar >> 12) & 0x3F))
		word[2] = byte(uint32(word[2]&0xC0) | ((scalar >> 6) & 0x3F))
		word[3] = byte(uint32(word[3]&0xC0) | (scalar & 0x3F))
		return 4
	default:
		return 1
	}
}
