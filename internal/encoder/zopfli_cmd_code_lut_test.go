package encoder

import (
	"errors"
	"fmt"
	"math/bits"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

const zopfliReplayCorpus = "../../brotli-ref/tests/testdata/*.txt"

type updateNodesFunc func(nodes []zopfliNode, ringbuffer []byte, startingDistCache []int, matches []backwardMatch, model *zopfliCostModel, queue *startPosQueue, numBytes, blockStart, pos, ringBufferMask, maxBackwardLimit, gap uint, compound *compoundDictionary, numMatches uint, quality int) uint

type zopfliReplayInput struct {
	name string
	data []byte
}

type zopfliReplayFixture struct {
	ringbuffer       []byte
	matches          []backwardMatch
	numMatches       []uint32
	model            zopfliCostModel
	distCache        [4]int
	numBytes         uint
	ringBufferMask   uint
	maxBackwardLimit uint
	quality          int
}

func updateNodesWithScratch(sc *dcScratch) updateNodesFunc {
	return func(nodes []zopfliNode, ringbuffer []byte, startingDistCache []int, matches []backwardMatch, model *zopfliCostModel, queue *startPosQueue, numBytes, blockStart, pos, ringBufferMask, maxBackwardLimit, gap uint, compound *compoundDictionary, numMatches uint, quality int) uint {
		return updateNodes(nodes, ringbuffer, startingDistCache, matches, model, queue, numBytes, blockStart, pos, ringBufferMask, maxBackwardLimit, gap, compound, numMatches, quality, sc)
	}
}

type zopfliReplayRun struct {
	update updateNodesFunc
	nodes  []zopfliNode
	queue  startPosQueue
	skip   uint
}

func zopfliReplayFiles(tb testing.TB, patterns ...string) []zopfliReplayInput {
	tb.Helper()
	var inputs []zopfliReplayInput
	for _, pattern := range patterns {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			tb.Fatalf("glob %s: %v", pattern, err)
		}
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
			inputs = append(inputs, zopfliReplayInput{filepath.Base(p), data})
		}
	}
	return inputs
}

func newZopfliReplayFixture(in []byte, quality int) *zopfliReplayFixture {
	const lgwin = 24
	numBytes := uint(len(in))
	fx := &zopfliReplayFixture{
		ringbuffer:       make([]byte, 1<<bits.Len(numBytes)),
		distCache:        [4]int{4, 11, 15, 16},
		numBytes:         numBytes,
		maxBackwardLimit: 1<<lgwin - core.WindowGap,
		quality:          quality,
	}
	copy(fx.ringbuffer, in)
	fx.ringBufferMask = uint(len(fx.ringbuffer) - 1)

	h := &h10{lgwin: lgwin, quality: quality}
	h.reset(true, numBytes, nil)
	bufs := &q10Bufs{
		hqNumMatchesArr: make([]uint32, numBytes),
		hqMatches:       make([]backwardMatch, hqMatchesPerByte*numBytes),
	}
	col := &bufs.hqCollector
	col.bufs = bufs
	col.hasher = h
	col.ringbuffer = fx.ringbuffer
	col.numBytes = numBytes
	col.ringBufferMask = fx.ringBufferMask
	col.maxBackwardLimit = fx.maxBackwardLimit
	if numBytes >= h10MaxTreeCompLength {
		col.storeEnd = numBytes - h10MaxTreeCompLength + 1
	}
	col.quality = quality
	col.collect()
	fx.matches = bufs.hqMatches
	fx.numMatches = bufs.hqNumMatchesArr

	fx.model.init(64, numBytes)
	fx.model.setFromLiteralCosts(0, fx.ringbuffer, fx.ringBufferMask)
	return fx
}

func (fx *zopfliReplayFixture) replay(runs []*zopfliReplayRun, step func(pos uint) error) error {
	maxZopfli := maxZopfliLen(fx.quality)
	for _, r := range runs {
		initZopfliNodes(r.nodes)
		r.nodes[0].length = 0
		r.nodes[0].setCost(0)
		r.queue = startPosQueue{}
	}
	curMatchPos := uint(0)
	for i := uint(0); i+3 < fx.numBytes; i++ {
		for _, r := range runs {
			r.skip = r.update(r.nodes, fx.ringbuffer, fx.distCache[:], fx.matches[curMatchPos:], &fx.model, &r.queue,
				fx.numBytes, 0, i, fx.ringBufferMask, fx.maxBackwardLimit, 0, nil, uint(fx.numMatches[i]), fx.quality)
		}
		if step != nil {
			if err := step(i); err != nil {
				return err
			}
		}
		skip := runs[0].skip
		if skip < longCopyQuickStep {
			skip = 0
		}
		curMatchPos += uint(fx.numMatches[i])
		if fx.numMatches[i] == 1 && fx.matches[curMatchPos-1].matchLength() > maxZopfli {
			skip = max(fx.matches[curMatchPos-1].matchLength(), skip)
		}
		for ; skip > 1; skip-- {
			i++
			if i+3 >= fx.numBytes {
				break
			}
			for _, r := range runs {
				evaluateNode(r.nodes, i, 0, fx.maxBackwardLimit, 0, fx.distCache[:], &fx.model, &r.queue)
			}
			curMatchPos += uint(fx.numMatches[i])
		}
	}
	return nil
}

