package encoder

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

type h54Input struct {
	name string
	data []byte
}

func h54CorpusInputs(tb testing.TB) []h54Input {
	tb.Helper()
	var paths []string
	for _, pattern := range []string{"../../testdata/*", "../../brotli-ref/tests/testdata/*.txt"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			tb.Fatalf("glob %s: %v", pattern, err)
		}
		paths = append(paths, matches...)
	}
	var inputs []h54Input
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			tb.Fatalf("stat %s: %v", p, err)
		}
		if fi.IsDir() {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			tb.Fatalf("read %s: %v", p, err)
		}
		inputs = append(inputs, h54Input{filepath.Base(p), data})
	}
	return inputs
}

func h54EdgeInputs() []h54Input {
	r := rand.New(rand.NewSource(1))
	random := make([]byte, 300000)
	r.Read(random)
	words := make([]byte, 0, 600016)
	for len(words) < 600000 {
		words = append(words, random[r.Intn(4096):][:3+r.Intn(12)]...)
		words = append(words, ' ')
	}
	patternLens := []int{0, 1, 7, 8, 9, 16, 17, 100}
	inputs := make([]h54Input, 0, 4+len(patternLens))
	inputs = append(inputs,
		h54Input{"zeros_300000", make([]byte, 300000)},
		h54Input{"random_300000", random},
		h54Input{"period2_300000", bytes.Repeat([]byte("ab"), 150000)},
		h54Input{"random_words_600000", words},
	)
	pattern := bytes.Repeat([]byte("abcdefgh"), 16)
	for _, n := range patternLens {
		inputs = append(inputs, h54Input{fmt.Sprintf("pattern_%d", n), pattern[:n]})
	}
	return inputs
}

func runH54Stream(h *h54, s *encodeState, input []byte, lgwin, blockSize int, before bool) {
	s.reset(4, lgwin, uint(len(input)))
	for off := 0; off < len(input); off += blockSize {
		block := input[off:min(off+blockSize, len(input))]
		s.copyInputToRingBuffer(block)
		if off == 0 {
			h.reset(len(block) == len(input), uint(len(input)), s.data)
		}
		pos := wrapPosition(uint64(off))
		h.stitchToPreviousBlock(uint(len(block)), pos, s.data, uint(s.mask))
		if before {
			h.createBackwardReferencesBefore(s, uint32(len(block)), uint32(pos))
		} else {
			h.createBackwardReferences(s, uint32(len(block)), uint32(pos))
		}
	}
}

func TestH54CreateBackwardReferencesMatchesPreChangeCopyOnCorpusAndEdgeCasesAcrossWrapAndBlockSizes(t *testing.T) {
	inputs := append(h54CorpusInputs(t), h54EdgeInputs()...)
	hBefore, hAfter := new(h54), new(h54)
	sBefore, sAfter := new(encodeState), new(encodeState)
	for _, in := range inputs {
		for _, lgwin := range []int{16, 18, 22} {
			for _, blockSize := range []int{1 << 16, 40000} {
				name := fmt.Sprintf("%s/lgwin=%d/block=%d", in.name, lgwin, blockSize)
				runH54Stream(hBefore, sBefore, in.data, lgwin, blockSize, true)
				runH54Stream(hAfter, sAfter, in.data, lgwin, blockSize, false)
				if !slices.Equal(sBefore.commands, sAfter.commands) {
					first := 0
					for first < min(len(sBefore.commands), len(sAfter.commands)) && sBefore.commands[first] == sAfter.commands[first] {
						first++
					}
					t.Errorf("%s: command streams diverge at index %d (before %d commands, after %d); the h54 change must emit exactly the commands the C-identical pre-change loop emits", name, first, len(sBefore.commands), len(sAfter.commands))
				}
				if sBefore.distCache != sAfter.distCache {
					t.Errorf("%s: distCache before=%v after=%v; the next metablock would encode different distance codes", name, sBefore.distCache, sAfter.distCache)
				}
				if sBefore.lastInsertLen != sAfter.lastInsertLen || sBefore.numLiterals != sAfter.numLiterals || sBefore.numCommands != sAfter.numCommands {
					t.Errorf("%s: lastInsertLen/numLiterals/numCommands before=%d/%d/%d after=%d/%d/%d; metablock bookkeeping must not change", name, sBefore.lastInsertLen, sBefore.numLiterals, sBefore.numCommands, sAfter.lastInsertLen, sAfter.numLiterals, sAfter.numCommands)
				}
				if hBefore.buckets != hAfter.buckets {
					t.Errorf("%s: hash buckets differ after the stream; the next block would probe different candidates", name)
				}
				if want := len(in.data) > int(sAfter.mask)+1; hAfter.everWrapped != want {
					t.Errorf("%s: everWrapped=%v, want %v; the no-wrap fast path must run exactly while every position stays below mask+1", name, hAfter.everWrapped, want)
				}
			}
		}
	}
}

