//go:build purego || !(386 || amd64 || arm64 || loong64 || ppc64 || ppc64le || s390x || wasm)

// Decoder helpers with Go bounds checks.

package core

func (sl SymbolList) get(i int) uint16 {
	return sl.Storage[sl.Offset+i]
}

// replicateValue fills a Huffman table at step intervals.
func replicateValue(table []HuffmanCode, code HuffmanCode, step, end int) {
	for {
		end -= step
		table[end] = code
		if end <= 0 {
			break
		}
	}
}

// TransformDictionaryWordAt writes a transformed dictionary word to dst.
// It returns the result length.
func TransformDictionaryWordAt(dst []byte, offset, length, transformIdx int) int {
	return TransformDictionaryWord(dst, DictData[offset:offset+length], transformIdx)
}
