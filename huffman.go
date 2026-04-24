package brrr

// Huffman (prefix code) construction, encoding, and decoding table building.
//
// A symbol is an element of an alphabet. Each alphabet has its own Huffman tree
// (prefix code), and decoding one entry from that tree produces one symbol.
// The RFC uses "symbol" and "code" interchangeably; the meaning depends on
// which alphabet is in context:
//
//   - Literal symbols:            0–255   (raw bytes)
//   - Insert-and-copy symbols:    0–703   (Section 5)
//   - Distance symbols:           0–N     (Section 4, N depends on parameters)
//   - Block count symbols:        0–25    (Section 6.3)
//   - Block type symbols:         0–N     (Section 6, N = number of block types + 1)
//   - Code length symbols:        0–17    (Section 3.5, used to build other trees)
//
// Some symbols map directly to values (e.g., literal symbols are byte values).
// Others are intermediary codes that require extra bits to produce a final value
// (see coderange.go).
//
// Brotli transmits Huffman trees ("prefix codes") using a three-layer encoding.
// The layers, from outermost (closest to raw bits) to innermost (used on data):
//
//	Layer 0: Fixed code (hardcoded, never transmitted)
//	    Encodes values 0-5, which represent possible code lengths
//	    for code length alphabet symbols.
//
//	        Value   Bits
//	          0       00
//	          1     0111
//	          2      011
//	          3       01
//	          4       10
//	          5     1111
//
//	Layer 1: Code length alphabet (symbols 0-17)
//	    Symbols 0-15 are literal code lengths.
//	    Symbol 16 repeats the previous non-zero code length (RLE).
//	    Symbol 17 repeats zero (RLE for unused symbols).
//	    Each symbol's own code length (0-5) is transmitted using Layer 0.
//
//	Layer 2: Data alphabet (literals, insert-and-copy, distances)
//	    Each symbol's code length (0-15) is transmitted using the
//	    Huffman tree built from Layer 1.
//
// Concrete example — encoding a data alphabet with 6 symbols:
//
//	Data symbol:   0   1   2   3   4   5
//	Code length:   2   3   3   3   3   2
//
// Layer 2 → 1: Encode the code length sequence using the code length alphabet.
// Instead of transmitting [2, 3, 3, 3, 3, 2] literally, use RLE:
//
//	Stream:  [2] [3] [16 +bits=00] [2]
//	          │   │   │             └─ literal code length 2
//	          │   │   └─ repeat previous non-zero (3) three more times
//	          │   └─ literal code length 3
//	          └─ literal code length 2
//
// Code length alphabet symbols used: {2, 3, 16}. Each needs a Huffman code.
//
// Layer 1 → 0: Assign code lengths to code length alphabet symbols.
//
//	Code length alphabet symbol:  0  1  2  3  4  5 ... 15  16  17
//	Code length OF that symbol:   0  0  1  2  0  0 ...  0   2   0
//
// These values are transmitted in the order
// [1, 2, 3, 4, 0, 5, 17, 6, 16, 7, 8, 9, 10, 11, 12, 13, 14, 15]
// using the Layer 0 fixed code. Trailing zeros are omitted.
//
//	Transmission        Code length     Encoded with
//	order       Symbol  to send         Layer 0 fixed code
//	─────       ──────  ───────         ──────────────────
//	  1st          1       0 (unused)     00
//	  2nd          2       1              0111
//	  3rd          3       2              011
//	  4th          4       0 (unused)     00
//	  5th          0       0 (unused)     00
//	  6th          5       0 (unused)     00
//	  7th         17       0 (unused)     00
//	  8th          6       0 (unused)     00
//	  9th         16       2              011
//	              (remaining symbols are trailing zeros, omitted)
//
// The two-bit HSKIP field before the sequence allows skipping 0, 2, or 3
// leading entries (their code lengths are implicitly zero). HSKIP=1 signals
// a simple prefix code instead (section 3.4), which handles 1-4 symbols
// without the three-layer machinery.

import (
	"math"
	"math/bits"
	"slices"
	"unsafe"
)

// Huffman coding limits from the brotli specification.
const (
	huffmanMaxCodeLength           = 15
	huffmanMaxCodeLengthCodeLength = 5
)

