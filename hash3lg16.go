// H3 variant for lgwin <= 16 that stores uint16 positions in buckets,
// halving the table from 256 KB to 128 KB.

package brrr

// h3lg16RefreshInterval bounds the maximum age of any stored bucket entry.
// Refreshing the table before any entry can be more than 65535 positions
// behind current keeps `uint16(position - prev)` exact, so the maxDistance
// check still rejects all out-of-range entries at register cost.
const h3lg16RefreshInterval = 65535

// h3lg16 is the H3 hasher with uint16 bucket slots. Valid backward distances
// for lgwin <= 16 fit in 16 bits, so the slot value is `uint16(pos)` and the
// backward distance is recovered modulo 2^16. To prevent stale entries from
// aliasing to small bogus backwards, the table is zeroed every
// h3lg16RefreshInterval positions; this preserves the maxDistance check's
// ability to reject out-of-range entries without a wasted matchLenAt probe.
type h3lg16 struct {
	buckets    [bucketSize]uint16
	nextBucket uint16 // speculative load to warm cache for the next match lookup
	// refreshDeadline is the position at which the bucket array is next
	// zeroed. Set to position+h3lg16RefreshInterval on first call after
	// reset; sentinel value 0 means "uninitialized."
	refreshDeadline uint
	hasherCommon
}

func (h *h3lg16) common() *hasherCommon { return &h.hasherCommon }

// reset clears or selectively zeroes the hash table before use.
func (h *h3lg16) reset(oneShot bool, inputSize uint, data []byte) {
	partialPrepareThreshold := bucketSize >> 5
	if oneShot && inputSize <= uint(partialPrepareThreshold) {
		for i := range inputSize {
			key := hashBytes(data, i)
			h.buckets[key] = 0
			h.buckets[(key+8)&bucketMask] = 0
		}
	} else {
		h.buckets = [bucketSize]uint16{}
	}
	h.refreshDeadline = 0
	h.ready = true
}

// store records position pos in the bucket for the 5-byte sequence at
// data[pos & mask]. Uses the sweep offset to distribute entries.
func (h *h3lg16) store(data []byte, mask, pos uint) {
	key := hashBytes(data, pos&mask)
	off := uint32(pos) & h3BucketSweepMsk
	h.buckets[(key+off)&bucketMask] = uint16(pos)
}

// stitchToPreviousBlock seeds the hash table with the last 3 positions of
// the previous block so that cross-block matches can be found.
func (h *h3lg16) stitchToPreviousBlock(numBytes, position uint, ringBuffer []byte, ringBufferMask uint) {
	if numBytes >= hashTypeLength-1 && position >= 3 {
		h.store(ringBuffer, ringBufferMask, position-3)
		h.store(ringBuffer, ringBufferMask, position-2)
		h.store(ringBuffer, ringBufferMask, position-1)
	}
}

