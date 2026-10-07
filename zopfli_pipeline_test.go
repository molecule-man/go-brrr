package brrr

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/molecule-man/go-brrr/internal/creftest"
)

func periodicInputWhoseRingBufferWrapBlockIsSkippedByTheDPAloneThenProbed(lgwin int) []byte {
	ring := 1 << (1 + max(lgwin, 18))
	r := rand.New(rand.NewPCG(1, 2))
	fill := func(b []byte) {
		for i := range b {
			b[i] = byte(r.Uint32())
		}
	}

	period := make([]byte, 17<<10)
	fill(period)
	var in []byte
	for len(in) < ring+(256<<10) {
		in = append(in, period...)
	}
	in = in[:ring+(256<<10)]

	filler := make([]byte, 113)
	for off := 1002; off+48 < 200<<10; off += 8 * 97 {
		fill(filler)
		in = append(in, filler...)
		in = append(in, in[ring+off:ring+off+48]...)
	}
	fill(filler)
	return append(in, filler...)
}

func TestQ10MatchesTheCReferenceWhenTheDPSkipsABlockTheMatchCollectorAlreadyHashedDensely(t *testing.T) {
	t.Parallel()

	for _, lgwin := range []int{18, 20} {
		in := periodicInputWhoseRingBufferWrapBlockIsSkippedByTheDPAloneThenProbed(lgwin)
		for _, quality := range []int{10, 11} {
			for _, workers := range []bool{false, true} {
				t.Run(fmt.Sprintf("lgwin%d/q%d/workers=%t", lgwin, quality, workers), func(t *testing.T) {
					t.Parallel()

					var got bytes.Buffer
					w, err := newWriter(&got, quality, WriterOptions{LGWin: lgwin, SizeHint: uint(len(in))}, workers)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := w.Write(in); err != nil {
						t.Fatal(err)
					}
					if err := w.Close(); err != nil {
						t.Fatal(err)
					}

					want := creftest.BrotliCompress(t, in, quality, lgwin, uint(len(in)))
					if !bytes.Equal(got.Bytes(), want) {
						t.Fatalf("q%d lgwin %d workers=%t output differs from the C reference (%d vs %d bytes): the DP skipped a long "+
							"copy the match collector did not, so its hasher diverged and the block must be redone serially",
							quality, lgwin, workers, got.Len(), len(want))
					}
				})
			}
		}
	}
}
