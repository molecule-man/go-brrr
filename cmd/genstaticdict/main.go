// Generates static dictionary lookup tables for the Brotli encoder.
package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"log"
	"os"
)

const (
	dictDataSize      = 122784
	dictMinWordLength = 4
	dictMaxWordLength = 24
	hashMul32         = 0x1E35A7BD

	// staticDictBuckets/staticDictWords (Hash15-based, multi-entry chains).
	numLUTBuckets = 32768
	numLUTItems   = 31705

	// staticDictHashWords/staticDictHashLengths (Hash14-based, one per bucket).
	numHashBuckets = 32768

	transformIdentity       = 0
	transformUppercaseFirst = 10
	transformUppercaseAll   = 11
)

// RFC 7932 Appendix A constants (same values as dictionary.go).
var sizeBitsByLength = [32]byte{
	0, 0, 0, 0, 10, 10, 11, 11,
	10, 10, 10, 10, 10, 9, 9, 8,
	7, 7, 8, 7, 7, 6, 6, 5,
	5, 0, 0, 0, 0, 0, 0, 0,
}

var offsetsByLength = [32]uint32{
	0, 0, 0, 0, 0, 4096, 9216, 21504,
	35840, 44032, 53248, 63488, 74752, 87040, 93696, 100864,
	104704, 106752, 108928, 113536, 115968, 118528, 119872, 121280,
	122016, 122784, 122784, 122784, 122784, 122784, 122784, 122784,
}

