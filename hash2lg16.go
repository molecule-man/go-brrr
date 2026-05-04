// H2 variant for one-shot inputs with lgwin <= 16 and sizeHint <= 64 KiB.
// Stores uint16 positions in buckets, halving the table from 256 KB to 128 KB.
// The dispatch contract guarantees position never exceeds 2^16, so
// `uint16(pos)` is lossless and the maxDistance check rejects all out-of-range
// entries at register cost — no aliasing, no stale-probe overhead.

package brrr

// h2lg16 is the H2 hasher with uint16 bucket slots, dispatched only when the
// encoder knows the input fits in 64 KiB and uses lgwin <= 16. Under that
// contract every stored position is in [0, 65535], so `uint16(pos)` storage
// is lossless and `position - prev` (uint subtraction) is directly the real
// backward distance — no modular truncation needed. Semantics match h2 at
// half the memory; the lookup arithmetic is the same shape.
type h2lg16 struct {
	buckets    [bucketSize]uint16
	nextBucket uint16 // speculative load to warm cache for the next match lookup
	hasherCommon
}

func (h *h2lg16) common() *hasherCommon { return &h.hasherCommon }

// reset clears or selectively zeroes the hash table before use.
func (h *h2lg16) reset(oneShot bool, inputSize uint, data []byte) {
	partialPrepareThreshold := bucketSize >> 5
	if oneShot && inputSize <= uint(partialPrepareThreshold) {
		for i := range inputSize {
			key := hashBytes(data, i)
			h.buckets[key] = 0
		}
	} else {
		h.buckets = [bucketSize]uint16{}
	}
	h.ready = true
}

// store records position pos in the bucket for the 5-byte sequence at
// data[pos & mask].
func (h *h2lg16) store(data []byte, mask, pos uint) {
	key := hashBytes(data, pos&mask)
	h.buckets[key] = uint16(pos)
}

// storeRange records positions [start, end) in the hash table.
func (h *h2lg16) storeRange(data []byte, mask, start, end uint) {
	for i := start; i < end; i++ {
		h.store(data, mask, i)
	}
}

// stitchToPreviousBlock seeds the hash table with the last 3 positions of
// the previous block so that cross-block matches can be found.
func (h *h2lg16) stitchToPreviousBlock(numBytes, position uint, ringBuffer []byte, ringBufferMask uint) {
	if numBytes >= hashTypeLength-1 && position >= 3 {
		h.store(ringBuffer, ringBufferMask, position-3)
		h.store(ringBuffer, ringBufferMask, position-2)
		h.store(ringBuffer, ringBufferMask, position-1)
	}
}

