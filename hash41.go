// H41 forgetful chain hasher for quality 7/8 with small windows (lgwin <= 16).
//
// H41 shares the same chain structure as H40 (linked-list chains with banked
// slot storage, tiny hash for quick distance cache rejection), but expands the
// distance cache search from 4 to 10 entries and allows deeper chain traversal.

package brrr

import "unsafe"

// H41 configuration constants.
const (
	h41BucketBits = 15
	h41BucketSize = 1 << h41BucketBits // 32768
	h41BankBits   = 16
	h41BankSize   = 1 << h41BankBits // 65536
	h41NumBanks   = 1
	h41HashShift  = 32 - h41BucketBits // 17

	// h41NumLastDistances is the number of distance cache entries to check.
	// For quality 7–8, the C reference uses 10 (the 4 base entries plus 6
	// derived near-miss entries for dist[0]).
	h41NumLastDistances = 10

	// h41HashTypeLength is the minimum number of bytes needed to compute
	// the hash and verify a match (StoreLookahead in C).
	h41HashTypeLength = 4
)

// h41 is the H41 forgetful chain hasher. Each bucket maps to a linked list
// of slots stored in a single bank of 64K entries.
type h41 struct {
	maxHops     uint                  // Q7=56, Q8=112
	addr        [h41BucketSize]uint32 // position at bucket head
	head        [h41BucketSize]uint16 // index of head slot in bank
	tinyHash    [65536]uint8          // quick rejection for distance cache
	slots       [h41BankSize]h40Slot  // 1 bank × 64K slots (reuses h40Slot type)
	freeSlotIdx uint16                // monotonically increasing, wraps
	hasherCommon
}

func (h *h41) common() *hasherCommon { return &h.hasherCommon }

// hash computes a 15-bit bucket index from 4 bytes at data[i:i+4].
func (h *h41) hash(data []byte, i uint) uint32 {
	return (loadU32LE(data, i) * hashMul32) >> h41HashShift
}

// reset prepares the hasher for use. Fills addr with 0xCCCCCCCC (sentinel),
// zeroes head, tinyHash, and freeSlotIdx.
func (h *h41) reset(oneShot bool, inputSize uint, data []byte) {
	partialPrepareThreshold := h41BucketSize >> 3
	if oneShot && inputSize <= uint(partialPrepareThreshold) {
		for i := range inputSize {
			bucket := h.hash(data, i)
			h.addr[bucket] = 0xCCCCCCCC
			h.head[bucket] = 0xCCCC
		}
	} else {
		for i := range h.addr {
			h.addr[i] = 0xCCCCCCCC
		}
		h.head = [h41BucketSize]uint16{}
	}
	h.tinyHash = [65536]uint8{}
	h.freeSlotIdx = 0
	h.ready = true
}

// store records position ix in the chain for the 4-byte sequence at
// data[ix & mask].
func (h *h41) store(data []byte, mask, ix uint) {
	key := h.hash(data, ix&mask)
	bank := key & (h41NumBanks - 1) // always 0 for NUM_BANKS=1
	idx := h.freeSlotIdx & (h41BankSize - 1)
	h.freeSlotIdx++
	delta := ix - uint(h.addr[key])
	h.tinyHash[uint16(ix)] = uint8(key)
	if delta > 0xFFFF {
		delta = 0xFFFF
	}
	slotBase := uint(bank) * h41BankSize
	h.slots[slotBase+uint(idx)].delta = uint16(delta)
	h.slots[slotBase+uint(idx)].next = h.head[key]
	h.addr[key] = uint32(ix)
	h.head[key] = idx
}

// storeRange records positions [start, end) in the hash table.
func (h *h41) storeRange(data []byte, mask, start, end uint) {
	for i := start; i < end; i++ {
		h.store(data, mask, i)
	}
}

// stitchToPreviousBlock seeds the hash table with the last 3 positions of
// the previous block so that cross-block matches can be found.
func (h *h41) stitchToPreviousBlock(numBytes, position uint, ringBuffer []byte, ringBufferMask uint) {
	if numBytes >= h41HashTypeLength-1 && position >= 3 {
		h.store(ringBuffer, ringBufferMask, position-3)
		h.store(ringBuffer, ringBufferMask, position-2)
		h.store(ringBuffer, ringBufferMask, position-1)
	}
}