// frozenIdx is the bitmap from the C reference (dictionary_hash.c) that marks
// which hash bucket entries are "final" and should not be overwritten.
var frozenIdx = [1688]byte{
	0, 0, 8, 164, 32, 56, 31, 191, 36, 4,
	128, 81, 68, 132, 145, 129, 0, 0, 0, 28, 0, 8, 1, 1, 64, 3, 1, 0, 0, 0, 0, 0, 4,
	64, 1, 2, 128, 0, 132, 49, 0, 0, 0, 0, 0, 0, 0, 0, 17, 0, 0, 0, 1, 0, 36, 152,
	0, 0, 0, 0, 128, 8, 0, 0, 128, 0, 0, 8, 0, 0, 64, 0, 0, 0, 0, 0, 0, 0, 0, 0, 8,
	0, 0, 0, 1, 0, 64, 133, 0, 32, 0, 0, 128, 1, 0, 0, 0, 0, 4, 4, 4, 32, 16, 130,
	0, 128, 8, 0, 0, 0, 0, 0, 64, 0, 64, 0, 160, 0, 148, 53, 0, 0, 0, 0, 0, 128, 0,
	130, 0, 0, 0, 8, 0, 0, 0, 0, 0, 48, 0, 0, 0, 0, 0, 0, 32, 1, 32, 129, 0, 12, 0,
	1, 0, 0, 0, 0, 0, 0, 0, 16, 0, 0, 0, 16, 32, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 8,
	0, 0, 2, 0, 0, 0, 0, 0, 32, 0, 0, 0, 2, 66, 128, 0, 0, 16, 0, 0, 0, 0, 64, 1, 6,
	128, 8, 0, 192, 24, 32, 0, 0, 8, 4, 128, 128, 2, 160, 0, 160, 0, 64, 0, 0, 2, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 32, 1, 0, 0, 64, 0, 0, 0, 0, 0, 0, 32, 0, 66, 0, 2, 0,
	4, 0, 8, 0, 2, 0, 0, 33, 8, 0, 0, 0, 8, 0, 128, 162, 4, 128, 0, 2, 33, 0, 160,
	0, 8, 0, 64, 0, 160, 0, 129, 4, 0, 0, 32, 0, 0, 32, 0, 2, 0, 0, 0, 0, 0, 0, 128,
	0, 0, 0, 0, 0, 64, 10, 0, 0, 0, 0, 32, 64, 0, 0, 0, 0, 0, 16, 0, 16, 16, 0, 0,
	80, 2, 0, 0, 0, 0, 8, 0, 0, 16, 0, 8, 0, 0, 0, 8, 64, 128, 0, 0, 0, 8, 208, 0,
	0, 0, 0, 0, 0, 0, 32, 0, 0, 0, 0, 0, 0, 32, 0, 8, 0, 128, 0, 0, 0, 1, 0, 0, 0,
	16, 8, 1, 136, 0, 0, 36, 0, 64, 9, 0, 1, 32, 8, 0, 64, 64, 131, 16, 224, 32, 4,
	0, 4, 5, 160, 0, 131, 0, 4, 96, 0, 0, 184, 192, 0, 177, 205, 96, 0, 0, 0, 0, 2,
	0, 32, 0, 0, 0, 0, 0, 0, 0, 0, 64, 0, 0, 128, 0, 0, 8, 0, 0, 0, 0, 1, 4, 0, 1,
	0, 0, 0, 4, 0, 0, 0, 0, 0, 0, 4, 0, 0, 64, 69, 0, 0, 8, 2, 66, 32, 64, 0, 0, 0,
	0, 0, 1, 0, 128, 17, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 12, 0, 16, 0, 0, 4, 128, 64,
	0, 0, 0, 0, 0, 0, 0, 0, 224, 0, 8, 0, 0, 130, 16, 64, 128, 2, 64, 0, 0, 0, 128,
	2, 192, 64, 0, 65, 0, 0, 0, 16, 0, 0, 0, 32, 4, 2, 2, 76, 0, 0, 0, 4, 72, 52,
	131, 44, 76, 0, 0, 0, 0, 64, 1, 16, 148, 4, 0, 16, 10, 64, 0, 2, 0, 1, 0, 128,
	64, 68, 0, 0, 0, 0, 0, 64, 144, 0, 8, 0, 2, 0, 0, 0, 0, 0, 0, 3, 64, 0, 0, 0, 0,
	1, 128, 0, 0, 32, 66, 0, 0, 0, 40, 0, 18, 0, 0, 0, 0, 0, 33, 0, 0, 32, 0, 0, 32,
	0, 128, 4, 64, 145, 140, 0, 0, 0, 128, 0, 2, 0, 0, 20, 0, 80, 38, 0, 0, 32, 0,
	32, 64, 4, 4, 0, 4, 0, 0, 0, 129, 4, 0, 0, 144, 17, 32, 130, 16, 132, 24, 134,
	0, 0, 64, 2, 5, 50, 8, 194, 33, 1, 68, 117, 1, 8, 32, 161, 54, 0, 130, 34, 0, 0,
	0, 64, 128, 0, 0, 2, 0, 0, 0, 0, 32, 1, 0, 0, 0, 3, 14, 0, 0, 0, 0, 0, 16, 4, 0,
	0, 0, 0, 0, 0, 0, 0, 96, 1, 24, 18, 0, 1, 128, 24, 0, 64, 0, 4, 0, 16, 128, 0,
	64, 0, 0, 0, 64, 0, 8, 0, 0, 0, 0, 0, 66, 128, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 16, 0, 64, 2, 0, 0, 0, 0, 6, 0, 8, 8, 2, 0, 64, 0, 0, 0, 0, 128, 2,
	2, 12, 64, 0, 64, 0, 8, 0, 128, 32, 0, 0, 10, 0, 0, 32, 0, 128, 32, 33, 8, 136,
	0, 96, 64, 0, 0, 0, 0, 0, 64, 4, 16, 4, 8, 0, 0, 0, 16, 0, 2, 0, 0, 1, 128, 0,
	64, 16, 0, 0, 0, 0, 0, 0, 0, 0, 8, 0, 0, 2, 0, 16, 0, 4, 0, 8, 0, 0, 0, 0, 0,
	20, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 8, 136, 0, 0, 0, 0, 0, 8, 0,
	0, 0, 0, 0, 2, 0, 0, 0, 64, 0, 0, 1, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 128, 0, 0,
	0, 0, 4, 0, 0, 0, 0, 65, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 2, 128, 0, 0, 0, 8, 2, 0, 0, 128, 0, 16, 2, 0, 0, 4, 0, 32, 0, 0, 1,
	4, 64, 64, 0, 4, 0, 1, 0, 16, 0, 32, 68, 4, 4, 65, 10, 0, 20, 37, 18, 1, 148, 0,
	32, 128, 3, 8, 0, 64, 0, 0, 0, 0, 0, 0, 4, 0, 16, 1, 128, 0, 0, 0, 128, 16, 0,
	0, 0, 0, 1, 128, 0, 0, 128, 64, 128, 64, 0, 130, 0, 164, 8, 0, 0, 1, 64, 128, 0,
	18, 0, 2, 150, 0, 8, 0, 0, 64, 0, 81, 0, 0, 16, 128, 2, 8, 36, 32, 129, 4, 144,
	13, 0, 0, 3, 8, 1, 0, 2, 0, 0, 64, 0, 5, 0, 1, 34, 1, 32, 2, 16, 128, 128, 128,
	0, 0, 0, 2, 0, 4, 18, 8, 12, 34, 32, 192, 6, 64, 224, 33, 0, 0, 137, 72, 64, 0,
	24, 8, 128, 128, 0, 16, 0, 32, 128, 128, 132, 8, 0, 0, 16, 0, 64, 0, 0, 4, 0, 0,
	16, 0, 4, 128, 64, 0, 0, 1, 0, 4, 64, 32, 144, 130, 2, 128, 0, 192, 0, 64, 82,
	64, 1, 32, 128, 128, 2, 0, 84, 0, 32, 0, 44, 24, 72, 80, 32, 16, 0, 0, 44, 16,
	96, 64, 1, 72, 131, 0, 0, 0, 16, 0, 0, 165, 0, 129, 2, 49, 48, 64, 64, 12, 64,
	176, 64, 84, 8, 128, 20, 64, 213, 136, 104, 1, 41, 15, 83, 170, 0, 0, 41, 1, 64,
	64, 0, 193, 64, 64, 8, 0, 128, 0, 0, 64, 8, 64, 8, 1, 16, 0, 8, 0, 0, 2, 1, 128,
	28, 84, 141, 97, 0, 0, 68, 0, 0, 129, 8, 0, 16, 8, 32, 0, 64, 0, 0, 0, 24, 0, 0,
	0, 192, 0, 8, 128, 0, 0, 0, 0, 0, 64, 0, 1, 0, 0, 0, 0, 40, 1, 128, 64, 0, 4, 2,
	32, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 128, 32, 8, 0, 32, 0, 0, 0, 16, 17, 0,
	2, 4, 0, 0, 33, 128, 2, 0, 0, 0, 0, 129, 0, 2, 0, 0, 0, 36, 0, 32, 2, 0, 0, 0,
	0, 0, 0, 32, 0, 0, 0, 0, 4, 0, 0, 0, 0, 0, 0, 0, 4, 32, 64, 0, 0, 0, 0, 0, 0,
	32, 0, 0, 32, 128, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 128, 16, 0, 0, 0, 0, 0, 0, 0,
	1, 0, 136, 0, 0, 24, 192, 128, 3, 0, 17, 18, 2, 0, 66, 0, 4, 24, 0, 9, 208, 167,
	0, 144, 20, 64, 0, 130, 64, 0, 2, 16, 136, 8, 74, 32, 0, 168, 0, 65, 32, 8, 12,
	1, 3, 1, 64, 180, 3, 0, 64, 0, 8, 0, 0, 32, 65, 0, 4, 16, 4, 16, 68, 32, 64, 36,
	32, 24, 33, 1, 128, 0, 0, 8, 0, 32, 64, 81, 0, 1, 10, 19, 8, 0, 0, 4, 5, 144, 0,
	0, 8, 128, 0, 0, 4, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 8, 0, 0, 0, 0, 0, 80, 1, 0, 0,
	33, 0, 32, 66, 4, 2, 0, 1, 43, 2, 0, 0, 4, 32, 16, 0, 64, 0, 3, 32, 0, 2, 64,
	64, 116, 0, 65, 52, 64, 0, 17, 64, 192, 96, 8, 10, 8, 2, 4, 0, 17, 64, 0, 4, 0,
	0, 4, 128, 0, 0, 9, 0, 0, 130, 2, 0, 192, 0, 48, 128, 64, 0, 96, 0, 64, 0, 1,
	16, 32, 0, 1, 32, 6, 128, 2, 32, 0, 12, 0, 0, 48, 32, 8, 0, 0, 128, 0, 18, 0,
	0, 28, 24, 41, 16, 5, 32, 0, 0, 0, 0, 0, 0, 0, 16, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 1, 0, 0, 0, 16, 0, 0, 0, 0, 64, 0, 0, 0, 0, 8, 0, 0, 0, 0, 16, 128,
	0, 0, 0, 16, 0, 0, 0, 0, 0, 0, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 33, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 16, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0,
}

