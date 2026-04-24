// H5 hasher for the streaming encoder (quality 5).
//
// H5 is a forgetful hash table that maps 4-byte sequences to positions,
// using a ring buffer of entries per bucket. Unlike the "quickly" hashers
// (H2-H4) that store one or a few positions per bucket, H5 stores up to
// blockSize positions in each bucket, allowing it to find longer matches
// at the cost of more memory and slightly more search time.
//
// The hash function uses a 32-bit multiply-and-shift (kHashMul32),
// hashing 4 bytes (not 5 like the "quickly" hashers).

package brrr

// H5 configuration constants for quality 5.
const (
	h5BucketBits = 14
	h5BucketSize = 1 << h5BucketBits // 16384
	h5BlockBits  = 4
	h5BlockSize  = 1 << h5BlockBits // 16
	h5BlockMask  = h5BlockSize - 1
	h5HashShift  = 32 - h5BucketBits // 18

	// h5HashTypeLength is the minimum number of bytes needed to compute
	// the hash and verify a match (StoreLookahead in C).
	h5HashTypeLength = 4

	// h5NumLastDistances is the number of distance cache entries to check.
	// For quality 5 (< 7), the C reference uses 4.
	h5NumLastDistances = 4
)

// h5 is the H5 hasher: a forgetful hash table where each bucket holds a ring
// buffer of up to h5BlockSize (16) positions.
type h5 struct {
	num        [h5BucketSize]uint16               // entry count per bucket
	buckets    [h5BucketSize * h5BlockSize]uint32 // position ring buffers
	nextBucket uint32                             // speculative load to warm cache
	hasherCommon
}

func (h *h5) common() *hasherCommon { return &h.hasherCommon }

// hash computes a 14-bit bucket index from 4 bytes at data[0:4].
// Uses the 32-bit hash multiplier kHashMul32 (0x1E35A7BD).
func (h *h5) hash(data []byte, i uint) uint32 {
	return (loadU32LE(data, i) * hashMul32) >> h5HashShift
}

// reset zeroes the entry counts before use.
// When oneShot is true and the input is small, only the touched buckets
// are cleared (partial prepare). Otherwise the full count array is zeroed.
func (h *h5) reset(oneShot bool, inputSize uint, data []byte) {
	partialPrepareThreshold := h5BucketSize >> 6
	if oneShot && inputSize <= uint(partialPrepareThreshold) {
		for i := range inputSize {
			key := h.hash(data, i)
			h.num[key] = 0
		}
	} else {
		h.num = [h5BucketSize]uint16{}
	}
	h.ready = true
}

// store records position pos in the ring buffer for the 4-byte sequence at
// data[pos & mask].
func (h *h5) store(data []byte, mask, pos uint) {
	key := h.hash(data, pos&mask)
	minorIx := h.num[key] & h5BlockMask
	offset := uint(minorIx) + uint(key)<<h5BlockBits
	h.num[key]++
	h.buckets[offset] = uint32(pos)
}

// storeRange records positions [start, end) in the hash table.
func (h *h5) storeRange(data []byte, mask, start, end uint) {
	for i := start; i < end; i++ {
		h.store(data, mask, i)
	}
}

// stitchToPreviousBlock seeds the hash table with the last 3 positions of
// the previous block so that cross-block matches can be found.
func (h *h5) stitchToPreviousBlock(numBytes, position uint, ringBuffer []byte, ringBufferMask uint) {
	if numBytes >= h5HashTypeLength-1 && position >= 3 {
		h.store(ringBuffer, ringBufferMask, position-3)
		h.store(ringBuffer, ringBufferMask, position-2)
		h.store(ringBuffer, ringBufferMask, position-1)
	}
}