// reverseBitsWidth is the bit width used for canonical-code reversal (8 = one byte).
const reverseBitsWidth = 8

// reverseBitsHigh is the highest bit in a reverseBitsWidth-wide field.
const reverseBitsHigh = uint64(1) << (reverseBitsWidth - 1)

// Compile-time assertion: createHuffmanTree reinterprets []huffmanTreeNode as
// []uint64 for sorting, so the struct must be exactly 8 bytes.
var _ [unsafe.Sizeof(huffmanTreeNode{})]struct{} = [8]struct{}{}

// huffmanCode is a single entry in a Huffman lookup table.
type huffmanCode struct {
	bits  byte
	value uint16
}

// symbolList provides access to a uint16 slice with a base offset,
// supporting the negative-index linked-list pattern used during Huffman table construction.
type symbolList struct {
	storage []uint16
	offset  int
}

// huffmanTreeNode is a node in a Huffman tree used during tree construction.
type huffmanTreeNode struct {
	totalCount   uint32
	left         int16
	rightOrValue int16
}

func (sl symbolList) get(i int) uint16 {
	return *(*uint16)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(sl.storage)), uintptr(sl.offset+i)*2))
}

// --- Tree construction ---

// createHuffmanTree builds a Huffman tree from symbol frequencies and writes
// the resulting bit depths into depth[].
//
// The tree cannot be arbitrarily deep. Brotli specifies a maximum depth of
// 15 bits for "code trees" and 7 bits for "code length code trees."
//
// countLimit is faked as the minimum value and raised until the tree fits
// within treeLimit.
//
// This algorithm is not of excellent performance for very long data blocks,
// especially when population counts are longer than 2**treeLimit, but
// we are not planning to use this with extremely long blocks.
//
// See http://en.wikipedia.org/wiki/Huffman_coding
func createHuffmanTree(data []uint32, treeLimit int, tree []huffmanTreeNode, depth []byte) {
	sentinel := huffmanTreeNode{totalCount: math.MaxUint32, left: -1, rightOrValue: -1}

	// For block sizes below 64 kB, we never need to do a second iteration
	// of this loop. Probably all of our block sizes will be smaller than
	// that, so this loop is mostly of academic interest. If we actually
	// would need this, we would be better off with the Katajainen algorithm.
	for countLimit := uint32(1); ; countLimit *= 2 {
		n := 0
		for i := len(data) - 1; i >= 0; i-- {
			if data[i] != 0 {
				tree[n] = huffmanTreeNode{
					totalCount:   max(data[i], countLimit),
					left:         -1,
					rightOrValue: int16(i),
				}
				n++
			}
		}

		if n == 1 {
			depth[tree[0].rightOrValue] = 1 // Only one element.
			break
		}

		// Sort leaf nodes by (totalCount ASC, rightOrValue DESC).
		// Pack each node into a uint64 sort key so we can use slices.Sort
		// (direct integer comparison, no closure overhead) instead of
		// slices.SortFunc. huffmanTreeNode is 8 bytes == uint64, so we
		// reinterpret the same backing memory.
		sortKeys := unsafe.Slice((*uint64)(unsafe.Pointer(&tree[0])), n)
		for i := 0; i < n; i++ {
			tc := tree[i].totalCount
			rv := uint16(tree[i].rightOrValue)
			sortKeys[i] = uint64(tc)<<32 | uint64(math.MaxInt16-rv)<<16 | uint64(rv)
		}
		slices.Sort(sortKeys)
		for i := 0; i < n; i++ {
			packed := sortKeys[i]
			tree[i] = huffmanTreeNode{
				totalCount:   uint32(packed >> 32),
				left:         -1,
				rightOrValue: int16(packed & 0xFFFF),
			}
		}

		// The nodes are:
		//   [0, n): the sorted leaf nodes that we start with.
		//   [n]: we add a sentinel here.
		//   [n + 1, 2n): new parent nodes are added here, starting from
		//                (n+1). These are naturally in ascending order.
		//   [2n]: we add a sentinel at the end as well.
		//   There will be (2n+1) elements at the end.
		tree[n] = sentinel
		tree[n+1] = sentinel

		i := 0     // Points to the next leaf node.
		j := n + 1 // Points to the next non-leaf node.
		for k := n - 1; k != 0; k-- {
			var left, right int
			if tree[i].totalCount <= tree[j].totalCount {
				left = i
				i++
			} else {
				left = j
				j++
			}

			if tree[i].totalCount <= tree[j].totalCount {
				right = i
				i++
			} else {
				right = j
				j++
			}

			// The sentinel node becomes the parent node.
			jEnd := 2*n - k
			tree[jEnd] = huffmanTreeNode{
				totalCount:   tree[left].totalCount + tree[right].totalCount,
				left:         int16(left),
				rightOrValue: int16(right),
			}

			// Add back the last sentinel node.
			tree[jEnd+1] = sentinel
		}

		if setDepth(tree, depth, 2*n-1, treeLimit) {
			// We need to pack the Huffman tree in treeLimit bits. If this was not
			// successful, add fake entities to the lowest values and retry.
			break
		}
	}
}

