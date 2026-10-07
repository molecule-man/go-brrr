package encoder

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

func TestUpdateNodesRescansAQueueEntryWhoseDistanceCacheDiffersFromAnEarlierOneOnlyInAnOlderDistance(t *testing.T) {
	const pos, numBytes = 2048, 4096
	rnd := rand.New(rand.NewSource(1))
	data := make([]byte, numBytes)
	rnd.Read(data)
	copy(data[pos:pos+80], data[pos-700:])
	copy(data[pos-300:pos-260], data[pos-700:])
	model := new(zopfliCostModel)
	model.init(64, numBytes)
	model.setFromLiteralCosts(0, data, numBytes-1)
	var errs []error
	// The first queue entry finds nothing. The second differs only in an older
	// distance that matches at pos, so reusing the first entry's scan loses the copy.
	for _, tc := range []struct {
		caches   [2][4]int
		distance uint32
		length   uint
	}{
		{[2][4]int{{11, 22, 33, 44}, {11, 22, 300, 44}}, 300, 40},
		{[2][4]int{{11, 22, 33, 44}, {11, 22, 33, 700}}, 700, 80},
	} {
		var queue startPosQueue
		for k, dc := range tc.caches {
			queue.push(&posData{pos: pos - 1 - uint(k), distanceCache: dc, costdiff: float32(k)})
		}
		nodes := make([]zopfliNode, numBytes+1)
		initZopfliNodes(nodes)
		var sc dcScratch
		updateNodes(nodes, data, []int{4, 11, 15, 16}, nil, model, &queue, numBytes, 0, pos, numBytes-1, 1<<22-core.WindowGap, 0, nil, 0, 11, &sc)
		n := &nodes[pos+tc.length]
		if n.copyDistance() != tc.distance || uint(n.copyLength()) != tc.length {
			errs = append(errs, fmt.Errorf("queue caches %v: nodes[pos+%d] holds a copy of %d bytes at distance %d, want %d bytes at distance %d",
				tc.caches, tc.length, n.copyLength(), n.copyDistance(), tc.length, tc.distance))
		}
	}
	if err := errors.Join(errs...); err != nil {
		t.Error(err)
	}
}

func shortCodeFilterScalar(ringbuffer []byte, curIxMasked, bestLen, ringBufferMask, maxDistance uint, dc *[4]int) (want uint16, regular bool) {
	d0, d1 := dc[0], dc[1]
	back := [core.NumDistanceShortCodes]uint{
		uint(d0), uint(d1), uint(dc[2]), uint(dc[3]),
		uint(d0 - 1), uint(d0 + 1), uint(d0 - 2), uint(d0 + 2), uint(d0 - 3), uint(d0 + 3),
		uint(d1 - 1), uint(d1 + 1), uint(d1 - 2), uint(d1 + 2), uint(d1 - 3), uint(d1 + 3),
	}
	continuation := ringbuffer[curIxMasked+bestLen]
	regular = true
	for j, backward := range back {
		if backward == 0 || backward > maxDistance {
			regular = false
			continue
		}
		prevIxMasked := (curIxMasked - backward) & ringBufferMask
		if prevIxMasked+bestLen <= ringBufferMask && ringbuffer[prevIxMasked+bestLen] == continuation {
			want |= 1 << j
		}
	}
	return want, regular
}

