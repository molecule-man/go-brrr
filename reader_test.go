package brrr

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// compress is a test helper that brotli-compresses data at a given quality.
func compress(t testing.TB, data []byte, quality int) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, WriterOptions{Quality: quality})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes()
}

func TestReaderRoundTrip(t *testing.T) {
	sizes := []struct {
		name string
		size int
	}{
		{"empty", 0},
		{"1byte", 1},
		{"1KB", 1024},
		{"64KB", 64 * 1024},
		{"1MB", 1024 * 1024},
	}

	for _, sz := range sizes {
		for _, q := range writerLevels {
			t.Run(fmt.Sprintf("%s/q%d", sz.name, q), func(t *testing.T) {
				input := make([]byte, sz.size)
				if sz.size > 0 {
					// Mix of compressible and random data.
					copy(input, bytes.Repeat([]byte("abcdefgh"), sz.size/8+1))
				}

				compressed := compress(t, input, q)
				r := NewReader(bytes.NewReader(compressed))
				got, err := io.ReadAll(r)
				if err != nil {
					t.Fatalf("ReadAll: %v", err)
				}
				if !bytes.Equal(got, input) {
					t.Errorf("round-trip mismatch: got %d bytes, want %d", len(got), len(input))
				}
			})
		}
	}
}

func TestReaderOneByteSource(t *testing.T) {
	input := bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog. "), 200)

	for _, q := range []int{0, 1} {
		t.Run(fmt.Sprintf("q%d", q), func(t *testing.T) {
			compressed := compress(t, input, q)
			r := NewReader(iotest.OneByteReader(bytes.NewReader(compressed)))
			got, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if !bytes.Equal(got, input) {
				t.Errorf("round-trip mismatch: got %d bytes, want %d", len(got), len(input))
			}
		})
	}
}

func TestReaderSmallChunks(t *testing.T) {
	input := bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog. "), 200)
	compressed := compress(t, input, 0)

	for _, chunkSize := range []int{len(compressed), 1024, 128, 16, 8, 4, 2, 1} {
		t.Run(fmt.Sprintf("chunk%d", chunkSize), func(t *testing.T) {
			src := &chunkedReader{data: compressed, chunkSize: chunkSize}
			r := NewReader(src)
			got, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("ReadAll (chunk=%d): %v", chunkSize, err)
			}
			if !bytes.Equal(got, input) {
				t.Errorf("mismatch: got %d bytes, want %d", len(got), len(input))
			}
		})
	}
}

type chunkedReader struct {
	data      []byte
	pos       int
	chunkSize int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	end := min(r.pos+r.chunkSize, len(r.data))
	n := copy(p, r.data[r.pos:end])
	r.pos += n
	return n, nil
}

func TestReaderSmallBuffer(t *testing.T) {
	input := bytes.Repeat([]byte("small buffer test "), 100)
	compressed := compress(t, input, 1)

	r := NewReader(bytes.NewReader(compressed))
	var got []byte
	p := make([]byte, 1)
	for {
		n, err := r.Read(p)
		if n > 0 {
			got = append(got, p[:n]...)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
	if !bytes.Equal(got, input) {
		t.Errorf("round-trip mismatch: got %d bytes, want %d", len(got), len(input))
	}
}

func TestReaderReset(t *testing.T) {
	inputA := []byte("stream A data, hello world")
	inputB := bytes.Repeat([]byte("stream B repeated "), 50)
	compA := compress(t, inputA, 1)
	compB := compress(t, inputB, 1)

	r := NewReader(bytes.NewReader(compA))
	gotA, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll A: %v", err)
	}
	if !bytes.Equal(gotA, inputA) {
		t.Fatalf("stream A mismatch: got %d bytes, want %d", len(gotA), len(inputA))
	}

	r.Reset(bytes.NewReader(compB))
	gotB, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll B: %v", err)
	}
	if !bytes.Equal(gotB, inputB) {
		t.Errorf("stream B mismatch: got %d bytes, want %d", len(gotB), len(inputB))
	}
}

