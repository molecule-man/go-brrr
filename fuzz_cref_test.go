//go:build cgo

package brrr

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/molecule-man/go-brrr/internal/cref"
)

const (
	// Limit expensive high-quality compression and one-byte streaming.
	fuzzMaxPlain = 64 << 10
	// This limits mutation size, not decoded output size.
	fuzzMaxCompressed = 8 << 10
)

// fuzzLGWin maps an arbitrary byte onto the valid window range.
func fuzzLGWin(b uint8) int {
	return minLGWin + int(b)%(maxLGWin-minLGWin+1)
}

// FuzzEncodeCRef checks encoder equivalence and cross-decoding with C.
func FuzzEncodeCRef(f *testing.F) {
	for quality := uint8(0); quality <= 11; quality++ {
		f.Add([]byte{}, quality, uint8(22), uint16(1), uint16(1), false)
		f.Add([]byte("hello, brotli fuzzer!"), quality, uint8(10), uint16(3), uint16(5), true)
		f.Add(bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog\n"), 100),
			quality, uint8(24), uint16(127), uint16(4096), false)
	}
	// Cross small-window fragment boundaries with both fast encoders.
	f.Add(pseudoRandomBytesCRef(32<<10, 42), uint8(0), uint8(10), uint16(700), uint16(1000), false)
	f.Add(bytes.Repeat([]byte("abcdefgh"), 4096), uint8(1), uint8(10), uint16(701), uint16(1), true)

	f.Fuzz(func(t *testing.T, data []byte, qualityByte, windowByte uint8, chunkWord, readWord uint16, flush bool) {
		data = data[:min(len(data), fuzzMaxPlain)]
		quality := int(qualityByte) % 12
		lgwin := fuzzLGWin(windowByte)
		chunkSize := max(1, int(chunkWord))
		readSize := max(1, int(readWord))
		sizeHint := uint(len(data))

		// Use cref directly: caching every mutated input would fill the disk.
		cEncoded, err := cref.Encode(data, quality, lgwin, sizeHint)
		if err != nil {
			t.Fatalf("C Encode (q=%d, lgwin=%d): %v", quality, lgwin, err)
		}
		assertGoDecodes(t, cEncoded, data, nil, chunkSize, readSize)

		opts := WriterOptions{LGWin: lgwin, SizeHint: sizeHint}
		// C receives one write. Chunk boundaries can change match selection.
		goEncoded := encodeChunked(t, data, quality, opts, max(1, len(data)), false)
		assertCRefDecodes(t, goEncoded, data, nil)
		assertGoDecodes(t, goEncoded, data, nil, chunkSize, readSize)
		// Other settings may choose different valid encodings and compressed sizes.
		exact := quality < 10 && (quality < 5 || lgwin <= 16)
		if exact && !bytes.Equal(goEncoded, cEncoded) {
			t.Fatalf("Go stream differs from C: %s", firstDiff(goEncoded, cEncoded))
		}

		chunked := encodeChunked(t, data, quality, opts, chunkSize, flush)
		assertCRefDecodes(t, chunked, data, nil)
		assertGoDecodes(t, chunked, data, nil, chunkSize, readSize)

		// Compress uses the default window, so only check interoperability.
		oneShot, err := Compress(data, quality)
		if err != nil {
			t.Fatalf("Compress: %v", err)
		}
		assertCRefDecodes(t, oneShot, data, nil)
	})
}

