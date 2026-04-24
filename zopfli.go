// Zopfli optimal parsing: DP core, start-position queue, and helpers.
//
// The Zopfli algorithm finds the globally optimal command sequence for a
// metablock by running a dynamic-programming forward pass over all positions.
// At each position it evaluates all candidate (insert-length, copy-length,
// distance) triples and tracks the minimum-cost path.

package brrr

// Zopfli quality parameters.
const (
	// maxZopfliLenQ10 is the maximum copy length for which Q10 evaluates
	// all individual lengths (beyond this, only the maximum match length
	// is tried). Shorter limit = faster but slightly worse compression.
	maxZopfliLenQ10 = 150

	// maxZopfliLenQ11 is the same threshold for Q11.
	maxZopfliLenQ11 = 325

	// longCopyQuickStep: when a copy this long is found, skip detailed
	// evaluation of the copied positions (they are unlikely to start
	// new commands).
	longCopyQuickStep = 16384
)

// Distance cache index and offset tables for the 16 distance short codes
// (RFC 7932 Section 4). These map short code j to:
//
//	distance = distCache[distanceCacheIndex[j]] + distanceCacheOffset[j]
var distanceCacheIndex = [numDistanceShortCodes]uint{
	0, 1, 2, 3, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1,
}

var distanceCacheOffset = [numDistanceShortCodes]int{
	0, 0, 0, 0, -1, 1, -2, 2, -3, 3, -1, 1, -2, 2, -3, 3,
}

// posData holds a candidate starting position and its associated state,
// used by startPosQueue to track the best insert-length candidates.
type posData struct {
	pos           uint
	distanceCache [4]int
	costdiff      float32
	cost          float32
}

// startPosQueue maintains the 8 best starting positions ordered by cost
// difference vs. literal-only cost. The DP evaluates candidates from this
// queue at each position to limit the O(n²) insert-length search space.
type startPosQueue struct {
	q   [8]posData
	idx uint
}

// size returns the number of entries in the queue (at most 8).
func (q *startPosQueue) size() uint {
	return min(q.idx, 8)
}

// push inserts a new entry and restores sorted order by costdiff.
// The queue uses a rotating index so no entries need shifting.
func (q *startPosQueue) push(p *posData) {
	offset := ^q.idx & 7
	q.idx++
	length := q.size()
	// Find the insertion point by scanning for the first element with
	// costdiff >= p.costdiff (cheap float32 comparisons only).
	insertAt := uint(0)
	for insertAt < length-1 {
		next := (offset + insertAt + 1) & 7
		if q.q[next].costdiff >= p.costdiff {
			break
		}
		insertAt++
	}
	// Shift elements [0, insertAt) down by one to make room.
	dst := offset & 7
	for i := uint(0); i < insertAt; i++ {
		src := (offset + i + 1) & 7
		q.q[dst] = q.q[src]
		dst = src
	}
	q.q[(offset+insertAt)&7] = *p
}

// at returns a pointer to the k-th element (0 = best/lowest costdiff).
func (q *startPosQueue) at(k uint) *posData {
	return &q.q[(k-q.idx)&7]
}

// maxZopfliLen returns the maximum Zopfli copy length for the given quality.
func maxZopfliLen(quality int) uint {
	if quality <= 10 {
		return maxZopfliLenQ10
	}
	return maxZopfliLenQ11
}

// maxZopfliCandidates returns the number of start-position queue candidates
// to evaluate per position.
func maxZopfliCandidates(quality int) uint {
	if quality <= 10 {
		return 1
	}
	return 5
}

// computeDistanceShortcut determines which earlier node provides the
// distance cache for this node's position. If the current node introduced
// a new distance (not from the static dictionary and not code 0), its own
// position is the shortcut. Otherwise, it inherits the shortcut from the
// node that started the current command.
func computeDistanceShortcut(nodes []zopfliNode, blockStart, pos, maxBackwardLimit, gap uint) uint32 {
	cLen := uint(nodes[pos].copyLength())
	iLen := uint(nodes[pos].dcodeInsertLength & 0x7FFFFFF)
	dist := uint(nodes[pos].copyDistance())
	if pos == 0 {
		return 0
	}
	if dist+cLen <= blockStart+pos+gap &&
		dist <= maxBackwardLimit+gap &&
		nodes[pos].distanceCode() > 0 {
		return uint32(pos)
	}
	return nodes[pos-cLen-iLen].u // shortcut from the previous command's start
}

// computeDistanceCache walks the shortcut chain to fill distCache[0..3]
// with the four most recent distances at the given position.
func computeDistanceCache(nodes []zopfliNode, pos uint, startingDistCache, distCache []int) {
	idx := 0
	p := uint(nodes[pos].u) // shortcut
	for idx < 4 && p > 0 {
		n := nodes[p]
		distCache[idx] = int(n.distance)
		idx++
		p = uint(nodes[p-uint(n.length&0x1FFFFFF)-uint(n.dcodeInsertLength&0x7FFFFFF)].u)
	}
	sdcIdx := 0
	for ; idx < 4; idx++ {
		distCache[idx] = startingDistCache[sdcIdx]
		sdcIdx++
	}
}