func TestReaderResetReusesDecoderBuffers(t *testing.T) {
	inputA := bytes.Repeat([]byte("first stream repeated payload "), 2048)
	inputB := bytes.Repeat([]byte("second stream payload "), 1024)
	compA := compress(t, inputA, 1)
	compB := compress(t, inputB, 1)

	r := NewReader(bytes.NewReader(compA))
	gotA, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll A: %v", err)
	}
	if !bytes.Equal(gotA, inputA) {
		t.Fatalf("stream A mismatch: got %d bytes, want %d", len(gotA), len(inputA))
	}

	if len(r.state.ringbuffer) == 0 || len(r.state.blockTypeTrees) == 0 {
		t.Fatal("expected decoder buffers to be allocated after first decode")
	}
	ringbufferPtr := &r.state.ringbuffer[0]
	blockTreesPtr := &r.state.blockTypeTrees[0]

	r.Reset(bytes.NewReader(compB))
	gotB, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll B: %v", err)
	}
	if !bytes.Equal(gotB, inputB) {
		t.Fatalf("stream B mismatch: got %d bytes, want %d", len(gotB), len(inputB))
	}

	if ringbufferPtr != &r.state.ringbuffer[0] {
		t.Fatal("Reset did not reuse ring buffer allocation")
	}
	if blockTreesPtr != &r.state.blockTypeTrees[0] {
		t.Fatal("Reset did not reuse block tree allocation")
	}
}

func TestReaderIOCopy(t *testing.T) {
	input := bytes.Repeat([]byte("io.Copy test data "), 500)
	compressed := compress(t, input, 1)

	var dst bytes.Buffer
	r := NewReader(bytes.NewReader(compressed))
	n, err := io.Copy(&dst, r)
	if err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	if int(n) != len(input) {
		t.Errorf("io.Copy returned n=%d, want %d", n, len(input))
	}
	if !bytes.Equal(dst.Bytes(), input) {
		t.Errorf("round-trip mismatch: got %d bytes, want %d", dst.Len(), len(input))
	}
}

func TestReaderTruncated(t *testing.T) {
	input := bytes.Repeat([]byte("truncation test "), 100)
	compressed := compress(t, input, 1)

	// Cut the compressed data in half.
	truncated := compressed[:len(compressed)/2]
	r := NewReader(bytes.NewReader(truncated))
	_, err := io.ReadAll(r)
	if err == nil {
		t.Fatal("expected error for truncated input, got nil")
	}
	if errors.Is(err, io.EOF) {
		t.Fatal("expected decode error, got io.EOF")
	}
}

func TestReaderCorrupted(t *testing.T) {
	input := bytes.Repeat([]byte("corruption test "), 100)
	compressed := compress(t, input, 1)

	// Flip bits in the middle of compressed data.
	corrupted := make([]byte, len(compressed))
	copy(corrupted, compressed)
	mid := len(corrupted) / 2
	corrupted[mid] ^= 0xFF

	r := NewReader(bytes.NewReader(corrupted))
	_, err := io.ReadAll(r)
	if err == nil {
		t.Fatal("expected error for corrupted input, got nil")
	}
	if errors.Is(err, io.EOF) {
		t.Fatal("expected decode error, got io.EOF")
	}
}

func TestReaderZeroLenP(t *testing.T) {
	input := []byte("hello")
	compressed := compress(t, input, 1)

	r := NewReader(bytes.NewReader(compressed))
	n, err := r.Read(nil)
	if n != 0 || err != nil {
		t.Errorf("Read(nil) = (%d, %v), want (0, nil)", n, err)
	}
	n, err = r.Read(make([]byte, 0))
	if n != 0 || err != nil {
		t.Errorf("Read([]byte{}) = (%d, %v), want (0, nil)", n, err)
	}

	// Verify the reader still works after zero-length reads.
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, input) {
		t.Errorf("got %q, want %q", got, input)
	}
}

func TestReaderRandomData(t *testing.T) {
	// Incompressible random data exercises different code paths.
	input := make([]byte, 10*1024)
	if _, err := rand.Read(input); err != nil {
		t.Fatal(err)
	}
	compressed := compress(t, input, 1)

	r := NewReader(bytes.NewReader(compressed))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, input) {
		t.Errorf("round-trip mismatch for random data: got %d bytes, want %d", len(got), len(input))
	}
}