type dictWord struct {
	len       uint8
	transform uint8
	idx       uint16
}

func hash14(data []byte) uint32 {
	return (binary.LittleEndian.Uint32(data) * hashMul32) >> 18
}

func hash15(data []byte) uint32 {
	return (binary.LittleEndian.Uint32(data) * hashMul32) >> 17
}

func main() {
	dict, err := os.ReadFile("dictionary.bin")
	if err != nil {
		log.Fatal(err)
	}
	if len(dict) != dictDataSize {
		log.Fatalf("dictionary.bin: got %d bytes, want %d", len(dict), dictDataSize)
	}

	// Working storage: linked-list chains per bucket.
	slots := make([]dictWord, 0, numLUTItems)
	heads := make([]uint16, numLUTBuckets)  // bucket → head slot index (1-based into slots)
	counts := make([]uint16, numLUTBuckets) // entries per bucket
	prev := make([]uint16, 0, numLUTItems)  // slot → previous slot index (1-based)

	// Allocate slot 0 so real entries are 1-based (0 = nil pointer).
	slots = append(slots, dictWord{})
	prev = append(prev, 0)

	appendSlot := func(key uint32, w dictWord) {
		slots = append(slots, w)
		prev = append(prev, heads[key])
		heads[key] = uint16(len(slots) - 1)
		counts[key]++
	}

	for l := dictMinWordLength; l <= dictMaxWordLength; l++ {
		n := uint32(1) << sizeBitsByLength[l]
		base := offsetsByLength[l]

		// Identity entries.
		for i := range n {
			word := dict[base+uint32(l)*i : base+uint32(l)*i+uint32(l)]
			key := hash15(word)
			appendSlot(key, dictWord{len: uint8(l), transform: transformIdentity, idx: uint16(i)})
		}

		// Uppercase-first entries.
		transformed := make([]byte, l)
		for i := range n {
			word := dict[base+uint32(l)*i : base+uint32(l)*i+uint32(l)]
			if word[0] < 'a' || word[0] > 'z' {
				continue
			}
			copy(transformed, word)
			transformed[0] -= 32
			key := hash15(transformed)

			// Dedup: check if an identical-bytes entry exists in the chain.
			prefix := binary.LittleEndian.Uint32(transformed) & ^uint32(0x20202020)
			found := false
			curr := heads[key]
			for curr != 0 {
				s := &slots[curr]
				if s.len != uint8(l) {
					break
				}
				otherWord := dict[base+uint32(l)*uint32(s.idx) : base+uint32(l)*uint32(s.idx)+uint32(l)]
				otherPrefix := binary.LittleEndian.Uint32(otherWord) & ^uint32(0x20202020)
				if prefix == otherPrefix {
					if bytesEqual(transformed[:l], otherWord) {
						found = true
						break
					}
				}
				curr = prev[curr]
			}
			if found {
				continue
			}
			appendSlot(key, dictWord{len: uint8(l), transform: transformUppercaseFirst, idx: uint16(i)})
		}

		// Uppercase-all entries.
		transformedOther := make([]byte, l)
		for i := range n {
			word := dict[base+uint32(l)*i : base+uint32(l)*i+uint32(l)]

			isASCII := true
			hasLower := false
			for k := 0; k < l; k++ {
				if word[k] >= 128 {
					isASCII = false
				}
				if k > 0 && word[k] >= 'a' && word[k] <= 'z' {
					hasLower = true
				}
			}
			if !isASCII || !hasLower {
				continue
			}

			copy(transformed, word)
			prefix := binary.LittleEndian.Uint32(transformed) & ^uint32(0x20202020)
			for k := 0; k < l; k++ {
				if transformed[k] >= 'a' && transformed[k] <= 'z' {
					transformed[k] -= 32
				}
			}
			key := hash15(transformed)

			found := false
			curr := heads[key]
			for curr != 0 {
				s := &slots[curr]
				if s.len != uint8(l) {
					break
				}
				otherWord := dict[base+uint32(l)*uint32(s.idx) : base+uint32(l)*uint32(s.idx)+uint32(l)]
				otherPrefix := binary.LittleEndian.Uint32(otherWord) & ^uint32(0x20202020)
				if prefix == otherPrefix {
					switch s.transform {
					case transformIdentity:
						if bytesEqual(transformed[:l], otherWord) {
							found = true
						}
					case transformUppercaseFirst:
						if transformed[0] == otherWord[0]-32 &&
							bytesEqual(transformed[1:l], otherWord[1:l]) {
							found = true
						}
					default:
						for k := 0; k < l; k++ {
							if otherWord[k] >= 'a' && otherWord[k] <= 'z' {
								transformedOther[k] = otherWord[k] - 32
							} else {
								transformedOther[k] = otherWord[k]
							}
						}
						if bytesEqual(transformed[:l], transformedOther[:l]) {
							found = true
						}
					}
					if found {
						break
					}
				}
				curr = prev[curr]
			}
			if found {
				continue
			}
			appendSlot(key, dictWord{len: uint8(l), transform: transformUppercaseAll, idx: uint16(i)})
		}
	}

	// slots[0] is the dummy entry; real entries are slots[1:].
	nextSlot := len(slots) - 1
	if nextSlot != 31704 {
		log.Fatalf("next_slot = %d, want 31704", nextSlot)
	}

	// Build output arrays: walk each bucket chain, write entries in FIFO order
	// (reverse the linked list), set end-of-bucket flag on last entry.
	buckets := make([]uint16, numLUTBuckets)
	words := make([]dictWord, numLUTItems)

	// Entry 0 is unused placeholder (offsets start from 1).
	pos := 1
	for i := range numLUTBuckets {
		num := int(counts[i])
		if num == 0 {
			buckets[i] = 0
			continue
		}
		buckets[i] = uint16(pos)
		curr := heads[i]
		end := pos + num
		for k := range num {
			words[end-1-k] = slots[curr]
			curr = prev[curr]
		}
		words[end-1].len |= 0x80
		pos = end
	}

	// --- Hash14-based dictionary hash tables (one entry per bucket) ---
	hashWords, hashLengths, hashFirstBytes := buildDictHash(dict)

	// Emit Go source to static_dict_lut.go.
	f, err := os.Create("static_dict_lut.go")
	if err != nil {
		log.Fatal(err)
	}
	out := bufio.NewWriter(f)

	p := func(format string, args ...any) {
		if _, err := fmt.Fprintf(out, format, args...); err != nil {
			log.Fatal(err)
		}
	}

	p(`// Code generated by cmd/genstaticdict; DO NOT EDIT.

package brrr

// dictWord is a static dictionary hash entry for encoder lookups.
type dictWord struct {
	len       uint8  // word length (bits 0-4) | end-of-bucket flag (bit 7)
	transform uint8  // 0=identity, 10=uppercaseFirst, 11=uppercaseAll
	idx       uint16 // word index within dict data for this length
}

`)

	// staticDictBuckets
	p("var staticDictBuckets = [%d]uint16{", numLUTBuckets)
	for i, v := range buckets {
		if i%20 == 0 {
			p("\n\t")
		}
		p("%d,", v)
	}
	p("\n}\n\n")

	// staticDictWords
	p("var staticDictWords = [%d]dictWord{", numLUTItems)
	for i, w := range words {
		if i%6 == 0 {
			p("\n\t")
		}
		p("{%d,%d,%d},", w.len, w.transform, w.idx)
	}
	p("\n}\n\n")

	// staticDictHashWords
	p("var staticDictHashWords = [%d]uint16{", numHashBuckets)
	for i := range hashWords {
		if i%20 == 0 {
			p("\n\t")
		}
		p("%d,", hashWords[i])
	}
	p("\n}\n\n")

	// staticDictHashLengths
	p("var staticDictHashLengths = [%d]byte{", numHashBuckets)
	for i := range hashLengths {
		if i%32 == 0 {
			p("\n\t")
		}
		p("%d,", hashLengths[i])
	}
	p("\n}\n\n")

	// staticDictHashFirstBytes
	p("var staticDictHashFirstBytes = [%d]byte{", numHashBuckets)
	for i := range hashFirstBytes {
		if i%32 == 0 {
			p("\n\t")
		}
		p("%d,", hashFirstBytes[i])
	}
	p("\n}\n")

	if err := out.Flush(); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
}