// evaluateNode computes the shortcut for a processed node and pushes it
// to the queue if its cost beats the literal-only cost to that position.
func evaluateNode(nodes []zopfliNode, pos, blockStart, maxBackwardLimit, gap uint, startingDistCache []int, model *zopfliCostModel, queue *startPosQueue) {
	// Save cost before ComputeDistanceCache overwrites the u field.
	nodeCost := nodes[pos].cost()
	nodes[pos].u = computeDistanceShortcut(nodes, blockStart, pos, maxBackwardLimit, gap)
	if nodeCost <= model.getLiteralCosts(0, pos) {
		var pd posData
		pd.pos = pos
		pd.cost = nodeCost
		pd.costdiff = nodeCost - model.getLiteralCosts(0, pos)
		computeDistanceCache(nodes, pos, startingDistCache, pd.distanceCache[:])
		queue.push(&pd)
	}
}

// computeMinimumCopyLength finds the shortest copy length that could
// improve on already-known costs at future positions. This prunes the
// inner DP loop by skipping lengths that cannot possibly be better.
//
// The copy length code uses a staircase of extra bits: every time the
// length crosses a bucket boundary, one extra bit is needed, so the
// minimum achievable cost increases by 1.
func computeMinimumCopyLength(nodes []zopfliNode, pos, numBytes uint, startCost float32) uint {
	minCost := startCost
	length := uint(2)
	nextLenBucket := uint(4)
	nextLenOffset := uint(10)
	for pos+length <= numBytes && nodes[pos+length].cost() <= minCost {
		length++
		if length == nextLenOffset {
			minCost += 1.0
			nextLenOffset += nextLenBucket
			nextLenBucket *= 2
		}
	}
	return length
}

// updateNodes is the heart of the Zopfli DP. For each starting position
// in the queue (up to maxZopfliCandidates), it evaluates:
//  1. Distance-cache matches (16 short codes)
//  2. Hash-table matches from findAllMatches
//  3. Updates nodes with better costs
//
// Returns the longest copy length found (used for skip-ahead).
func updateNodes(nodes []zopfliNode, ringbuffer []byte, startingDistCache []int, matches []backwardMatch, model *zopfliCostModel, queue *startPosQueue, numBytes, blockStart, pos, ringBufferMask, maxBackwardLimit, gap uint, compound *compoundDictionary, numMatches uint, quality int) uint {
	curIx := blockStart + pos
	curIxMasked := curIx & ringBufferMask
	maxDistance := min(curIx, maxBackwardLimit)
	maxLen := numBytes - pos
	maxZopfli := maxZopfliLen(quality)
	maxIters := maxZopfliCandidates(quality)

	// BCE hints: let the compiler prove that mask-derived and
	// pos+len-derived indices are always in bounds.
	_ = ringbuffer[ringBufferMask]
	_ = nodes[numBytes]

	evaluateNode(nodes, pos, blockStart, maxBackwardLimit, gap, startingDistCache, model, queue)

	// Compute minLen from the best queue entry.
	var minLen uint
	{
		pd := queue.at(0)
		minCost := pd.cost + model.getMinCostCmd() +
			model.getLiteralCosts(pd.pos, pos)
		minLen = computeMinimumCopyLength(nodes, pos, numBytes, minCost)
	}

	result := uint(0)

	for k := uint(0); k < maxIters && k < queue.size(); k++ {
		pd := queue.at(k)
		start := pd.pos
		insCode := getInsertLenCode(pos - start)
		startCostdiff := pd.costdiff
		baseCost := startCostdiff + float32(insertExtra[insCode]) +
			model.getLiteralCosts(0, pos)

		// Phase 1: Distance cache matches.
		bestLen := minLen - 1
		for j := uint(0); j < numDistanceShortCodes && bestLen < maxLen; j++ {
			idx := distanceCacheIndex[j] & 3
			backward := uint(pd.distanceCache[idx] + distanceCacheOffset[j])
			if backward == 0 {
				continue
			}
			prevIx := curIx - backward
			continuation := ringbuffer[curIxMasked+bestLen]
			if curIxMasked+bestLen > ringBufferMask {
				break
			}
			if backward > maxDistance+gap {
				// Would be word dictionary → ignore.
				continue
			}
			var length uint
			switch {
			case backward <= maxDistance:
				// Regular backward reference.
				if prevIx >= curIx {
					continue
				}
				prevIxMasked := prevIx & ringBufferMask
				if prevIxMasked+bestLen > ringBufferMask ||
					continuation != ringbuffer[prevIxMasked+bestLen] {
					continue
				}
				length = uint(matchLen(
					ringbuffer[prevIxMasked:],
					ringbuffer[curIxMasked:],
					int(maxLen),
				))
			case compound != nil && compound.numChunks > 0:
				// Compound dictionary reference.
				d := 0
				offset := maxDistance + 1 + compound.totalSize - 1
				for offset >= backward+compound.chunkOffsets[d+1] {
					d++
				}
				source := compound.chunkSource[d]
				offset = offset - compound.chunkOffsets[d] - backward
				limit := min(compound.chunkOffsets[d+1]-compound.chunkOffsets[d]-offset, maxLen)
				if bestLen >= limit || continuation != source[offset+bestLen] {
					continue
				}
				length = uint(matchLen(
					source[offset:],
					ringbuffer[curIxMasked:],
					int(limit),
				))
			default:
				// Gray area: addressable by decoder but not available here.
				continue
			}

			distCost := baseCost + model.distanceCost(j)
			nodesAtPos := nodes[pos:]
			_ = nodesAtPos[length] // BCE: l ≤ length
			for l := bestLen + 1; l <= length; l++ {
				copyCode := getCopyLenCode(l)
				cmdCode := combineLengthCodes(insCode, copyCode, j == 0)
				cost := baseCost
				if cmdCode >= 128 {
					cost = distCost
				}
				cost = (cost + float32(copyExtra[copyCode])) + model.commandCost(cmdCode)
				if cost < nodesAtPos[l].cost() {
					updateZopfliNode(nodes, pos, start, l, l, backward, j+1, cost)
					if l > result {
						result = l
					}
				}
				bestLen = l
			}
		}

		// At higher iterations look only for distance cache matches.
		if k >= 2 {
			continue
		}

		// Phase 2: Hash-table matches.
		matchLen := minLen
		for j := range numMatches {
			match := matches[j]
			dist := uint(match.distance)
			isDictionaryMatch := dist > maxDistance+gap
			distCode := dist + numDistanceShortCodes - 1
			distSymbol, distExtra := prefixEncodeSimpleDistance(distCode)
			distNumExtra := distSymbol >> 10
			distCost := baseCost + float32(distNumExtra) +
				model.distanceCost(uint(distSymbol&0x3FF))

			maxMatchLen := match.matchLength()
			if matchLen < maxMatchLen && (isDictionaryMatch || maxMatchLen > maxZopfli) {
				matchLen = maxMatchLen
			}
			nodesAtPos := nodes[pos:]
			_ = nodesAtPos[maxMatchLen] // BCE: matchLen ≤ maxMatchLen
			for ; matchLen <= maxMatchLen; matchLen++ {
				lenCode := matchLen
				if isDictionaryMatch {
					lenCode = match.matchLengthCode()
				}
				copyCode := getCopyLenCode(lenCode)
				cmdCode := combineLengthCodes(insCode, copyCode, false)
				cost := distCost + float32(copyExtra[copyCode]) +
					model.commandCost(cmdCode)
				if cost < nodesAtPos[matchLen].cost() {
					updateZopfliNode(nodes, pos, start, matchLen, lenCode, dist, 0, cost)
					if matchLen > result {
						result = matchLen
					}
				}
			}
			_ = distExtra
		}
	}
	return result
}