func BenchmarkH54CreateBackwardReferences(b *testing.B) {
	for _, in := range h54CorpusInputs(b) {
		if len(in.data) < 256<<10 {
			continue
		}
		for _, impl := range []struct {
			name   string
			before bool
		}{{"before", true}, {"after", false}} {
			b.Run(in.name+"/impl="+impl.name, func(b *testing.B) {
				h, s := new(h54), new(encodeState)
				runH54Stream(h, s, in.data, 22, 1<<16, impl.before)
				b.ReportAllocs()
				b.SetBytes(int64(len(in.data)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					runH54Stream(h, s, in.data, 22, 1<<16, impl.before)
				}
			})
		}
	}
}

func (h *h54) createBackwardReferencesBefore(s *encodeState, bytes, wrappedPos uint32) {
	data := s.data
	mask := uint(s.mask)
	maxBackwardLimit := (uint(1) << s.lgwin) - core.WindowGap
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

	for position+hashTypeLength < posEnd {
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
			guardByte := loadByte(data, curMasked)
			key := h.hash(data, curMasked)
			bestScore := sr.score
			bestLen := uint(0)

			hkey0 := key
			hkey1 := (key + 8) & h54BucketMask
			hkey2 := (key + 16) & h54BucketMask
			hkey3 := (key + 24) & h54BucketMask

			keyOut := (key + uint32(position&h54BucketSweepMsk)) & h54BucketMask

			prev0 := uint(buckets[hkey0])
			prev1 := uint(buckets[hkey1])
			prev2 := uint(buckets[hkey2])
			prev3 := uint(buckets[hkey3])

			limit := int(maxLength)

			{
				prev := position - lastDistance
				if prev < position {
					prev &= mask
					if guardByte == loadByte(data, prev+bestLen) {
						length := matchLenAt(data, prev, curMasked, limit)
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
				backward := position - prev0
				prev0 &= mask
				if guardByte == loadByte(data, prev0+bestLen) && backward != 0 && backward <= maxDistance {
					length := matchLenAt(data, prev0, curMasked, limit)
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
				backward := position - prev1
				prev1 &= mask
				if guardByte == loadByte(data, prev1+bestLen) && backward != 0 && backward <= maxDistance {
					length := matchLenAt(data, prev1, curMasked, limit)
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
				backward := position - prev2
				prev2 &= mask
				if guardByte == loadByte(data, prev2+bestLen) && backward != 0 && backward <= maxDistance {
					length := matchLenAt(data, prev2, curMasked, limit)
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
				backward := position - prev3
				prev3 &= mask
				if guardByte == loadByte(data, prev3+bestLen) && backward != 0 && backward <= maxDistance {
					length := matchLenAt(data, prev3, curMasked, limit)
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

			buckets[keyOut] = uint32(position)
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
					bestLen := sr2.len
					guardByte := loadByte(data, curMasked+bestLen)
					key := h.hash(data, curMasked)
					bestScore := sr2.score

					hkey0 := key
					hkey1 := (key + 8) & h54BucketMask
					hkey2 := (key + 16) & h54BucketMask
					hkey3 := (key + 24) & h54BucketMask

					keyOut := (key + uint32(cur2&h54BucketSweepMsk)) & h54BucketMask

					prev0 := uint(buckets[hkey0])
					prev1 := uint(buckets[hkey1])
					prev2 := uint(buckets[hkey2])
					prev3 := uint(buckets[hkey3])

					limit := int(maxLength)

					{
						prev := cur2 - lastDistance
						if prev < cur2 {
							prev &= mask
							if guardByte == loadByte(data, prev+bestLen) {
								length := matchLenAt(data, prev, curMasked, limit)
								if length >= 4 {
									score := backwardReferenceScoreUsingLastDistance(uint(length))
									if bestScore < score {
										bestLen = uint(length)
										sr2.len = bestLen
										sr2.distance = lastDistance
										sr2.score = score
										bestScore = score
										guardByte = loadByte(data, curMasked+bestLen)
									}
								}
							}
						}
					}

					{
						backward := cur2 - prev0
						prev0 &= mask
						if guardByte == loadByte(data, prev0+bestLen) && backward != 0 && backward <= maxDistance {
							length := matchLenAt(data, prev0, curMasked, limit)
							if length >= 4 {
								score := backwardReferenceScore(uint(length), backward)
								if bestScore < score {
									bestLen = uint(length)
									sr2.len = bestLen
									guardByte = loadByte(data, curMasked+bestLen)
									bestScore = score
									sr2.score = score
									sr2.distance = backward
								}
							}
						}
					}

					{
						backward := cur2 - prev1
						prev1 &= mask
						if guardByte == loadByte(data, prev1+bestLen) && backward != 0 && backward <= maxDistance {
							length := matchLenAt(data, prev1, curMasked, limit)
							if length >= 4 {
								score := backwardReferenceScore(uint(length), backward)
								if bestScore < score {
									bestLen = uint(length)
									sr2.len = bestLen
									guardByte = loadByte(data, curMasked+bestLen)
									bestScore = score
									sr2.score = score
									sr2.distance = backward
								}
							}
						}
					}

					{
						backward := cur2 - prev2
						prev2 &= mask
						if guardByte == loadByte(data, prev2+bestLen) && backward != 0 && backward <= maxDistance {
							length := matchLenAt(data, prev2, curMasked, limit)
							if length >= 4 {
								score := backwardReferenceScore(uint(length), backward)
								if bestScore < score {
									bestLen = uint(length)
									sr2.len = bestLen
									guardByte = loadByte(data, curMasked+bestLen)
									bestScore = score
									sr2.score = score
									sr2.distance = backward
								}
							}
						}
					}

					{
						backward := cur2 - prev3
						prev3 &= mask
						if guardByte == loadByte(data, prev3+bestLen) && backward != 0 && backward <= maxDistance {
							length := matchLenAt(data, prev3, curMasked, limit)
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

					buckets[keyOut] = uint32(cur2)
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

			s.commands = append(s.commands, newCommandSimpleDistBefore(
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