// --- Tree encoding (RLE + code length serialization) ---

// optimizeHuffmanCountsForRLE adjusts population counts so that the
// subsequent Huffman tree compression (especially its RLE part) is more
// likely to compress efficiently.
//
// goodForRLEBuf is a reusable scratch buffer to avoid per-call allocation;
// callers pass the same pointer across calls so the backing array is reused.
func optimizeHuffmanCountsForRLE(counts []uint32, goodForRLEBuf *[]bool) {
	streakLimit := 1240

	// Single forward pass: find trimmed length, count nonzeros, find smallest.
	length := 0
	nonzeroCount := 0
	smallestNonzero := uint32(1 << 30)
	for i, c := range counts {
		if c != 0 {
			length = i + 1
			nonzeroCount++
			if c < smallestNonzero {
				smallestNonzero = c
			}
		}
	}

	if nonzeroCount < 16 {
		return
	}

	if smallestNonzero < 4 {
		zeros := length - nonzeroCount
		if zeros < 6 {
			for i := 1; i < length-1; i++ {
				if counts[i-1] != 0 && counts[i] == 0 && counts[i+1] != 0 {
					counts[i] = 1
				}
			}
		}
	}

	if nonzeroCount < 28 {
		return
	}

	// Mark all population counts that already can be encoded with an RLE code.
	var goodForRLE []bool
	if cap(*goodForRLEBuf) < length {
		*goodForRLEBuf = make([]bool, length)
	} else {
		*goodForRLEBuf = (*goodForRLEBuf)[:length]
		clear(*goodForRLEBuf)
	}
	goodForRLE = *goodForRLEBuf

	// Don't spoil any of the existing good RLE codes.
	// Mark any seq of 0's longer than 5 as goodForRLE.
	// Mark any seq of non-0's longer than 7 as goodForRLE.
	sym := counts[0]
	step := 0
	for i := 0; i <= length; i++ {
		if i == length || counts[i] != sym {
			if (sym == 0 && step >= 5) || (sym != 0 && step >= 7) {
				for k := 0; k < step; k++ {
					goodForRLE[i-k-1] = true
				}
			}

			step = 1
			if i != length {
				sym = counts[i]
			}
		} else {
			step++
		}
	}

	// Replace population counts that lead to more RLE codes.
	// Math here is in 24.8 fixed point representation.
	stride := 0
	limit := int(256*(counts[0]+counts[1]+counts[2])/3 + 420)
	sum := 0
	for i := 0; i <= length; i++ {
		breakStride := i == length
		if !breakStride {
			val := int(256*counts[i]) - limit
			breakStride = goodForRLE[i] || (i != 0 && goodForRLE[i-1]) || val < -streakLimit || val >= streakLimit
		}
		if breakStride {
			if stride >= 4 || (stride >= 3 && sum == 0) {
				count := (sum + stride/2) / stride
				// The stride must end, collapse what we have, if we have enough (4).
				if count == 0 {
					count = 1
				}
				if sum == 0 {
					// Don't make an all zeros stride to be upgraded to ones.
					count = 0
				}

				for k := 0; k < stride; k++ {
					// We don't want to change value at counts[i],
					// that is already belonging to the next stride. Thus - 1.
					counts[i-k-1] = uint32(count)
				}
			}

			stride = 0
			sum = 0
			switch {
			case i < length-2:
				// All interesting strides have a count of at least 4,
				// at least when non-zeros.
				limit = int(256*(counts[i]+counts[i+1]+counts[i+2])/3 + 420)
			case i < length:
				limit = int(256 * counts[i])
			default:
				limit = 0
			}
		}

		stride++
		if i != length {
			sum += int(counts[i])
			if stride >= 4 {
				// float64 division is ~3× faster than IDIVQ for variable
				// divisors, and exact for our input range (numerator < 2^25,
				// denominator < 2^9, quotient < 2^17 — all exactly
				// representable in float64's 52-bit mantissa).
				limit = int(float64(256*sum+stride/2) / float64(stride))
			}
			if stride == 4 {
				limit += 120
			}
		}
	}
}

