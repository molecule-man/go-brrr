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
