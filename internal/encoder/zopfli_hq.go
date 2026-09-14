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

package encoder

import (
	"runtime"
	"sync/atomic"

	"github.com/molecule-man/go-brrr/internal/core"
)

const (
	feedPublishBatch    = 512
	feedSpinBeforeYield = 128
)

// hqMatchesPerByte sizes the match buffer per input byte, matching preallocQ10;
// tests lower it to force the reallocation that aborts the feed.
var hqMatchesPerByte uint = 8

// matchFeed publishes how many positions of the match buffer are final, so the
// first DP pass can consume matches while collection is still producing them.
// aborted is raised when the producer had to reallocate the buffer: positions
// past that point live only in the new array, so the watermark stops and the
// consumer restarts on the reallocated buffer once the producer is done.
type matchFeed struct {
	_       [64]byte
	ready   atomic.Uint64
	aborted atomic.Bool
	_       [64]byte
}

func (f *matchFeed) wait(i uint) bool {
	return f == nil || f.ready.Load() > uint64(i) || f.waitSlow(i)
}

func (f *matchFeed) waitSlow(i uint) bool {
	for spin := 0; ; spin++ {
		if f.ready.Load() > uint64(i) {
			return true
		}
		if f.aborted.Load() {
			return false
		}
		if spin >= feedSpinBeforeYield {
			runtime.Gosched()
			spin = 0
		}
	}
}

// createHqZopfliBackwardReferences is the Q11 top-level entry point.
func createHqZopfliBackwardReferences(numBytes, position uint, ringbuffer []byte, ringBufferMask uint, quality, lgwin int, gap uint, compound *compoundDictionary, distCache []int, hasher *h10, lastInsertLen *uint, commands *[]command, numLiterals *uint, bufs *q10Bufs) {
	maxBackwardLimit := (uint(1) << lgwin) - core.WindowGap
	hasCompound := compound != nil && compound.numChunks > 0
	shadowMatches := uint(0)
	if hasCompound {
		shadowMatches = h10MaxNumMatches + 128
	}
	matchesSize := hqMatchesPerByte*numBytes + shadowMatches
	storeEnd := position
	if numBytes >= h10MaxTreeCompLength {
		storeEnd = position + numBytes - h10MaxTreeCompLength + 1
	}
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
	// The consumer sees the whole capacity so the producer's reslices are
	// invisible; only a true reallocation (which aborts the feed) matters.
	matches := bufs.hqMatches[:cap(bufs.hqMatches)]

	needed := int(numBytes + 1)
	if cap(bufs.zNodes) < needed {
		bufs.zNodes = make([]zopfliNode, needed)
	} else {
		bufs.zNodes = bufs.zNodes[:needed]
	}
	nodes := bufs.zNodes
	model := &bufs.zCostModel
	model.init(64, numBytes) // distAlphabetSize=64 for NPOSTFIX=0, NDIRECT=0

	feed := &bufs.hqFeed
	feed.ready.Store(0)
	feed.aborted.Store(false)
	wg := &bufs.hqWG
	wg.Add(1)
	go func() {
		defer wg.Done()
		matches := bufs.hqMatches
		curMatchPos := uint(0)
		nextPublish := uint(0)
		aborted := false
		// Phase 1: Collect all matches.
		for i := uint(0); i+h10HashTypeLength-1 < numBytes; i++ {
			pos := position + i
			maxDistance := min(pos, maxBackwardLimit)
			maxLength := numBytes - i

			// Ensure capacity (grow-and-reuse via bufs).
			if curMatchPos+h10MaxNumMatches+shadowMatches > uint(len(matches)) {
				newSize := max(uint(len(matches))*2, curMatchPos+h10MaxNumMatches+shadowMatches)
				if cap(bufs.hqMatches) < int(newSize) {
					if !aborted {
						aborted = true
						feed.aborted.Store(true)
					}
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
			if !aborted && i+1 >= nextPublish {
				feed.ready.Store(uint64(i + 1))
				nextPublish = i + 1 + feedPublishBatch
			}
		}
		if !aborted {
			feed.ready.Store(uint64(numBytes))
		}

	}()

	// Save original state for the two-pass loop.
	origNumLiterals := *numLiterals
	origLastInsertLen := *lastInsertLen
	origDistCache := [4]int{distCache[0], distCache[1], distCache[2], distCache[3]}
	origNumCommands := len(*commands)
	restore := func() {
		*commands = (*commands)[:origNumCommands]
		*numLiterals = origNumLiterals
		*lastInsertLen = origLastInsertLen
		distCache[0] = origDistCache[0]
		distCache[1] = origDistCache[1]
		distCache[2] = origDistCache[2]
		distCache[3] = origDistCache[3]
	}

	// Phase 2 & 3: Two DP passes. Pass 1 overlaps match collection through the
	// feed; pass 2 needs every match and the pass-1 commands, so it waits.
	for pass := range 2 {
		initZopfliNodes(nodes)
		var f *matchFeed
		if pass == 0 {
			model.setFromLiteralCosts(position, ringbuffer, ringBufferMask)
			f = feed
		} else {
			wg.Wait()
			passCommands := (*commands)[origNumCommands:]
			model.setFromCommands(position, ringbuffer, ringBufferMask,
				passCommands, origLastInsertLen)
		}
		restore()

		if zopfliIterate(nodes, ringbuffer, distCache, model, numMatchesArr, matches,
			numBytes, position, ringBufferMask, gap, compound, quality, lgwin, f) == zopfliIterateAborted {
			wg.Wait()
			matches = bufs.hqMatches
			initZopfliNodes(nodes)
			restore()
			zopfliIterate(nodes, ringbuffer, distCache, model, numMatchesArr, matches,
				numBytes, position, ringBufferMask, gap, compound, quality, lgwin, nil)
		}

		zopfliCreateCommands(nodes, numBytes, position, maxBackwardLimit, gap, distCache, lastInsertLen, commands, numLiterals)
	}
}
