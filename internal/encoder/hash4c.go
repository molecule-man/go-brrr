// H40 and H41 chain hashers for qualities 5 through 8 with lgwin <= 16.
//
// Each bucket heads a chain in one bank of 64K packed slots.
// A tiny hash rejects distance cache candidates.
// The type argument sets the cache depth as a compile-time constant.
// It has no methods, which prevents generic dictionary calls in hot paths.

package encoder

import (
	"unsafe"

	"github.com/molecule-man/go-brrr/internal/core"
)

const (
	h4cBucketBits = 15
	h4cBucketSize = 1 << h4cBucketBits // 32768
	h4cBankBits   = 16
	h4cBankSize   = 1 << h4cBankBits   // 65536
	h4cHashShift  = 32 - h4cBucketBits // 17

	// h4cHashTypeLength is the minimum number of bytes needed to compute
	// the hash and verify a match (StoreLookahead in C).
	h4cHashTypeLength = 4

	// Distance cache depths: four for qualities 5 and 6, and ten for qualities 7 and 8.
	h40NumLastDistances = 4
	h41NumLastDistances = 10

	// Partial reset limits affect speed only.
	// The partial path clears every bucket that the input can query.
	// A zero addr value stops traversal before code reads a stale head entry.
	h40PartialResetMax = h4cBucketSize >> 6
	h41PartialResetMax = h4cBucketSize >> 3
)

type h4cDistances interface {
	~[h40NumLastDistances]byte | ~[h41NumLastDistances]byte
}

// h4cPackedSlot stores a 16-bit delta and a 16-bit next-slot index.
type h4cPackedSlot uint32

// h4c uses D as the distance cache depth.
type h4c[D h4cDistances] struct {
	_               [0]D
	maxHops         uint                  // Q5=16, Q6=32, Q7=56, Q8=112
	partialResetMax uint                  // largest input for the partial reset
	addr            [h4cBucketSize]uint32 // position at bucket head
	head            [h4cBucketSize]uint16 // index of head slot in bank
	tinyHash        [65536]uint8          // quick rejection for distance cache
	slots           [h4cBankSize]h4cPackedSlot
	freeSlotIdx     uint16 // monotonically increasing, wraps
	hasherCommon
}

// h40 serves qualities 5 and 6.
type h40 = h4c[[h40NumLastDistances]byte]

// h41 serves qualities 7 and 8.
type h41 = h4c[[h41NumLastDistances]byte]

func newH40(maxHops uint) *h40 {
	return &h40{maxHops: maxHops, partialResetMax: h40PartialResetMax}
}

func newH41(maxHops uint) *h41 {
	return &h41{maxHops: maxHops, partialResetMax: h41PartialResetMax}
}

func (h *h4c[D]) common() *hasherCommon { return &h.hasherCommon }

func (h *h4c[D]) hash(data []byte, i uint) uint32 {
	return (loadU32LE(data, i) * hashMul32) >> h4cHashShift
}

// addr stores complemented positions, so zero decodes to the 0xFFFFFFFF sentinel.
// The sentinel lets a full reset use memclr.
func (h *h4c[D]) reset(oneShot bool, inputSize uint, data []byte) {
	if oneShot && inputSize <= h.partialResetMax {
		for i := range inputSize {
			bucket := h.hash(data, i)
			h.addr[bucket] = 0
		}
	} else {
		clear(h.addr[:])
		h.head = [h4cBucketSize]uint16{}
	}
	h.tinyHash = [65536]uint8{}
	h.freeSlotIdx = 0
	h.ready = true
}

// store adds ix to the chain. Complemented positions preserve the zero sentinel.
func (h *h4c[D]) store(data []byte, mask, ix uint) {
	key := h.hash(data, ix&mask)
	idx := h.freeSlotIdx
	h.freeSlotIdx++
	delta := ix - uint(^h.addr[key])
	h.tinyHash[uint16(ix)] = uint8(key)
	if delta > 0xFFFF {
		delta = 0xFFFF
	}
	h.slots[idx] = h4cPackedSlot(uint32(delta) | uint32(h.head[key])<<16)
	h.addr[key] = ^uint32(ix)
	h.head[key] = idx
}