func TestReaderEOFAfterDone(t *testing.T) {
	input := []byte("eof test")
	compressed := compress(t, input, 1)

	r := NewReader(bytes.NewReader(compressed))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, input) {
		t.Fatalf("got %q, want %q", got, input)
	}

	// Subsequent reads return (0, io.EOF).
	p := make([]byte, 10)
	n, err := r.Read(p)
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Errorf("Read after EOF = (%d, %v), want (0, io.EOF)", n, err)
	}
}

func TestReaderClose(t *testing.T) {
	input := []byte("close test")
	compressed := compress(t, input, 1)

	r := NewReader(bytes.NewReader(compressed))
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// After Close, fields should be nil/zero.
	if r.src != nil || r.out != nil {
		t.Error("Close did not release resources")
	}

	n, err := r.Read(make([]byte, 1))
	if n != 0 || !errors.Is(err, errReaderClosed) {
		t.Fatalf("Read after Close = (%d, %v), want (0, %v)", n, err, errReaderClosed)
	}
}

// errReader returns err after n bytes.
type errReader struct {
	data []byte
	pos  int
	err  error
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	if r.pos >= len(r.data) {
		return n, r.err
	}
	return n, nil
}

func TestReaderSourceError(t *testing.T) {
	input := bytes.Repeat([]byte("source error test "), 100)
	compressed := compress(t, input, 1)

	// Return a non-EOF error partway through.
	srcErr := fmt.Errorf("disk read error")
	r := NewReader(&errReader{
		data: compressed[:len(compressed)/2],
		err:  srcErr,
	})
	_, err := io.ReadAll(r)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "disk read error") {
		t.Errorf("expected source error propagated, got: %v", err)
	}
}

func TestReaderDeferredSourceErrorAfterFinalBytes(t *testing.T) {
	input := bytes.Repeat([]byte("final source error test "), 100)
	compressed := compress(t, input, 1)
	srcErr := fmt.Errorf("disk read error")

	r := NewReader(&errReader{
		data: compressed,
		err:  srcErr,
	})

	got, err := io.ReadAll(r)
	if !errors.Is(err, srcErr) {
		t.Fatalf("ReadAll error = %v, want %v", err, srcErr)
	}
	if !bytes.Equal(got, input) {
		t.Fatalf("round-trip mismatch: got %d bytes, want %d", len(got), len(input))
	}
}

func TestReaderAttachDictionaryErrors(t *testing.T) {
	t.Run("after decoding started", func(t *testing.T) {
		input := []byte("hello")
		compressed := compress(t, input, 1)
		r := NewReader(bytes.NewReader(compressed))

		// Drive one Read so started becomes true.
		buf := make([]byte, 1)
		if _, err := r.Read(buf); err != nil {
			t.Fatalf("Read: %v", err)
		}

		err := r.AttachDictionary([]byte("dict data"))
		if err == nil {
			t.Fatal("expected error attaching dictionary after decoding started")
		}
	})

	t.Run("empty data", func(t *testing.T) {
		r := NewReader(bytes.NewReader(nil))
		if err := r.AttachDictionary([]byte{}); !errors.Is(err, errEmptyDict) {
			t.Fatalf("expected errEmptyDict, got %v", err)
		}
	})

	t.Run("too many dictionaries", func(t *testing.T) {
		r := NewReader(bytes.NewReader(nil))
		for i := range 15 {
			if err := r.AttachDictionary(fmt.Appendf(nil, "dict%d___", i)); err != nil {
				t.Fatalf("AttachDictionary %d: %v", i, err)
			}
		}
		if err := r.AttachDictionary([]byte("overflow")); !errors.Is(err, errTooManyDicts) {
			t.Fatalf("expected errTooManyDicts, got %v", err)
		}
	})

	t.Run("after close", func(t *testing.T) {
		r := NewReader(bytes.NewReader(nil))
		_ = r.Close()
		err := r.AttachDictionary([]byte("dict data"))
		if err == nil {
			t.Fatal("expected error attaching dictionary after Close")
		}
	})
}