// createBackwardReferences finds backward reference matches using this hasher
// and populates s.commands. Mirrors h2.createBackwardReferences with uint16
// bucket slots; positions in [0, 65535] make the storage lossless.
func (h *h2lg16) createBackwardReferences(s *encodeState, bytes, wrappedPos uint32) {
	data := s.data
	mask := uint(s.mask)
	maxBackwardLimit := (uint(1) << s.lgwin) - windowGap
	gap := s.compound.totalSize

	insertLength := s.lastInsertLen
	position := uint(wrappedPos)
	posEnd := position + uint(bytes)

	storeEnd := position
	if uint(bytes) >= hashTypeLength {
		storeEnd = posEnd - hashTypeLength + 1
	}

	const randomHeuristicsWindowSize = 64
	applyRandomHeuristics := position + randomHeuristicsWindowSize

	origCmdCount := uint(len(s.commands))
	buckets := &h.buckets
	const directStoreRangeMinBytes = 4096
	directStoreRange := bytes >= directStoreRangeMinBytes

	for position+hashTypeLength < posEnd {
		maxLength := posEnd - position
		maxDistance := min(position, maxBackwardLimit)

		var sr hasherSearchResult
		sr.len = 0
		sr.lenCodeDelta = 0
		sr.distance = 0
		sr.score = minScore

		var dictEligible bool
		{
			lastDistance := s.distCache[0]
			curMasked := position & mask
			guardByte := loadByte(data, curMasked)
			key := hashBytes(data, curMasked)
			bestScore := sr.score

			lastDistanceHit := false
			{
				prev := position - lastDistance
				if prev < position {
					prev &= mask
					if guardByte == loadByte(data, prev) {
						length := matchLenAt(data, prev, curMasked, int(maxLength))
						if length >= 4 {
							score := backwardReferenceScoreUsingLastDistance(uint(length))
							if bestScore < score {
								sr.len = uint(length)
								sr.distance = lastDistance
								sr.score = score
								buckets[key] = uint16(position)
								lastDistanceHit = true
							}
						}
					}
				}
			}

			if !lastDistanceHit {
				prev := uint(buckets[key])
				buckets[key] = uint16(position)
				backward := position - prev
				prev &= mask
				if guardByte == loadByte(data, prev) && backward != 0 && backward <= maxDistance {
					dictEligible = true
					length := matchLenAt(data, prev, curMasked, int(maxLength))
					if length >= 4 {
						score := backwardReferenceScore(uint(length), backward)
						if bestScore < score {
							sr.len = uint(length)
							sr.distance = backward
							sr.score = score
						}
					}
				}
			}
		}

		if dictEligible && sr.score == minScore {
			if m, ok := searchStaticDictionary(data[position&mask:], maxLength, maxDistance+gap, maxBackwardDistance,
				&s.dictNumLookups, &s.dictNumMatches, sr.score); ok {
				sr = m
			}
		}

		if sr.score > minScore {
			delayedBackwardReferencesInRow := 0
			maxLength--
			for {
				const costDiffLazy = 175
				var sr2 hasherSearchResult
				sr2.len = min(sr.len-1, maxLength)
				sr2.lenCodeDelta = 0
				sr2.distance = 0
				sr2.score = minScore
				maxDistance = min(position+1, maxBackwardLimit)

				var dictEligible2 bool
				{
					cur2 := position + 1
					lastDistance := s.distCache[0]
					curMasked := cur2 & mask
					bestLen := sr2.len
					guardByte := loadByte(data, curMasked+bestLen)
					key := hashBytes(data, curMasked)
					bestScore := sr2.score

					lastDistanceHit := false
					{
						prev := cur2 - lastDistance
						if prev < cur2 {
							prev &= mask
							if guardByte == loadByte(data, prev+bestLen) {
								length := matchLenAt(data, prev, curMasked, int(maxLength))
								if length >= 4 {
									score := backwardReferenceScoreUsingLastDistance(uint(length))
									if bestScore < score {
										sr2.len = uint(length)
										sr2.distance = lastDistance
										sr2.score = score
										buckets[key] = uint16(cur2)
										lastDistanceHit = true
									}
								}
							}
						}
					}

					if !lastDistanceHit {
						prev := uint(buckets[key])
						buckets[key] = uint16(cur2)
						backward := cur2 - prev
						prev &= mask
						if guardByte == loadByte(data, prev+bestLen) && backward != 0 && backward <= maxDistance {
							dictEligible2 = true
							length := matchLenAt(data, prev, curMasked, int(maxLength))
							if length >= 4 {
								score := backwardReferenceScore(uint(length), backward)
								if bestScore < score {
									sr2.len = uint(length)
									sr2.distance = backward
									sr2.score = score
								}
							}
						}
					}
				}

				if dictEligible2 && sr2.score == minScore {
					if m, ok := searchStaticDictionary(data[(position+1)&mask:], maxLength, maxDistance+gap, maxBackwardDistance,
						&s.dictNumLookups, &s.dictNumMatches, sr2.score); ok {
						sr2 = m
					}
				}

				if sr2.score >= sr.score+costDiffLazy {
					position++
					insertLength++
					sr = sr2
					delayedBackwardReferencesInRow++
					if delayedBackwardReferencesInRow < 4 &&
						position+hashTypeLength < posEnd {
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

			delta := uint32(uint8(int8(sr.lenCodeDelta)))
			distPrefix, distExtra := prefixEncodeSimpleDistance(distanceCode)
			effectiveCopyLen := uint(int(sr.len) + sr.lenCodeDelta)
			insCode := getInsertLenCode(insertLength)
			copyCode := getCopyLenCode(effectiveCopyLen)
			cmdPrefix := combineLengthCodes(insCode, copyCode, (distPrefix&0x3FF) == 0)
			s.commands = append(s.commands, command{
				insertLen:  uint32(insertLength),
				copyLen:    uint32(sr.len) | (delta << 25),
				distExtra:  distExtra,
				cmdPrefix:  cmdPrefix,
				distPrefix: distPrefix,
			})
			if s.cmdHisto != nil {
				s.cmdHisto[cmdPrefix]++
				if cmdPrefix >= 128 {
					s.distHisto[distPrefix&0x3FF]++
				}
				basePos := position - insertLength
				for j := uint(0); j < insertLength; j++ {
					s.litHisto[data[(basePos+j)&mask]]++
				}
			}
			s.numLiterals += insertLength
			insertLength = 0

			nextMatchPos := position + sr.len
			if nextMatchPos+hashTypeLength < posEnd {
				h.nextBucket = buckets[hashBytes(data, nextMatchPos&mask)]
			}

			rangeStart := position + 2
			rangeEnd := min(position+sr.len, storeEnd)
			if sr.distance < sr.len>>2 {
				rangeStart = min(rangeEnd, max(rangeStart, position+sr.len-(sr.distance<<2)))
			}
			if directStoreRange {
				for i := rangeStart; i < rangeEnd; i++ {
					key := hashBytes(data, i&mask)
					buckets[key] = uint16(i)
				}
			} else {
				h.storeRange(data, mask, rangeStart, rangeEnd)
			}

			position += sr.len
		} else {
			insertLength++
			position++

			if position > applyRandomHeuristics {
				if position > applyRandomHeuristics+4*randomHeuristicsWindowSize {
					posJump := min(position+16, posEnd-(hashTypeLength-1))
					for position < posJump {
						h.store(data, mask, position)
						insertLength += 4
						position += 4
					}
				} else {
					posJump := min(position+8, posEnd-(hashTypeLength-1))
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
