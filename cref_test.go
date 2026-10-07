package brrr

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/iotest"

	"github.com/molecule-man/go-brrr/internal/creftest"
	"github.com/molecule-man/go-brrr/internal/encoder"
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

	type tc struct {
		name  string
		input []byte
	}

	cases := make([]struct {
		name  string
		input []byte
	}, 0, len(corpusFiles)+7)

	for _, name := range corpusFiles {
		cases = append(cases, tc{
			name:  name,
			input: readTestdata(t, filepath.Join("brotli-ref", "tests", "testdata", name)),
		})
	}

	// Synthetic cases that exercise patterns the corpus doesn't cover.
	cases = append(cases,
		tc{"hello_world", []byte("Hello, World!")},
		tc{"repeated_a_1000", bytes.Repeat([]byte("a"), 1000)},
		tc{"pseudo_random_2048", pseudoRandomBytesCRef(2048, 42)},
		tc{"multi_block_130000", bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz"), 5000)},
		tc{"pseudo_random_65536", pseudoRandomBytesCRef(65536, 99)},
		// Found in silesia/webster. It corrupted q10
		tc{"webster_dict_words", []byte(" the physical properties of the body bei")},
		tc{"match_ending_at_ring_end_262144", matchEndingAtRingEndCRef(1 << 18)},
		tc{"run_heavy_327680", runHeavyCRef(1<<18+1<<16, 118)},
	)

	return cases
}

func assertMatchesCRef(t *testing.T, goOut, cOut []byte) {
	t.Helper()
	if !bytes.Equal(goOut, cOut) {
		t.Errorf("output mismatch: Go produced %d bytes, C produced %d bytes, %s",
			len(goOut), len(cOut), firstDiff(goOut, cOut))
	}
}

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
			w.ResetWithSizeHint(&goBuf, sizeHint)
			if _, err := w.Write(tt.input); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			goOut := goBuf.Bytes()

			// Only q0/q1 guarantee chunk-invariant output. At higher qualities,
			// ring-buffer lookahead can change matches even with a fixed size hint.
			if quality <= 1 || sizeHint != 0 {
				const chunkSize = 7000 // straddles every fragment size in the matrix
				var chunkBuf bytes.Buffer
				cw, err := NewWriterOptions(&chunkBuf, quality, WriterOptions{
					LGWin:    lgwin,
					SizeHint: sizeHint,
				})
				if err != nil {
					t.Fatalf("NewWriter (chunked): %v", err)
				}
				for off := 0; off < len(tt.input); off += chunkSize {
					end := min(off+chunkSize, len(tt.input))
					if _, err := cw.Write(tt.input[off:end]); err != nil {
						t.Fatalf("chunked Write: %v", err)
					}
				}
				if err := cw.Close(); err != nil {
					t.Fatalf("chunked Close: %v", err)
				}
				if quality <= 1 && !bytes.Equal(chunkBuf.Bytes(), goOut) {
					t.Errorf("chunked output differs from single Write: chunked %d bytes, single-shot %d bytes",
						chunkBuf.Len(), len(goOut))
				}
				chunkDecoded := creftest.BrotliDecompress(t, chunkBuf.Bytes())
				if !bytes.Equal(chunkDecoded, tt.input) {
					t.Fatal("C chunked roundtrip mismatch")
				}
				r.Reset(bytes.NewReader(chunkBuf.Bytes()))
				if err := streamCompareReader(r, tt.input); err != nil {
					t.Fatalf("Go chunked roundtrip: %v", err)
				}
			}

			// Roundtrip: decompress with C reference and verify correctness.
			decompressed := creftest.BrotliDecompress(t, goOut)
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

			cOut := creftest.BrotliCompress(t, tt.input, quality, lgwin, sizeHint)

			assertMatchesCRef(t, goOut, cOut)
		})
	}
}

// TestMatchesCRef verifies the Go streaming encoder against the C reference
// across quality levels, window sizes, and size hints. It checks
// byte-identical output. All qualities verify roundtrip via C and Go
// decompression.
func TestMatchesCRef(t *testing.T) {
	t.Parallel()

	sizeHints := []struct {
		name string
		hint uint
	}{
		{"auto", 0},
		{"64KiB", 1 << 16},
		{"1MiB", 1 << 20},
	}

	lgwinValues := []int{14, 17, 22, 24}
	if os.Getenv("BRRR_LONG_TESTS") != "" {
		lgwinValues = []int{10, 14, 17, 18, 22, 24}
	}

	for _, sh := range sizeHints {
		t.Run("hint_"+sh.name, func(t *testing.T) {
			t.Parallel()
			for quality := 0; quality <= 11; quality++ {
				for _, lgwin := range lgwinValues {
					t.Run(fmt.Sprintf("q%d_lgwin%d", quality, lgwin), func(t *testing.T) {
						t.Parallel()
						testMatchesCRef(t, quality, lgwin, sh.hint)
					})
				}
			}
		})
	}
}

