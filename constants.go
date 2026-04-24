package brrr

const (
	// Alphabet sizes from the brotli specification.
	// https://datatracker.ietf.org/doc/html/rfc7932#section-3.3
	alphabetSizeLiteral             = 256
	alphabetSizeInsertAndCopyLength = 704
	alphabetSizeBlockCount          = 26

	// Number of distance short codes that reference the ring buffer of
	// recent distances (RFC 7932 Section 4).
	numDistanceShortCodes = 16

	// Complex prefix codes
	// https://datatracker.ietf.org/doc/html/rfc7932#section-3.5
	alphabetSizeRepeatZeroCodeLength = 17
	alphabetSizeCodeLengths          = alphabetSizeRepeatZeroCodeLength + 1

	// Maximum distance alphabet size across all valid parameter combinations.
	// Covers NPOSTFIX=0..3, NDIRECT=0..120 (RFC 7932 Section 4).
	alphabetSizeDistance = 140

	// numHistogramDistanceSymbols is the fixed histogram bin count used for
	// distance histograms in the block splitter and histogram clustering.
	// Matches BROTLI_NUM_HISTOGRAM_DISTANCE_SYMBOLS in the C reference.
	numHistogramDistanceSymbols = 544

	// Maximum number of extra bits in a distance code (RFC 7932 Section 4).
	// The distance alphabet size formula uses this as the MAXNBITS parameter.
	maxDistanceBits = 24

	// Safety margin between the window size and the maximum backward distance.
	// Reserves space for ring-buffer read-ahead in the streaming encoder.
	windowGap = 16

	// Entropy coding constants
	// https://datatracker.ietf.org/doc/html/rfc7932#section-3.5
	repeatPreviousCodeLength  = 16 // used for repeating previous non-zero code length
	initialRepeatedCodeLength = 8  // "code length of 8 is repeated"
	maxHuffmanBits            = 16 // 0..15 are values for bits

	// Maximum number of block types (RFC 7932 Section 6).
	maxNumberOfBlockTypes = 256
	maxBlockTypeSymbols   = maxNumberOfBlockTypes + 2 // 258

	// Absolute max backward distance expressible in the brotli bitstream
	// (NDIRECT=0, NPOSTFIX=0, MAXNBITS=24). RFC 7932 Section 4.
	maxBackwardDistance = 0x3FFFFFC
)

// Order in which code length codes are transmitted (RFC 7932 section 3.5).
var codeLengthCodeOrder = [...]byte{
	1, 2, 3, 4, 0, 5, 17, 6, 16, 7, 8, 9, 10, 11, 12, 13, 14, 15,
}

// Static Huffman code for encoding code length bit depths:
//
//	Symbol   Code
//	  0        00
//	  1      1110
//	  2       110
//	  3        01
//	  4        10
//	  5      1111
var codeLengthCodeSymbols = [...]byte{0, 7, 3, 2, 1, 15}
var codeLengthCodeBitLengths = [...]byte{2, 4, 3, 2, 2, 4}

// Block length prefix code ranges (RFC 7932 Section 6).
// Each entry is {offset, nbits}; the block length for code c is:
//
//	offset[c] + ReadBits(nbits[c])
var blockLengthOffset = [alphabetSizeBlockCount]uint32{
	1, 5, 9, 13, 17, 25, 33, 41, 49, 65, 81, 97,
	113, 145, 177, 209, 241, 305, 369, 497, 753, 1265, 2289, 4337,
	8433, 16625,
}

var blockLengthNBits = [alphabetSizeBlockCount]uint32{
	2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4,
	5, 5, 5, 5, 6, 6, 7, 8, 9, 10, 11, 12,
	13, 24,
}

// Insert/copy length code lookup tables (RFC 7932 Section 5).
// Indexed by insert length code (0–23) or copy length code (0–23).
var insertBase = [24]uint32{
	0, 1, 2, 3, 4, 5, 6, 8, 10, 14, 18, 26,
	34, 50, 66, 98, 130, 194, 322, 578, 1090, 2114, 6210, 22594,
}

var insertExtra = [24]uint32{
	0, 0, 0, 0, 0, 0, 1, 1, 2, 2, 3, 3,
	4, 4, 5, 5, 6, 7, 8, 9, 10, 12, 14, 24,
}

var copyBase = [24]uint32{
	2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 14, 18,
	22, 30, 38, 54, 70, 102, 134, 198, 326, 582, 1094, 2118,
}

var copyExtra = [24]uint32{
	0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 2, 2,
	3, 3, 4, 4, 5, 5, 6, 7, 8, 9, 10, 24,
}

// getBlockLengthPrefixCode returns the prefix code, number of extra bits,
// and extra bits value for a block length.
func getBlockLengthPrefixCode(length uint32) (code, nExtra, extra uint32) {
	// Fast jump to approximate code, then linear scan.
	switch {
	case length >= 753:
		code = 20
	case length >= 177:
		code = 14
	case length >= 41:
		code = 7
	default:
		code = 0
	}
	for code < alphabetSizeBlockCount-1 && length >= blockLengthOffset[code+1] {
		code++
	}
	nExtra = blockLengthNBits[code]
	extra = length - blockLengthOffset[code]
	return code, nExtra, extra
}
