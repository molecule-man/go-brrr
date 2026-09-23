package brrr

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"runtime"
	"testing"
	"time"
)

func TestReusedWriterCompressesAStreamWithoutAllocating(t *testing.T) {
	in := allocTestPayload(t)
	for _, parallelism := range []int{1, 2} {
		for level := 0; level <= 11; level++ {
			t.Run(fmt.Sprintf("p%d/q%d", parallelism, level), func(t *testing.T) {
				testReusedWriterAllocations(t, in, level, parallelism)
			})
		}
	}
}

func testReusedWriterAllocations(t *testing.T, in []byte, level, parallelism int) {
	w, err := NewWriterOptions(io.Discard, level, WriterOptions{SizeHint: uint(len(in)), Parallelism: parallelism})
	if err != nil {
		t.Fatal(err)
	}
	stream := func() {
		w.Reset(io.Discard)
		if _, err := w.Write(in); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// AllocsPerRun counts mallocs for the whole process, so a goroutine
	// left over from an earlier test allocating inside the window is
	// charged here. Settle and measure once more before failing.
	allocs := allocsBetweenCollections(2, stream)
	if allocs != 0 {
		waitForGoroutines(t, runtime.NumGoroutine()-1)
		allocs = allocsBetweenCollections(2, stream)
	}
	if allocs != 0 {
		t.Errorf("q%d p%d: got %.1f allocations per stream, want 0", level, parallelism, allocs)
	}
}

func waitForGoroutines(tb testing.TB, want int) int {
	tb.Helper()
	got := runtime.NumGoroutine()
	for range 200 {
		if got <= want {
			return got
		}
		runtime.GC()
		time.Sleep(5 * time.Millisecond)
		got = runtime.NumGoroutine()
	}
	return got
}

func TestHighQualityWriterReleasesItsMatchCollectorWhenClosedOrAbandoned(t *testing.T) {
	in := allocTestPayload(t)
	baseline := waitForGoroutines(t, runtime.NumGoroutine())

	w, err := NewWriterOptions(io.Discard, 11, WriterOptions{SizeHint: uint(len(in)), Parallelism: 8})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(in); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := waitForGoroutines(t, baseline); got > baseline {
		t.Errorf("closing a q11 Writer left %d goroutines running (baseline %d); the collector must stop "+
			"when the encoder is released or every stream leaks one", got, baseline)
	}

	func() {
		abandoned, err := NewWriterOptions(io.Discard, 10, WriterOptions{SizeHint: uint(len(in)), Parallelism: 2})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := abandoned.Write(in); err != nil {
			t.Fatal(err)
		}
		if err := abandoned.Flush(); err != nil {
			t.Fatal(err)
		}
	}()
	if got := waitForGoroutines(t, baseline); got > baseline {
		t.Errorf("an unclosed q10 Writer that became unreachable left %d goroutines running (baseline %d); "+
			"its finalizer must stop the collector so a forgotten Close cannot leak it", got, baseline)
	}
}

func TestSequentialWriterStartsNoGoroutine(t *testing.T) {
	in := allocTestPayload(t)
	baseline := waitForGoroutines(t, runtime.NumGoroutine())
	for _, level := range []int{10, 11} {
		w, err := NewWriterOptions(io.Discard, level, WriterOptions{SizeHint: uint(len(in)), Parallelism: 1})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(in); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		if got := runtime.NumGoroutine(); got > baseline {
			t.Errorf("q%d p1: got %d goroutines, baseline %d", level, got, baseline)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWriterOutputIsTheSameAtEveryParallelism(t *testing.T) {
	in := allocTestPayload(t)
	for _, level := range []int{9, 10, 11} {
		want := writerStreamParallel(t, in, level, 0)
		for _, parallelism := range []int{1, 2, 8} {
			if got := writerStreamParallel(t, in, level, parallelism); !bytes.Equal(got, want) {
				t.Errorf("q%d p%d: output differs from p0 (%d bytes versus %d)",
					level, parallelism, len(got), len(want))
			}
		}
	}
}

func TestWriterRejectsNegativeParallelism(t *testing.T) {
	w, err := NewWriterOptions(io.Discard, 11, WriterOptions{Parallelism: -1})
	if err == nil || w != nil {
		t.Fatalf("NewWriterOptions with Parallelism -1 returned (%v, %v); want a nil Writer and an error", w, err)
	}
	if want := "brrr: invalid parallelism: -1"; err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func writerStream(tb testing.TB, in []byte, level int) []byte {
	tb.Helper()
	return writerStreamParallel(tb, in, level, 0)
}

func writerStreamParallel(tb testing.TB, in []byte, level, parallelism int) []byte {
	tb.Helper()
	var buf bytes.Buffer
	w, err := NewWriterOptions(&buf, level, WriterOptions{SizeHint: uint(len(in)), Parallelism: parallelism})
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := w.Write(in); err != nil {
		tb.Fatal(err)
	}
	if err := w.Close(); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

func TestOneshotCompressionMatchesTheStreamingWriterAndOnlyAllocatesTheResult(t *testing.T) {
	in := allocTestPayload(t)
	for level := 0; level <= 11; level++ {
		t.Run(fmt.Sprintf("q%d", level), func(t *testing.T) {
			want := writerStream(t, in, level)
			if level < minWorkerLevel {
				overOneMiB := bytes.Repeat(in, 3)
				if _, err := Compress(overOneMiB, level); err != nil {
					t.Fatal(err)
				}

				noisy := make([]byte, 256<<10)
				rng := rand.New(rand.NewPCG(uint64(level), 17))
				for i := range noisy {
					noisy[i] = byte(rng.Uint32())
				}
				gotNoisy, err := Compress(noisy, level)
				if err != nil {
					t.Fatal(err)
				}
				if wantNoisy := writerStream(t, noisy, level); !bytes.Equal(gotNoisy, wantNoisy) ||
					len(gotNoisy) <= encodeChunkSize {
					t.Fatalf("Compress of incompressible input produced %d bytes, the Writer %d; output larger than "+
						"one %d-byte chunk must be stitched from several chunks in order without losing a byte",
						len(gotNoisy), len(wantNoisy), encodeChunkSize)
				}
			}

			got, err := Compress(in, level)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("Compress produced %d bytes that differ from the %d-byte Writer stream; the pooled "+
					"one-shot compressor must reset to exactly the state a fresh Writer starts from, including the "+
					"new input's size hint after a call above 1 MiB", len(got), len(want))
			}
			if cap(got) != len(got) {
				t.Errorf("Compress returned cap %d for len %d; the result must be one exact-size allocation", cap(got), len(got))
			}

			if raceDetectorEnabled {
				return
			}
			if allocs := allocsBetweenCollections(2, func() {
				if _, err := Compress(in, level); err != nil {
					t.Fatal(err)
				}
			}); allocs != 1 {
				t.Errorf("Compress allocated %.1f times; it must allocate only the exact-size result and "+
					"collect output in pooled chunks instead of a growing buffer", allocs)
			}
		})
	}
}