// findLongestMatch searches for the best backward reference at position cur
// in the ring buffer, then stores cur in the hash table.
//
// The search has three phases:
//  1. Distance cache: try the last 4 cached distances (and 6 derived
//     near-miss distances for the first two). Accept length >= 3, or
//     length == 2 for the first two cache entries.
//  2. Hash bucket scan: walk the ring buffer of up to 16 positions for the
//     bucket. Reject candidates with a 4-byte quick comparison, accept
//     length >= 4.
//  3. Static dictionary fallback: when neither phase produced a match,
//     search the static dictionary with shallow=false (deep search).
func (h *h5) findLongestMatch(
	data []byte, ringBufferMask uint,
	distCache *[4]uint,
	cur, maxLength, maxBackward, dictDistance uint,
	dictNumLookups, dictNumMatches *uint,
	out *hasherSearchResult,
) {
	// For small initial payloads the ring buffer backing array may be shorter
	// than ringBufferMask+1.  Delegate to the generic path that keeps all
	// runtime bounds checks so the compiler-proven fast path below stays
	// panic-free.
	if ringBufferMask >= uint(len(data)) {
		h.findLongestMatchSmallBuf(data, ringBufferMask, distCache,
			cur, maxLength, maxBackward, dictDistance,
			dictNumLookups, dictNumMatches, out)
		return
	}

	// --- fast path: ringBufferMask < len(data) ---
	// The compiler can now prove that every data[x & ringBufferMask] access
	// is in bounds, eliminating per-access bounds checks in phases 1–3.
	_ = data[ringBufferMask]

	curMasked := cur & ringBufferMask
	bestScore := out.score
	bestLen := out.len
	key := h.hash(data, curMasked)
	bucket := h.buckets[uint(key)<<h5BlockBits:]

	// Speculatively touch the next position's bucket. Loading through the
	// stored position into data was more expensive than the cache warmup it
	// provided for quality-5 varied payloads.
	nextKey := h.hash(data, (cur+1)&ringBufferMask)
	h.nextBucket = h.buckets[uint(nextKey)<<h5BlockBits]

	out.len = 0
	out.lenCodeDelta = 0

	// Phase 1: try cached distances.
	// In the fast path, the ring buffer has a mirrored tail of tailSize bytes
	// beyond ringBufferMask (see copyInputToRingBuffer). Since bestLen ≤
	// maxLength ≤ tailSize, loadByte accesses are always within len(data), so
	// the per-iteration wrap-around bounds guards are not needed here.
	// backward-1 >= maxBackward is a single check replacing both
	// "prev >= cur" (backward==0) and "backward > maxBackward".
	for i := range uint(h5NumLastDistances) {
		backward := distCache[i]
		if backward-1 >= maxBackward {
			continue
		}
		prev := (cur - backward) & ringBufferMask

		if loadByte(data, curMasked+bestLen) != loadByte(data, prev+bestLen) {
			continue
		}

		ml := uint(matchLenAtNoInline(data, prev, curMasked, int(maxLength)))
		if ml >= 3 || (ml == 2 && i < 2) {
			score := backwardReferenceScoreUsingLastDistance(ml)
			if bestScore < score {
				if i != 0 {
					score -= backwardReferencePenaltyUsingLastDistance(i)
				}
				if bestScore < score {
					bestScore = score
					bestLen = ml
					out.len = bestLen
					out.distance = backward
					out.score = bestScore
				}
			}
		}
	}

	// Raise bestLen floor to 3 so phase 2 only accepts length >= 4
	// (the 4-byte quick rejection compares bestLen-3 .. bestLen).
	if bestLen < 3 {
		bestLen = 3
	}

	// Phase 2: scan hash bucket entries.
	// Same tail guarantee: ring buffer end checks are omitted for the fast path.
	// backward == 0 is impossible here: we store cur after the loop, so all
	// bucket entries refer to strictly earlier positions.
	n := h.num[key]
	down := uint(0)
	if uint(n) > h5BlockSize {
		down = uint(n) - h5BlockSize
	}
	curProbe := loadU32LE(data, curMasked+bestLen-3)
	for i := uint(n); i > down; {
		i--
		prev := uint(bucket[i&h5BlockMask])
		backward := cur - prev
		if backward > maxBackward {
			break
		}
		prev &= ringBufferMask
		if curProbe != loadU32LE(data, prev+bestLen-3) {
			continue
		}

		ml := uint(matchLenAtNoInline(data, prev, curMasked, int(maxLength)))
		if ml >= 4 {
			score := backwardReferenceScore(ml, backward)
			if bestScore < score {
				bestScore = score
				bestLen = ml
				out.len = bestLen
				out.distance = backward
				out.score = bestScore
				curProbe = loadU32LE(data, curMasked+bestLen-3)
			}
		}
	}

	// Store current position in the bucket.
	h.buckets[uint(h.num[key]&h5BlockMask)+uint(key)<<h5BlockBits] = uint32(cur)
	h.num[key]++

	// Phase 3: static dictionary fallback when no hash match was found.
	if bestScore == minScore {
		searchStaticDictionaryDeep(data[curMasked:], maxLength, dictDistance, maxBackwardDistance,
			dictNumLookups, dictNumMatches, out)
	}
}