func TestShortCodeCandidatesEqualsTheScalarContinuationFilterForEveryLanePatternOfBothWindowsAndBothSingleDistances(t *testing.T) {
	const mask = 4095
	mismatch := [4]byte{0x01, 0x80, 0x7F, 0xFF}
	geometries := []struct{ cur, bestLen, d0, d1, d2, d3 uint }{
		{2000, 5, 100, 300, 700, 1000},
		{2000, 1, 300, 100, 1000, 700},
		{2000, 9, 100, 107, 50, 60},
		{2000, 2, 100, 103, 101, 97},
		{3000, 64, 4, 2000, 1, 2003},
	}
	var errs []error
	for _, g := range geometries {
		rb := make([]byte, mask+1)
		const cont = 0xA5
		rb[g.cur+g.bestLen] = cont
		dc := [4]int{int(g.d0), int(g.d1), int(g.d2), int(g.d3)}
		for a := range 128 {
			for b := range 128 {
				for c := range 4 {
					for w, d := range []uint{g.d0, g.d1} {
						lanes := [2]int{a, b}[w]
						base := g.cur - d - 4 + g.bestLen
						rb[base] = cont
						for k := range 7 {
							rb[base+1+uint(k)] = cont ^ mismatch[(a+b+k)&3]
							if lanes>>k&1 != 0 {
								rb[base+1+uint(k)] = cont
							}
						}
					}
					for i, d := range []uint{g.d2, g.d3} {
						rb[g.cur-d+g.bestLen] = cont ^ mismatch[(a+i)&3]
						if c>>i&1 != 0 {
							rb[g.cur-d+g.bestLen] = cont
						}
					}
					rb[g.cur+g.bestLen] = cont
					want, regular := shortCodeFilterScalar(rb, g.cur, g.bestLen, mask, 3000, &dc)
					got := shortCodeCandidates(rb, g.cur, g.bestLen, mask, 3000, &dc, cont)
					if !regular || got != want {
						errs = append(errs, fmt.Errorf("geometry %+v lanes d0=%07b d1=%07b singles=%02b: got %016b, scalar filter %016b (regular=%v)", g, a, b, c, got, want, regular))
					}
				}
			}
		}
	}
	if len(errs) > 0 {
		t.Errorf("the lane tables must map byte k+1 of the 8-byte window loaded at cur-d-4+bestLen to the RFC 7932 short code of distance d+3-k; %d of %d patterns differ, first ones:\n%v", len(errs), len(geometries)*128*128*4, errors.Join(errs[:min(len(errs), 8)]...))
	}
}

func TestShortCodeCandidatesKeepsAllSixteenCandidatesWhenACandidateIsIrregularOrAWindowWrapsAndFiltersExactlyOtherwise(t *testing.T) {
	const mask = 4095
	r := rand.New(rand.NewSource(7))
	rb := make([]byte, mask+1)
	for i := range rb {
		rb[i] = "ab"[r.Intn(2)]
	}
	var errs []error
	checked, filtered := 0, 0
	for _, maxDistance := range []uint{8, 12, 3000} {
		ds := []int{0, 1, 2, 3, 4, 5, 6, 7, 11, 60, int(maxDistance) - 4, int(maxDistance) - 3, int(maxDistance) - 2, int(maxDistance), int(maxDistance) + 1}
		for _, cur := range []uint{0, 3, 8, 11, 12, 13, 100, 2000, 4000, 4094} {
			for _, bestLen := range []uint{1, 2, 7, 64} {
				if cur+bestLen > mask {
					continue
				}
				for _, d0 := range ds {
					for _, d1 := range ds {
						for _, d2 := range []int{0, 1, 5, int(maxDistance), int(maxDistance) + 1} {
							for _, d3 := range []int{0, 2, int(maxDistance), int(maxDistance) + 1} {
								dc := [4]int{d0, d1, d2, d3}
								want, regular := shortCodeFilterScalar(rb, cur, bestLen, mask, maxDistance, &dc)
								wraps := (cur-uint(d0)-4)&mask+bestLen+7 > mask || (cur-uint(d1)-4)&mask+bestLen+7 > mask
								if regular && !wraps {
									filtered++
								} else {
									want = 0xFFFF
								}
								checked++
								if got := shortCodeCandidates(rb, cur, bestLen, mask, maxDistance, &dc, rb[cur+bestLen]); got != want {
									errs = append(errs, fmt.Errorf("cur=%d bestLen=%d maxDistance=%d dc=%v: got %016b, want %016b (regular=%v wraps=%v)", cur, bestLen, maxDistance, dc, got, want, regular, wraps))
								}
							}
						}
					}
				}
			}
		}
	}
	if len(errs) > 0 {
		t.Errorf("a compound or gray-area candidate is invisible to the ring-buffer bytes and a wrapped window is not contiguous, so both must disable the filter, while an eligible geometry must filter exactly; %d of %d cases differ, first ones:\n%v", len(errs), checked, errors.Join(errs[:min(len(errs), 8)]...))
	}
	if filtered == 0 || filtered == checked {
		t.Errorf("%d of %d geometries were eligible for filtering; the grid must exercise both the filtered path and the keep-everything fallback", filtered, checked)
	}
}
