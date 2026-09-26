package encoder

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

type h10Before struct {
	h10
}

func (h *h10Before) reset(oneShot bool, inputSize uint, _ []byte) {
	lgwin := h.lgwin
	h.windowMask = (1 << lgwin) - 1
	h.invalidPos = 0 - h.windowMask

	numNodes := uint(1) << lgwin
	if oneShot && inputSize < numNodes {
		numNodes = inputSize
	}
	if len(h.forest) < int(2*numNodes) {
		h.forest = make([]uint32, 2*numNodes)
	}

	for i := range h.buckets {
		h.buckets[i] = h.invalidPos
	}
	h.ready = true
}

func (h *h10Before) stitchToPreviousBlock(numBytes, position uint, ringBuffer []byte, ringBufferMask uint) {
	if numBytes < 3 || position < h10MaxTreeCompLength {
		return
	}

	iStart := position - h10MaxTreeCompLength + 1
	iEnd := min(position, iStart+numBytes)

	for i := iStart; i < iEnd; i++ {
		maxBackward := uint(h.windowMask) - max(core.WindowGap-1, position-i)
		h.storeOnly(ringBuffer, i, ringBufferMask, maxBackward)
	}
}

type h10Stream func(c Compressor, dst io.Writer) error

type h10ForestRun struct {
	outputs  [][]byte
	forest   int
	snapshot int
}

func compressWithFreshH10(tb testing.TB, before, parallel bool, quality, lgwin int, streams ...h10Stream) h10ForestRun {
	tb.Helper()
	e := NewCompressor(quality, lgwin, 0, parallel).(*encoderSplit)
	defer e.Release()
	releaseHasher(e.hasher)
	h := new(h10Before)
	h.lgwin, h.quality, h.bufs = lgwin, quality, &e.q10
	e.hasher = &h.h10
	if before {
		e.hasher = h
	}
	e.resetHasher()
	e.q10.hqHasherSnap = nil

	var run h10ForestRun
	for i, stream := range streams {
		if i > 0 {
			e.Reset()
		}
		var out bytes.Buffer
		if err := stream(e, &out); err != nil {
			tb.Fatal(err)
		}
		run.outputs = append(run.outputs, out.Bytes())
	}
	run.forest = len(h.forest)
	run.snapshot = cap(e.q10.hqHasherSnap)
	return run
}

func h10WriteAll(in []byte) h10Stream {
	return func(c Compressor, dst io.Writer) error {
		if _, err := c.Write(dst, in); err != nil {
			return err
		}
		return c.Close(dst)
	}
}

func h10WriteFlushing(in []byte) h10Stream {
	return func(c Compressor, dst io.Writer) error {
		if _, err := c.Write(dst, in[:100]); err != nil {
			return err
		}
		if err := c.Flush(dst); err != nil {
			return err
		}
		for i, off := 1, 100; off < len(in); i, off = i+1, off+4097 {
			if _, err := c.Write(dst, in[off:min(off+4097, len(in))]); err != nil {
				return err
			}
			if i%16 == 0 {
				if err := c.Flush(dst); err != nil {
					return err
				}
			}
		}
		return c.Close(dst)
	}
}

func h10WriteFrom(pos uint64, in []byte) h10Stream {
	return func(c Compressor, dst io.Writer) error {
		if !SeedStreamPosForTest(c, pos) {
			return errors.New("the stream position can only be seeded on a streaming encoder")
		}
		return h10WriteAll(in)(c, dst)
	}
}

type h10ForestInput struct {
	name string
	data []byte
}

