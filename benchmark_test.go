package brrr_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	brrr "github.com/molecule-man/go-brrr"
)

type compressor interface {
	io.WriteCloser
	Reset(io.Writer)
}

type compressorFactory func(w io.Writer, quality, lgwin int) (compressor, error)

var extraCompressors []struct {
	name    string
	factory compressorFactory
}

type oneshotCompressorFactory func(w io.Writer, quality, lgwin int) (io.WriteCloser, error)

// oneshotOnlyCompressors holds implementations that lack true Reset support
// and are only included in oneshot benchmarks.
var oneshotOnlyCompressors []struct {
	name    string
	factory oneshotCompressorFactory
}

var testCases = []struct {
	name  string
	paths []string
}{
	{"MixedPayloads", []string{
		filepath.Join("brotli-ref", "tests", "testdata", "alice29.txt"),
		filepath.Join("brotli-ref", "tests", "testdata", "asyoulik.txt"),
		filepath.Join("brotli-ref", "tests", "testdata", "lcet10.txt"),
		filepath.Join("brotli-ref", "tests", "testdata", "mapsdatazrh"),
	}},
	// VariedPayloads rotates through a larger, heterogeneous set of
	// payloads (mixed sizes and content types). Acts as a marker against
	// benchmark-shaped optimizations: wins that only show up when the
	// same input is fed back-to-back should not move this row.
	{"VariedPayloads", []string{
		filepath.Join("testdata", "github_events_2k.json"),
		filepath.Join("testdata", "github_events_5k.json"),
		filepath.Join("testdata", "github_events_8k.json"),
		filepath.Join("testdata", "gh_172KB.html"),
		filepath.Join("testdata", "reactcore_187KB.js"),
		filepath.Join("brotli-ref", "tests", "testdata", "alice29.txt"),
		filepath.Join("brotli-ref", "tests", "testdata", "asyoulik.txt"),
		filepath.Join("brotli-ref", "tests", "testdata", "lcet10.txt"),
		filepath.Join("brotli-ref", "tests", "testdata", "plrabn12.txt"),
	}},
	{"Small", []string{filepath.Join("brotli-ref", "tests", "testdata", "monkey")}},
	{"Json_2k", []string{filepath.Join("testdata", "github_events_2k.json")}},
	{"Json_5k", []string{filepath.Join("testdata", "github_events_5k.json")}},
	{"Json_8k", []string{filepath.Join("testdata", "github_events_8k.json")}},
	{"Medium", []string{filepath.Join("brotli-ref", "tests", "testdata", "alice29.txt")}},
	{"Large", []string{filepath.Join("brotli-ref", "tests", "testdata", "plrabn12.txt")}},
	{"LargeTechnical", []string{filepath.Join("brotli-ref", "tests", "testdata", "lcet10.txt")}},
}

func benchLGWin() int {
	if s := os.Getenv("BENCH_LGWIN"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil {
			panic(fmt.Sprintf("invalid BENCH_LGWIN=%q: %v", s, err))
		}
		return v
	}
	return 22
}

// benchQualities returns the quality levels to benchmark. By default it
// returns {1, 6, 11}. Set BENCH_QUALITIES=all to run all 12 levels (0–11),
// or a comma-separated list like BENCH_QUALITIES=0,3,6,9.
func benchQualities() []int {
	s := os.Getenv("BENCH_QUALITIES")
	switch s {
	case "", "default":
		return []int{1, 6, 11}
	case "all":
		qs := make([]int, 12)
		for i := range qs {
			qs[i] = i
		}
		return qs
	default:
		parts := bytes.Split([]byte(s), []byte(","))
		qs := make([]int, 0, len(parts))
		for _, p := range parts {
			v, err := strconv.Atoi(strings.TrimSpace(string(p)))
			if err != nil {
				panic(fmt.Sprintf("invalid BENCH_QUALITIES=%q: %v", s, err))
			}
			qs = append(qs, v)
		}
		return qs
	}
}

func benchSizeHint() uint {
	if s := os.Getenv("BENCH_SIZE_HINT"); s != "" {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			panic(fmt.Sprintf("invalid BENCH_SIZE_HINT=%q: %v", s, err))
		}
		return uint(v)
	}
	return 0
}

