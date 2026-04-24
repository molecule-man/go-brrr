package brrr

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
)

// writerLevels are the quality levels exercised by round-trip tests.
var writerLevels = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

func TestWriterMultipleWrites(t *testing.T) {
	// Write data in multiple small chunks, then close.
	input := bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog. "), 100)

	for _, level := range writerLevels {
		t.Run(fmt.Sprintf("quality_%d", level), func(t *testing.T) {
			var buf bytes.Buffer
			w, err := NewWriter(&buf, WriterOptions{Quality: level})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}

			// Write in 100-byte chunks.
			for i := 0; i < len(input); i += 100 {
				end := min(i+100, len(input))
				_, err := w.Write(input[i:end])
				if err != nil {
					t.Fatalf("Write: %v", err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			decompressed := brotliDecompress(t, buf.Bytes())
			if !bytes.Equal(decompressed, input) {
				t.Errorf("round-trip mismatch: got %d bytes, want %d bytes",
					len(decompressed), len(input))
			}
		})
	}
}

func TestWriterFlush(t *testing.T) {
	// Write some data, flush, write more, close.
	part1 := []byte("first part of the data, hello world! ")
	part2 := bytes.Repeat([]byte("second part repeated "), 50)
	want := make([]byte, 0, len(part1)+len(part2))
	want = append(want, part1...)
	want = append(want, part2...)

	for _, level := range writerLevels {
		t.Run(fmt.Sprintf("quality_%d", level), func(t *testing.T) {
			var buf bytes.Buffer
			w, err := NewWriter(&buf, WriterOptions{Quality: level})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}

			_, err = w.Write(part1)
			if err != nil {
				t.Fatalf("Write part1: %v", err)
			}
			if err := w.Flush(); err != nil {
				t.Fatalf("Flush: %v", err)
			}

			// After flush, some data should have been written.
			if buf.Len() == 0 {
				t.Fatal("expected output after Flush, got none")
			}

			_, err = w.Write(part2)
			if err != nil {
				t.Fatalf("Write part2: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			decompressed := brotliDecompress(t, buf.Bytes())
			if !bytes.Equal(decompressed, want) {
				t.Errorf("round-trip mismatch: got %d bytes, want %d bytes",
					len(decompressed), len(want))
			}
		})
	}
}

func TestWriterEmpty(t *testing.T) {
	// Closing without writing any data should produce a valid empty stream.
	for _, level := range writerLevels {
		t.Run(fmt.Sprintf("quality_%d", level), func(t *testing.T) {
			var buf bytes.Buffer
			w, err := NewWriter(&buf, WriterOptions{Quality: level})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			decompressed := brotliDecompress(t, buf.Bytes())
			if len(decompressed) != 0 {
				t.Errorf("expected empty output, got %d bytes: %q", len(decompressed), decompressed)
			}
		})
	}
}

func TestWriterReset(t *testing.T) {
	input1 := []byte("first stream content")
	input2 := []byte("second stream content, different data")

	for _, level := range writerLevels {
		t.Run(fmt.Sprintf("quality_%d", level), func(t *testing.T) {
			var buf1, buf2 bytes.Buffer
			w, err := NewWriter(&buf1, WriterOptions{Quality: level})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}

			_, err = w.Write(input1)
			if err != nil {
				t.Fatalf("Write 1: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close 1: %v", err)
			}

			// Reset and compress different data.
			w.Reset(&buf2)
			_, err = w.Write(input2)
			if err != nil {
				t.Fatalf("Write 2: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close 2: %v", err)
			}

			dec1 := brotliDecompress(t, buf1.Bytes())
			if !bytes.Equal(dec1, input1) {
				t.Errorf("stream 1 mismatch: got %q, want %q", dec1, input1)
			}

			dec2 := brotliDecompress(t, buf2.Bytes())
			if !bytes.Equal(dec2, input2) {
				t.Errorf("stream 2 mismatch: got %q, want %q", dec2, input2)
			}
		})
	}
}

func TestWriterCloseIdempotent(t *testing.T) {
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, WriterOptions{})
	_, _ = w.Write([]byte("data"))
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Second close should not error.
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestWriterWriteAfterClose(t *testing.T) {
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, WriterOptions{})
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err := w.Write([]byte("data"))
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Write after Close: got err %v, want io.ErrClosedPipe", err)
	}
}

func TestWriterIOWriter(t *testing.T) {
	// Verify Writer satisfies io.WriteCloser at compile time.
	var _ io.WriteCloser = (*Writer)(nil)
}

