// H10 binary tree hasher for quality 10–11 (Zopfli).
//
// H10 is a hash table where each bucket contains a binary search tree of
// sequences whose first 4 bytes share the same hash code. Each sequence is
// up to h10MaxTreeCompLength (128) bytes long and is identified by its
// starting position in the input data. The binary tree is sorted by the
// lexicographic order of the sequences, and it is also a max-heap with
// respect to starting positions (newer positions are always ancestors of
// older ones).
//
// Unlike the bucket-chain hashers (H5/H6) which return the single best
// match, H10 returns all matches at a position sorted by increasing length.
// This match set is consumed by the Zopfli optimal parsing algorithm.

package brrr

// H10 configuration constants.
const (
	h10BucketBits = 17
	h10BucketSize = 1 << h10BucketBits // 131,072

	// h10MaxTreeSearchDepth is the maximum number of tree nodes examined
	// per storeAndFindMatches call, bounding worst-case search time.
	h10MaxTreeSearchDepth = 64

	// h10MaxTreeCompLength is the maximum number of bytes compared per
	// tree node. Also used as the StoreLookahead for H10: positions can
	// only be inserted into the tree when at least this many bytes remain.
	h10MaxTreeCompLength = 128

	// h10MaxNumMatches is the maximum number of matches returned by
	// findAllMatches (64 short-range + 64 tree matches).
	h10MaxNumMatches = 128

	h10HashShift = 32 - h10BucketBits // 15
)

// h10 is the H10 binary tree hasher.
//
// Each hash bucket is the root of a binary search tree keyed by the
// lexicographic order of the byte sequences at stored positions. The tree
// is also a max-heap on position: every node's position is greater than its
// children's, so a single root-to-leaf traversal both searches for matches
// and re-roots the tree at the current position.
//
// The forest stores left/right child pointers for every position in the
// sliding window: forest[2*(pos & windowMask)] is the left child,
// forest[2*(pos & windowMask)+1] is the right child.
type h10 struct {
	bufs    *q10Bufs // reusable scratch buffers for Zopfli DP
	forest  []uint32 // length = 2 * window size
	lgwin   int
	quality int
	hasherCommon
	windowMask uint32
	invalidPos uint32
	buckets    [h10BucketSize]uint32
}

func (h *h10) common() *hasherCommon {
	return &h.hasherCommon
}

// reset initializes the hasher for a new compression session.
// All bucket roots are set to invalidPos (sentinel for empty tree).
// The forest is allocated once and reused across metablocks.
func (h *h10) reset(oneShot bool, inputSize uint, _ []byte) {
	lgwin := h.lgwin
	h.windowMask = (1 << lgwin) - 1
	h.invalidPos = 0 - h.windowMask

	numNodes := uint(1) << lgwin
	if oneShot && inputSize < numNodes {
		numNodes = inputSize
	}
	if len(h.forest) < int(2*numNodes) {
		h.forest = make([]uint32, 2*numNodes)
	}

	for i := range h.buckets {
		h.buckets[i] = h.invalidPos
	}
	h.ready = true
}