// benchParamSuffix returns slash-separated lgwin/sizeHint name sections
// to append after the payload section when their env vars are set.
func benchParamSuffix(lgwin int, sizeHint uint) string {
	var s string
	if os.Getenv("BENCH_LGWIN") != "" {
		s += fmt.Sprintf("/lgwin=%d", lgwin)
	}
	if os.Getenv("BENCH_SIZE_HINT") != "" && sizeHint > 0 {
		s += fmt.Sprintf("/sizeHint=%d", sizeHint)
	}
	return s
}

func BenchmarkCompress(b *testing.B) {
	lgwin := benchLGWin()
	sizeHint := benchSizeHint()

	for q := range 12 {
		b.Run(fmt.Sprintf("q=%d", q), func(b *testing.B) {
			suffix := benchParamSuffix(lgwin, sizeHint)

			for _, tc := range testCases {
				payloads := make([][]byte, len(tc.paths))
				for i, path := range tc.paths {
					data, err := os.ReadFile(path)
					if err != nil {
						b.Fatal(err)
					}
					payloads[i] = data
				}

				b.Run("payload="+tc.name+suffix, func(b *testing.B) {
					b.Run("impl=go-brrr", func(b *testing.B) {
						w, err := brrr.NewWriter(io.Discard, brrr.WriterOptions{Quality: q, LGWin: lgwin, SizeHint: sizeHint})
						if err != nil {
							b.Fatal(err)
						}
						benchCompress(b, w, payloads)
					})
					for _, ec := range extraCompressors {
						b.Run("impl="+ec.name, func(b *testing.B) {
							w, err := ec.factory(io.Discard, q, lgwin)
							if err != nil {
								b.Fatal(err)
							}
							benchCompress(b, w, payloads)
						})
					}
				})
			}
		})
	}
}

// hasherBenchCase maps an encoder/hasher name to the parameters that trigger it.
var hasherBenchCases = []struct {
	name     string
	quality  int
	lgwin    int
	sizeHint uint
}{
	{"onepass", 0, 22, 0},
	{"twopass", 1, 22, 0},
	{"h2", 2, 22, 0},
	{"h3", 3, 22, 0},
	{"h4", 4, 22, 0},
	{"h54", 4, 22, 1 << 20},
	{"h40q5", 5, 16, 0},
	{"h40q6", 6, 16, 0},
	{"h5", 5, 22, 0},
	{"h6", 5, 22, 1 << 20},
	{"h5b5", 6, 22, 0},
	{"h6b5", 6, 22, 1 << 20},
	{"h41q7", 7, 16, 0},
	{"h41q8", 8, 16, 0},
	{"h5b6", 7, 22, 0},
	{"h6b6", 7, 22, 1 << 20},
	{"h5b7", 8, 22, 0},
	{"h6b7", 8, 22, 1 << 20},
	{"h42", 9, 16, 0},
	{"h5b8", 9, 22, 0},
	{"h6b8", 9, 22, 1 << 20},
	{"h10", 10, 22, 0},
}

func BenchmarkCompressHasher(b *testing.B) {
	for _, hc := range hasherBenchCases {
		b.Run("h="+hc.name, func(b *testing.B) {
			for _, tc := range testCases {
				payloads := make([][]byte, len(tc.paths))
				for i, path := range tc.paths {
					data, err := os.ReadFile(path)
					if err != nil {
						b.Fatal(err)
					}
					payloads[i] = data
				}

				b.Run("payload="+tc.name, func(b *testing.B) {
					w, err := brrr.NewWriter(io.Discard, brrr.WriterOptions{
						Quality:  hc.quality,
						LGWin:    hc.lgwin,
						SizeHint: hc.sizeHint,
					})
					if err != nil {
						b.Fatal(err)
					}
					benchCompress(b, w, payloads)
				})
			}
		})
	}
}

// dictCompressor wraps a compressor that uses a compound dictionary.
type dictCompressor interface {
	io.WriteCloser
	Reset(io.Writer)
}

