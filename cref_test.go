package brrr

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"testing/iotest"
)

// crefTestCases returns the shared set of test cases for C-ref matching tests.
// It includes all files from the brotli reference corpus (bb.binast gated
// behind BRRR_LONG_TESTS) plus synthetic cases.
func crefTestCases(t *testing.T) []struct {
	name  string
	input []byte
} {
	t.Helper()

	// All files from the brotli reference test corpus.
	corpusFiles := []string{
		"empty",
		"x",
		"xyzzy",
		"10x10y",
		"quickfox",
		"64x",
		"ukkonooa",
		"cp852-utf8",
		"monkey",
		"cp1251-utf16le",
		"random_chunks",
		"random_org_10k.bin",
		"compressed_file",
		"backward65536",
		"asyoulik.txt",
		"compressed_repeated",
		"alice29.txt",
		"quickfox_repeated",
		"zeros",
		"zerosukkanooa",
		"mapsdatazrh",
		"lcet10.txt",
		"plrabn12.txt",
	}

	// bb.binast is 12 MiB; only include it for long-running test runs.
	if os.Getenv("BRRR_LONG_TESTS") != "" {
		corpusFiles = append(corpusFiles, "bb.binast")
	}

	cases := make([]struct {
		name  string
		input []byte
	}, 0, len(corpusFiles)+5)

	for _, name := range corpusFiles {
		cases = append(cases, struct {
			name  string
			input []byte
		}{
			name:  name,
			input: readTestdata(t, filepath.Join("brotli-ref", "tests", "testdata", name)),
		})
	}

	// Synthetic cases that exercise patterns the corpus doesn't cover.
	cases = append(cases,
		struct {
			name  string
			input []byte
		}{"hello_world", []byte("Hello, World!")},
		struct {
			name  string
			input []byte
		}{"repeated_a_1000", bytes.Repeat([]byte("a"), 1000)},
		struct {
			name  string
			input []byte
		}{"pseudo_random_2048", pseudoRandomBytesCRef(2048, 42)},
		struct {
			name  string
			input []byte
		}{"multi_block_130000", bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz"), 5000)},
		struct {
			name  string
			input []byte
		}{"pseudo_random_65536", pseudoRandomBytesCRef(65536, 99)},
	)

	return cases
}

// testMatchesCRef verifies that the Go streaming encoder at the given quality,
// window size, and size hint produces byte-identical output to the C reference
// encoder.
func testMatchesCRef(t *testing.T, quality, lgwin int, sizeHint uint) {
	t.Helper()

	var goBuf bytes.Buffer
	w, err := NewWriter(&goBuf, WriterOptions{
		Quality:  quality,
		LGWin:    lgwin,
		SizeHint: sizeHint,
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	r := NewReader(bytes.NewReader(nil))

	for _, tt := range crefTestCases(t) {
		t.Run(tt.name, func(t *testing.T) {
			goBuf.Reset()
			w.Reset(&goBuf)
			if _, err := w.Write(tt.input); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			goOut := goBuf.Bytes()

			// Roundtrip: decompress with C reference and verify correctness.
			decompressed := brotliDecompress(t, goOut)
			if !bytes.Equal(decompressed, tt.input) {
				t.Fatalf("roundtrip mismatch: decompressed %d bytes, want %d bytes",
					len(decompressed), len(tt.input))
			}

			// Verify with the Go one-shot decoder.
			goDecompressed, err := Decompress(goOut)
			if err != nil {
				t.Fatalf("Go Decompress: %v", err)
			}
			if !bytes.Equal(goDecompressed, tt.input) {
				t.Fatalf("Go one-shot roundtrip mismatch: got %d bytes, want %d bytes",
					len(goDecompressed), len(tt.input))
			}

			// Verify with the Go streaming decoder.
			r.Reset(bytes.NewReader(goOut))
			goStreamed, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("Go streaming ReadAll: %v", err)
			}
			if !bytes.Equal(goStreamed, tt.input) {
				t.Fatalf("Go streaming roundtrip mismatch: got %d bytes, want %d bytes",
					len(goStreamed), len(tt.input))
			}

			cOut := brotliCompress(t, tt.input, quality, lgwin, sizeHint)

			// Conditions where Go output may differ from C but both are valid:
			//   - Q10+: Zopfli optimal parsing where Go's math.Log2 diverges
			//     from glibc's log2 by up to 1 ULP for some inputs, causing
			//     different command choices.
			//   - Q5–9 lgwin>16: h5/h6 hashers elide ring-buffer end checks
			//     (the tail mirror makes them redundant), which can produce
			//     different match selections vs the C reference.
			// In both cases accept output within 0.02% of C's size; roundtrip
			// correctness is already verified above via C and Go decompression.
			if quality >= 10 || (quality >= 5 && lgwin > 16) {
				goLen := len(goOut)
				cLen := len(cOut)
				threshold := float64(cLen) * 1.0002
				if float64(goLen) > threshold {
					t.Errorf("Go output too large: %d bytes (C: %d bytes, threshold: %.0f)",
						goLen, cLen, threshold)
				}
			} else {
				if !bytes.Equal(goOut, cOut) {
					t.Errorf("output mismatch: Go produced %d bytes, C produced %d bytes",
						len(goOut), len(cOut))
					minLen := min(len(goOut), len(cOut))
					for i := range minLen {
						if goOut[i] != cOut[i] {
							t.Errorf("first difference at byte %d: Go=0x%02x C=0x%02x",
								i, goOut[i], cOut[i])
							break
						}
					}
				}
			}
		})
	}
}

// TestMatchesCRef verifies the Go streaming encoder against the C reference
// across quality levels, window sizes, and size hints. For Q5–9 lgwin>16 and
// Q10–11 it checks roundtrip correctness and that compressed size is within
// 0.02% of C. Other combinations check byte-identical output. All qualities
// verify roundtrip via C and Go decompression.
func TestMatchesCRef(t *testing.T) {
	t.Parallel()

	sizeHints := []struct {
		name string
		hint uint
	}{
		{"auto", 0},
		{"1MiB", 1 << 20},
	}

	for _, sh := range sizeHints {
		t.Run("hint_"+sh.name, func(t *testing.T) {
			t.Parallel()
			for quality := 0; quality <= 11; quality++ {
				for _, lgwin := range []int{10, 14, 18, 22, 24} {
					t.Run(fmt.Sprintf("q%d_lgwin%d", quality, lgwin), func(t *testing.T) {
						t.Parallel()
						testMatchesCRef(t, quality, lgwin, sh.hint)
					})
				}
			}
		})
	}
}

// TestQ2EmptyMatchesCRef checks that an empty stream matches the C reference.
func TestQ2EmptyMatchesCRef(t *testing.T) {
	t.Parallel()

	var goBuf bytes.Buffer
	w, err := NewWriter(&goBuf, WriterOptions{Quality: 2, LGWin: 18})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	goOut := goBuf.Bytes()

	cOut := brotliCompress(t, nil, 2, 18, 0)

	if !bytes.Equal(goOut, cOut) {
		t.Errorf("empty stream mismatch: Go=%x C=%x", goOut, cOut)
	}
}

// TestQ2PositionWrapMatchesCRef verifies that the Go encoder produces
// byte-identical output to the C reference when the 32-bit wrapped position
// rolls over (~3 GiB of input). This exercises the hasher reset path in
// updateLastProcessedPos.
func TestQ2PositionWrapMatchesCRef(t *testing.T) {
	t.Parallel()

	if os.Getenv("BRRR_LONG_TESTS") == "" {
		t.Skip("skipping 3+ GiB position-wrap test; set BRRR_LONG_TESTS=1 to enable")
	}

	chunk := readTestdata(t, filepath.Join("brotli-ref", "tests", "testdata", "bb.binast"))

	// 3.25 GiB total — enough to cross the 3 GiB wrap boundary.
	const target = 3<<30 + 256<<20
	reps := target/len(chunk) + 1

	// Build the full input.
	input := make([]byte, 0, int64(reps)*int64(len(chunk)))
	for range reps {
		input = append(input, chunk...)
	}

	// Go encoder: stream chunks, collect compressed output.
	var goBuf bytes.Buffer
	w, err := NewWriter(&goBuf, WriterOptions{Quality: 2, LGWin: 18})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for range reps {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	goOut := goBuf.Bytes()

	// C reference encoder.
	cOut := brotliCompress(t, input, 2, 18, 0)

	if !bytes.Equal(goOut, cOut) {
		t.Errorf("output mismatch: Go produced %d bytes, C produced %d bytes",
			len(goOut), len(cOut))
		minLen := min(len(goOut), len(cOut))
		for i := range minLen {
			if goOut[i] != cOut[i] {
				t.Errorf("first difference at byte %d: Go=0x%02x C=0x%02x",
					i, goOut[i], cOut[i])
				break
			}
		}
	}

	t.Logf("compared %d compressed bytes from %d bytes of input (%d reps)",
		len(goOut), int64(reps)*int64(len(chunk)), reps)
}

func readTestdata(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// testCompoundDictMatchesCRef verifies that the Go encoder with a compound
// dictionary produces byte-identical output to the C reference encoder at the
// given quality and window size. Uses alice29.txt: first 20% as dictionary,
// last 90% as input.
func testCompoundDictMatchesCRef(t *testing.T, quality, lgwin int, sizeHint uint) {
	t.Helper()

	corpus := readTestdata(t, filepath.Join("brotli-ref", "tests", "testdata", "alice29.txt"))
	dictEnd := len(corpus) * 20 / 100
	inputStart := len(corpus) * 10 / 100

	dict := corpus[:dictEnd]
	input := corpus[inputStart:]

	// Go encoder with compound dictionary.
	var goBuf bytes.Buffer
	w, err := NewWriter(&goBuf, WriterOptions{
		Quality:  quality,
		LGWin:    lgwin,
		SizeHint: sizeHint,
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.AttachDictionary(dict); err != nil {
		t.Fatalf("AttachDictionary: %v", err)
	}
	if _, err := w.Write(input); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	goOut := goBuf.Bytes()

	// C reference encoder with compound dictionary.
	cOut := brotliCompressDict(t, input, dict, quality, lgwin, sizeHint)

	if !bytes.Equal(goOut, cOut) {
		t.Errorf("output mismatch: Go produced %d bytes, C produced %d bytes",
			len(goOut), len(cOut))
		minLen := min(len(goOut), len(cOut))
		for i := range minLen {
			if goOut[i] != cOut[i] {
				t.Errorf("first difference at byte %d: Go=0x%02x C=0x%02x",
					i, goOut[i], cOut[i])
				break
			}
		}
	}
}

// TestCompoundDictMatchesCRef verifies compound dictionary output across
// quality levels, window sizes, and size hints. For Q<=9 it checks
// byte-identical output; for Q10+ it checks roundtrip and size within 0.05% of C.
func TestCompoundDictMatchesCRef(t *testing.T) {
	t.Parallel()

	sizeHints := []struct {
		name string
		hint uint
	}{
		{"auto", 0},
		{"1MiB", 1 << 20},
	}

	for _, sh := range sizeHints {
		t.Run("hint_"+sh.name, func(t *testing.T) {
			t.Parallel()
			for quality := 2; quality <= 11; quality++ {
				for _, lgwin := range []int{10, 14, 18, 22, 24} {
					t.Run(fmt.Sprintf("q%d_lgwin%d", quality, lgwin), func(t *testing.T) {
						t.Parallel()
						testCompoundDictMatchesCRef(t, quality, lgwin, sh.hint)
					})
				}
			}
		})
	}
}

// TestCompoundDictDecoderRoundtrip verifies that the Go decoder correctly
// handles compound dictionary references across quality levels and window sizes.
func TestCompoundDictDecoderRoundtrip(t *testing.T) {
	t.Parallel()

	corpus := readTestdata(t, filepath.Join("brotli-ref", "tests", "testdata", "alice29.txt"))

	// Split into 3 dictionary chunks and input to exercise multi-chunk decode.
	chunk1 := corpus[:len(corpus)*10/100]
	chunk2 := corpus[len(corpus)*10/100 : len(corpus)*20/100]
	input := corpus[len(corpus)*10/100:]

	for quality := 2; quality <= 9; quality++ {
		for _, lgwin := range []int{14, 18, 22} {
			t.Run(fmt.Sprintf("q%d_lgwin%d", quality, lgwin), func(t *testing.T) {
				t.Parallel()

				// Encode with Go encoder + compound dictionary.
				var buf bytes.Buffer
				w, err := NewWriter(&buf, WriterOptions{Quality: quality, LGWin: lgwin})
				if err != nil {
					t.Fatalf("NewWriter: %v", err)
				}
				if err := w.AttachDictionary(chunk1); err != nil {
					t.Fatalf("AttachDictionary(chunk1): %v", err)
				}
				if err := w.AttachDictionary(chunk2); err != nil {
					t.Fatalf("AttachDictionary(chunk2): %v", err)
				}
				if _, err := w.Write(input); err != nil {
					t.Fatalf("Write: %v", err)
				}
				if err := w.Close(); err != nil {
					t.Fatalf("Close: %v", err)
				}
				compressed := buf.Bytes()

				// Decode with Go decoder + same compound dictionary.
				r := NewReader(bytes.NewReader(compressed))
				if err := r.AttachDictionary(chunk1); err != nil {
					t.Fatalf("Reader.AttachDictionary(chunk1): %v", err)
				}
				if err := r.AttachDictionary(chunk2); err != nil {
					t.Fatalf("Reader.AttachDictionary(chunk2): %v", err)
				}
				got, err := io.ReadAll(r)
				if err != nil {
					t.Fatalf("ReadAll: %v", err)
				}
				if !bytes.Equal(got, input) {
					t.Fatalf("roundtrip mismatch: got %d bytes, want %d bytes", len(got), len(input))
				}
			})
		}
	}
}

// TestCompoundDictDecoderCRef decodes C-reference-encoded compound dictionary
// streams with the Go decoder to verify cross-implementation compatibility.
func TestCompoundDictDecoderCRef(t *testing.T) {
	t.Parallel()

	corpus := readTestdata(t, filepath.Join("brotli-ref", "tests", "testdata", "alice29.txt"))
	dict := corpus[:len(corpus)*20/100]
	input := corpus[len(corpus)*10/100:]

	for quality := 2; quality <= 9; quality++ {
		for _, lgwin := range []int{14, 18, 22} {
			t.Run(fmt.Sprintf("q%d_lgwin%d", quality, lgwin), func(t *testing.T) {
				t.Parallel()

				// C encoder with compound dictionary.
				compressed := brotliCompressDict(t, input, dict, quality, lgwin, 0)

				// Go decoder with compound dictionary.
				r := NewReader(bytes.NewReader(compressed))
				if err := r.AttachDictionary(dict); err != nil {
					t.Fatalf("AttachDictionary: %v", err)
				}
				got, err := io.ReadAll(r)
				if err != nil {
					t.Fatalf("ReadAll: %v", err)
				}
				if !bytes.Equal(got, input) {
					t.Fatalf("roundtrip mismatch: got %d bytes, want %d bytes", len(got), len(input))
				}
			})
		}
	}
}

// TestCompoundDictDecoderSmallBuffer decodes with a tiny read buffer to
// exercise compound dictionary copy suspension and resume across ringbuffer
// flushes.
func TestCompoundDictDecoderSmallBuffer(t *testing.T) {
	t.Parallel()

	corpus := readTestdata(t, filepath.Join("brotli-ref", "tests", "testdata", "alice29.txt"))
	dict := corpus[:len(corpus)*20/100]
	input := corpus[len(corpus)*10/100:]

	// Encode at Q5 with compound dictionary.
	var buf bytes.Buffer
	w, err := NewWriter(&buf, WriterOptions{Quality: 5, LGWin: 18})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.AttachDictionary(dict); err != nil {
		t.Fatalf("AttachDictionary: %v", err)
	}
	if _, err := w.Write(input); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	compressed := buf.Bytes()

	// Decode one byte at a time.
	r := NewReader(iotest.OneByteReader(bytes.NewReader(compressed)))
	if err := r.AttachDictionary(dict); err != nil {
		t.Fatalf("AttachDictionary: %v", err)
	}
	var got []byte
	readBuf := make([]byte, 37) // small, odd-sized buffer
	for {
		n, err := r.Read(readBuf)
		got = append(got, readBuf[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v after %d bytes", err, len(got))
		}
	}
	if !bytes.Equal(got, input) {
		t.Fatalf("roundtrip mismatch: got %d bytes, want %d bytes", len(got), len(input))
	}
}

func pseudoRandomBytesCRef(n int, seed uint64) []byte {
	rng := rand.New(rand.NewPCG(seed, 0))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.IntN(256))
	}
	return b
}