// encodeHuffmanTreeRepetitions encodes repetitions of value into tree/extraBitsData
// starting at position 0. Returns the number of elements written.
func encodeHuffmanTreeRepetitions(tree, extraBitsData []byte, prevValue, value byte, repetitions int) int {
	assert(repetitions > 0)
	n := 0
	if prevValue != value {
		tree[n] = value
		extraBitsData[n] = 0
		n++
		repetitions--
	}

	if repetitions == 7 {
		tree[n] = value
		extraBitsData[n] = 0
		n++
		repetitions--
	}

	if repetitions < 3 {
		for range repetitions {
			tree[n] = value
			extraBitsData[n] = 0
			n++
		}
	} else {
		start := n
		repetitions -= 3
		for {
			tree[n] = repeatPreviousCodeLength
			extraBitsData[n] = byte(repetitions & 0x3)
			n++
			repetitions >>= 2
			if repetitions == 0 {
				break
			}
			repetitions--
		}

		slices.Reverse(tree[start:n])
		slices.Reverse(extraBitsData[start:n])
	}
	return n
}

// encodeHuffmanTreeRepetitionsZeros encodes repetitions of zero into tree/extraBitsData
// starting at position 0. Returns the number of elements written.
func encodeHuffmanTreeRepetitionsZeros(tree, extraBitsData []byte, repetitions int) int {
	n := 0
	if repetitions == 11 {
		tree[n] = 0
		extraBitsData[n] = 0
		n++
		repetitions--
	}

	if repetitions < 3 {
		for range repetitions {
			tree[n] = 0
			extraBitsData[n] = 0
			n++
		}
	} else {
		start := n
		repetitions -= 3
		for {
			tree[n] = alphabetSizeRepeatZeroCodeLength
			extraBitsData[n] = byte(repetitions & 0x7)
			n++
			repetitions >>= 3
			if repetitions == 0 {
				break
			}
			repetitions--
		}

		slices.Reverse(tree[start:n])
		slices.Reverse(extraBitsData[start:n])
	}
	return n
}

// findRunEnd returns the first index in depth[start:] where the value
// changes from depth[start-1], i.e., the exclusive end of a run. It scans
// 8 bytes at a time using loadU64LE for speed, then handles any remainder
// byte-by-byte. Equivalent to:
//
//	i := start
//	for i < len(depth) && depth[i] == value { i++ }
//	return i
func findRunEnd(depth []byte, start int, value byte) int {
	v64 := uint64(value) * 0x0101010101010101
	n := start
	for ; n+8 <= len(depth); n += 8 {
		diff := loadU64LE(depth, uint(n)) ^ v64
		if diff != 0 {
			return n + bits.TrailingZeros64(diff)/8
		}
	}
	for ; n < len(depth) && depth[n] == value; n++ {
	}
	return n
}

// decideOverRLEUse examines depth and returns whether RLE encoding
// should be used for nonzero and zero runs respectively.
func decideOverRLEUse(depth []byte) (useNonZero, useZero bool) {
	totalRepsZero := 0
	totalRepsNonZero := 0
	countRepsZero := 1
	countRepsNonZero := 1

	for i := 0; i < len(depth); {
		value := depth[i]
		end := findRunEnd(depth, i+1, value)
		reps := end - i

		if reps >= 3 && value == 0 {
			totalRepsZero += reps
			countRepsZero++
		}
		if reps >= 4 && value != 0 {
			totalRepsNonZero += reps
			countRepsNonZero++
		}

		i = end
	}

	return totalRepsNonZero > countRepsNonZero*2, totalRepsZero > countRepsZero*2
}

