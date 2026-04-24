// Hash table for the streaming encoder (quality 2).
// BUCKET_BITS=16, BUCKET_SWEEP=1, HASH_LEN=5.

package brrr

// h2 is the H2 hasher: a forgetful hash table of fixed size that maps
// 5-byte sequences to positions. Each bucket stores a single uint32 position.
type h2 struct {
	buckets    [bucketSize]uint32
	nextBucket uint32 // speculative load to warm cache for the next match lookup
	hasherCommon
}

func (h *h2) common() *hasherCommon { return &h.hasherCommon }

// reset clears or selectively zeroes the hash table before use.
func (h *h2) reset(oneShot bool, inputSize uint, data []byte) {
	partialPrepareThreshold := bucketSize >> 5
	if oneShot && inputSize <= uint(partialPrepareThreshold) {
		for i := range inputSize {
			key := hashBytes(data, i)
			h.buckets[key] = 0
		}
	} else {
		h.buckets = [bucketSize]uint32{}
	}
	h.ready = true
}

// store records position pos in the bucket for the 5-byte sequence at
// data[pos & mask].
func (h *h2) store(data []byte, mask, pos uint) {
	key := hashBytes(data, pos&mask)
	h.buckets[key] = uint32(pos)
}

// storeRange records positions [start, end) in the hash table.
func (h *h2) storeRange(data []byte, mask, start, end uint) {
	for i := start; i < end; i++ {
		h.store(data, mask, i)
	}
}

// stitchToPreviousBlock seeds the hash table with the last 3 positions of
// the previous block so that cross-block matches can be found.
func (h *h2) stitchToPreviousBlock(numBytes, position uint, ringBuffer []byte, ringBufferMask uint) {
	if numBytes >= hashTypeLength-1 && position >= 3 {
		h.store(ringBuffer, ringBufferMask, position-3)
		h.store(ringBuffer, ringBufferMask, position-2)
		h.store(ringBuffer, ringBufferMask, position-1)
	}
}