// findLongestMatchSmallBuf is the generic version of findLongestMatch used
// when the ring buffer backing array is smaller than ringBufferMask+1 (i.e.
// the first small write hasn't triggered a full allocation yet). It keeps
// all runtime bounds checks and is only called for small initial payloads.
func (h *h5) findLongestMatchSmallBuf(
	data []byte, ringBufferMask uint,
	distCache *[4]uint,
	cur, maxLength, maxBackward, dictDistance uint,
	dictNumLookups, dictNumMatches *uint,
	out *hasherSearchResult,
) {
	curMasked := cur & ringBufferMask
	bestScore := out.score
	bestLen := out.len
	key := h.hash(data, curMasked)
	bucket := h.buckets[uint(key)<<h5BlockBits:]

	nextKey := h.hash(data, (cur+1)&ringBufferMask)
	h.nextBucket = h.buckets[uint(nextKey)<<h5BlockBits]

	out.len = 0
	out.lenCodeDelta = 0

	// backward-1 >= maxBackward fuses both "backward == 0" (wraps to MAX_UINT)
	// and "backward > maxBackward" into a single compare. Since all callers
	// guarantee maxBackward <= cur, this also subsumes the "backward > cur"
	// (prev-underflow) check that the explicit version needed. Matches the
	// fast-path Phase 1 simplification applied in the fast-path function.
	for i := range uint(h5NumLastDistances) {
		backward := distCache[i]
		if backward-1 >= maxBackward {
			continue
		}
		prev := (cur - backward) & ringBufferMask

		if curMasked+bestLen > ringBufferMask {
			break
		}
		if prev+bestLen > ringBufferMask ||
			data[curMasked+bestLen] != data[prev+bestLen] {
			continue
		}

		ml := uint(matchLenAtNoInline(data, prev, curMasked, int(maxLength)))
		if ml >= 3 || (ml == 2 && i < 2) {
			score := backwardReferenceScoreUsingLastDistance(ml)
			if bestScore < score {
				if i != 0 {
					score -= backwardReferencePenaltyUsingLastDistance(i)
				}
				if bestScore < score {
					bestScore = score
					bestLen = ml
					out.len = bestLen
					out.distance = backward
					out.score = bestScore
				}
			}
		}
	}

	if bestLen < 3 {
		bestLen = 3
	}

	n := h.num[key]
	down := uint(0)
	if uint(n) > h5BlockSize {
		down = uint(n) - h5BlockSize
	}
	curProbe := loadU32LE(data, curMasked+bestLen-3)
	for i := uint(n); i > down; {
		i--
		prev := uint(bucket[i&h5BlockMask])
		backward := cur - prev
		if backward == 0 || backward > maxBackward {
			break
		}
		prev &= ringBufferMask
		if curMasked+bestLen > ringBufferMask {
			break
		}
		if prev+bestLen > ringBufferMask ||
			curProbe != loadU32LE(data, prev+bestLen-3) {
			continue
		}

		ml := uint(matchLenAtNoInline(data, prev, curMasked, int(maxLength)))
		if ml >= 4 {
			score := backwardReferenceScore(ml, backward)
			if bestScore < score {
				bestScore = score
				bestLen = ml
				out.len = bestLen
				out.distance = backward
				out.score = bestScore
				curProbe = loadU32LE(data, curMasked+bestLen-3)
			}
		}
	}

	h.buckets[uint(h.num[key]&h5BlockMask)+uint(key)<<h5BlockBits] = uint32(cur)
	h.num[key]++

	if bestScore == minScore {
		searchStaticDictionaryDeep(data[curMasked:], maxLength, dictDistance, maxBackwardDistance,
			dictNumLookups, dictNumMatches, out)
	}
}