// encodeHuffmanTree encodes a Huffman tree from bit depths into the bit-stream
// representation of a Huffman tree. The generated Huffman tree is to be
// compressed once more using a Huffman tree. Returns the number of elements written.
func encodeHuffmanTree(depth, tree, extraBitsData []byte) int {
	prevValue := byte(initialRepeatedCodeLength)
	useRLENonZero := false
	useRLEZero := false

	// Trim trailing zeros.
	newLength := len(depth)
	for newLength > 0 && depth[newLength-1] == 0 {
		newLength--
	}

	// First gather statistics on if it is a good idea to do RLE.
	if len(depth) > 50 {
		// Find RLE coding for longer codes.
		// Shorter codes seem not to benefit from RLE.
		useRLENonZero, useRLEZero = decideOverRLEUse(depth[:newLength])
	}

	// Actual RLE coding.
	size := 0
	for i := 0; i < newLength; {
		value := depth[i]
		reps := 1
		if (value != 0 && useRLENonZero) || (value == 0 && useRLEZero) {
			end := findRunEnd(depth[:newLength], i+1, value)
			reps = end - i
		}

		if value == 0 {
			size += encodeHuffmanTreeRepetitionsZeros(tree[size:], extraBitsData[size:], reps)
		} else {
			size += encodeHuffmanTreeRepetitions(tree[size:], extraBitsData[size:], prevValue, value, reps)
			prevValue = value
		}

		i += reps
	}
	return size
}

// --- Bit depth / symbol conversion ---

func reverseBits(numBits int, val uint16) uint16 {
	return bits.Reverse16(val) >> uint(16-numBits)
}

// convertBitDepthsToSymbols generates the actual bit values for a tree of
// bit depths. In Brotli, all bit depths are [1..15]; 0 means the symbol
// does not exist.
func convertBitDepthsToSymbols(depth []byte, symbols []uint16) {
	var blCount [maxHuffmanBits]uint16
	var nextCode [maxHuffmanBits]uint16

	// Build the histogram using 4-way interleaving to reduce store-to-load
	// forwarding stalls when consecutive depth values map to the same bucket.
	// Each sub-histogram (bc1..bc3) is independent, so the CPU can pipeline
	// four scatter-increments per iteration without RAW hazards.
	code := 0
	{
		var bc1, bc2, bc3 [maxHuffmanBits]uint16
		i, n := 0, len(depth)
		for ; i+3 < n; i += 4 {
			blCount[depth[i]&0x0F]++
			bc1[depth[i+1]&0x0F]++
			bc2[depth[i+2]&0x0F]++
			bc3[depth[i+3]&0x0F]++
		}
		for ; i < n; i++ {
			blCount[depth[i]&0x0F]++
		}
		for j := range blCount {
			blCount[j] += bc1[j] + bc2[j] + bc3[j]
		}
	}

	blCount[0] = 0
	nextCode[0] = 0
	for i := 1; i < maxHuffmanBits; i++ {
		code = (code + int(blCount[i-1])) << 1
		nextCode[i] = uint16(code)
	}

	// Narrow symbols to the exact length so the compiler sees
	// len(symbols)==len(depth) and can eliminate the symbols[i] check.
	symbols = symbols[:len(depth)]
	for i, d := range depth {
		if d != 0 {
			dm := d & 0x0F
			symbols[i] = reverseBits(int(dm), nextCode[dm])
			nextCode[dm]++
		}
	}
}

// --- Decoding table construction ---

// replicateValue fills table[0], table[step], table[2*step], ..., table[end-step] with code.
// Uses unsafe to pack the huffmanCode into a single uint32 store per iteration,
// avoiding separate byte+uint16 stores and per-iteration bounds checks.
func replicateValue(table []huffmanCode, code huffmanCode, step, end int) {
	base := unsafe.Pointer(unsafe.SliceData(table))
	packed := uint32(code.bits) | uint32(code.value)<<16
	for {
		end -= step
		*(*uint32)(unsafe.Add(base, uintptr(end)*4)) = packed
		if end <= 0 {
			break
		}
	}
}