func TestCompressMatchesCRef(t *testing.T) {
	t.Parallel()

	for quality := 0; quality <= 11; quality++ {
		t.Run(fmt.Sprintf("q%d", quality), func(t *testing.T) {
			t.Parallel()
			for _, tt := range crefTestCases(t) {
				t.Run(tt.name, func(t *testing.T) {
					goOut, err := Compress(tt.input, quality)
					if err != nil {
						t.Fatalf("Compress: %v", err)
					}

					decompressed := creftest.BrotliDecompress(t, goOut)
					if !bytes.Equal(decompressed, tt.input) {
						t.Fatalf("C roundtrip mismatch: decompressed %d bytes, want %d bytes",
							len(decompressed), len(tt.input))
					}

					goDecompressed, err := Decompress(goOut)
					if err != nil {
						t.Fatalf("Go Decompress: %v", err)
					}
					if !bytes.Equal(goDecompressed, tt.input) {
						t.Fatalf("Go roundtrip mismatch: got %d bytes, want %d bytes",
							len(goDecompressed), len(tt.input))
					}

					cOut := creftest.BrotliCompress(t, tt.input, quality, defaultLGWin, uint(len(tt.input)))

					assertMatchesCRef(t, goOut, cOut)
				})
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

func TestFlushedStreamBeyondTheDefaultWindowDecodes(t *testing.T) {
	t.Parallel()

	html := readTestdata(t, filepath.Join("testdata", "gh_172KB.html"))
	js := readTestdata(t, filepath.Join("testdata", "reactcore_187KB.js"))
	events := readTestdata(t, filepath.Join("testdata", "github_events_8k.json"))
	src := slices.Concat(html, js, events)
	stream := similarCopies(src, (9<<20)/len(src)+1)
	const message = 32 << 10

	for quality := 0; quality <= 11; quality++ {
		t.Run(fmt.Sprintf("q%d", quality), func(t *testing.T) {
			t.Parallel()
			encoded := encodeChunked(t, stream, quality, WriterOptions{LGWin: defaultLGWin}, message, true)
			assertCRefDecodes(t, encoded, stream, nil)
			assertGoDecodes(t, encoded, stream, nil, message, message)
		})
	}
}

// seedEncoderPosForTest advances the streaming encoder's stream-position
// fields to pos so the next Write straddles the 32-bit wrap boundary without
// having to compress GiB of input first. Delegates to encoder package since
// the field pokes are not visible from this package.
func seedEncoderPosForTest(t *testing.T, w *Writer, pos uint64) {
	t.Helper()
	if !encoder.SeedStreamPosForTest(w.c, pos) {
		t.Fatalf("unexpected encoder type %T (Q0/Q1 do not use the wrap path)", w.c)
	}
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
// given quality and window size. For each corpus it uses the first 20% as
// dictionary and the last 90% as input.
func testCompoundDictMatchesCRef(t *testing.T, quality, lgwin int, sizeHint uint) {
	t.Helper()

	corpora := []string{"alice29.txt", "monkey"}

	for _, name := range corpora {
		t.Run(name, func(t *testing.T) {
			corpus := readTestdata(t, filepath.Join("brotli-ref", "tests", "testdata", name))
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
			cOut := creftest.BrotliCompressDict(t, input, dict, quality, lgwin, sizeHint)

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
		})
	}
}

// TestCompoundDictMatchesCRef verifies compound dictionary output across
// quality levels, window sizes, and size hints. It checks byte-identical output.
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
				for _, lgwin := range []int{10, 18, 22, 24} {
					t.Run(fmt.Sprintf("q%d_lgwin%d", quality, lgwin), func(t *testing.T) {
						t.Parallel()
						testCompoundDictMatchesCRef(t, quality, lgwin, sh.hint)
					})
				}
			}
		})
	}
}

func TestCompoundDictShortDictionaryMatchesCRef(t *testing.T) {
	input := []byte("uick 00cove01")
	dict := []byte("uick")
	pd, err := PrepareDictionary(dict)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := NewWriterOptions(&buf, 9, WriterOptions{
		LGWin:        19,
		SizeHint:     uint(len(input)),
		Dictionaries: []*PreparedDictionary{pd},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(input); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	want := creftest.BrotliCompressDict(t, input, dict, 9, 19, uint(len(input)))
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("Go stream differs from C:\n got %x\nwant %x", buf.Bytes(), want)
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
				compressed := creftest.BrotliCompressDict(t, input, dict, quality, lgwin, 0)

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

func similarCopies(data []byte, n int) []byte {
	if n <= 1 || len(data) == 0 {
		return data
	}
	out := make([]byte, 0, n*len(data))
	rng := rand.New(rand.NewPCG(uint64(len(data)), uint64(n)))
	for k := range n {
		start := len(out)
		out = append(out, data...)
		for range min(k, 4) {
			out[start+rng.IntN(len(data))] ^= byte(1 + rng.IntN(255))
		}
	}
	return out
}

func pseudoRandomBytesCRef(n int, seed uint64) []byte {
	rng := rand.New(rand.NewPCG(seed, 0))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.IntN(256))
	}
	return b
}

// TestTagSwitchMatchesCRef covers the q5/q6 large-input hashers after they
// switch to tags. The first part is binary, so that the encoder switches.
// The rest copies pieces of it, so that matches use the rebuilt tags.
func TestTagSwitchMatchesCRef(t *testing.T) {
	t.Parallel()

	const binaryLen = 768 << 10
	input := pseudoRandomBytesCRef(binaryLen, 11)
	rng := rand.New(rand.NewPCG(11, 12))
	for len(input) < 2<<20 {
		off := rng.IntN(binaryLen - 2048)
		input = append(input, input[off:off+8+rng.IntN(2040)]...)
		input = append(input, pseudoRandomBytesCRef(rng.IntN(64), rng.Uint64())...)
	}

	for _, quality := range []int{5, 6} {
		t.Run(fmt.Sprintf("q%d", quality), func(t *testing.T) {
			t.Parallel()

			var goBuf bytes.Buffer
			w, err := NewWriterOptions(&goBuf, quality, WriterOptions{SizeHint: uint(len(input))})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			if _, err := w.Write(input); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			cOut := creftest.BrotliCompress(t, input, quality, defaultLGWin, uint(len(input)))
			assertMatchesCRef(t, goBuf.Bytes(), cOut)
		})
	}
}

func TestSizeHintedRingWrapMatchesCRef(t *testing.T) {
	t.Parallel()

	const lgwin = 19
	names := []string{"plrabn12.txt", "lcet10.txt", "mapsdatazrh", "alice29.txt", "plrabn12.txt", "lcet10.txt"}
	parts := make([][]byte, len(names))
	for i, name := range names {
		parts[i] = readTestdata(t, filepath.Join("brotli-ref", "tests", "testdata", name))
	}
	text := slices.Concat(parts...)
	inputs := []struct {
		name  string
		input []byte
	}{
		{"text", text},
		{"run_heavy", runHeavyCRef(5<<19, 119)},
	}

	for _, in := range inputs {
		for quality := 5; quality <= 9; quality++ {
			t.Run(fmt.Sprintf("%s/q%d", in.name, quality), func(t *testing.T) {
				t.Parallel()

				var goBuf bytes.Buffer
				w, err := NewWriterOptions(&goBuf, quality, WriterOptions{LGWin: lgwin, SizeHint: uint(len(in.input))})
				if err != nil {
					t.Fatalf("NewWriter: %v", err)
				}
				if _, err := w.Write(in.input); err != nil {
					t.Fatalf("Write: %v", err)
				}
				if err := w.Close(); err != nil {
					t.Fatalf("Close: %v", err)
				}
				cOut := creftest.BrotliCompress(t, in.input, quality, lgwin, uint(len(in.input)))
				assertMatchesCRef(t, goBuf.Bytes(), cOut)
			})
		}
	}
}

func matchEndingAtRingEndCRef(n int) []byte {
	rng := rand.New(rand.NewPCG(uint64(n), 7))
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a' + byte(rng.IntN(16))
	}
	tail := n - 32
	copy(b[tail-4999:tail-4999+32], b[tail-9000:tail-9000+32])
	b[n-4999] = b[0]
	for i, d := range []int{9000, 7000, 6000, 5000} {
		at := tail - 800 + i*200
		copy(b[at:at+200], b[at-d:at-d+200])
	}
	copy(b[tail:], b[tail-9000:tail-9000+32])
	return b
}

func runHeavyCRef(n int, seed uint64) []byte {
	rng := rand.New(rand.NewPCG(seed, 5))
	b := make([]byte, 0, n)
	for len(b) < n {
		switch rng.IntN(3) {
		case 0:
			c := byte('a' + rng.IntN(2))
			for k := 1 + rng.IntN(300); k > 0; k-- {
				b = append(b, c)
			}
		case 1:
			if len(b) > 8 {
				d := 1 + rng.IntN(min(len(b), 60000))
				for k := 4 + rng.IntN(300); k > 0; k-- {
					b = append(b, b[len(b)-d])
				}
			}
		default:
			for k := 1 + rng.IntN(20); k > 0; k-- {
				b = append(b, byte('a'+rng.IntN(2)))
			}
		}
	}
	return b[:n]
}