// storeAndFindMatches is the core operation of the binary tree hasher.
// In a single tree traversal it simultaneously:
//  1. Searches for matches longer than *bestLen
//  2. Re-roots the binary tree at curIx
//  3. Appends found matches to the matches slice
//
// When maxLength < h10MaxTreeCompLength, the tree is searched but not
// modified because the incomplete sequence cannot be correctly ordered.
//
// Returns the number of matches written to matches.
func (h *h10) storeAndFindMatches(
	data []byte, curIx, ringBufferMask, maxLength, maxBackward uint,
	bestLen *uint, matches []backwardMatch,
) int {
	curIxMasked := curIx & ringBufferMask
	maxCompLen := min(maxLength, h10MaxTreeCompLength)
	shouldReroot := maxLength >= h10MaxTreeCompLength

	key := h.hash(data, curIxMasked)
	prevIx := uint(h.buckets[key])

	// nodeLeft/nodeRight track where to attach subtrees as the tree is
	// re-rooted. They are forest indices, not positions.
	nodeLeft := h.leftChild(curIx)
	nodeRight := h.rightChild(curIx)

	// bestLenLeft/bestLenRight are the known match lengths of the
	// boundary nodes of the left and right subtrees being built.
	var bestLenLeft, bestLenRight uint

	if shouldReroot {
		h.buckets[key] = uint32(curIx)
	}

	nMatches := 0

	for depth := h10MaxTreeSearchDepth; ; depth-- {
		backward := curIx - prevIx
		prevIxMasked := prevIx & ringBufferMask

		if backward == 0 || backward > maxBackward || depth == 0 {
			if shouldReroot {
				h.forest[nodeLeft] = h.invalidPos
				h.forest[nodeRight] = h.invalidPos
			}
			break
		}

		curLen := min(bestLenLeft, bestLenRight)
		length := curLen + uint(matchLenAt(
			data,
			curIxMasked+curLen,
			prevIxMasked+curLen,
			int(maxLength-curLen),
		))

		if matches != nil && length > *bestLen {
			*bestLen = length
			matches[nMatches] = newBackwardMatch(backward, length)
			nMatches++
		}

		if length >= maxCompLen {
			// Full match up to comparison limit: steal the old node's children.
			if shouldReroot {
				h.forest[nodeLeft] = h.forest[h.leftChild(prevIx)]
				h.forest[nodeRight] = h.forest[h.rightChild(prevIx)]
			}
			break
		}

		// Lexicographic comparison determines left vs right subtree placement.
		if data[curIxMasked+length] > data[prevIxMasked+length] {
			bestLenLeft = length
			if shouldReroot {
				h.forest[nodeLeft] = uint32(prevIx)
			}
			nodeLeft = h.rightChild(prevIx)
			prevIx = uint(h.forest[nodeLeft])
		} else {
			bestLenRight = length
			if shouldReroot {
				h.forest[nodeRight] = uint32(prevIx)
			}
			nodeRight = h.leftChild(prevIx)
			prevIx = uint(h.forest[nodeRight])
		}
	}

	return nMatches
}

// findAllMatches finds all backward matches at curIx and stores curIx in the
// hash table. Matches are sorted by strictly increasing length and
// non-strictly increasing distance.
//
// The search proceeds in three phases:
//  1. Short-match scan: linear brute-force backward search for 2-byte prefix
//     matches (up to 16 positions for Q10, 64 for Q11).
//  2. Tree search: calls storeAndFindMatches for longer matches via the
//     binary search tree.
//  3. Static dictionary: searches the RFC 7932 static dictionary for matches
//     longer than the best LZ77 match.
//
// The matches slice must have capacity for at least h10MaxNumMatches entries.
// Returns the number of matches found.
func (h *h10) findAllMatches(
	data []byte, ringBufferMask, curIx, maxLength, maxBackward, dictionaryDistance uint,
	quality int, matches []backwardMatch,
) uint {
	curIxMasked := curIx & ringBufferMask
	bestLen := uint(1)
	nMatches := 0

	// Phase 1: Short-match brute-force scan.
	// Quality 10 searches 16 positions back; quality 11 searches 64.
	shortMatchMaxBackward := uint(16)
	if quality == 11 {
		shortMatchMaxBackward = 64
	}

	stop := uint(0)
	if curIx > shortMatchMaxBackward {
		stop = curIx - shortMatchMaxBackward
	}

	for i := curIx - 1; i > stop && bestLen <= 2; i-- {
		backward := curIx - i
		if backward > maxBackward {
			break
		}
		prevIxMasked := i & ringBufferMask
		if data[curIxMasked] != data[prevIxMasked] ||
			data[curIxMasked+1] != data[prevIxMasked+1] {
			continue
		}
		length := uint(matchLenAt(data, prevIxMasked, curIxMasked, int(maxLength)))
		if length > bestLen {
			bestLen = length
			matches[nMatches] = newBackwardMatch(backward, length)
			nMatches++
		}
	}

	// Phase 2: Tree search for longer matches.
	if bestLen < maxLength {
		nMatches += h.storeAndFindMatches(
			data, curIx, ringBufferMask, maxLength, maxBackward,
			&bestLen, matches[nMatches:],
		)
	}

	// Phase 3: Static dictionary search.
	// Search the RFC 7932 static dictionary for matches at all lengths
	// longer than the best LZ77 match found so far. Each length's best
	// dictionary match is converted to a backwardMatch.
	var dictMatches [maxStaticDictMatchLen + 1]uint32
	for i := range dictMatches {
		dictMatches[i] = invalidMatch
	}
	minLen := max(uint(4), bestLen+1)
	if findAllStaticDictionaryMatches(data[curIxMasked:], minLen, maxLength, dictMatches[:]) {
		maxLen := min(uint(maxStaticDictMatchLen), maxLength)
		for l := minLen; l <= maxLen; l++ {
			dictID := dictMatches[l]
			if dictID < invalidMatch {
				distance := dictionaryDistance + uint(dictID>>5) + 1
				if distance <= maxBackwardDistance {
					matches[nMatches] = newDictionaryBackwardMatch(distance, l, uint(dictID&31))
					nMatches++
				}
			}
		}
	}

	return uint(nMatches)
}

