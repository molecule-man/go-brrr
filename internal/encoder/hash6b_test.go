package encoder

import (
	"bytes"
	"errors"
	"fmt"
	"math/bits"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/molecule-man/go-brrr/internal/core"
)

func (h *h6b[B]) findLongestMatchBefore(
	data []byte, ringBufferMask uint,
	distCache *[16]int,
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

	_ = data[ringBufferMask]

	blockSize := uint(unsafe.Sizeof(h.buckets)) / h6bBucketSize / 4
	blockShift := uint(bits.TrailingZeros(blockSize))
	blockMask := blockSize - 1
	curMasked := cur & ringBufferMask
	bestScore := out.score
	bestLen := out.len
	key := h6bHash(data, curMasked)

	nextKey := h6bHash(data, (cur+1)&ringBufferMask)
	nextBucket := bucketRingAt(unsafe.Pointer(&h.buckets), nextKey, blockShift)
	nextN := h.num[nextKey]
	h.nextBucket = nextBucket.at(0)
	if nextN > 0 {
		p := uint(nextBucket.at(uint((nextN-1)&uint16(blockMask)))) & ringBufferMask
		h.nextBucket = uint32(data[p])
	}

	out.len = 0
	out.lenCodeDelta = 0

	backward := uint(distCache[0])
	if backward-1 < maxBackward {
		prev := (cur - backward) & ringBufferMask
		if loadByte(data, curMasked+bestLen) == loadByte(data, prev+bestLen) {
			ml := uint(matchLenAtNoInline(data, prev, curMasked, int(maxLength)))
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
	backward = uint(distCache[1])
	if backward-1 < maxBackward {
		prev := (cur - backward) & ringBufferMask
		if loadByte(data, curMasked+bestLen) == loadByte(data, prev+bestLen) {
			ml := uint(matchLenAtNoInline(data, prev, curMasked, int(maxLength)))
			if ml >= 2 {
				score := backwardReferenceScoreUsingLastDistance(ml)
				if bestScore < score {
					score -= 39
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
	for i := uint(2); i < h6bNumLastDistances; i++ {
		backward := uint(distCache[i])
		if backward-1 >= maxBackward {
			continue
		}
		prev := (cur - backward) & ringBufferMask

		if loadByte(data, curMasked+bestLen) != loadByte(data, prev+bestLen) {
			continue
		}

		ml := uint(matchLenAtNoInline(data, prev, curMasked, int(maxLength)))
		if ml >= 3 {
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

	if bestLen < 3 {
		bestLen = 3
	}

	bucket := bucketRingAt(unsafe.Pointer(&h.buckets), key, blockShift)
	n := h.num[key]
	down := uint(0)
	if uint(n) > blockSize {
		down = uint(n) - blockSize
	}
	minPrev := cur - maxBackward
	curProbe := loadU32LE(data, curMasked+bestLen-3)
	for i := uint(n); i > down; {
		i--
		prevRaw := uint(bucket.at(i & blockMask))
		if prevRaw < minPrev {
			break
		}
		prevMasked := prevRaw & ringBufferMask
		if curProbe != loadU32LE(data, prevMasked+bestLen-3) {
			continue
		}

		ml := uint(matchLenAtNoInline(data, prevMasked, curMasked, int(maxLength)))
		if ml >= 4 {
			backward := cur - prevRaw
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

	bucket.put(uint(h.num[key])&blockMask, uint32(cur))
	h.num[key]++

	if out.score == minScore {
		searchStaticDictionaryDeep(data[curMasked:], maxLength, dictDistance, maxBackwardDistance,
			dictNumLookups, dictNumMatches, out)
	}
}

type h6bInput struct {
	name  string
	data  []byte
	lgwin uint
}

type h6bSearch func(data []byte, mask uint, distCache *[16]int, cur, maxLength, maxBackward uint, out *hasherSearchResult) error

type h6bFind[B h6bBlock] func(h *h6b[B], data []byte, ringBufferMask uint, distCache *[16]int, cur, maxLength, maxBackward, dictDistance uint, dictNumLookups, dictNumMatches *uint, out *hasherSearchResult)

func h6bCorpus(tb testing.TB) []h6bInput {
	tb.Helper()
	names := []string{"plrabn12.txt", "lcet10.txt", "mapsdatazrh"}
	in := make([]h6bInput, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join("..", "..", "brotli-ref", "tests", "testdata", name))
		if err != nil {
			tb.Fatal(err)
		}
		in = append(in, h6bInput{name, data, 22})
	}
	return in
}

func h6bRingState(input h6bInput) *encodeState {
	return &encodeState{lgblock: 16, ringBufSize: 1 << (input.lgwin + 1)}
}

func h6bGreedyWalk(s *encodeState, input h6bInput, search h6bSearch, h interface {
	storeRange(data []byte, mask, start, end uint)
}) error {
	tailSize := 1 << s.lgblock
	maxBackwardLimit := uint(1)<<input.lgwin - core.WindowGap
	s.ringBufPos = 0
	s.inputPos = 0
	s.distCache = [4]uint{4, 11, 15, 16}
	var distCache [16]int
	for start := 0; start < len(input.data); start += tailSize {
		chunk := input.data[start:min(start+tailSize, len(input.data))]
		s.copyInputToRingBuffer(chunk)
		mask := uint(s.ringBufSize - 1)
		position := uint(start)
		posEnd := position + uint(len(chunk))
		storeEnd := position
		if len(chunk) >= h6bHashTypeLength {
			storeEnd = posEnd - h6bHashTypeLength + 1
		}
		for position+h6bHashTypeLength < posEnd {
			for i, d := range s.distCache {
				distCache[i] = int(d)
			}
			prepareDistanceCache10(&distCache)
			maxDistance := min(position, maxBackwardLimit)
			sr := hasherSearchResult{score: minScore}
			if err := search(s.data, mask, &distCache, position, posEnd-position, maxDistance, &sr); err != nil {
				return fmt.Errorf("%s lgwin=%d: %w", input.name, input.lgwin, err)
			}
			if sr.score <= minScore {
				position++
				continue
			}
			if sr.distance <= maxDistance && computeDistanceCode(sr.distance, maxDistance, &s.distCache) > 0 {
				s.distCache = [4]uint{sr.distance, s.distCache[0], s.distCache[1], s.distCache[2]}
			}
			h.storeRange(s.data, mask, position+2, min(position+sr.len, storeEnd))
			position += sr.len
		}
	}
	return nil
}

type h6bPair[B h6bBlock] struct {
	after, before h6b[B]
}

func (p *h6bPair[B]) storeRange(data []byte, mask, start, end uint) {
	p.after.storeRange(data, mask, start, end)
	p.before.storeRange(data, mask, start, end)
}

func h6bCompareUnrolledWithLoop[B h6bBlock](input h6bInput) error {
	p := new(h6bPair[B])
	p.after.reset(false, 0, nil)
	p.before.reset(false, 0, nil)
	var afterLookups, afterMatches, beforeLookups, beforeMatches uint
	search := func(data []byte, mask uint, distCache *[16]int, cur, maxLength, maxBackward uint, out *hasherSearchResult) error {
		want := *out
		p.before.findLongestMatchBefore(data, mask, distCache, cur, maxLength, maxBackward, maxBackward, &beforeLookups, &beforeMatches, &want)
		p.after.findLongestMatch(data, mask, distCache, cur, maxLength, maxBackward, maxBackward, &afterLookups, &afterMatches, out)
		if *out != want || p.after.nextBucket != p.before.nextBucket || afterLookups != beforeLookups || afterMatches != beforeMatches {
			return fmt.Errorf("cur=%d maxLength=%d maxBackward=%d distCache=%v: unrolled search gave %+v nextBucket=%d dict=%d/%d, the loop it replaces gave %+v nextBucket=%d dict=%d/%d, so q7/q8 output would stop matching the C reference",
				cur, maxLength, maxBackward, distCache[:h6bNumLastDistances],
				*out, p.after.nextBucket, afterLookups, afterMatches,
				want, p.before.nextBucket, beforeLookups, beforeMatches)
		}
		return nil
	}
	if err := h6bGreedyWalk(h6bRingState(input), input, search, p); err != nil {
		return err
	}
	if p.after.num != p.before.num || p.after.buckets != p.before.buckets {
		return fmt.Errorf("%s lgwin=%d: hash table contents differ after the walk, so later positions would search different candidates", input.name, input.lgwin)
	}
	return nil
}

func TestH6bUnrolledDistanceCacheSearchReturnsTheSameMatchesAndHashStateAsTheLoopItReplaces(t *testing.T) {
	var inputs []h6bInput
	for _, name := range []string{"gh_172KB.html", "reactcore_187KB.js", "github_events_8k.json"} {
		data, err := os.ReadFile(filepath.Join("../../testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, h6bInput{name + " wrapping a 128 KiB ring", data, 16})
	}
	corpus := h6bCorpus(t)
	var all []byte
	for _, in := range corpus {
		all = append(all, in.data...)
	}
	if len(all) > 0 {
		inputs = append(inputs, h6bInput{"1 MiB corpus concatenated, wrapping a 1 MiB ring", all, 19})
	}
	inputs = append(inputs, corpus...)
	rnd := rand.New(rand.NewSource(1))
	random := make([]byte, 100000)
	rnd.Read(random)
	var drift bytes.Buffer
	for drift.Len() < 200000 {
		drift.WriteString("the quick brown fox jumps over the lazy dog")
		drift.Write(bytes.Repeat([]byte{' '}, rnd.Intn(7)))
	}
	inputs = append(inputs,
		h6bInput{"zeros, where dist 1 makes near-miss cache entries 0 and negative", make([]byte, 300000), 16},
		h6bInput{"random bytes, where phase 3 runs the dictionary on almost every position", random, 16},
		h6bInput{"sentence with 0-6 random spaces, so the last-distance -3..+3 entries win", drift.Bytes(), 16},
		h6bInput{"input shorter than one block, which takes the small-buffer path", bytes.Repeat([]byte("abcdefgh abcdefg "), 60), 22},
	)
	errs := make([]error, 0, 2*len(inputs))
	for _, in := range inputs {
		errs = append(errs, h6bCompareUnrolledWithLoop[[h6b6BlockSize]uint32](in), h6bCompareUnrolledWithLoop[[h6b7BlockSize]uint32](in))
	}
	if err := errors.Join(errs...); err != nil {
		t.Error(err)
	}
}

func benchmarkH6bGreedyWalk[B h6bBlock](b *testing.B, input h6bInput, find h6bFind[B]) {
	b.ReportAllocs()
	h := new(h6b[B])
	s := h6bRingState(input)
	var lookups, matches uint
	search := func(data []byte, mask uint, distCache *[16]int, cur, maxLength, maxBackward uint, out *hasherSearchResult) error {
		find(h, data, mask, distCache, cur, maxLength, maxBackward, maxBackward, &lookups, &matches, out)
		return nil
	}
	b.SetBytes(int64(len(input.data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.reset(false, 0, nil)
		if err := h6bGreedyWalk(s, input, search, h); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkH6bFindLongestMatchGreedyWalk(b *testing.B) {
	corpus := h6bCorpus(b)
	if len(corpus) == 0 {
		b.Fatal("no reference files under brotli-ref/tests/testdata")
	}
	for _, in := range corpus {
		b.Run("q7_"+in.name+"/impl=before", func(b *testing.B) {
			benchmarkH6bGreedyWalk(b, in, (*h6b6).findLongestMatchBefore)
		})
		b.Run("q7_"+in.name+"/impl=after", func(b *testing.B) {
			benchmarkH6bGreedyWalk(b, in, (*h6b6).findLongestMatch)
		})
		b.Run("q8_"+in.name+"/impl=before", func(b *testing.B) {
			benchmarkH6bGreedyWalk(b, in, (*h6b7).findLongestMatchBefore)
		})
		b.Run("q8_"+in.name+"/impl=after", func(b *testing.B) {
			benchmarkH6bGreedyWalk(b, in, (*h6b7).findLongestMatch)
		})
	}
}