// createBackwardReferences finds backward reference matches using this hasher
// and populates s.commands. The hot findLongestMatch/store calls are direct
// (non-virtual) since the receiver is concrete.
func (h *h3lg16) createBackwardReferences(s *encodeState, bytes, wrappedPos uint32) {
	data := s.data
	mask := uint(s.mask)
	maxBackwardLimit := (uint(1) << s.lgwin) - windowGap
	gap := s.compound.totalSize
	hasCompound := s.compound.numChunks > 0

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

	if h.refreshDeadline == 0 {
		h.refreshDeadline = position + h3lg16RefreshInterval
	}

	// Refresh fires at most once per call (max metablock <= ring-buffer size
	// <= refresh interval), so the inner loop runs at most twice — once up to
	// the refresh deadline, once to posEnd. This keeps the hot loop free of
	// any per-iteration refresh check.
	for {
		loopEnd := min(posEnd, h.refreshDeadline+hashTypeLength)

		for position+hashTypeLength < loopEnd {
			maxLength := posEnd - position
			maxDistance := min(position, maxBackwardLimit)

			var sr hasherSearchResult
			sr.len = 0
			sr.lenCodeDelta = 0
			sr.distance = 0
			sr.score = minScore

			{
				lastDistance := s.distCache[0]
				curMasked := position & mask
				curWord := loadU64LE(data, curMasked)
				guardByte := byte(curWord)
				key := uint32(((curWord << (64 - 8*hashLen)) * hashMul64) >> (64 - bucketBits))
				bestScore := sr.score
				bestLen := uint(0)

				hkey0 := key
				hkey1 := (key + 8) & bucketMask
				keyOut := hkey0
				if (position & h3BucketSweepMsk) != 0 {
					keyOut = hkey1
				}

				prev0 := uint(buckets[hkey0])
				prev1 := uint(buckets[hkey1])

				{
					prev := position - lastDistance
					if prev < position && lastDistance <= maxDistance {
						prev &= mask
						if guardByte == loadByte(data, prev+bestLen) {
							length := matchLenAt(data, prev, curMasked, int(maxLength))
							if length >= 4 {
								score := backwardReferenceScoreUsingLastDistance(uint(length))
								if bestScore < score {
									bestLen = uint(length)
									sr.len = bestLen
									sr.distance = lastDistance
									sr.score = score
									bestScore = score
									guardByte = loadByte(data, curMasked+bestLen)
								}
							}
						}
					}
				}

				{
					backward := uint(uint16(position - prev0))
					prev0 &= mask
					if guardByte == loadByte(data, prev0+bestLen) && backward != 0 && backward <= maxDistance {
						length := matchLenAt(data, prev0, curMasked, int(maxLength))
						if length >= 4 {
							score := backwardReferenceScore(uint(length), backward)
							if bestScore < score {
								bestLen = uint(length)
								sr.len = bestLen
								guardByte = loadByte(data, curMasked+bestLen)
								bestScore = score
								sr.score = score
								sr.distance = backward
							}
						}
					}
				}

				{
					backward := uint(uint16(position - prev1))
					prev1 &= mask
					if guardByte == loadByte(data, prev1+bestLen) && backward != 0 && backward <= maxDistance {
						length := matchLenAt(data, prev1, curMasked, int(maxLength))
						if length >= 4 {
							score := backwardReferenceScore(uint(length), backward)
							if bestScore < score {
								sr.len = uint(length)
								sr.score = score
								sr.distance = backward
							}
						}
					}
				}

				buckets[keyOut] = uint16(position)
			}

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
					sr2.len = min(sr.len-1, maxLength)
					sr2.lenCodeDelta = 0
					sr2.distance = 0
					sr2.score = minScore
					maxDistance = min(position+1, maxBackwardLimit)

					{
						cur2 := position + 1
						lastDistance := s.distCache[0]
						curMasked := cur2 & mask
						bestLen2 := sr2.len
						guardByte := loadByte(data, curMasked+bestLen2)
						key := hashBytes(data, curMasked)
						bestScore := sr2.score

						hkey0 := key
						hkey1 := (key + 8) & bucketMask
						keyOut := hkey0
						if (cur2 & h3BucketSweepMsk) != 0 {
							keyOut = hkey1
						}

						prev0 := uint(buckets[hkey0])
						prev1 := uint(buckets[hkey1])

						{
							prev := cur2 - lastDistance
							if prev < cur2 && lastDistance <= maxDistance {
								prev &= mask
								if guardByte == loadByte(data, prev+bestLen2) {
									length := matchLenAt(data, prev, curMasked, int(maxLength))
									if length >= 4 {
										score := backwardReferenceScoreUsingLastDistance(uint(length))
										if bestScore < score {
											bestLen2 = uint(length)
											sr2.len = bestLen2
											sr2.distance = lastDistance
											sr2.score = score
											bestScore = score
											guardByte = loadByte(data, curMasked+bestLen2)
										}
									}
								}
							}
						}

						{
							backward := uint(uint16(cur2 - prev0))
							prev0 &= mask
							if guardByte == loadByte(data, prev0+bestLen2) && backward != 0 && backward <= maxDistance {
								length := matchLenAt(data, prev0, curMasked, int(maxLength))
								if length >= 4 {
									score := backwardReferenceScore(uint(length), backward)
									if bestScore < score {
										bestLen2 = uint(length)
										sr2.len = bestLen2
										guardByte = loadByte(data, curMasked+bestLen2)
										bestScore = score
										sr2.score = score
										sr2.distance = backward
									}
								}
							}
						}

						{
							backward := uint(uint16(cur2 - prev1))
							prev1 &= mask
							if guardByte == loadByte(data, prev1+bestLen2) && backward != 0 && backward <= maxDistance {
								length := matchLenAt(data, prev1, curMasked, int(maxLength))
								if length >= 4 {
									score := backwardReferenceScore(uint(length), backward)
									if bestScore < score {
										sr2.len = uint(length)
										sr2.score = score
										sr2.distance = backward
									}
								}
							}
						}

						buckets[keyOut] = uint16(cur2)
					}

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

				{
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
				for i := rangeStart; i < rangeEnd; i++ {
					key := hashBytes(data, i&mask)
					off := uint32(i) & h3BucketSweepMsk
					buckets[(key+off)&bucketMask] = uint16(i)
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

		if position+hashTypeLength >= posEnd {
			break
		}

		// Hit the refresh deadline mid-call; zero the table and continue.
		h.buckets = [bucketSize]uint16{}
		h.refreshDeadline = position + h3lg16RefreshInterval
	}

	insertLength += posEnd - position
	s.lastInsertLen = insertLength
	s.numCommands += uint(len(s.commands)) - origCmdCount
}