// nextTableBitSize returns the width of the next 2nd-level table.
// count is the histogram of bit lengths for remaining symbols,
// length is the code length of the next processed symbol.
func nextTableBitSize(count []uint16, length, rootBits int) int {
	left := 1 << uint(length-rootBits)
	for length < huffmanMaxCodeLength {
		left -= int(count[length])
		if left <= 0 {
			break
		}
		length++
		left <<= 1
	}
	return length - rootBits
}

func buildCodeLengthsHuffmanTable(table []huffmanCode, codeLengths []byte, count []uint16) {
	assert(huffmanMaxCodeLengthCodeLength <= reverseBitsWidth)

	var sorted [alphabetSizeCodeLengths]int
	var offset [huffmanMaxCodeLengthCodeLength + 1]int

	// Generate offsets into sorted symbol table by code length.
	sym := -1
	for bitLen := 1; bitLen <= huffmanMaxCodeLengthCodeLength; bitLen++ {
		sym += int(count[bitLen])
		offset[bitLen] = sym
	}

	// Symbols with code length 0 are placed after all other symbols.
	offset[0] = alphabetSizeCodeLengths - 1

	// Sort symbols by length, by symbol order within each length.
	sym = alphabetSizeCodeLengths
	for {
		for range 6 {
			sym--
			sorted[offset[codeLengths[sym]]] = sym
			offset[codeLengths[sym]]--
		}
		if sym == 0 {
			break
		}
	}

	tableSize := 1 << huffmanMaxCodeLengthCodeLength

	// Special case: all symbols but one have 0 code length.
	if offset[0] == 0 {
		code := huffmanCode{bits: 0, value: uint16(sorted[0])}
		for key := range tableSize {
			table[key] = code
		}
		return
	}

	// Fill in table.
	key := uint64(0)
	keyStep := reverseBitsHigh
	sym = 0
	step := 2
	for bitLen := 1; bitLen <= huffmanMaxCodeLengthCodeLength; bitLen++ {
		for n := int(count[bitLen]); n > 0; n-- {
			code := huffmanCode{bits: byte(bitLen), value: uint16(sorted[sym])}
			sym++
			replicateValue(table[bits.Reverse8(byte(key)):], code, step, tableSize)
			key += keyStep
		}
		step <<= 1
		keyStep >>= 1
	}
}

func buildHuffmanTable(rootTable []huffmanCode, rootBits int, symbols symbolList, count []uint16) uint32 { //nolint:unparam // rootBits varies at runtime
	assert(rootBits <= reverseBitsWidth)
	assert(huffmanMaxCodeLength-rootBits <= reverseBitsWidth)

	maxLength := -1
	for symbols.get(maxLength) == 0xFFFF {
		maxLength--
	}
	maxLength += huffmanMaxCodeLength + 1

	table := rootTable
	tableBits := rootBits
	tableSize := 1 << uint(tableBits)
	totalSize := tableSize

	// Reduce the root table size if possible, and create repetitions by copy.
	if tableBits > maxLength {
		tableBits = maxLength
		tableSize = 1 << uint(tableBits)
	}

	key := uint64(0)
	keyStep := reverseBitsHigh
	step := 2
	for bitLen := 1; bitLen <= tableBits; bitLen++ {
		sym := bitLen - (huffmanMaxCodeLength + 1)
		for n := int(count[bitLen]); n > 0; n-- {
			sym = int(symbols.get(sym))
			code := huffmanCode{bits: byte(bitLen), value: uint16(sym)}
			replicateValue(table[bits.Reverse8(byte(key)):], code, step, tableSize)
			key += keyStep
		}
		step <<= 1
		keyStep >>= 1
	}

	// If rootBits != tableBits then replicate to fill the remaining slots.
	for totalSize != tableSize {
		copy(table[tableSize:], table[:uint(tableSize)])
		tableSize <<= 1
	}

	// Fill in 2nd level tables and add pointers to root table.
	keyStep = reverseBitsHigh >> uint(rootBits-1)
	subKey := reverseBitsHigh << 1
	subKeyStep := reverseBitsHigh
	step = 2
	for codeLen := rootBits + 1; codeLen <= maxLength; codeLen++ {
		sym := codeLen - (huffmanMaxCodeLength + 1)
		for ; count[codeLen] != 0; count[codeLen]-- {
			if subKey == reverseBitsHigh<<1 {
				table = table[tableSize:]
				tableBits = nextTableBitSize(count, codeLen, rootBits)
				tableSize = 1 << uint(tableBits)
				totalSize += tableSize
				subKey = uint64(bits.Reverse8(byte(key)))
				key += keyStep
				rootTable[subKey] = huffmanCode{
					bits:  byte(tableBits + rootBits),
					value: uint16(uint64(uint(-cap(table)+cap(rootTable))) - subKey),
				}
				subKey = 0
			}

			sym = int(symbols.get(sym))
			code := huffmanCode{bits: byte(codeLen - rootBits), value: uint16(sym)}
			replicateValue(table[bits.Reverse8(byte(subKey)):], code, step, tableSize)
			subKey += subKeyStep
		}
		step <<= 1
		subKeyStep >>= 1
	}

	return uint32(totalSize)
}