// Keep this loop local to avoid a store call.
func (h *h4c[D]) storeRange(data []byte, mask, start, end uint) {
	for i := start; i < end; i++ {
		key := h.hash(data, i&mask)
		idx := h.freeSlotIdx
		h.freeSlotIdx++
		delta := i - uint(^h.addr[key])
		h.tinyHash[uint16(i)] = uint8(key)
		if delta > 0xFFFF {
			delta = 0xFFFF
		}
		h.slots[idx] = h4cPackedSlot(uint32(delta) | uint32(h.head[key])<<16)
		h.addr[key] = ^uint32(i)
		h.head[key] = idx
	}
}

// stitchToPreviousBlock seeds the hash table with the last 3 positions of
// the previous block so that cross-block matches can be found.
func (h *h4c[D]) stitchToPreviousBlock(numBytes, position uint, ringBuffer []byte, ringBufferMask uint) {
	if numBytes >= h4cHashTypeLength-1 && position >= 3 {
		h.store(ringBuffer, ringBufferMask, position-3)
		h.store(ringBuffer, ringBufferMask, position-2)
		h.store(ringBuffer, ringBufferMask, position-1)
	}
}

// findLongestMatch checks cached distances, the chain, and the static dictionary.
// It stores cur before the chain walk.
func (h *h4c[D]) findLongestMatch(
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

	// Check four distances directly. The loop for other distances compiles away for h40.
	// The first distance skips tinyHash because it is hot.
	{
		backward := uint(distCache[0])
		prevIx := cur - backward
		if prevIx < cur && backward <= maxBackward {
			prevIx &= ringBufferMask
			if loadByte(data, prevIx) == loadByte(data, curMasked) &&
				loadByte(data, prevIx+1) == loadByte(data, curMasked+1) {
				ml := uint(matchLenAtNoInline(data, prevIx, curMasked, int(maxLength)))
				if ml >= 2 {
					score := backwardReferenceScoreUsingLastDistance(ml)
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
	}

	{
		backward := uint(distCache[1])
		prevIx := cur - backward
		if h.tinyHash[uint16(prevIx)] == tinyHash {
			if prevIx < cur && backward <= maxBackward {
				prevIx &= ringBufferMask
				if loadByte(data, prevIx) == loadByte(data, curMasked) &&
					loadByte(data, prevIx+1) == loadByte(data, curMasked+1) {
					ml := uint(matchLenAtNoInline(data, prevIx, curMasked, int(maxLength)))
					if ml >= 2 {
						score := backwardReferenceScoreUsingLastDistance(ml)
						if bestScore < score {
							score -= backwardReferencePenaltyUsingLastDistance(1)
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
			}
		}
	}
	{
		backward := uint(distCache[2])
		prevIx := cur - backward
		if h.tinyHash[uint16(prevIx)] == tinyHash {
			if prevIx < cur && backward <= maxBackward {
				prevIx &= ringBufferMask
				if loadByte(data, prevIx) == loadByte(data, curMasked) &&
					loadByte(data, prevIx+1) == loadByte(data, curMasked+1) {
					ml := uint(matchLenAtNoInline(data, prevIx, curMasked, int(maxLength)))
					if ml >= 2 {
						score := backwardReferenceScoreUsingLastDistance(ml)
						if bestScore < score {
							score -= backwardReferencePenaltyUsingLastDistance(2)
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
			}
		}
	}
	{
		backward := uint(distCache[3])
		prevIx := cur - backward
		if h.tinyHash[uint16(prevIx)] == tinyHash {
			if prevIx < cur && backward <= maxBackward {
				prevIx &= ringBufferMask
				if loadByte(data, prevIx) == loadByte(data, curMasked) &&
					loadByte(data, prevIx+1) == loadByte(data, curMasked+1) {
					ml := uint(matchLenAtNoInline(data, prevIx, curMasked, int(maxLength)))
					if ml >= 2 {
						score := backwardReferenceScoreUsingLastDistance(ml)
						if bestScore < score {
							score -= backwardReferencePenaltyUsingLastDistance(3)
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
			}
		}
	}

	// unsafe.Sizeof does not evaluate its operand and yields a constant depth for each type.
	//nolint:govet // nilness: Sizeof does not evaluate its operand
	numLastDistances := uint(unsafe.Sizeof(*(*D)(nil)))
	for i := uint(h40NumLastDistances); i < numLastDistances; i++ {
		backward := uint(distCache[i])
		prevIx := cur - backward
		if h.tinyHash[uint16(prevIx)] != tinyHash {
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
				score -= backwardReferencePenaltyUsingLastDistance(i)
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
	// Store cur first so writes overlap serial slot reads.
	// oldHead keeps the prior chain reachable.
	{
		oldAddr := uint(^h.addr[key])
		oldHead := h.head[key]

		newIdx := h.freeSlotIdx
		h.freeSlotIdx++
		storeDelta := cur - oldAddr
		h.tinyHash[uint16(cur)] = uint8(key)
		if storeDelta > 0xFFFF {
			storeDelta = 0xFFFF
		}
		h.slots[newIdx] = h4cPackedSlot(uint32(storeDelta) | uint32(oldHead)<<16)
		h.addr[key] = ^uint32(cur)
		h.head[key] = newIdx

		backward := uint(0)
		hops := h.maxHops
		delta := cur - oldAddr
		slot := oldHead
		for hops > 0 {
			hops--
			backward += delta
			if backward > maxBackward {
				break
			}
			prevIx := (cur - backward) & ringBufferMask
			slotEntry := uint32(h.slots[slot])
			slot = uint16(slotEntry >> 16)
			delta = uint(uint16(slotEntry))
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
	}

	// Phase 3: static dictionary fallback when no match was found.
	if out.score == minScore {
		searchStaticDictionaryDeep(data[curMasked:], maxLength, dictDistance, maxBackwardDistance,
			dictNumLookups, dictNumMatches, out)
	}
}

// findLongestMatchSmallBuf handles initial arrays smaller than ringBufferMask+1.
// It retains runtime bounds checks.
func (h *h4c[D]) findLongestMatchSmallBuf(
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
	// unsafe.Sizeof does not evaluate its operand and yields a constant depth for each type.
	//nolint:govet // nilness: Sizeof does not evaluate its operand
	numLastDistances := uint(unsafe.Sizeof(*(*D)(nil)))
	for i := range numLastDistances {
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
		oldAddr := uint(^h.addr[key])
		oldHead := h.head[key]

		newIdx := h.freeSlotIdx
		h.freeSlotIdx++
		storeDelta := cur - oldAddr
		h.tinyHash[uint16(cur)] = uint8(key)
		if storeDelta > 0xFFFF {
			storeDelta = 0xFFFF
		}
		h.slots[newIdx] = h4cPackedSlot(uint32(storeDelta) | uint32(oldHead)<<16)
		h.addr[key] = ^uint32(cur)
		h.head[key] = newIdx

		backward := uint(0)
		hops := h.maxHops
		delta := cur - oldAddr
		slot := oldHead
		for hops > 0 {
			hops--
			backward += delta
			if backward > maxBackward {
				break
			}
			prevIx := (cur - backward) & ringBufferMask
			slotEntry := uint32(h.slots[slot])
			slot = uint16(slotEntry >> 16)
			delta = uint(uint16(slotEntry))
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
	}

	// Phase 3: static dictionary fallback when no match was found.
	if out.score == minScore {
		searchStaticDictionaryDeep(data[curMasked:], maxLength, dictDistance, maxBackwardDistance,
			dictNumLookups, dictNumMatches, out)
	}
}

// createBackwardReferences adds matches to s.commands.
func (h *h4c[D]) createBackwardReferences(s *encodeState, bytes, wrappedPos uint32) {
	data := s.data
	mask := uint(s.mask)
	maxBackwardLimit := (uint(1) << s.lgwin) - core.WindowGap
	gap := s.compound.totalSize
	hasCompound := s.compound.numChunks > 0

	insertLength := s.lastInsertLen
	position := uint(wrappedPos)
	posEnd := position + uint(bytes)

	storeEnd := position
	if uint(bytes) >= h4cHashTypeLength {
		storeEnd = posEnd - h4cHashTypeLength + 1
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

	for position+h4cHashTypeLength < posEnd {
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
						position+h4cHashTypeLength < posEnd {
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
					posJump := min(position+16, posEnd-max(h4cHashTypeLength-1, 4))
					for position < posJump {
						h.store(data, mask, position)
						insertLength += 4
						position += 4
					}
				} else {
					posJump := min(position+8, posEnd-(h4cHashTypeLength-1))
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