// createBackwardReferences finds backward reference matches using this hasher
// and populates s.commands. The hot findLongestMatch/store/storeRange calls
// are direct (non-virtual) since the receiver is concrete.
func (h *h5) createBackwardReferences(s *encodeState, bytes, wrappedPos uint32) {
	data := s.data
	mask := uint(s.mask)
	maxBackwardLimit := (uint(1) << s.lgwin) - windowGap
	gap := s.compound.totalSize
	hasCompound := s.compound.numChunks > 0

	insertLength := s.lastInsertLen
	position := uint(wrappedPos)
	posEnd := position + uint(bytes)

	storeEnd := position
	if uint(bytes) >= h5HashTypeLength {
		storeEnd = posEnd - h5HashTypeLength + 1
	}

	const randomHeuristicsWindowSize = 64
	applyRandomHeuristics := position + randomHeuristicsWindowSize

	origCmdCount := uint(len(s.commands))

	distCache := &s.distCache

	for position+h5HashTypeLength < posEnd {
		maxLength := posEnd - position
		maxDistance := min(position, maxBackwardLimit)

		var sr hasherSearchResult
		sr.score = minScore

		h.findLongestMatch(data, mask, distCache,
			position, maxLength, maxDistance, maxDistance+gap,
			&s.dictNumLookups, &s.dictNumMatches, &sr)
		if hasCompound {
			s.compound.lookupMatch(data, mask,
				&s.distCache, position, maxLength,
				maxDistance, &sr)
		}

		if sr.score > minScore {
			delayedBackwardReferencesInRow := 0
			maxLength--
			for {
				const costDiffLazy = 175
				var sr2 hasherSearchResult
				// Quality >= 5: extensive lazy search (sr2.len starts at 0).
				sr2.score = minScore
				maxDistance = min(position+1, maxBackwardLimit)

				h.findLongestMatch(data, mask, distCache,
					position+1, maxLength, maxDistance, maxDistance+gap,
					&s.dictNumLookups, &s.dictNumMatches, &sr2)
				if hasCompound {
					s.compound.lookupMatch(data, mask,
						&s.distCache, position+1, maxLength,
						maxDistance, &sr2)
				}

				if sr2.score >= sr.score+costDiffLazy {
					position++
					insertLength++
					sr = sr2
					delayedBackwardReferencesInRow++
					if delayedBackwardReferencesInRow < 4 &&
						position+h5HashTypeLength < posEnd {
						maxLength--
						continue
					}
				}
				break
			}

			applyRandomHeuristics = position + 2*sr.len + randomHeuristicsWindowSize

			// Recompute maxDistance after the lazy loop because position may
			// have advanced. This matches the C reference's dictionary_start.
			maxDistance = min(position, maxBackwardLimit)
			distanceCode := computeDistanceCode(sr.distance, maxDistance+gap, &s.distCache)
			if sr.distance <= maxDistance+gap && distanceCode > 0 {
				s.distCache[3] = s.distCache[2]
				s.distCache[2] = s.distCache[1]
				s.distCache[1] = s.distCache[0]
				s.distCache[0] = sr.distance
			}

			s.commands = append(s.commands, newCommandSimpleDist(
				insertLength, sr.len, sr.lenCodeDelta, distanceCode,
			))
			s.numLiterals += insertLength
			insertLength = 0

			rangeStart := position + 2
			rangeEnd := min(position+sr.len, storeEnd)
			if sr.distance < sr.len>>2 {
				rangeStart = min(rangeEnd, max(rangeStart, position+sr.len-(sr.distance<<2)))
			}

			// Pre-warm the hash bucket for the next findLongestMatch call at
			// position+sr.len. The speculative load inside findLongestMatch only
			// covers pos+1; after a match the position jumps by sr.len, leaving
			// that bucket cold (potential L3 miss). Issuing the load here lets
			// the storeRange loop below (~10–80 cycles) hide the latency.
			nextMatchPos := position + sr.len
			if nextMatchPos+h5HashTypeLength < posEnd {
				wk := h.hash(data, nextMatchPos&mask)
				h.nextBucket = h.buckets[uint(wk)<<h5BlockBits]
			}

			h.storeRange(data, mask, rangeStart, rangeEnd)

			position += sr.len
		} else {
			insertLength++
			position++

			if position > applyRandomHeuristics {
				if position > applyRandomHeuristics+4*randomHeuristicsWindowSize {
					posJump := min(position+16, posEnd-max(h5HashTypeLength-1, 4))
					for position < posJump {
						h.store(data, mask, position)
						insertLength += 4
						position += 4
					}
				} else {
					posJump := min(position+8, posEnd-(h5HashTypeLength-1))
					for position < posJump {
						h.store(data, mask, position)
						insertLength += 2
						position += 2
					}
				}
			}
		}
	}

	insertLength += posEnd - position
	s.lastInsertLen = insertLength
	s.numCommands += uint(len(s.commands)) - origCmdCount
}

// prepareDistanceCache expands the 4-entry distance cache into additional
// derived entries. For each of the first two cached distances, six near-miss
// variants (d-1, d+1, d-2, d+2, d-3, d+3) are computed:
//
//   - Entries 4–9:  derived from distCache[0]
//   - Entries 10–15: derived from distCache[1]
//
// Hashers with numLastDistances <= 10 only use entries 0–9; quality 9 hashers
// use all 16 entries.
func prepareDistanceCache(distCache []int) {
	last := distCache[0]
	distCache[4] = last - 1
	distCache[5] = last + 1
	distCache[6] = last - 2
	distCache[7] = last + 2
	distCache[8] = last - 3
	distCache[9] = last + 3
	if len(distCache) > 10 {
		nextLast := distCache[1]
		distCache[10] = nextLast - 1
		distCache[11] = nextLast + 1
		distCache[12] = nextLast - 2
		distCache[13] = nextLast + 2
		distCache[14] = nextLast - 3
		distCache[15] = nextLast + 3
	}
}

// backwardReferencePenaltyUsingLastDistance returns the score penalty for
// reusing a cached distance that is not cache[0]. The penalty increases
// for less-recently-used cache entries.
func backwardReferencePenaltyUsingLastDistance(distanceShortCode uint) uint {
	return 39 + ((0x1CA10 >> (distanceShortCode & 0xE)) & 0xE)
}
