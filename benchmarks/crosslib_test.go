// Cross-library compression benchmarks for comparing speed vs compression ratio.
// Compression timings reset reused streaming encoders and discard timed output.
//go:build bench

package benchmarks

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/molecule-man/go-brrr"
)

type namedPayload struct {
	name string
	data [][]byte
}

func crosslibPayloads(b *testing.B) []namedPayload {
	b.Helper()

	if tc, ok := globTestCase(b); ok {
		return []namedPayload{{tc.name, loadPayloads(b, tc)}}
	}

	if os.Getenv("BENCH_TESTCASES") != "" {
		var payloads []namedPayload
		for _, tc := range benchTestCases(b) {
			data := loadPayloads(b, tc)
			if len(data) == 1 {
				payloads = append(payloads, namedPayload{tc.name, data})
				continue
			}
			for i, p := range data {
				payloads = append(payloads, namedPayload{fmt.Sprintf("%s_%d", tc.name, i), [][]byte{p}})
			}
		}
		return payloads
	}

	path := resolveUserPath(os.Getenv("BENCH_CORPUS_FILE"))
	if path == "" {
		path = dataPath("brotli-ref", "tests", "testdata", "alice29.txt")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	return []namedPayload{{filepath.Base(path), [][]byte{data}}}
}

func BenchmarkCrossLib(b *testing.B) {
	payloads := crosslibPayloads(b)
	for _, np := range payloads {
		if len(payloads) == 1 {
			benchCrossLibCompress(b, np.data)
			continue
		}
		b.Run("payload="+np.name, func(b *testing.B) {
			benchCrossLibCompress(b, np.data)
		})
	}
}

func benchCrossLibCompress(b *testing.B, payloads [][]byte) {
	type lib struct {
		name   string
		levels []int
		create func(w io.Writer, level int) (compressor, error)
	}

	libs := []lib{
		{
			name:   "go-brrr",
			levels: goBrrrLevels(),
			create: func(w io.Writer, level int) (compressor, error) {
				return brrr.NewWriter(w, level)
			},
		},
		// {
		// 	name:   "andybalholm",
		// 	levels: intRange(0, 11),
		// 	create: func(w io.Writer, level int) (compressor, error) {
		// 		return andybalholm.NewWriterOptions(w, andybalholm.WriterOptions{Quality: level}), nil
		// 	},
		// },
		{
			name: "zstd",
			levels: []int{
				int(zstd.SpeedFastest),
				int(zstd.SpeedDefault),
				int(zstd.SpeedBetterCompression),
				int(zstd.SpeedBestCompression),
			},
			create: func(w io.Writer, level int) (compressor, error) {
				return zstd.NewWriter(w,
					zstd.WithEncoderLevel(zstd.EncoderLevel(level)),
					zstd.WithEncoderConcurrency(1),
				)
			},
		},
	}

	if gzipEnabled() {
		libs = append(libs, lib{
			name:   "gzip",
			levels: intRange(1, 9),
			create: func(w io.Writer, level int) (compressor, error) {
				return gzip.NewWriterLevel(w, level)
			},
		})
	}

	for _, lib := range libs {
		b.Run("lib="+lib.name, func(b *testing.B) {
			for _, level := range lib.levels {
				b.Run(fmt.Sprintf("level=%d", level), func(b *testing.B) {
					var buf bytes.Buffer
					w, err := lib.create(&buf, level)
					if err != nil {
						b.Fatal(err)
					}

					totalBytes, compressedSize := 0, 0
					for _, payload := range payloads {
						buf.Reset()
						w.Reset(&buf)

						if _, err := w.Write(payload); err != nil {
							b.Fatal(err)
						}

						if err := w.Close(); err != nil {
							b.Fatal(err)
						}

						totalBytes += len(payload)
						compressedSize += buf.Len()
					}

					b.SetBytes(int64(totalBytes))
					b.ReportAllocs()

					for b.Loop() {
						for _, payload := range payloads {
							w.Reset(io.Discard)

							if _, err := w.Write(payload); err != nil {
								b.Fatal(err)
							}

							if err := w.Close(); err != nil {
								b.Fatal(err)
							}
						}
					}

					b.ReportMetric(float64(totalBytes)/float64(compressedSize), "ratio")
				})
			}
		})
	}
}

func BenchmarkCrossLibDecompress(b *testing.B) {
	payloads := crosslibPayloads(b)
	for _, np := range payloads {
		if len(payloads) == 1 {
			benchCrossLibDecompress(b, np.data)
			continue
		}
		b.Run("payload="+np.name, func(b *testing.B) {
			benchCrossLibDecompress(b, np.data)
		})
	}
}

func benchCrossLibDecompress(b *testing.B, payloads [][]byte) {
	type lib struct {
		name     string
		levels   []int
		compress func(data []byte, level int) []byte
		decomp   func(b *testing.B, compressed [][]byte, origSizes []int)
	}

	libs := []lib{
		{
			name:   "go-brrr",
			levels: goBrrrLevels(),
			compress: func(data []byte, level int) []byte {
				var buf bytes.Buffer
				w, err := brrr.NewWriter(&buf, level)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := w.Write(data); err != nil {
					b.Fatal(err)
				}
				if err := w.Close(); err != nil {
					b.Fatal(err)
				}
				return buf.Bytes()
			},
			decomp: func(b *testing.B, compressed [][]byte, origSizes []int) {
				r := brrr.NewReader(bytes.NewReader(nil))
				b.SetBytes(int64(sum(origSizes)))
				b.ReportAllocs()
				for b.Loop() {
					for i, data := range compressed {
						r.Reset(bytes.NewReader(data))
						n, err := io.Copy(io.Discard, r)
						if err != nil {
							b.Fatal(err)
						}
						if int(n) != origSizes[i] {
							b.Fatalf("size mismatch: got %d, want %d", n, origSizes[i])
						}
					}
				}
			},
		},
		{
			name: "zstd",
			levels: []int{
				int(zstd.SpeedFastest),
				int(zstd.SpeedDefault),
				int(zstd.SpeedBetterCompression),
				int(zstd.SpeedBestCompression),
			},
			compress: func(data []byte, level int) []byte {
				var buf bytes.Buffer
				w, err := zstd.NewWriter(&buf,
					zstd.WithEncoderLevel(zstd.EncoderLevel(level)),
					zstd.WithEncoderConcurrency(1),
				)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := w.Write(data); err != nil {
					b.Fatal(err)
				}
				if err := w.Close(); err != nil {
					b.Fatal(err)
				}
				return buf.Bytes()
			},
			decomp: func(b *testing.B, compressed [][]byte, origSizes []int) {
				dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
				if err != nil {
					b.Fatal(err)
				}
				b.SetBytes(int64(sum(origSizes)))
				b.ReportAllocs()
				for b.Loop() {
					for i, data := range compressed {
						result, err := dec.DecodeAll(data, nil)
						if err != nil {
							b.Fatal(err)
						}
						if len(result) != origSizes[i] {
							b.Fatalf("size mismatch: got %d, want %d", len(result), origSizes[i])
						}
					}
				}
			},
		},
		{
			name:   "gzip",
			levels: intRange(1, 9),
			compress: func(data []byte, level int) []byte {
				var buf bytes.Buffer
				w, err := gzip.NewWriterLevel(&buf, level)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := w.Write(data); err != nil {
					b.Fatal(err)
				}
				if err := w.Close(); err != nil {
					b.Fatal(err)
				}
				return buf.Bytes()
			},
			decomp: func(b *testing.B, compressed [][]byte, origSizes []int) {
				r, err := gzip.NewReader(bytes.NewReader(compressed[0]))
				if err != nil {
					b.Fatal(err)
				}
				b.SetBytes(int64(sum(origSizes)))
				b.ReportAllocs()
				for b.Loop() {
					for i, data := range compressed {
						if err := r.Reset(bytes.NewReader(data)); err != nil {
							b.Fatal(err)
						}
						n, err := io.Copy(io.Discard, r)
						if err != nil {
							b.Fatal(err)
						}
						if int(n) != origSizes[i] {
							b.Fatalf("size mismatch: got %d, want %d", n, origSizes[i])
						}
					}
				}
			},
		},
	}

	origSizes := make([]int, len(payloads))
	for i, p := range payloads {
		origSizes[i] = len(p)
	}

	for _, lib := range libs {
		b.Run("lib="+lib.name, func(b *testing.B) {
			for _, level := range lib.levels {
				compressed := make([][]byte, len(payloads))
				for i, p := range payloads {
					compressed[i] = lib.compress(p, level)
				}
				b.Run(fmt.Sprintf("level=%d", level), func(b *testing.B) {
					lib.decomp(b, compressed, origSizes)
				})
			}
		})
	}
}

func sum(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}

func goBrrrLevels() []int {
	maxLevel := 11
	if s := os.Getenv("BENCH_MAX_LEVEL"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil {
			panic(fmt.Sprintf("invalid BENCH_MAX_LEVEL=%q: %v", s, err))
		}
		maxLevel = v
	}
	return intRange(0, maxLevel)
}

func gzipEnabled() bool {
	return os.Getenv("BENCH_NO_GZIP") == ""
}

func intRange(from, to int) []int {
	s := make([]int, 0, to-from+1)
	for i := from; i <= to; i++ {
		s = append(s, i)
	}
	return s
}
