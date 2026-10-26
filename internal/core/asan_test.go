//go:build asan

// Heap copies expose dictionary reads to ASan.

package core

import (
	"strings"
	"testing"
)

func lastDictWords() map[int]int {
	words := make(map[int]int)
	for length := DictMinWordLength; length <= DictMaxWordLength; length++ {
		words[length] = int(DictOffsetsByLength[length]) + (1<<DictSizeBitsByLength[length]-1)*length
	}
	return words
}

func TestTransformDictionaryWordASan(t *testing.T) {
	var scratch [256]byte
	words := []string{"a", "time", "abcdefghijklmnopqrstuvwx"}
	for length, offset := range lastDictWords() {
		words = append(words, DictData[offset:offset+length])
	}
	for _, w := range words {
		word := strings.Clone(w)
		for transformIdx := range NumTransforms {
			n := TransformDictionaryWord(scratch[:], word, transformIdx)
			dst := make([]byte, n)
			TransformDictionaryWord(dst, word, transformIdx)
		}
	}
}

func TestTransformDictionaryWordAtASan(t *testing.T) {
	saved := DictData
	t.Cleanup(func() { DictData = saved })
	DictData = strings.Clone(DictData)

	var scratch [256]byte
	for length, last := range lastDictWords() {
		for offset := max(last-4*length, int(DictOffsetsByLength[length])); offset <= last; offset += length {
			for transformIdx := range NumTransforms {
				n := TransformDictionaryWordAt(scratch[:], offset, length, transformIdx)
				dst := make([]byte, n+32)
				TransformDictionaryWordAt(dst, offset, length, transformIdx)
			}
		}
	}
}
