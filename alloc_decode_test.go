package brrr

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestReusedReaderDecodesAStreamWithoutAllocating(t *testing.T) {
	in := allocTestPayload(t)
	for _, level := range []int{1, 5, 11} {
		t.Run(fmt.Sprintf("q%d", level), func(t *testing.T) {
			comp, err := Compress(in, level)
			if err != nil {
				t.Fatal(err)
			}
			src := bytes.NewReader(comp)
			r := NewReader(src)
			out := make([]byte, len(in))
			allocs := testing.AllocsPerRun(3, func() {
				src.Reset(comp)
				r.Reset(src)
				if _, err := io.ReadFull(r, out); err != nil {
					t.Fatal(err)
				}
				if n, err := r.Read(out[:1]); n != 0 || !errors.Is(err, io.EOF) {
					t.Fatalf("trailing read = %d, %v; want 0, EOF", n, err)
				}
			})
			if allocs != 0 {
				t.Errorf("a reused Reader allocated %.1f times per stream; its ring buffer and tables must be "+
					"kept across Reset so steady-state decoding never touches the heap", allocs)
			}
			if !bytes.Equal(out, in) {
				t.Fatal("decoded bytes differ from the input")
			}
		})
	}
}

func TestOneshotDecompressionMatchesTheInputAndOnlyAllocatesTheResult(t *testing.T) {
	in := allocTestPayload(t)
	for _, level := range []int{1, 5, 11} {
		t.Run(fmt.Sprintf("q%d", level), func(t *testing.T) {
			comp, err := Compress(in, level)
			if err != nil {
				t.Fatal(err)
			}

			got, err := Decompress(comp)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, in) || cap(got) != len(got) {
				t.Fatalf("Decompress returned %d bytes (cap %d) for a %d-byte input; it must return exactly the "+
					"input in one exact-size slice", len(got), cap(got), len(in))
			}

			if raceDetectorEnabled {
				return
			}
			if allocs := allocsBetweenCollections(3, func() {
				if _, err := Decompress(comp); err != nil {
					t.Fatal(err)
				}
			}); allocs != 1 {
				t.Errorf("Decompress allocated %.1f times; it must allocate only the exact-size result and "+
					"collect output in pooled chunks instead of doubling a buffer", allocs)
			}
		})
	}
}
