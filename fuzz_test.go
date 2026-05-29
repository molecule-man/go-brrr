package brrr

import (
	"bytes"
	"io"
	"testing"
)

// FuzzDecode feeds random bytes into the decoder and checks that it never
// panics or hangs. Any error returned by the decoder is acceptable; only
// crashes count as failures.
func FuzzDecode(f *testing.F) {
	// Seed with a minimal valid brotli stream (empty input, quality 0).
	f.Add(compress(f, []byte{}, 0))
	// Seed with a non-trivial compressed payload.
	f.Add(compress(f, []byte("hello, brotli fuzzer!"), 6))
	// Seed with pure garbage.
	f.Add([]byte{0xff, 0xfe, 0xfd, 0x00, 0x01})

	f.Fuzz(func(t *testing.T, data []byte) {
		r := NewReader(bytes.NewReader(data))
		// Drain the reader; errors are expected for invalid input.
		io.Copy(io.Discard, r) //nolint:errcheck
	})
}

// FuzzRoundtrip compresses random data then decompresses it, asserting that
// the output matches the original input byte-for-byte.
func FuzzRoundtrip(f *testing.F) {
	f.Add([]byte{}, 0)
	f.Add([]byte("hello, brotli fuzzer!"), 6)
	f.Add(bytes.Repeat([]byte("abcdefgh"), 1024), 11)

	f.Fuzz(func(t *testing.T, data []byte, quality int) {
		// Clamp quality to the valid range [0, 11].
		quality = quality % 12
		if quality < 0 {
			quality += 12
		}

		// Compress.
		compressed := compress(t, data, quality)

		// Decompress.
		got, err := Decompress(compressed)
		if err != nil {
			t.Fatalf("Decompress: %v", err)
		}

		if !bytes.Equal(got, data) {
			t.Fatalf("round-trip mismatch: input %d bytes, got %d bytes", len(data), len(got))
		}
	})
}

// FuzzRoundtripChunkedWrites drives the fast path (q0/q1) with many small
// Writes and interleaved Flushes at a small window, so it must drain full
// fragments mid-stream and carry sub-byte bits across Write and Flush
// boundaries. The decompressed result must equal the concatenated input.
func FuzzRoundtripChunkedWrites(f *testing.F) {
	f.Add([]byte("hello, brotli fuzzer!"), 0, 3, 7)
	f.Add(bytes.Repeat([]byte("abcdefgh"), 1024), 1, 5, 0)
	f.Add(bytes.Repeat([]byte("the quick brown fox "), 500), 0, 100, 13)

	f.Fuzz(func(t *testing.T, data []byte, quality, chunk, flushEvery int) {
		quality = ((quality % 2) + 2) % 2 // fast path only: q0, q1
		if chunk <= 0 {
			chunk = 1
		}

		var buf bytes.Buffer
		w, err := NewWriterOptions(&buf, quality, WriterOptions{LGWin: 10})
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		for i, written := 0, 0; i < len(data); i += chunk {
			end := min(i+chunk, len(data))
			if _, err := w.Write(data[i:end]); err != nil {
				t.Fatalf("Write: %v", err)
			}
			written++
			if flushEvery > 0 && written%flushEvery == 0 {
				if err := w.Flush(); err != nil {
					t.Fatalf("Flush: %v", err)
				}
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		got, err := Decompress(buf.Bytes())
		if err != nil {
			t.Fatalf("Decompress: %v", err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("round-trip mismatch: input %d bytes, got %d bytes", len(data), len(got))
		}
	})
}

// FuzzRoundtripStreaming compresses random data and decompresses it through the
// streaming Reader fed one byte at a time. This exercises the decoder's
// needsMoreInput slow paths at every possible byte boundary.
func FuzzRoundtripStreaming(f *testing.F) {
	f.Add([]byte{}, 0)
	f.Add([]byte("hello, brotli fuzzer!"), 6)
	f.Add(bytes.Repeat([]byte("abcdefgh"), 1024), 11)

	f.Fuzz(func(t *testing.T, data []byte, quality int) {
		quality = quality % 12
		if quality < 0 {
			quality += 12
		}

		compressed := compress(t, data, quality)

		r := NewReader(bytes.NewReader(compressed))
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}

		if !bytes.Equal(got, data) {
			t.Fatalf("round-trip mismatch: input %d bytes, got %d bytes", len(data), len(got))
		}
	})
}