func buildSimpleHuffmanTable(table []huffmanCode, rootBits int, val []uint16, numSymbols uint32) uint32 { //nolint:unparam // rootBits varies at runtime
	tableSize := uint32(1)
	goalSize := uint32(1) << uint(rootBits)

	switch numSymbols {
	case 0:
		table[0] = huffmanCode{bits: 0, value: val[0]}

	case 1:
		if val[1] > val[0] {
			table[0] = huffmanCode{bits: 1, value: val[0]}
			table[1] = huffmanCode{bits: 1, value: val[1]}
		} else {
			table[0] = huffmanCode{bits: 1, value: val[1]}
			table[1] = huffmanCode{bits: 1, value: val[0]}
		}
		tableSize = 2

	case 2:
		table[0] = huffmanCode{bits: 1, value: val[0]}
		table[2] = huffmanCode{bits: 1, value: val[0]}
		if val[2] > val[1] {
			table[1] = huffmanCode{bits: 2, value: val[1]}
			table[3] = huffmanCode{bits: 2, value: val[2]}
		} else {
			table[1] = huffmanCode{bits: 2, value: val[2]}
			table[3] = huffmanCode{bits: 2, value: val[1]}
		}
		tableSize = 4

	case 3:
		for i := range 3 {
			for k := i + 1; k < 4; k++ {
				if val[k] < val[i] {
					val[k], val[i] = val[i], val[k]
				}
			}
		}
		table[0] = huffmanCode{bits: 2, value: val[0]}
		table[2] = huffmanCode{bits: 2, value: val[1]}
		table[1] = huffmanCode{bits: 2, value: val[2]}
		table[3] = huffmanCode{bits: 2, value: val[3]}
		tableSize = 4

	case 4:
		if val[3] < val[2] {
			val[3], val[2] = val[2], val[3]
		}
		table[0] = huffmanCode{bits: 1, value: val[0]}
		table[1] = huffmanCode{bits: 2, value: val[1]}
		table[2] = huffmanCode{bits: 1, value: val[0]}
		table[3] = huffmanCode{bits: 3, value: val[2]}
		table[4] = huffmanCode{bits: 1, value: val[0]}
		table[5] = huffmanCode{bits: 2, value: val[1]}
		table[6] = huffmanCode{bits: 1, value: val[0]}
		table[7] = huffmanCode{bits: 3, value: val[3]}
		tableSize = 8
	}

	for tableSize != goalSize {
		copy(table[tableSize:], table[:uint(tableSize)])
		tableSize <<= 1
	}

	return goalSize
}

// setDepth walks the Huffman tree rooted at pool[p0] and writes the
// depth of each leaf into depth[]. Returns false if any leaf exceeds maxDepth.
func setDepth(pool []huffmanTreeNode, depth []byte, p0, maxDepth int) bool {
	var stack [16]int
	level := 0
	p := p0
	assert(maxDepth <= 15)
	stack[0] = -1
	for {
		if pool[p].left >= 0 {
			level++
			if level > maxDepth {
				return false
			}
			stack[level] = int(pool[p].rightOrValue)
			p = int(pool[p].left)
			continue
		}
		depth[pool[p].rightOrValue] = byte(level)

		for level >= 0 && stack[level] == -1 {
			level--
		}
		if level < 0 {
			return true
		}
		p = stack[level]
		stack[level] = -1
	}
}

func assert(cond bool) {
	if !cond {
		panic("assertion failure")
	}
}