func h10ForestInputs(tb testing.TB) (html, js []byte, large []h10ForestInput) {
	tb.Helper()
	var err error
	if html, err = os.ReadFile("../../testdata/gh_172KB.html"); err != nil {
		tb.Fatal(err)
	}
	if js, err = os.ReadFile("../../testdata/reactcore_187KB.js"); err != nil {
		tb.Fatal(err)
	}
	both := append(append([]byte(nil), html...), js...)
	rng := rand.New(rand.NewPCG(10, 11))
	block := make([]byte, 64<<10)
	for i := range block {
		block[i] = byte(rng.Uint32())
	}
	flipped := bytes.Repeat(block, 10)
	for off := 0; off < len(flipped); off += len(block) {
		flipped[off+rng.IntN(len(block))] ^= 0xff
	}
	names := []string{"plrabn12.txt", "lcet10.txt", "mapsdatazrh"}
	large = make([]h10ForestInput, 0, 4+len(names))
	large = append(large,
		h10ForestInput{"html+js", both},
		h10ForestInput{"html+js_x3", bytes.Repeat(both, 3)},
		h10ForestInput{"zeros_600KiB", make([]byte, 600<<10)},
		h10ForestInput{"random_64KiB_x10_one_flipped_byte_each", flipped},
	)
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join("..", "..", "brotli-ref", "tests", "testdata", name))
		if err != nil {
			tb.Fatal(err)
		}
		large = append(large, h10ForestInput{name, data})
	}
	return html, js, large
}

func TestH10ForestGrownBlockByBlockCompressesByteForByteLikeTheWholeWindowForestItReplaces(t *testing.T) {
	html, js, large := h10ForestInputs(t)
	both := large[0].data

	check := func(name string, lgwin int, streams ...h10Stream) {
		for _, quality := range []int{10, 11} {
			for _, parallel := range []bool{false, true} {
				t.Run(fmt.Sprintf("q%d/parallel=%v/%s", quality, parallel, name), func(t *testing.T) {
					t.Parallel()
					want := compressWithFreshH10(t, true, parallel, quality, lgwin, streams...)
					got := compressWithFreshH10(t, false, parallel, quality, lgwin, streams...)
					for i := range want.outputs {
						if !bytes.Equal(got.outputs[i], want.outputs[i]) {
							t.Errorf("stream %d: %d bytes with the forest grown per block, %d with the whole-window "+
								"forest; every slot the tree reads was written in this stream and growing copies "+
								"them all, so the matches and therefore the output must be identical",
								i, len(got.outputs[i]), len(want.outputs[i]))
						}
					}
				})
			}
		}
	}

	for _, in := range large {
		check(in.name+"/lgwin22", 22, h10WriteAll(in.data))
	}
	for _, in := range []h10ForestInput{{"html", html}, {"js", js}} {
		check(in.name+"/lgwin16_window_wraps", 16, h10WriteAll(in.data))
		check(in.name+"/lgwin10_forest_capped_by_the_window", 10, h10WriteAll(in.data))
	}
	check("html+js/100_byte_first_block_then_4097_byte_writes_and_flushes", 22, h10WriteFlushing(both))
	check("reused_encoder/html_then_html+js_x3_then_js", 22,
		h10WriteAll(html), h10WriteAll(large[1].data), h10WriteAll(js))
	check("html+js/across_the_32_bit_position_wrap", 18, h10WriteFrom((3<<30)-(128<<10), both))
}

func TestH10ForestAndQ10SnapshotFollowTheInputSeenSoFarInsteadOfReservingTheWholeWindow(t *testing.T) {
	_, _, large := h10ForestInputs(t)
	in := large[1].data
	const lgwin = 22
	run := compressWithFreshH10(t, false, true, 10, lgwin, h10WriteAll(in))
	if run.forest < 2*len(in) || run.forest > 4*len(in) {
		t.Errorf("a %d-byte q10 stream at lgwin %d left a forest of %d entries; it needs 2 per byte seen and "+
			"may round up to twice that, but must not reserve the %d entries of the whole window",
			len(in), lgwin, run.forest, 2<<lgwin)
	}
	if run.snapshot >= 2<<lgwin {
		t.Errorf("the q10 hasher snapshot has capacity %d; it copies the forest in use plus the buckets, so it "+
			"must follow the grown forest instead of the %d-entry window", run.snapshot, 2<<lgwin)
	}
}