//nolint:unused // oneshotDictCompressorFactory is used by extraDictCompressBenches.
type oneshotDictCompressorFactory func(w io.Writer, quality int, dict []byte) (io.WriteCloser, error)

// extraDictCompressBenches holds dict compress benchmarks that need custom
// setup (e.g. managing PreparedDictionary lifetime).
var extraDictCompressBenches []struct {
	name string
	fn   func(b *testing.B, input []byte, quality int, dict []byte)
}

func BenchmarkCompressDict(b *testing.B) {
	corpus, err := os.ReadFile(filepath.Join("brotli-ref", "tests", "testdata", "alice29.txt"))
	if err != nil {
		b.Fatal(err)
	}

	dictEnd := len(corpus) * 20 / 100
	inputStart := len(corpus) * 10 / 100
	dict := corpus[:dictEnd]
	input := corpus[inputStart:]

	for _, q := range []int{5, 6, 7, 8, 9, 10, 11} {
		b.Run(fmt.Sprintf("q=%d", q), func(b *testing.B) {
			b.Run("impl=go-brrr", func(b *testing.B) {
				w, err := brrr.NewWriter(io.Discard, brrr.WriterOptions{Quality: q})
				if err != nil {
					b.Fatal(err)
				}
				if err := w.AttachDictionary(dict); err != nil {
					b.Fatal(err)
				}
				benchCompressDict(b, w, input)
			})
			for _, ec := range extraDictCompressBenches {
				b.Run("impl="+ec.name, func(b *testing.B) {
					ec.fn(b, input, q, dict)
				})
			}
		})
	}
}

