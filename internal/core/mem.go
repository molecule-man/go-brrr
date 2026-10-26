//go:build !purego && (386 || amd64 || arm64 || loong64 || ppc64 || ppc64le || s390x || wasm)

// Unsafe decoder helpers for targets with unaligned access.

package core

import "unsafe"

// Maximum bytes read from a dictionary word start.
const transformWordReadSize = 9 + 32

// Eight-byte loads need seven readable bytes after each entry.
func init() {
	for i, s := range &transformPrefixSuffix {
		transformPrefixSuffix[i] = (s + "\x00\x00\x00\x00\x00\x00\x00")[:len(s)]
	}
}

func (sl SymbolList) get(i int) uint16 {
	return *(*uint16)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(sl.Storage)), uintptr(sl.Offset+i)*2))
}

// replicateValue fills a Huffman table at step intervals.
func replicateValue(table []HuffmanCode, code HuffmanCode, step, end int) {
	base := unsafe.Pointer(unsafe.SliceData(table))
	packed := uint32(code.Bits) | uint32(code.Value)<<16
	for {
		end -= step
		*(*uint32)(unsafe.Add(base, uintptr(end)*4)) = packed
		if end <= 0 {
			break
		}
	}
}

// TransformDictionaryWordAt writes a transformed dictionary word to dst.
// It returns the result length. dst needs 32 spare bytes.
func TransformDictionaryWordAt(dst []byte, offset, length, transformIdx int) int {
	word := DictData[offset : offset+length]
	// Fast loads need transformWordReadSize bytes from offset.
	if offset > len(DictData)-transformWordReadSize {
		return TransformDictionaryWord(dst, word, transformIdx)
	}

	prefix := transformPrefixSuffix[TransformTriplets[transformIdx*3]]
	t := TransformTriplets[transformIdx*3+1]
	suffix := transformPrefixSuffix[TransformTriplets[transformIdx*3+2]]

	// The bound check protects word loads. Prefixes and suffixes have padding.
	dstBase := unsafe.Pointer(unsafe.SliceData(dst))

	idx := len(prefix)
	if idx > 0 {
		*(*[8]byte)(dstBase) = *(*[8]byte)(unsafe.Pointer(unsafe.StringData(prefix)))
	}

	wordLen := len(word)
	wordStart := 0
	if t <= TransformOmitLast9 {
		wordLen -= int(t)
		if wordLen < 0 {
			wordLen = 0
		}
	} else if t >= TransformOmitFirst1 && t <= TransformOmitFirst9 {
		skip := min(int(t)-(TransformOmitFirst1-1), wordLen)
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
	case TransformUppercaseFirst:
		toUpperCase(dst[idx-wordLen:])
	case TransformUppercaseAll:
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
