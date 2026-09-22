package encoder

import (
	"os"
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

// h10StoreAndFindMatchesBefore is the single combined tree walk this branch
// splits into storeOnly and findMatchesNoStore, copied from before the split.
// leftChild and rightChild were removed with it, so their bodies are inlined.
func h10StoreAndFindMatchesBefore(
	h *h10,
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
	nodeLeft := 2 * (curIx & uint(h.windowMask))
	nodeRight := 2*(curIx&uint(h.windowMask)) + 1

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
				h.forest[nodeLeft] = h.forest[2*(prevIx&uint(h.windowMask))]
				h.forest[nodeRight] = h.forest[2*(prevIx&uint(h.windowMask))+1]
			}
			break
		}

		// Lexicographic comparison determines left vs right subtree placement.
		if data[curIxMasked+length] > data[prevIxMasked+length] {
			bestLenLeft = length
			if shouldReroot {
				h.forest[nodeLeft] = uint32(prevIx)
			}
			nodeLeft = 2*(prevIx&uint(h.windowMask)) + 1
			prevIx = uint(h.forest[nodeLeft])
		} else {
			bestLenRight = length
			if shouldReroot {
				h.forest[nodeRight] = uint32(prevIx)
			}
			nodeRight = 2 * (prevIx & uint(h.windowMask))
			prevIx = uint(h.forest[nodeRight])
		}
	}

	return nMatches
}

const h10BenchSize = 128 << 10

func h10Fixture(tb testing.TB) (*h10, []byte, uint) {
	tb.Helper()
	data, err := os.ReadFile("../../testdata/gh_172KB.html")
	if err != nil {
		tb.Fatal(err)
	}
	if len(data) < h10BenchSize {
		tb.Fatalf("fixture holds %d bytes, the benchmark name promises %d", len(data), h10BenchSize)
	}
	h := &h10{lgwin: 17, quality: 11}
	h.reset(true, h10BenchSize, nil)
	return h, data[:h10BenchSize], uint(h10BenchSize - 1)
}

func h10MaxBackward(h *h10) uint {
	return uint(h.windowMask) - core.WindowGap + 1
}

// h10Positions is the span stored or searched per benchmark iteration. It stays
// well inside the window so no position is evicted mid-run.
const h10Positions = 16384

var (
	h10MatchSink int
	h10LenSink   uint
)

func h10TreeState(h *h10) ([]uint32, []uint32) {
	return append([]uint32(nil), h.forest...), append([]uint32(nil), h.buckets[:]...)
}

func TestStoreOnlyBuildsByteIdenticalTreeStateToTheCombinedWalkItReplaces(t *testing.T) {
	hOld, data, mask := h10Fixture(t)
	hNew, _, _ := h10Fixture(t)
	maxBackward := h10MaxBackward(hOld)

	for i := range uint(h10Positions) {
		h10StoreAndFindMatchesBefore(hOld, data, i, mask, h10MaxTreeCompLength, maxBackward, nil, nil)
		hNew.storeOnly(data, i, mask, maxBackward)
	}

	oldForest, oldBuckets := h10TreeState(hOld)
	newForest, newBuckets := h10TreeState(hNew)
	for i := range oldForest {
		if oldForest[i] != newForest[i] {
			t.Fatalf("forest[%d] = %d after storeOnly, %d after the combined walk; the tree drives "+
				"every later match search, so a difference changes the compressed output",
				i, newForest[i], oldForest[i])
		}
	}
	for i := range oldBuckets {
		if oldBuckets[i] != newBuckets[i] {
			t.Fatalf("buckets[%d] = %d after storeOnly, %d after the combined walk",
				i, newBuckets[i], oldBuckets[i])
		}
	}
}