// store records position ix in the binary tree without returning matches.
// Requires that at least h10MaxTreeCompLength bytes are available at ix.
func (h *h10) store(data []byte, mask, ix uint) {
	// Maximum distance is window size - 16 (RFC 7932 Section 9.1).
	maxBackward := uint(h.windowMask) - windowGap + 1
	h.storeAndFindMatches(data, ix, mask, h10MaxTreeCompLength, maxBackward, nil, nil)
}

// storeRange stores positions ixStart..ixEnd-1 in the binary tree.
// For large ranges, a sparse prefix (every 8th position) is stored first,
// followed by a dense tail of the last 63 positions.
func (h *h10) storeRange(data []byte, mask, ixStart, ixEnd uint) {
	i := ixStart
	j := ixStart

	// Dense tail: always store the last 63 positions.
	if ixStart+63 <= ixEnd {
		i = ixEnd - 63
	}
	// Sparse prefix: store every 8th position if the range is large enough.
	if ixStart+512 <= i {
		for ; j < i; j += 8 {
			h.store(data, mask, j)
		}
	}
	// Dense tail.
	for ; i < ixEnd; i++ {
		h.store(data, mask, i)
	}
}

// stitchToPreviousBlock stores positions from the end of the previous block
// that could not be stored earlier because they required data from the
// current block (the sequence at those positions spans the block boundary).
func (h *h10) stitchToPreviousBlock(numBytes, position uint, ringBuffer []byte, ringBufferMask uint) {
	// Need at least 3 bytes (hashTypeLength - 1 = 4 - 1) and the position
	// must be past the initial StoreLookahead region.
	if numBytes < 3 || position < h10MaxTreeCompLength {
		return
	}

	iStart := position - h10MaxTreeCompLength + 1
	iEnd := min(position, iStart+numBytes)

	for i := iStart; i < iEnd; i++ {
		// Maximum distance is window size - 16 (RFC 7932 Section 9.1).
		// Also ensure we don't look further back than the start of the
		// current block to avoid reading overwritten ring buffer data.
		maxBackward := uint(h.windowMask) - max(windowGap-1, position-i)
		h.storeAndFindMatches(ringBuffer, i, ringBufferMask,
			h10MaxTreeCompLength, maxBackward, nil, nil)
	}
}

// createBackwardReferences runs the Zopfli optimal parsing algorithm to
// find backward references for the given input range.
//
// This bridges the streamHasher interface to the Zopfli DP entry point,
// converting the encodeState's [4]uint distance cache to the []int form
// that the Zopfli functions use.
func (h *h10) createBackwardReferences(s *encodeState, bytes, wrappedPos uint32) {
	distCache := [4]int{int(s.distCache[0]), int(s.distCache[1]), int(s.distCache[2]), int(s.distCache[3])}

	origCmdCount := len(s.commands)
	gap := s.compound.totalSize
	if s.quality == 11 {
		createHqZopfliBackwardReferences(uint(bytes), uint(wrappedPos), s.data, uint(s.mask),
			s.quality, s.lgwin, gap, &s.compound, distCache[:], h, &s.lastInsertLen, &s.commands, &s.numLiterals, h.bufs)
	} else {
		createZopfliBackwardReferences(uint(bytes), uint(wrappedPos), s.data, uint(s.mask),
			s.quality, s.lgwin, gap, &s.compound, distCache[:], h, &s.lastInsertLen, &s.commands, &s.numLiterals, h.bufs)
	}

	s.distCache = [4]uint{uint(distCache[0]), uint(distCache[1]), uint(distCache[2]), uint(distCache[3])}
	s.numCommands += uint(len(s.commands) - origCmdCount)
}

// hash computes a 17-bit bucket index from 4 bytes at data[i:i+4].
func (h *h10) hash(data []byte, i uint) uint32 {
	return (loadU32LE(data, i) * hashMul32) >> h10HashShift
}

// leftChild returns the forest index of the left child for the given position.
func (h *h10) leftChild(pos uint) uint {
	return 2 * (pos & uint(h.windowMask))
}

// rightChild returns the forest index of the right child for the given position.
func (h *h10) rightChild(pos uint) uint {
	return 2*(pos&uint(h.windowMask)) + 1
}