// FuzzDecodeCRef checks decoder acceptance and output against C.
func FuzzDecodeCRef(f *testing.F) {
	plain := [][]byte{
		{},
		[]byte("hello, brotli fuzzer!"),
		bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog\n"), 40),
		pseudoRandomBytesCRef(2000, 7),
	}
	for _, p := range plain {
		for _, quality := range []int{0, 1, 5, 9, 11} {
			for _, lgwin := range []int{10, 16, 22} {
				encoded, err := cref.Encode(p, quality, lgwin, uint(len(p)))
				if err != nil {
					f.Fatalf("C Encode: %v", err)
				}
				f.Add(encoded, uint16(1), uint16(1))
				f.Add(encoded, uint16(len(encoded)), uint16(len(p)+1))
			}
		}
		f.Add(compress(f, p, 6), uint16(3), uint16(7))
	}
	// Trailing byte after a complete stream, a truncated stream, and garbage.
	full := compress(f, plain[1], 6)
	f.Add(append(bytes.Clone(full), 0x00), uint16(1), uint16(1))
	f.Add(full[:len(full)/2], uint16(1), uint16(1))
	f.Add([]byte{0xff, 0xfe, 0xfd, 0x00, 0x01}, uint16(1), uint16(1))

	f.Fuzz(func(t *testing.T, data []byte, chunkWord, readWord uint16) {
		data = data[:min(len(data), fuzzMaxCompressed)]
		chunkSize := max(1, int(chunkWord))
		readSize := max(1, int(readWord))

		cOut, consumed, cErr := cref.DecodeConsumed(data)
		goOut, goErr := Decompress(data)

		if cErr != nil {
			if goErr == nil {
				t.Fatalf("C rejects, Go accepts (%d bytes out)", len(goOut))
			}
			assertGoStreamRejects(t, data, chunkSize, readSize)
			return
		}

		if consumed < len(data) {
			// C ignores trailing bytes; Go must reject them and accept the prefix.
			if !errors.Is(goErr, ErrExcessiveInput) {
				t.Fatalf("C accepts and ignores %d trailing bytes; Go error = %v, want ErrExcessiveInput",
					len(data)-consumed, goErr)
			}
			data = data[:consumed]
			goOut, goErr = Decompress(data)
		}
		if goErr != nil {
			t.Fatalf("C accepts (%d bytes out), Go rejects: %v", len(cOut), goErr)
		}
		if !bytes.Equal(goOut, cOut) {
			t.Fatalf("both accept, output differs: %s", firstDiff(goOut, cOut))
		}
		assertGoDecodes(t, data, cOut, nil, chunkSize, readSize)
	})
}

// FuzzCompoundDictCRef checks cross-decoding and encoder output with a dictionary.
func FuzzCompoundDictCRef(f *testing.F) {
	fox := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog\n"), 100)
	for quality := uint8(2); quality <= 11; quality++ {
		f.Add(fox[len(fox)/2:], fox[:len(fox)/3], quality, uint8(22), uint16(1), uint16(64))
		f.Add([]byte("hello, brotli fuzzer!"), []byte("brotli"), quality, uint8(10), uint16(5), uint16(1))
	}
	f.Add(pseudoRandomBytesCRef(20<<10, 3), pseudoRandomBytesCRef(8<<10, 3), uint8(5), uint8(12), uint16(4096), uint16(4096))

	f.Fuzz(func(t *testing.T, data, dict []byte, qualityByte, windowByte uint8, chunkWord, readWord uint16) {
		data = data[:min(len(data), fuzzMaxPlain)]
		dict = dict[:min(len(dict), fuzzMaxPlain)]
		if len(dict) == 0 {
			dict = []byte{0}
		}
		quality := 2 + int(qualityByte)%10
		lgwin := fuzzLGWin(windowByte)
		chunkSize := max(1, int(chunkWord))
		readSize := max(1, int(readWord))
		sizeHint := uint(len(data))

		cEncoded, err := cref.EncodeDict(data, dict, quality, lgwin, sizeHint)
		if err != nil {
			t.Fatalf("C EncodeDict (q=%d, lgwin=%d): %v", quality, lgwin, err)
		}
		assertGoDecodes(t, cEncoded, data, dict, chunkSize, readSize)

		pd, err := PrepareDictionary(dict)
		if err != nil {
			t.Fatalf("PrepareDictionary: %v", err)
		}
		opts := WriterOptions{
			LGWin:        lgwin,
			SizeHint:     sizeHint,
			Dictionaries: []*PreparedDictionary{pd},
		}
		goEncoded := encodeChunked(t, data, quality, opts, max(1, len(data)), false)
		assertCRefDecodes(t, goEncoded, data, dict)
		assertGoDecodes(t, goEncoded, data, dict, chunkSize, readSize)
		// Optimal parsing at q10–11 may change commands and compressed size.
		if quality <= 9 && !bytes.Equal(goEncoded, cEncoded) {
			t.Fatalf("Go stream differs from C: %s", firstDiff(goEncoded, cEncoded))
		}

		chunked := encodeChunked(t, data, quality, opts, chunkSize, false)
		assertCRefDecodes(t, chunked, data, dict)
		assertGoDecodes(t, chunked, data, dict, chunkSize, readSize)
	})
}

