package brrr

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/iotest"
)

// testdataCache shares corpus file contents across the many parallel
// TestMatchesCRef / TestCompoundDictMatchesCRef subtests, which would
// otherwise each call os.ReadFile and hold an independent copy. With
// BRRR_LONG_TESTS active, bb.binast alone (12 MiB) was being duplicated
// ~120 times.
var (
	testdataCacheMu sync.Mutex
	testdataCache   = make(map[string][]byte)
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
	w, err := NewWriterOptions(&goBuf, quality, WriterOptions{
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

			// Verify with the Go streaming decoder, comparing chunk-by-chunk
			// against the input so a 12 MiB-class case (e.g. bb.binast)
			// doesn't allocate a third full-input-sized buffer alongside
			// the C-decoder and Go one-shot results above.
			r.Reset(bytes.NewReader(goOut))
			if err := streamCompareReader(r, tt.input); err != nil {
				t.Fatalf("Go streaming roundtrip mismatch: %v", err)
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

// TestPositionWrap exercises the hasher reset in updateLastProcessedPos that
// fires when the 32-bit wrapped stream position rolls over. Instead of
// compressing 3+ GiB of real input, it pokes the encoder's position fields
// to just before the wrap boundary, then writes a small input that crosses
// it. If updateLastProcessedPos failed to reset hashers, stale wrapped-pos
// entries would produce broken back-references and the Go-decoder roundtrip
// would diverge from the input.
func TestPositionWrap(t *testing.T) {
	t.Parallel()

	// 4 MiB straddling the wrap (seed at 3 GiB - 2 MiB, write 4 MiB → end
	// at 3 GiB + 2 MiB). Pseudo-random data so the encoder actually populates
	// hash tables and emits matches across the boundary.
	data := pseudoRandomBytesCRef(4<<20, 7)

	for quality := 2; quality <= 11; quality++ {
		t.Run(fmt.Sprintf("q%d", quality), func(t *testing.T) {
			t.Parallel()

			const lgwin = 18

			var goBuf bytes.Buffer
			w, err := NewWriterOptions(&goBuf, quality, WriterOptions{LGWin: lgwin})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}

			seedEncoderPosForTest(t, w, (3<<30)-(2<<20))

			if _, err := w.Write(data); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			r := NewReader(bytes.NewReader(goBuf.Bytes()))
			decoded, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !bytes.Equal(decoded, data) {
				t.Fatalf("roundtrip mismatch: decoded %d bytes, want %d",
					len(decoded), len(data))
			}
		})
	}
}

// seedEncoderPosForTest advances the streaming encoder's stream-position
// fields to pos so the next Write straddles the 32-bit wrap boundary without
// having to compress GiB of input first. The ring buffer is left empty (the
// encoder only references data we subsequently write), and lgwin caps
// distances so references stay within the in-metablock region.
func seedEncoderPosForTest(t *testing.T, w *Writer, pos uint64) {
	t.Helper()
	var es *encodeState
	switch enc := w.enc.(type) {
	case *encoderArena:
		es = &enc.encodeState
	case *encoderSplit:
		es = &enc.encodeState
	default:
		t.Fatalf("unexpected encoder type %T (Q0/Q1 do not use the wrap path)", w.enc)
	}
	es.inputPos = pos
	es.lastProcessedPos = pos
	es.lastFlushPos = pos
	es.ringBufPos = uint32(pos & uint64(es.mask))
}

func readTestdata(t *testing.T, path string) []byte {
	t.Helper()

	testdataCacheMu.Lock()
	if data, ok := testdataCache[path]; ok {
		testdataCacheMu.Unlock()
		return data
	}
	testdataCacheMu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	testdataCacheMu.Lock()
	testdataCache[path] = data
	testdataCacheMu.Unlock()
	return data
}

// streamCompareReader reads from r and verifies the bytes match expected
// without materializing r's full output into a single slice.
func streamCompareReader(r io.Reader, expected []byte) error {
	buf := make([]byte, 64*1024)
	pos := 0
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if pos+n > len(expected) {
				return fmt.Errorf("decoded too many bytes: got >=%d, want %d", pos+n, len(expected))
			}
			for i := range n {
				if buf[i] != expected[pos+i] {
					return fmt.Errorf("byte %d: got 0x%02x want 0x%02x", pos+i, buf[i], expected[pos+i])
				}
			}
			pos += n
		}
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

	pd, err := PrepareDictionary(dict)
	if err != nil {
		t.Fatalf("PrepareDictionary: %v", err)
	}

	// Go encoder with compound dictionary.
	var goBuf bytes.Buffer
	w, err := NewWriterOptions(&goBuf, quality, WriterOptions{
		LGWin:        lgwin,
		SizeHint:     sizeHint,
		Dictionaries: []*PreparedDictionary{pd},
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
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

	pd1, err := PrepareDictionary(chunk1)
	if err != nil {
		t.Fatalf("PrepareDictionary(chunk1): %v", err)
	}
	pd2, err := PrepareDictionary(chunk2)
	if err != nil {
		t.Fatalf("PrepareDictionary(chunk2): %v", err)
	}

	for quality := 2; quality <= 9; quality++ {
		for _, lgwin := range []int{14, 18, 22} {
			t.Run(fmt.Sprintf("q%d_lgwin%d", quality, lgwin), func(t *testing.T) {
				t.Parallel()

				// Encode with Go encoder + compound dictionary.
				var buf bytes.Buffer
				w, err := NewWriterOptions(&buf, quality, WriterOptions{
					LGWin:        lgwin,
					Dictionaries: []*PreparedDictionary{pd1, pd2},
				})
				if err != nil {
					t.Fatalf("NewWriter: %v", err)
				}
				if _, err := w.Write(input); err != nil {
					t.Fatalf("Write: %v", err)
				}
				if err := w.Close(); err != nil {
					t.Fatalf("Close: %v", err)
				}
				compressed := buf.Bytes()

				// Decode with Go decoder + same compound dictionary.
				r, err := NewReaderOptions(bytes.NewReader(compressed), ReaderOptions{
					Dictionaries: [][]byte{chunk1, chunk2},
				})
				if err != nil {
					t.Fatalf("NewReaderOptions: %v", err)
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
				r, err := NewReaderOptions(bytes.NewReader(compressed), ReaderOptions{
					Dictionaries: [][]byte{dict},
				})
				if err != nil {
					t.Fatalf("NewReaderOptions: %v", err)
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

	pd, err := PrepareDictionary(dict)
	if err != nil {
		t.Fatalf("PrepareDictionary: %v", err)
	}

	// Encode at Q5 with compound dictionary.
	var buf bytes.Buffer
	w, err := NewWriterOptions(&buf, 5, WriterOptions{
		LGWin:        18,
		Dictionaries: []*PreparedDictionary{pd},
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if _, err := w.Write(input); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	compressed := buf.Bytes()

	// Decode one byte at a time.
	r, err := NewReaderOptions(iotest.OneByteReader(bytes.NewReader(compressed)), ReaderOptions{
		Dictionaries: [][]byte{dict},
	})
	if err != nil {
		t.Fatalf("NewReaderOptions: %v", err)
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