func TestCmdCodeLUTHoldsCombineLengthCodesForEveryInsertCodeCopyCodeAndLastDistanceFlag(t *testing.T) {
	var errs []error
	for row, useLastDistance := range []bool{false, true} {
		for insCode := range uint16(24) {
			for copyCode := range uint16(24) {
				got := cmdCodeLUT[row][insCode][copyCode]
				want := combineLengthCodes(insCode, copyCode, useLastDistance)
				if got != want {
					errs = append(errs, fmt.Errorf("cmdCodeLUT[%d][%d][%d] = %d, combineLengthCodes(%d, %d, %t) = %d",
						row, insCode, copyCode, got, insCode, copyCode, useLastDistance, want))
				}
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("the Zopfli DP reads its insert-and-copy command codes from this table instead of calling combineLengthCodes, "+
			"so every reachable entry must equal the function or the q10/q11 cost model prices the wrong symbol:\n%v", err)
	}
}

func TestUpdateNodesWithTheCommandCodeTableMakesEveryDPDecisionOfTheCombineLengthCodesVersionOnRealAndEdgeInputs(t *testing.T) {
	inputs := zopfliReplayFiles(t, "../../testdata/*", zopfliReplayCorpus)
	if len(inputs) == 0 {
		t.Fatal("no input files found; the comparison needs real data to mean anything")
	}
	rng := rand.New(rand.NewPCG(7, 11))
	random := make([]byte, 30000)
	for i := range random {
		random[i] = byte(rng.Uint32())
	}
	inputs = append(inputs,
		zopfliReplayInput{"zeros", make([]byte, 70000)},
		zopfliReplayInput{"random_then_repeat", slices.Concat(random, random, random[:1000])},
	)
	for _, in := range inputs {
		for _, quality := range []int{10, 11} {
			t.Run(fmt.Sprintf("q%d/%s", quality, in.name), func(t *testing.T) {
				fx := newZopfliReplayFixture(in.data, quality)
				before := &zopfliReplayRun{update: updateNodesBefore, nodes: make([]zopfliNode, fx.numBytes+1)}
				after := &zopfliReplayRun{update: updateNodesWithScratch(new(dcScratch)), nodes: make([]zopfliNode, fx.numBytes+1)}
				err := fx.replay([]*zopfliReplayRun{before, after}, func(pos uint) error {
					end := pos + before.skip + 1
					if after.skip != before.skip || after.queue != before.queue || !slices.Equal(after.nodes[pos:end], before.nodes[pos:end]) {
						return fmt.Errorf("position %d: updateNodes returned %d, the combineLengthCodes version %d, or left a different queue or node window; "+
							"the table lookup must reproduce every DP update or the q10/q11 commands stop being byte-identical with the C reference",
							pos, after.skip, before.skip)
					}
					return nil
				})
				if err == nil && (!slices.Equal(after.nodes, before.nodes) || after.queue != before.queue) {
					err = errors.New("the final node arrays or queues differ although every step matched")
				}
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func BenchmarkUpdateNodesDPReplay(b *testing.B) {
	for _, in := range zopfliReplayFiles(b, zopfliReplayCorpus) {
		kind, _, _ := strings.Cut(in.name, "_")
		for _, quality := range []int{10, 11} {
			fx := newZopfliReplayFixture(in.data[:min(len(in.data), 1<<18)], quality)
			run := &zopfliReplayRun{nodes: make([]zopfliNode, fx.numBytes+1)}
			runs := []*zopfliReplayRun{run}
			for _, impl := range []struct {
				name   string
				update updateNodesFunc
			}{{"before", updateNodesBefore}, {"after", updateNodesWithScratch(new(dcScratch))}} {
				b.Run(fmt.Sprintf("q%d/%s/impl=%s", quality, kind, impl.name), func(b *testing.B) {
					run.update = impl.update
					b.ReportAllocs()
					b.SetBytes(int64(fx.numBytes))
					for i := 0; i < b.N; i++ {
						if err := fx.replay(runs, nil); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