// createBackwardReferences finds backward reference matches using this hasher
// and populates s.commands. findLongestMatch is manually inlined at both call
// sites below — the compiler cannot inline it (cost > budget), so we eliminate
// the call overhead directly. H2 uses BUCKET_SWEEP=1 and supports static
// dictionary fallback when the bucket probe is a near-miss.
func (h *h2) createBackwardReferences(s *encodeState, bytes, wrappedPos uint32) {
	data := s.data
	mask := uint(s.mask)
	maxBackwardLimit := (uint(1) << s.lgwin) - windowGap
	gap := s.compound.totalSize

	insertLength := s.lastInsertLen
	position := uint(wrappedPos)
	posEnd := position + uint(bytes)

	// StoreLookahead for H2 is 8.
	storeEnd := position
	if uint(bytes) >= hashTypeLength {
		storeEnd = posEnd - hashTypeLength + 1
	}

	// Random heuristics for uncompressible data. Quality < 9 → window = 64.
	const randomHeuristicsWindowSize = 64
	applyRandomHeuristics := position + randomHeuristicsWindowSize

	origCmdCount := uint(len(s.commands))
	buckets := &h.buckets
	// Small blocks prefer the compact helper path; larger blocks benefit from
	// avoiding the extra store helper in the hot matched-range loop.
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

		// Manually inlined findLongestMatch at the current position. The
		// last-distance fast path short-circuits to skip both the bucket
		// probe and the dictionary lookup on success.
		var dictEligible bool
		{
			lastDistance := s.distCache[0]
			curMasked := position & mask
			// sr.len == 0 at entry → bestLenIn == 0; guardByte is the byte at
			// curMasked, and match-tail checks use prev+0.
			guardByte := loadByte(data, curMasked)
			key := hashBytes(data, curMasked)
			bestScore := sr.score // minScore

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
								buckets[key] = uint32(position)
								lastDistanceHit = true
							}
						}
					}
				}
			}

			if !lastDistanceHit {
				// BUCKET_SWEEP == 1: single bucket lookup.
				prev := uint(buckets[key])
				buckets[key] = uint32(position)
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
			// Found a match. Try lazy matching: look one position ahead.
			delayedBackwardReferencesInRow := 0
			maxLength--
			for {
				const costDiffLazy = 175
				var sr2 hasherSearchResult
				// quality < MIN_QUALITY_FOR_EXTENSIVE_REFERENCE_SEARCH (5):
				// cap sr2.len to sr.len-1.
				sr2.len = min(sr.len-1, maxLength)
				sr2.lenCodeDelta = 0
				sr2.distance = 0
				sr2.score = minScore
				maxDistance = min(position+1, maxBackwardLimit)

				// Manually inlined findLongestMatch at position+1.
				var dictEligible2 bool
				{
					cur2 := position + 1
					lastDistance := s.distCache[0]
					curMasked := cur2 & mask
					bestLen := sr2.len
					guardByte := loadByte(data, curMasked+bestLen)
					key := hashBytes(data, curMasked)
					bestScore := sr2.score // minScore

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
										buckets[key] = uint32(cur2)
										lastDistanceHit = true
									}
								}
							}
						}
					}

					if !lastDistanceHit {
						prev := uint(buckets[key])
						buckets[key] = uint32(cur2)
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
					// Better match one byte ahead. Write one literal.
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

			// Compute distance code and update distance cache.
			// Recompute maxDistance after the lazy loop because position may
			// have advanced. This matches the C reference's dictionary_start.
			maxDistance = min(position, maxBackwardLimit)
			distanceCode := computeDistanceCode(sr.distance, maxDistance+gap, &s.distCache)
			if sr.distance <= maxDistance+gap && distanceCode > 0 {
				s.distCache[3] = s.distCache[2]
				s.distCache[2] = s.distCache[1]
				s.distCache[1] = s.distCache[0]
				s.distCache[0] = sr.distance
				// H2 PrepareDistanceCache is a no-op (BUCKET_SWEEP=1).
			}

			// Manually inlined newCommandSimpleDist to avoid non-inlineable
			// function call overhead and struct return copy.
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
			// Inline histogram update: accumulate symbol counts while the
			// insert-literal bytes and command fields are hot in registers,
			// avoiding the separate tally pass in writeMetaBlockFast.
			if s.cmdHisto != nil {
				s.cmdHisto[cmdPrefix]++
				// Distance symbol is written when cmdPrefix >= 128
				// (!usesLastDistance). This matches tally's condition:
				//   copyLen != 0 && !cmd.usesLastDistance()
				// Note: distPrefix&0x3FF may be 0 even when cmdPrefix >= 128
				// (last-distance copy with long insert/copy length codes).
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

			// Pre-warm the hash bucket for the next main-loop iteration at
			// position+sr.len. After a match the position jumps by sr.len, leaving
			// that bucket cold (potential L3 miss). Issuing the load here lets the
			// store loop below hide the cache-miss latency.
			nextMatchPos := position + sr.len
			if nextMatchPos+hashTypeLength < posEnd {
				h.nextBucket = buckets[hashBytes(data, nextMatchPos&mask)]
			}

			// Store hash entries for the matched range, avoiding RLE poisoning.
			rangeStart := position + 2
			rangeEnd := min(position+sr.len, storeEnd)
			if sr.distance < sr.len>>2 {
				rangeStart = min(rangeEnd, max(rangeStart, position+sr.len-(sr.distance<<2)))
			}
			if directStoreRange {
				for i := rangeStart; i < rangeEnd; i++ {
					key := hashBytes(data, i&mask)
					buckets[key] = uint32(i)
				}
			} else {
				h.storeRange(data, mask, rangeStart, rangeEnd)
			}

			position += sr.len
		} else {
			// No good match. Insert a literal.
			insertLength++
			position++

			// Random heuristics: skip match lookups in uncompressible regions.
			if position > applyRandomHeuristics {
				if position > applyRandomHeuristics+4*randomHeuristicsWindowSize {
					// Very long run without matches: stride by 4, store every 4th.
					posJump := min(position+16, posEnd-(hashTypeLength-1))
					for position < posJump {
						h.store(data, mask, position)
						insertLength += 4
						position += 4
					}
				} else {
					// Moderate run without matches: stride by 2, store every 2nd.
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
