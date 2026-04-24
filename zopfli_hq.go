// Zopfli Q11 entry point: two-pass HQ backward references.
//
// Q11 uses a two-pass approach:
//  1. Match collection: iterate all positions, call h10.findAllMatches,
//     and store all matches in a flat array.
//  2. DP pass 1: run zopfliIterate with a literal-cost model.
//  3. DP pass 2: re-initialize the cost model from pass-1 commands,
//     re-run zopfliIterate for better results.
//
// The second pass produces better commands because the cost model reflects
// actual symbol distributions from the first pass.

package brrr

// createHqZopfliBackwardReferences is the Q11 top-level entry point.
func createHqZopfliBackwardReferences(numBytes, position uint, ringbuffer []byte, ringBufferMask uint, quality, lgwin int, gap uint, compound *compoundDictionary, distCache []int, hasher *h10, lastInsertLen *uint, commands *[]command, numLiterals *uint, bufs *q10Bufs) {
	maxBackwardLimit := (uint(1) << lgwin) - windowGap
	hasCompound := compound != nil && compound.numChunks > 0
	shadowMatches := uint(0)
	if hasCompound {
		shadowMatches = h10MaxNumMatches + 128
	}
	matchesSize := 4*numBytes + shadowMatches
	storeEnd := position
	if numBytes >= h10MaxTreeCompLength {
		storeEnd = position + numBytes - h10MaxTreeCompLength + 1
	}
	curMatchPos := uint(0)

	// Reuse preallocated numMatchesArr from bufs.
	if cap(bufs.hqNumMatchesArr) < int(numBytes) {
		bufs.hqNumMatchesArr = make([]uint32, numBytes)
	} else {
		bufs.hqNumMatchesArr = bufs.hqNumMatchesArr[:numBytes]
		clear(bufs.hqNumMatchesArr)
	}
	numMatchesArr := bufs.hqNumMatchesArr

	// Reuse preallocated matches from bufs.
	if cap(bufs.hqMatches) < int(matchesSize) {
		bufs.hqMatches = make([]backwardMatch, matchesSize)
	} else {
		bufs.hqMatches = bufs.hqMatches[:matchesSize]
	}
	matches := bufs.hqMatches

	// Phase 1: Collect all matches.
	for i := uint(0); i+h10HashTypeLength-1 < numBytes; i++ {
		pos := position + i
		maxDistance := min(pos, maxBackwardLimit)
		maxLength := numBytes - i

		// Ensure capacity (grow-and-reuse via bufs).
		if curMatchPos+h10MaxNumMatches+shadowMatches > uint(len(matches)) {
			newSize := max(uint(len(matches))*2, curMatchPos+h10MaxNumMatches+shadowMatches)
			if cap(bufs.hqMatches) < int(newSize) {
				grown := make([]backwardMatch, newSize)
				copy(grown, matches[:curMatchPos])
				bufs.hqMatches = grown
			} else {
				bufs.hqMatches = bufs.hqMatches[:newSize]
			}
			matches = bufs.hqMatches
		}

		numFound := hasher.findAllMatches(
			ringbuffer, ringBufferMask, pos, maxLength, maxDistance,
			maxDistance+gap, quality, matches[curMatchPos+shadowMatches:])

		if hasCompound {
			cdMatches := compound.lookupAllMatches(
				ringbuffer, ringBufferMask, pos, 3, maxLength,
				maxDistance, maxBackwardDistance,
				matches[curMatchPos+shadowMatches-64:curMatchPos+shadowMatches],
			)
			if cdMatches > 0 {
				lzSlice := make([]backwardMatch, numFound)
				copy(lzSlice, matches[curMatchPos+shadowMatches:curMatchPos+shadowMatches+numFound])
				cdSlice := matches[curMatchPos+shadowMatches-64 : curMatchPos+shadowMatches-64+cdMatches]
				mergeMatches(matches[curMatchPos:], cdSlice, lzSlice)
				numFound += cdMatches
			} else {
				copy(matches[curMatchPos:], matches[curMatchPos+shadowMatches:curMatchPos+shadowMatches+numFound])
			}
		}

		curMatchEnd := curMatchPos + numFound
		numMatchesArr[i] = uint32(numFound)

		if numFound > 0 {
			matchLen := matches[curMatchEnd-1].matchLength()
			if matchLen > maxZopfliLenQ11 {
				skip := matchLen - 1
				matches[curMatchPos] = matches[curMatchEnd-1]
				curMatchPos++
				numMatchesArr[i] = 1
				// Store the tail in the hasher.
				hasher.storeRange(ringbuffer, ringBufferMask,
					pos+1, min(pos+matchLen, storeEnd))
				for j := uint(1); j <= skip; j++ {
					if i+j < numBytes {
						numMatchesArr[i+j] = 0
					}
				}
				i += skip
			} else {
				curMatchPos = curMatchEnd
			}
		}
	}

	// Save original state for the two-pass loop.
	origNumLiterals := *numLiterals
	origLastInsertLen := *lastInsertLen
	origDistCache := [4]int{distCache[0], distCache[1], distCache[2], distCache[3]}
	origNumCommands := len(*commands)

	needed := int(numBytes + 1)
	if cap(bufs.zNodes) < needed {
		bufs.zNodes = make([]zopfliNode, needed)
	} else {
		bufs.zNodes = bufs.zNodes[:needed]
	}
	nodes := bufs.zNodes
	model := &bufs.zCostModel
	model.init(64, numBytes) // distAlphabetSize=64 for NPOSTFIX=0, NDIRECT=0

	// Phase 2 & 3: Two DP passes.
	for pass := range 2 {
		initZopfliNodes(nodes)
		if pass == 0 {
			model.setFromLiteralCosts(position, ringbuffer, ringBufferMask)
		} else {
			passCommands := (*commands)[origNumCommands:]
			model.setFromCommands(position, ringbuffer, ringBufferMask,
				passCommands, origLastInsertLen)
		}

		// Restore state for this pass.
		*commands = (*commands)[:origNumCommands]
		*numLiterals = origNumLiterals
		*lastInsertLen = origLastInsertLen
		distCache[0] = origDistCache[0]
		distCache[1] = origDistCache[1]
		distCache[2] = origDistCache[2]
		distCache[3] = origDistCache[3]

		numCmds := zopfliIterate(nodes, ringbuffer, distCache, model, numMatchesArr, matches,
			numBytes, position, ringBufferMask, gap, compound, quality, lgwin)
		_ = numCmds

		zopfliCreateCommands(nodes, numBytes, position, maxBackwardLimit, gap, distCache, lastInsertLen, commands, numLiterals)
	}
}