func TestFindMatchesNoStoreReturnsTheSameMatchesAndLeavesTheTreeUntouched(t *testing.T) {
	hOld, data, mask := h10Fixture(t)
	hNew, _, _ := h10Fixture(t)
	maxBackward := h10MaxBackward(hOld)

	// Both hashers need the same populated tree before the search paths diverge.
	for i := range uint(h10Positions) {
		hOld.storeOnly(data, i, mask, maxBackward)
		hNew.storeOnly(data, i, mask, maxBackward)
	}
	beforeForest, beforeBuckets := h10TreeState(hNew)

	oldMatches := make([]backwardMatch, h10MaxNumMatches)
	newMatches := make([]backwardMatch, h10MaxNumMatches)
	// maxLength below h10MaxTreeCompLength is the regime findMatchesNoStore
	// serves: the old walk left the tree alone there too.
	for _, maxLength := range []uint{4, 17, 64, h10MaxTreeCompLength - 1} {
		for i := uint(h10Positions); i < uint(h10Positions)+512; i++ {
			oldBest, newBest := uint(0), uint(0)
			nOld := h10StoreAndFindMatchesBefore(hOld, data, i, mask, maxLength, maxBackward, &oldBest, oldMatches)
			nNew := hNew.findMatchesNoStore(data, i, mask, maxLength, maxBackward, &newBest, newMatches)

			if nOld != nNew || oldBest != newBest {
				t.Fatalf("maxLength=%d pos=%d: findMatchesNoStore returned %d matches (bestLen %d), "+
					"the combined walk %d (bestLen %d)", maxLength, i, nNew, newBest, nOld, oldBest)
			}
			for k := range nOld {
				if oldMatches[k] != newMatches[k] {
					t.Fatalf("maxLength=%d pos=%d match %d: %+v vs %+v; matches feed the Zopfli DP",
						maxLength, i, k, newMatches[k], oldMatches[k])
				}
			}
		}
	}

	afterForest, afterBuckets := h10TreeState(hNew)
	for i := range beforeForest {
		if beforeForest[i] != afterForest[i] {
			t.Fatalf("findMatchesNoStore modified forest[%d]: %d became %d; it must not touch the tree",
				i, beforeForest[i], afterForest[i])
		}
	}
	for i := range beforeBuckets {
		if beforeBuckets[i] != afterBuckets[i] {
			t.Fatalf("findMatchesNoStore modified buckets[%d]: %d became %d", i, beforeBuckets[i], afterBuckets[i])
		}
	}
}

func TestStoreAndFindMatchesStillAgreesWithTheCombinedWalkAtFullLength(t *testing.T) {
	hOld, data, mask := h10Fixture(t)
	hNew, _, _ := h10Fixture(t)
	maxBackward := h10MaxBackward(hOld)

	oldMatches := make([]backwardMatch, h10MaxNumMatches)
	newMatches := make([]backwardMatch, h10MaxNumMatches)
	for i := range uint(h10Positions) {
		oldBest, newBest := uint(0), uint(0)
		maxLength := uint(h10BenchSize) - i
		nOld := h10StoreAndFindMatchesBefore(hOld, data, i, mask, maxLength, maxBackward, &oldBest, oldMatches)
		nNew := hNew.storeAndFindMatches(data, i, mask, maxLength, maxBackward, &newBest, newMatches)

		if nOld != nNew || oldBest != newBest {
			t.Fatalf("pos=%d: split walk returned %d matches (bestLen %d), combined %d (bestLen %d)",
				i, nNew, newBest, nOld, oldBest)
		}
		for k := range nOld {
			if oldMatches[k] != newMatches[k] {
				t.Fatalf("pos=%d match %d: %+v vs %+v", i, k, newMatches[k], oldMatches[k])
			}
		}
	}

	oldForest, _ := h10TreeState(hOld)
	newForest, _ := h10TreeState(hNew)
	for i := range oldForest {
		if oldForest[i] != newForest[i] {
			t.Fatalf("forest[%d] diverged: %d vs %d", i, newForest[i], oldForest[i])
		}
	}
}

func BenchmarkH10Store16384Positions(b *testing.B) {
	h, data, mask := h10Fixture(b)
	maxBackward := h10MaxBackward(h)
	b.Run("impl=before_combined_walk", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(h10Positions)
		for range b.N {
			b.StopTimer()
			h.reset(true, h10BenchSize, nil)
			b.StartTimer()
			for i := range uint(h10Positions) {
				h10StoreAndFindMatchesBefore(h, data, i, mask, h10MaxTreeCompLength, maxBackward, nil, nil)
			}
		}
	})
	b.Run("impl=after_store_only", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(h10Positions)
		for range b.N {
			b.StopTimer()
			h.reset(true, h10BenchSize, nil)
			b.StartTimer()
			for i := range uint(h10Positions) {
				h.storeOnly(data, i, mask, maxBackward)
			}
		}
	})
}

