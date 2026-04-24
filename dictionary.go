// Static dictionary data and metadata defined by RFC 7932 Appendix A.

package brrr

//go:generate go run ./cmd/genstaticdict

import _ "embed"

const (
	dictMinWordLength = 4
	dictMaxWordLength = 24
	dictDataSize      = 122784
)

// Number of index bits for each word length (RFC 7932 Appendix A).
var dictSizeBitsByLength = [32]byte{
	0, 0, 0, 0, 10, 10, 11, 11,
	10, 10, 10, 10, 10, 9, 9, 8,
	7, 7, 8, 7, 7, 6, 6, 5,
	5, 0, 0, 0, 0, 0, 0, 0,
}

// Byte offset into dictData where words of each length begin.
var dictOffsetsByLength = [32]uint32{
	0, 0, 0, 0, 0, 4096, 9216, 21504,
	35840, 44032, 53248, 63488, 74752, 87040, 93696, 100864,
	104704, 106752, 108928, 113536, 115968, 118528, 119872, 121280,
	122016, 122784, 122784, 122784, 122784, 122784, 122784, 122784,
}

// Brotli static dictionary (RFC 7932 Appendix A). Embedded as a string for
// zero-copy, read-only access.
//
//go:embed dictionary.bin
var dictData string