func TestNewWriterValidation(t *testing.T) {
	// Valid options.
	for _, level := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11} {
		w, err := NewWriter(io.Discard, WriterOptions{Quality: level})
		if err != nil {
			t.Errorf("NewWriter(quality=%d): unexpected error: %v", level, err)
		}
		if w == nil {
			t.Errorf("NewWriter(quality=%d): returned nil writer", level)
		}
	}

	// Invalid quality.
	for _, level := range []int{-1, 12, 100} {
		w, err := NewWriter(io.Discard, WriterOptions{Quality: level})
		if err == nil {
			t.Errorf("NewWriter(quality=%d): expected error, got nil", level)
		}
		if w != nil {
			t.Errorf("NewWriter(quality=%d): expected nil writer, got non-nil", level)
		}
	}

	// Invalid lgwin.
	for _, lgwin := range []int{5, 9, 25, 30} {
		w, err := NewWriter(io.Discard, WriterOptions{LGWin: lgwin})
		if err == nil {
			t.Errorf("NewWriter(lgwin=%d): expected error, got nil", lgwin)
		}
		if w != nil {
			t.Errorf("NewWriter(lgwin=%d): expected nil writer, got non-nil", lgwin)
		}
	}

	// Valid lgwin values.
	for lgwin := 10; lgwin <= 24; lgwin++ {
		w, err := NewWriter(io.Discard, WriterOptions{LGWin: lgwin})
		if err != nil {
			t.Errorf("NewWriter(lgwin=%d): unexpected error: %v", lgwin, err)
		}
		if w == nil {
			t.Errorf("NewWriter(lgwin=%d): returned nil writer", lgwin)
		}
	}
}

func TestWriterErrorPropagation(t *testing.T) {
	errFail := errors.New("write failed")
	// failAfter returns an io.Writer that fails after n bytes have been written.
	failAfter := func(n int) io.Writer {
		return &limitedWriter{limit: n, err: errFail}
	}

	// Generate enough data to force the streaming encoder to flush.
	input := bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz0123456789 "), 2000)

	// Test Write error propagation for streaming qualities (>= 2).
	// The streaming encoder needs a full ring buffer before it emits
	// compressed data. Write repeatedly until the error surfaces.
	for _, quality := range []int{2, 4, 6} {
		t.Run(fmt.Sprintf("write_q%d", quality), func(t *testing.T) {
			w, err := NewWriter(failAfter(0), WriterOptions{Quality: quality, LGWin: 10})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			var writeErr error
			for range 1000 {
				_, writeErr = w.Write(input)
				if writeErr != nil {
					break
				}
			}
			if !errors.Is(writeErr, errFail) {
				t.Errorf("Write: got err %v, want %v", writeErr, errFail)
			}
		})
	}

	// Test Close error propagation for streaming qualities.
	for _, quality := range []int{2, 4, 6} {
		t.Run(fmt.Sprintf("close_q%d", quality), func(t *testing.T) {
			w, err := NewWriter(failAfter(0), WriterOptions{Quality: quality})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			err = w.Close()
			if !errors.Is(err, errFail) {
				t.Errorf("Close: got err %v, want %v", err, errFail)
			}
		})
	}

	// Test Flush error propagation for streaming qualities.
	for _, quality := range []int{2, 4} {
		t.Run(fmt.Sprintf("flush_q%d", quality), func(t *testing.T) {
			w, err := NewWriter(failAfter(0), WriterOptions{Quality: quality})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			_, _ = w.Write([]byte("some data to flush"))
			err = w.Flush()
			if !errors.Is(err, errFail) {
				t.Errorf("Flush: got err %v, want %v", err, errFail)
			}
		})
	}

	// Test Flush and Close error propagation for batch qualities (0-1).
	for _, quality := range []int{0, 1} {
		t.Run(fmt.Sprintf("flush_q%d", quality), func(t *testing.T) {
			w, err := NewWriter(failAfter(0), WriterOptions{Quality: quality})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			_, _ = w.Write([]byte("some data"))
			err = w.Flush()
			if !errors.Is(err, errFail) {
				t.Errorf("Flush: got err %v, want %v", err, errFail)
			}
		})

		t.Run(fmt.Sprintf("close_q%d", quality), func(t *testing.T) {
			w, err := NewWriter(failAfter(0), WriterOptions{Quality: quality})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			_, _ = w.Write([]byte("some data"))
			err = w.Close()
			if !errors.Is(err, errFail) {
				t.Errorf("Close: got err %v, want %v", err, errFail)
			}
		})
	}
}

func TestWriterAttachDictionaryLowQuality(t *testing.T) {
	for _, quality := range []int{0, 1} {
		t.Run(fmt.Sprintf("quality_%d", quality), func(t *testing.T) {
			w, err := NewWriter(io.Discard, WriterOptions{Quality: quality})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			err = w.AttachDictionary([]byte("dictionary data"))
			if err == nil {
				t.Fatal("AttachDictionary: expected error for low quality, got nil")
			}
		})
	}
}

func TestWriterFlushAfterClose(t *testing.T) {
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, WriterOptions{})
	_ = w.Close()
	err := w.Flush()
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after Close: got err %v, want io.ErrClosedPipe", err)
	}
}