func benchmarkH10FindMatchesNoStore(b *testing.B, maxLength uint) {
	h, data, mask := h10Fixture(b)
	maxBackward := h10MaxBackward(h)
	for i := range uint(h10Positions) {
		h.storeOnly(data, i, mask, maxBackward)
	}
	matches := make([]backwardMatch, h10MaxNumMatches)
	const searches = 4096

	b.Run("impl=before_combined_walk", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(searches)
		for range b.N {
			for i := uint(h10Positions); i < h10Positions+searches; i++ {
				bestLen := uint(0)
				h10MatchSink = h10StoreAndFindMatchesBefore(h, data, i, mask, maxLength, maxBackward, &bestLen, matches)
				h10LenSink = bestLen
			}
		}
	})
	b.Run("impl=after_find_only", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(searches)
		for range b.N {
			for i := uint(h10Positions); i < h10Positions+searches; i++ {
				bestLen := uint(0)
				h10MatchSink = h.findMatchesNoStore(data, i, mask, maxLength, maxBackward, &bestLen, matches)
				h10LenSink = bestLen
			}
		}
	})
}

func BenchmarkH10FindMatchesNoStoreMaxLen64(b *testing.B)  { benchmarkH10FindMatchesNoStore(b, 64) }
func BenchmarkH10FindMatchesNoStoreMaxLen127(b *testing.B) { benchmarkH10FindMatchesNoStore(b, 127) }

func BenchmarkH10StoreAndFindMatches16384Positions(b *testing.B) {
	h, data, mask := h10Fixture(b)
	maxBackward := h10MaxBackward(h)
	matches := make([]backwardMatch, h10MaxNumMatches)
	b.Run("impl=before_combined_walk", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(h10Positions)
		for range b.N {
			b.StopTimer()
			h.reset(true, h10BenchSize, nil)
			b.StartTimer()
			for i := range uint(h10Positions) {
				bestLen := uint(0)
				h10MatchSink = h10StoreAndFindMatchesBefore(h, data, i, mask, h10BenchSize-i, maxBackward, &bestLen, matches)
			}
		}
	})
	b.Run("impl=after_split_walk", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(h10Positions)
		for range b.N {
			b.StopTimer()
			h.reset(true, h10BenchSize, nil)
			b.StartTimer()
			for i := range uint(h10Positions) {
				bestLen := uint(0)
				h10MatchSink = h.storeAndFindMatches(data, i, mask, h10BenchSize-i, maxBackward, &bestLen, matches)
			}
		}
	})
}

var h10FamSink uint

// benchmarkH10FindAllMatches measures the whole phase-1-then-tree-search path,
// which is where the hoisted bucket and forest probes can overlap with work.
func benchmarkH10FindAllMatches(b *testing.B, quality int) {
	h, data, mask := h10Fixture(b)
	h.quality = quality
	maxBackward := h10MaxBackward(h)
	matches := make([]backwardMatch, 4*h10MaxNumMatches)
	const searches = 4096

	b.ReportAllocs()
	b.SetBytes(searches)
	for range b.N {
		b.StopTimer()
		h.reset(true, h10BenchSize, nil)
		for i := range uint(h10Positions) {
			h.storeOnly(data, i, mask, maxBackward)
		}
		b.StartTimer()
		for i := uint(h10Positions); i < h10Positions+searches; i++ {
			h10FamSink = h.findAllMatches(data, mask, i, h10BenchSize-i, maxBackward,
				maxBackward, quality, matches)
		}
	}
}

func BenchmarkH10FindAllMatchesQ10(b *testing.B) { benchmarkH10FindAllMatches(b, 10) }
func BenchmarkH10FindAllMatchesQ11(b *testing.B) { benchmarkH10FindAllMatches(b, 11) }