// findLongestMatch searches for the best backward reference at position cur
// in the ring buffer, then stores cur in the hash table.
//
// The search has three phases:
//  1. Distance cache: try 10 entries (4 base + 6 derived near-miss), use
//     tinyHash for i>0 rejection, accept length >= 2 for all entries.
//  2. Chain walk: traverse slot chain up to maxHops, 4-byte quick reject,
//     accept length >= 4.
//  3. Static dictionary fallback.
func (h *h41) findLongestMatch(
	data []byte, ringBufferMask uint,
	distCache []int,
	cur, maxLength, maxBackward, dictDistance uint,
	dictNumLookups, dictNumMatches *uint,
	out *hasherSearchResult,
) {
	if ringBufferMask >= uint(len(data)) {
		h.findLongestMatchSmallBuf(data, ringBufferMask, distCache,
			cur, maxLength, maxBackward, dictDistance,
			dictNumLookups, dictNumMatches, out)
		return
	}

	// --- fast path: ringBufferMask < len(data) ---
	_ = data[ringBufferMask]

	curMasked := cur & ringBufferMask
	minScore := out.score
	bestScore := out.score
	bestLen := out.len
	key := h.hash(data, curMasked)
	tinyHash := uint8(key)
	out.len = 0
	out.lenCodeDelta = 0

	// Phase 1: try cached distances.
	for i := range uint(h41NumLastDistances) {
		backward := uint(distCache[i])
		prevIx := cur - backward
		if i > 0 && h.tinyHash[uint16(prevIx)] != tinyHash {
			continue
		}
		if prevIx >= cur || backward > maxBackward {
			continue
		}
		prevIx &= ringBufferMask

		if loadByte(data, prevIx) != loadByte(data, curMasked) ||
			loadByte(data, prevIx+1) != loadByte(data, curMasked+1) {
			continue
		}
		ml := uint(matchLenAtNoInline(data, prevIx, curMasked, int(maxLength)))
		if ml >= 2 {
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

	// Raise bestLen floor to 3 so phase 2 only accepts length >= 4.
	if bestLen < 3 {
		bestLen = 3
	}

	// Phase 2: walk the chain.
	//
	// h41NumBanks == 1, so bank is always 0 and slotBase is always 0.
	// A single 32-bit unsafe load reads both delta (bits 0–15) and next
	// (bits 16–31) in one instruction, replacing the two MOVWLZX that the
	// compiler emits for the individual h40Slot fields.
	{
		backward := uint(0)
		hops := h.maxHops
		delta := cur - uint(h.addr[key])
		slot := h.head[key]
		for hops > 0 {
			hops--
			backward += delta
			if backward > maxBackward {
				break
			}
			prevIx := (cur - backward) & ringBufferMask
			slotEntry := *(*uint32)(unsafe.Pointer(&h.slots[slot]))
			nextDelta := uint(uint16(slotEntry))
			nextSlot := uint16(slotEntry >> 16)
			slot = nextSlot
			delta = nextDelta
			if curMasked+bestLen > ringBufferMask ||
				prevIx+bestLen > ringBufferMask ||
				loadU32LE(data, curMasked+bestLen-3) != loadU32LE(data, prevIx+bestLen-3) {
				continue
			}

			ml := uint(matchLenAtNoInline(data, prevIx, curMasked, int(maxLength)))
			if ml >= 4 {
				score := backwardReferenceScore(ml, backward)
				if bestScore < score {
					bestScore = score
					bestLen = ml
					out.len = bestLen
					out.distance = backward
					out.score = bestScore
				}
			}
		}
		h.store(data, ringBufferMask, cur)
	}

	// Phase 3: static dictionary fallback when no match was found.
	if out.score == minScore {
		searchStaticDictionaryDeep(data[curMasked:], maxLength, dictDistance, maxBackwardDistance,
			dictNumLookups, dictNumMatches, out)
	}
}

// findLongestMatchSmallBuf is the generic version of findLongestMatch used
// when the ring buffer backing array is smaller than ringBufferMask+1 (i.e.
// the first small write hasn't triggered a full allocation yet). It keeps
// all runtime bounds checks and is only called for small initial payloads.
func (h *h41) findLongestMatchSmallBuf(
	data []byte, ringBufferMask uint,
	distCache []int,
	cur, maxLength, maxBackward, dictDistance uint,
	dictNumLookups, dictNumMatches *uint,
	out *hasherSearchResult,
) {
	curMasked := cur & ringBufferMask
	minScore := out.score
	bestScore := out.score
	bestLen := out.len
	key := h.hash(data, curMasked)
	tinyHash := uint8(key)
	out.len = 0
	out.lenCodeDelta = 0

	// Phase 1: try cached distances.
	for i := range uint(h41NumLastDistances) {
		backward := uint(distCache[i])
		prevIx := cur - backward
		if i > 0 && h.tinyHash[uint16(prevIx)] != tinyHash {
			continue
		}
		if prevIx >= cur || backward > maxBackward {
			continue
		}
		prevIx &= ringBufferMask

		ml := uint(matchLenAtNoInline(data, prevIx, curMasked, int(maxLength)))
		if ml >= 2 {
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

	// Raise bestLen floor to 3 so phase 2 only accepts length >= 4.
	if bestLen < 3 {
		bestLen = 3
	}

	// Phase 2: walk the chain.
	{
		bank := key & (h41NumBanks - 1)
		backward := uint(0)
		hops := h.maxHops
		delta := cur - uint(h.addr[key])
		slot := h.head[key]
		slotBase := uint(bank) * h41BankSize
		for hops > 0 {
			hops--
			backward += delta
			if backward > maxBackward {
				break
			}
			prevIx := (cur - backward) & ringBufferMask
			nextSlot := h.slots[slotBase+uint(slot)].next
			nextDelta := uint(h.slots[slotBase+uint(slot)].delta)
			slot = nextSlot
			delta = nextDelta
			if curMasked+bestLen > ringBufferMask ||
				prevIx+bestLen > ringBufferMask ||
				loadU32LE(data, curMasked+bestLen-3) != loadU32LE(data, prevIx+bestLen-3) {
				continue
			}

			ml := uint(matchLenAtNoInline(data, prevIx, curMasked, int(maxLength)))
			if ml >= 4 {
				score := backwardReferenceScore(ml, backward)
				if bestScore < score {
					bestScore = score
					bestLen = ml
					out.len = bestLen
					out.distance = backward
					out.score = bestScore
				}
			}
		}
		h.store(data, ringBufferMask, cur)
	}

	// Phase 3: static dictionary fallback when no match was found.
	if out.score == minScore {
		searchStaticDictionaryDeep(data[curMasked:], maxLength, dictDistance, maxBackwardDistance,
			dictNumLookups, dictNumMatches, out)
	}
}

// createBackwardReferences finds backward reference matches using this hasher
// and populates s.commands. The hot findLongestMatch/store/storeRange calls
// are direct (non-virtual) since the receiver is concrete.
func (h *h41) createBackwardReferences(s *encodeState, bytes, wrappedPos uint32) {
	data := s.data
	mask := uint(s.mask)
	maxBackwardLimit := (uint(1) << s.lgwin) - windowGap
	gap := s.compound.totalSize
	hasCompound := s.compound.numChunks > 0

	insertLength := s.lastInsertLen
	position := uint(wrappedPos)
	posEnd := position + uint(bytes)

	storeEnd := position
	if uint(bytes) >= h41HashTypeLength {
		storeEnd = posEnd - h41HashTypeLength + 1
	}

	const randomHeuristicsWindowSize = 64
	applyRandomHeuristics := position + randomHeuristicsWindowSize

	origCmdCount := uint(len(s.commands))

	// Expand the 4-entry distance cache to 10 derived entries.
	var distCache [16]int
	for i, d := range s.distCache {
		distCache[i] = int(d)
	}
	prepareDistanceCache(distCache[:])

	for position+h41HashTypeLength < posEnd {
		maxLength := posEnd - position
		maxDistance := min(position, maxBackwardLimit)

		var sr hasherSearchResult
		sr.score = minScore

		h.findLongestMatch(data, mask, distCache[:],
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
				sr2.score = minScore
				maxDistance = min(position+1, maxBackwardLimit)

				h.findLongestMatch(data, mask, distCache[:],
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
						position+h41HashTypeLength < posEnd {
						maxLength--
						continue
					}
				}
				break
			}

			applyRandomHeuristics = position + 2*sr.len + randomHeuristicsWindowSize

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
			h.storeRange(data, mask, rangeStart, rangeEnd)

			position += sr.len

			for i, d := range s.distCache {
				distCache[i] = int(d)
			}
			prepareDistanceCache(distCache[:])
		} else {
			insertLength++
			position++

			if position > applyRandomHeuristics {
				if position > applyRandomHeuristics+4*randomHeuristicsWindowSize {
					posJump := min(position+16, posEnd-max(h41HashTypeLength-1, 4))
					for position < posJump {
						h.store(data, mask, position)
						insertLength += 4
						position += 4
					}
				} else {
					posJump := min(position+8, posEnd-(h41HashTypeLength-1))
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