func encodeChunked(t *testing.T, data []byte, quality int, opts WriterOptions, chunkSize int, flush bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriterOptions(&buf, quality, opts)
	if err != nil {
		t.Fatalf("NewWriterOptions: %v", err)
	}
	for offset := 0; offset < len(data); {
		end := min(offset+chunkSize, len(data))
		if _, err := w.Write(data[offset:end]); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if flush {
			if err := w.Flush(); err != nil {
				t.Fatalf("Flush: %v", err)
			}
		}
		offset = end
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes()
}

func assertCRefDecodes(t *testing.T, encoded, want, dict []byte) {
	t.Helper()
	var got []byte
	var err error
	if dict == nil {
		got, err = cref.Decode(encoded)
	} else {
		got, err = cref.DecodeDict(encoded, dict)
	}
	if err != nil {
		t.Fatalf("C Decode of Go stream: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Go → C mismatch: %s", firstDiff(got, want))
	}
}

func assertGoDecodes(t *testing.T, encoded, want, dict []byte, chunkSize, readSize int) {
	t.Helper()
	if dict == nil {
		got, err := Decompress(encoded)
		if err != nil {
			t.Fatalf("Go Decompress: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("Go Decompress mismatch: %s", firstDiff(got, want))
		}
	}
	r := newFuzzReader(t, encoded, dict, chunkSize)
	if err := streamCompareReaderSized(r, want, readSize); err != nil {
		t.Fatalf("Go streaming (chunk=%d, read=%d): %v", chunkSize, readSize, err)
	}
}

func assertGoStreamRejects(t *testing.T, encoded []byte, chunkSize, readSize int) {
	t.Helper()
	r := newFuzzReader(t, encoded, nil, chunkSize)
	buf := make([]byte, readSize)
	for {
		_, err := r.Read(buf)
		if errors.Is(err, io.EOF) {
			t.Fatal("C rejects, Go streaming Reader accepts")
		}
		if err != nil {
			return
		}
	}
}

func newFuzzReader(t *testing.T, encoded, dict []byte, chunkSize int) *Reader {
	t.Helper()
	src := &chunkedReader{data: encoded, chunkSize: chunkSize}
	if dict == nil {
		return NewReader(src)
	}
	r, err := NewReaderOptions(src, ReaderOptions{Dictionaries: [][]byte{dict}})
	if err != nil {
		t.Fatalf("NewReaderOptions: %v", err)
	}
	return r
}

// Small reads exercise output-side suspension in the decoder.
func streamCompareReaderSized(r io.Reader, expected []byte, readSize int) error {
	buf := make([]byte, readSize)
	pos := 0
	for {
		n, err := r.Read(buf)
		if pos+n > len(expected) {
			return fmt.Errorf("decoded too many bytes: got >=%d, want %d", pos+n, len(expected))
		}
		if !bytes.Equal(buf[:n], expected[pos:pos+n]) {
			return fmt.Errorf("mismatch at offset %d: %s", pos, firstDiff(buf[:n], expected[pos:pos+n]))
		}
		pos += n
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
	}
	if pos != len(expected) {
		return fmt.Errorf("decoded %d bytes, want %d", pos, len(expected))
	}
	return nil
}

func firstDiff(got, want []byte) string {
	for i := range min(len(got), len(want)) {
		if got[i] != want[i] {
			return fmt.Sprintf("byte %d: got 0x%02x want 0x%02x (lengths %d, %d)",
				i, got[i], want[i], len(got), len(want))
		}
	}
	return fmt.Sprintf("lengths differ: got %d, want %d", len(got), len(want))
}