// buildDictHash generates the Hash14-based dictionary hash tables.
// Bucket index = (Hash14(word) << 1) | (len < 8 ? 1 : 0).
// Words are iterated longest-first, reverse index order. The frozenIdx bitmap
// controls which bucket entries are "final" and cannot be overwritten by
// shorter words.
func buildDictHash(dict []byte) (hashWords [numHashBuckets]uint16, hashLengths, hashFirstBytes [numHashBuckets]byte) {
	globalIdx := 0

	for l := dictMaxWordLength; l >= dictMinWordLength; l-- {
		lengthLt8 := uint32(0)
		if l < 8 {
			lengthLt8 = 1
		}
		n := int(1) << sizeBitsByLength[l]
		base := offsetsByLength[l]

		for i := range n {
			j := n - 1 - i
			word := dict[base+uint32(l)*uint32(j) : base+uint32(l)*uint32(j)+uint32(l)]
			key := hash14(word)
			idx := (key << 1) + lengthLt8

			if hashLengths[idx]&0x80 == 0 {
				isFrozen := frozenIdx[globalIdx/8]&(1<<(globalIdx%8)) != 0
				hashWords[idx] = uint16(j)
				hashFirstBytes[idx] = word[0]
				if isFrozen {
					hashLengths[idx] = byte(l) | 0x80
				} else {
					hashLengths[idx] = byte(l)
				}
			}
			globalIdx++
		}
	}

	// Strip the frozen bits.
	for i := range hashLengths {
		hashLengths[i] &= 0x7F
	}
	return
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