// zopfliIterate runs the DP over pre-collected matches (Q11 path).
func zopfliIterate(nodes []zopfliNode, ringbuffer []byte, distCache []int, model *zopfliCostModel, numMatches []uint32, matches []backwardMatch, numBytes, position, ringBufferMask, gap uint, compound *compoundDictionary, quality, lgwin int) uint {
	maxBackwardLimit := (uint(1) << lgwin) - windowGap
	maxZopfli := maxZopfliLen(quality)
	var queue startPosQueue
	curMatchPos := uint(0)

	nodes[0].length = 0
	nodes[0].setCost(0)

	for i := uint(0); i+3 < numBytes; i++ {
		skip := updateNodes(nodes, ringbuffer, distCache,
			matches[curMatchPos:], model, &queue,
			numBytes, position, i, ringBufferMask, maxBackwardLimit, gap, compound, uint(numMatches[i]), quality)
		if skip < longCopyQuickStep {
			skip = 0
		}
		curMatchPos += uint(numMatches[i])
		if numMatches[i] == 1 && matches[curMatchPos-1].matchLength() > maxZopfli {
			skip = max(matches[curMatchPos-1].matchLength(), skip)
		}
		if skip > 1 {
			skip--
			for skip > 0 {
				i++
				if i+3 >= numBytes {
					break
				}
				evaluateNode(nodes, i, position, maxBackwardLimit, gap, distCache, model, &queue)
				curMatchPos += uint(numMatches[i])
				skip--
			}
		}
	}
	return computeShortestPathFromNodes(nodes, numBytes)
}

// mergeMatches merges two sorted backward-match slices (sorted by match length)
// into dst, which must have room for len(src1)+len(src2) entries.
// Ties in length are broken by preferring the shorter distance.
func mergeMatches(dst, src1, src2 []backwardMatch) {
	i, j, k := 0, 0, 0
	for i < len(src1) && j < len(src2) {
		l1 := src1[i].matchLength()
		l2 := src2[j].matchLength()
		if l1 < l2 || (l1 == l2 && src1[i].distance < src2[j].distance) {
			dst[k] = src1[i]
			i++
		} else {
			dst[k] = src2[j]
			j++
		}
		k++
	}
	for ; i < len(src1); i++ {
		dst[k] = src1[i]
		k++
	}
	for ; j < len(src2); j++ {
		dst[k] = src2[j]
		k++
	}
}