func benchCompressDict(b *testing.B, w dictCompressor, input []byte) {
	b.Helper()
	b.SetBytes(int64(len(input)))
	b.ReportAllocs()

	var buf bytes.Buffer

	for b.Loop() {
		buf.Reset()
		w.Reset(&buf)
		if _, err := w.Write(input); err != nil {
			b.Fatal(err)
		}
		if err := w.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

//nolint:unused // benchCompressDictOneshot is used by extraDictCompressBenches.
func benchCompressDictOneshot(b *testing.B, factory oneshotDictCompressorFactory, input []byte, quality int, dict []byte) {
	b.Helper()
	b.SetBytes(int64(len(input)))
	b.ReportAllocs()

	var buf bytes.Buffer

	for b.Loop() {
		buf.Reset()
		w, err := factory(&buf, quality, dict)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := w.Write(input); err != nil {
			b.Fatal(err)
		}
		if err := w.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func benchCompress(b *testing.B, w compressor, payloads [][]byte) {
	b.Helper()

	totalBytes := 0
	for _, p := range payloads {
		totalBytes += len(p)
	}

	b.SetBytes(int64(totalBytes))
	b.ReportAllocs()

	var buf bytes.Buffer

	for b.Loop() {
		for _, data := range payloads {
			buf.Reset()
			w.Reset(&buf)
			if _, err := w.Write(data); err != nil {
				b.Fatal(err)
			}
			if err := w.Close(); err != nil {
				b.Fatal(err)
			}
		}
	}

	if os.Getenv("BENCH_REPORT_RATIO") != "" {
		compressedSize := 0
		for _, data := range payloads {
			buf.Reset()
			w.Reset(&buf)
			if _, err := w.Write(data); err != nil {
				b.Fatal(err)
			}
			if err := w.Close(); err != nil {
				b.Fatal(err)
			}
			compressedSize += buf.Len()
		}
		b.ReportMetric(float64(totalBytes)/float64(compressedSize), "ratio")
	}
}

func BenchmarkCompressOneshot(b *testing.B) {
	lgwin := benchLGWin()
	sizeHint := benchSizeHint()

	for _, q := range benchQualities() {
		b.Run(fmt.Sprintf("q=%d", q), func(b *testing.B) {
			suffix := benchParamSuffix(lgwin, sizeHint)

			for _, tc := range testCases {
				payloads := make([][]byte, len(tc.paths))
				for i, path := range tc.paths {
					data, err := os.ReadFile(path)
					if err != nil {
						b.Fatal(err)
					}
					payloads[i] = data
				}

				b.Run("payload="+tc.name+suffix, func(b *testing.B) {
					b.Run("impl=go-brrr", func(b *testing.B) {
						benchCompressOneshot(b, func(w io.Writer, quality, lgwin int) (io.WriteCloser, error) {
							return brrr.NewWriter(w, brrr.WriterOptions{Quality: quality, LGWin: lgwin, SizeHint: sizeHint})
						}, payloads, q, lgwin)
					})
					for _, ec := range extraCompressors {
						f := ec.factory
						b.Run("impl="+ec.name, func(b *testing.B) {
							benchCompressOneshot(b, func(w io.Writer, quality, lgwin int) (io.WriteCloser, error) {
								return f(w, quality, lgwin)
							}, payloads, q, lgwin)
						})
					}
					for _, ec := range oneshotOnlyCompressors {
						b.Run("impl="+ec.name, func(b *testing.B) {
							benchCompressOneshot(b, ec.factory, payloads, q, lgwin)
						})
					}
				})
			}
		})
	}
}

func benchCompressOneshot(b *testing.B, factory oneshotCompressorFactory, payloads [][]byte, quality, lgwin int) {
	b.Helper()

	totalBytes := 0
	for _, p := range payloads {
		totalBytes += len(p)
	}
	b.SetBytes(int64(totalBytes))
	b.ReportAllocs()

	var buf bytes.Buffer

	for b.Loop() {
		for _, data := range payloads {
			buf.Reset()
			w, err := factory(&buf, quality, lgwin)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := w.Write(data); err != nil {
				b.Fatal(err)
			}
			if err := w.Close(); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// decompressor reads brotli-compressed data and supports resetting the source.
type decompressor interface {
	io.ReadCloser
	Reset(io.Reader)
}

type decompressorFactory func(src io.Reader) decompressor

var extraDecompressors []struct {
	name    string
	factory decompressorFactory
}

type oneshotDecompressorFactory func(src io.Reader) io.ReadCloser

type oneshotBytesDecompressor func(src []byte) ([]byte, error)

var oneshotBytesDecompressors []struct {
	name    string
	factory oneshotBytesDecompressor
}

type oneshotDictDecompressorFactory func(src io.Reader, dict []byte) io.ReadCloser

var oneshotOnlyDictDecompressors []struct {
	name    string
	factory oneshotDictDecompressorFactory
}

func BenchmarkDecompress(b *testing.B) {
	lgwin := benchLGWin()

	for _, q := range benchQualities() {
		b.Run(fmt.Sprintf("q=%d", q), func(b *testing.B) {
			suffix := benchParamSuffix(lgwin, 0)

			for _, tc := range testCases {
				payloads := make([][]byte, len(tc.paths))
				for i, path := range tc.paths {
					data, err := os.ReadFile(path)
					if err != nil {
						b.Fatal(err)
					}
					payloads[i] = data
				}

				compressed := brrr.BenchCompressPayloads(b, payloads, q, lgwin)

				b.Run("payload="+tc.name+suffix, func(b *testing.B) {
					b.Run("impl=go-brrr", func(b *testing.B) {
						r := brrr.NewReader(bytes.NewReader(nil))
						benchDecompress(b, r, payloads, compressed)
					})
					for _, ed := range extraDecompressors {
						b.Run("impl="+ed.name, func(b *testing.B) {
							r := ed.factory(bytes.NewReader(nil))
							benchDecompress(b, r, payloads, compressed)
						})
					}
				})
			}
		})
	}
}

func BenchmarkDecompressOneshot(b *testing.B) {
	lgwin := benchLGWin()

	for _, q := range benchQualities() {
		b.Run(fmt.Sprintf("q=%d", q), func(b *testing.B) {
			suffix := benchParamSuffix(lgwin, 0)

			for _, tc := range testCases {
				payloads := make([][]byte, len(tc.paths))
				for i, path := range tc.paths {
					data, err := os.ReadFile(path)
					if err != nil {
						b.Fatal(err)
					}
					payloads[i] = data
				}

				compressed := brrr.BenchCompressPayloads(b, payloads, q, lgwin)

				b.Run("payload="+tc.name+suffix, func(b *testing.B) {
					b.Run("impl=go-brrr", func(b *testing.B) {
						benchDecompressBytes(b, brrr.Decompress, payloads, compressed)
					})
					for _, ed := range extraDecompressors {
						f := ed.factory
						b.Run("impl="+ed.name, func(b *testing.B) {
							benchDecompressOneshot(b, func(src io.Reader) io.ReadCloser {
								return f(src)
							}, payloads, compressed)
						})
					}
					for _, ed := range oneshotBytesDecompressors {
						b.Run("impl="+ed.name, func(b *testing.B) {
							benchDecompressBytes(b, ed.factory, payloads, compressed)
						})
					}
				})
			}
		})
	}
}

func benchDecompressOneshot(b *testing.B, factory oneshotDecompressorFactory, originals, compressed [][]byte) {
	b.Helper()

	totalBytes := 0
	for _, p := range originals {
		totalBytes += len(p)
	}
	b.SetBytes(int64(totalBytes))
	b.ReportAllocs()

	for b.Loop() {
		for i, data := range compressed {
			r := factory(bytes.NewReader(data))
			n, err := io.Copy(io.Discard, r)
			if err != nil {
				b.Fatal(err)
			}
			if int(n) != len(originals[i]) {
				b.Fatalf("decompressed size mismatch: got %d, want %d", n, len(originals[i]))
			}
		}
	}
}

func benchDecompressBytes(b *testing.B, decode oneshotBytesDecompressor, originals, compressed [][]byte) {
	b.Helper()

	totalBytes := 0
	for _, p := range originals {
		totalBytes += len(p)
	}
	b.SetBytes(int64(totalBytes))
	b.ReportAllocs()

	for b.Loop() {
		for i, data := range compressed {
			got, err := decode(data)
			if err != nil {
				b.Fatal(err)
			}
			if len(got) != len(originals[i]) {
				b.Fatalf("decompressed size mismatch: got %d, want %d", len(got), len(originals[i]))
			}
		}
	}
}

func BenchmarkDecompressDict(b *testing.B) {
	corpus, err := os.ReadFile(filepath.Join("brotli-ref", "tests", "testdata", "alice29.txt"))
	if err != nil {
		b.Fatal(err)
	}

	dictEnd := len(corpus) * 20 / 100
	inputStart := len(corpus) * 10 / 100
	dict := corpus[:dictEnd]
	input := corpus[inputStart:]

	for _, q := range []int{5, 6, 7, 8, 9, 10, 11} {
		// Compress with dictionary for the decompression benchmark.
		var buf bytes.Buffer
		w, err := brrr.NewWriter(&buf, brrr.WriterOptions{Quality: q})
		if err != nil {
			b.Fatal(err)
		}
		if err := w.AttachDictionary(dict); err != nil {
			b.Fatal(err)
		}
		if _, err := w.Write(input); err != nil {
			b.Fatal(err)
		}
		if err := w.Close(); err != nil {
			b.Fatal(err)
		}
		compressed := bytes.Clone(buf.Bytes())

		b.Run(fmt.Sprintf("q=%d", q), func(b *testing.B) {
			b.Run("impl=go-brrr", func(b *testing.B) {
				benchDecompressDict(b, input, compressed, dict, func(src io.Reader, d []byte) io.ReadCloser {
					r := brrr.NewReader(src)
					if err := r.AttachDictionary(d); err != nil {
						b.Fatal(err)
					}
					return r
				})
			})
			for _, ed := range oneshotOnlyDictDecompressors {
				b.Run("impl="+ed.name, func(b *testing.B) {
					benchDecompressDict(b, input, compressed, dict, ed.factory)
				})
			}
		})
	}
}

func BenchmarkCompressCorpusFile(b *testing.B) {
	path := os.Getenv("BENCH_CORPUS_FILE")
	if path == "" {
		b.Skip("BENCH_CORPUS_FILE not set")
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}

	lgwin := benchLGWin()
	sizeHint := benchSizeHint()
	name := filepath.Base(path)

	for q := range 12 {
		b.Run(fmt.Sprintf("q=%d", q), func(b *testing.B) {
			b.Run("payload="+name+benchParamSuffix(lgwin, sizeHint), func(b *testing.B) {
				b.Run("impl=go-brrr", func(b *testing.B) {
					w, err := brrr.NewWriter(io.Discard, brrr.WriterOptions{Quality: q, LGWin: lgwin, SizeHint: sizeHint})
					if err != nil {
						b.Fatal(err)
					}
					benchCompress(b, w, [][]byte{payload})
				})
				for _, ec := range extraCompressors {
					b.Run("impl="+ec.name, func(b *testing.B) {
						w, err := ec.factory(io.Discard, q, lgwin)
						if err != nil {
							b.Fatal(err)
						}
						benchCompress(b, w, [][]byte{payload})
					})
				}
			})
		})
	}
}

func BenchmarkDecompressCorpusFile(b *testing.B) {
	path := os.Getenv("BENCH_CORPUS_FILE")
	if path == "" {
		b.Skip("BENCH_CORPUS_FILE not set")
	}

	compressed, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}

	orig, err := brrr.Decompress(compressed)
	if err != nil {
		b.Fatal(err)
	}
	originals := [][]byte{orig}
	compressedSlice := [][]byte{compressed}

	name := filepath.Base(path)
	name = strings.TrimSuffix(name, ".br")

	b.Run("payload="+name, func(b *testing.B) {
		b.Run("impl=go-brrr", func(b *testing.B) {
			r := brrr.NewReader(bytes.NewReader(nil))
			benchDecompress(b, r, originals, compressedSlice)
		})
		for _, ed := range extraDecompressors {
			b.Run("impl="+ed.name, func(b *testing.B) {
				r := ed.factory(bytes.NewReader(nil))
				benchDecompress(b, r, originals, compressedSlice)
			})
		}
	})
}

func BenchmarkDecompressCorpus(b *testing.B) {
	dir := os.Getenv("BENCH_CORPUS_DIR")
	if dir == "" {
		b.Skip("BENCH_CORPUS_DIR not set")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		b.Fatal(err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".br") {
			continue
		}

		name := strings.TrimSuffix(e.Name(), ".br")
		compressed, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			b.Fatal(err)
		}

		// Decompress once to get the original size for SetBytes.
		orig, err := brrr.Decompress(compressed)
		if err != nil {
			b.Fatal(err)
		}
		originals := [][]byte{orig}
		compressedSlice := [][]byte{compressed}

		b.Run("payload="+name, func(b *testing.B) {
			b.Run("impl=go-brrr", func(b *testing.B) {
				r := brrr.NewReader(bytes.NewReader(nil))
				benchDecompress(b, r, originals, compressedSlice)
			})
			for _, ed := range extraDecompressors {
				b.Run("impl="+ed.name, func(b *testing.B) {
					r := ed.factory(bytes.NewReader(nil))
					benchDecompress(b, r, originals, compressedSlice)
				})
			}
		})
	}
}

func benchDecompress(b *testing.B, r decompressor, originals, compressed [][]byte) {
	b.Helper()

	totalBytes := 0
	for _, p := range originals {
		totalBytes += len(p)
	}
	b.SetBytes(int64(totalBytes))
	b.ReportAllocs()

	for b.Loop() {
		for i, data := range compressed {
			r.Reset(bytes.NewReader(data))
			n, err := io.Copy(io.Discard, r)
			if err != nil {
				b.Fatal(err)
			}
			if int(n) != len(originals[i]) {
				b.Fatalf("decompressed size mismatch: got %d, want %d", n, len(originals[i]))
			}
		}
	}
}

func benchDecompressDict(b *testing.B, original, compressed, dict []byte, factory oneshotDictDecompressorFactory) {
	b.Helper()
	b.SetBytes(int64(len(original)))
	b.ReportAllocs()

	for b.Loop() {
		r := factory(bytes.NewReader(compressed), dict)
		n, err := io.Copy(io.Discard, r)
		if closeErr := r.Close(); closeErr != nil {
			b.Fatal(closeErr)
		}
		if err != nil {
			b.Fatal(err)
		}
		if int(n) != len(original) {
			b.Fatalf("decompressed size mismatch: got %d, want %d", n, len(original))
		}
	}
}