func TestWriterFlushEmpty(t *testing.T) {
	// Flush with no buffered data should be a no-op for batch qualities.
	for _, quality := range []int{0, 1} {
		t.Run(fmt.Sprintf("quality_%d", quality), func(t *testing.T) {
			var buf bytes.Buffer
			w, err := NewWriter(&buf, WriterOptions{Quality: quality})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			if err := w.Flush(); err != nil {
				t.Fatalf("Flush: %v", err)
			}
			if buf.Len() != 0 {
				t.Errorf("expected no output from empty flush, got %d bytes", buf.Len())
			}
		})
	}
}

// limitedWriter is an io.Writer that returns an error after limit bytes.
type limitedWriter struct {
	written int
	limit   int
	err     error
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.written+len(p) > w.limit {
		remaining := w.limit - w.written
		if remaining > 0 {
			w.written += remaining
			return remaining, w.err
		}
		return 0, w.err
	}
	w.written += len(p)
	return len(p), nil
}

func TestWriterErrorStickiness(t *testing.T) {
	// After a write error surfaces, subsequent Write, Flush, and Close
	// must return the same cached error without attempting further I/O.
	errFail := errors.New("disk full")

	for _, quality := range []int{0, 1, 2, 4} {
		t.Run(fmt.Sprintf("quality_%d", quality), func(t *testing.T) {
			// Allow a small amount of output so the writer initialises,
			// then fail on subsequent writes.
			w, err := NewWriter(&limitedWriter{limit: 0, err: errFail}, WriterOptions{Quality: quality, LGWin: 10})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}

			// Push data until the error surfaces through Write or Close.
			input := bytes.Repeat([]byte("abcdefghijklmnop"), 5000)
			var firstErr error
			for range 200 {
				_, firstErr = w.Write(input)
				if firstErr != nil {
					break
				}
			}
			if firstErr == nil {
				// For batch qualities (0-1) the error only appears on Flush/Close.
				firstErr = w.Close()
			}
			if !errors.Is(firstErr, errFail) {
				t.Fatalf("expected initial error %v, got %v", errFail, firstErr)
			}

			// Subsequent Write must return the cached error.
			_, err = w.Write([]byte("more data"))
			if !errors.Is(err, errFail) {
				t.Errorf("Write after error: got %v, want %v", err, errFail)
			}

			// Subsequent Flush must return the cached error.
			err = w.Flush()
			if !errors.Is(err, errFail) {
				t.Errorf("Flush after error: got %v, want %v", err, errFail)
			}

			// Subsequent Close must return the cached error.
			err = w.Close()
			if !errors.Is(err, errFail) {
				t.Errorf("Close after error: got %v, want %v", err, errFail)
			}
		})
	}
}

func TestWriterResetQ6WithCompoundDict(t *testing.T) {
	dict := bytes.Repeat([]byte("dictionary content for compound test "), 50)
	input := bytes.Repeat([]byte("input referencing dictionary content here "), 100)

	// Compress with a fresh writer + compound dictionary.
	var freshBuf bytes.Buffer
	fresh, err := NewWriter(&freshBuf, WriterOptions{Quality: 6})
	if err != nil {
		t.Fatalf("NewWriter (fresh): %v", err)
	}
	if err := fresh.AttachDictionary(dict); err != nil {
		t.Fatalf("AttachDictionary (fresh): %v", err)
	}
	if _, err := fresh.Write(input); err != nil {
		t.Fatalf("Write (fresh): %v", err)
	}
	if err := fresh.Close(); err != nil {
		t.Fatalf("Close (fresh): %v", err)
	}

	// Use a reused writer: first compress something to dirty internal state,
	// then Reset, attach the same dictionary, and compress the same input.
	var discardBuf, reusedBuf bytes.Buffer
	reused, err := NewWriter(&discardBuf, WriterOptions{Quality: 6})
	if err != nil {
		t.Fatalf("NewWriter (reused): %v", err)
	}
	if _, err := reused.Write([]byte("throwaway data to dirty state")); err != nil {
		t.Fatalf("Write (throwaway): %v", err)
	}
	if err := reused.Close(); err != nil {
		t.Fatalf("Close (throwaway): %v", err)
	}

	reused.Reset(&reusedBuf)
	if err := reused.AttachDictionary(dict); err != nil {
		t.Fatalf("AttachDictionary (reused): %v", err)
	}
	if _, err := reused.Write(input); err != nil {
		t.Fatalf("Write (reused): %v", err)
	}
	if err := reused.Close(); err != nil {
		t.Fatalf("Close (reused): %v", err)
	}

	// A reused writer with the same dictionary and input must produce
	// byte-identical output to a fresh writer.
	if !bytes.Equal(freshBuf.Bytes(), reusedBuf.Bytes()) {
		t.Errorf("output mismatch: fresh=%d bytes, reused=%d bytes",
			freshBuf.Len(), reusedBuf.Len())
	}
}
