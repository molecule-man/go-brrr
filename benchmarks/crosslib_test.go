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
	"testing"

	"github.com/klauspost/compress/zstd"
	brrr "github.com/molecule-man/go-brrr"
)

func BenchmarkCrossLib(b *testing.B) {
	path := resolveUserPath(os.Getenv("BENCH_CORPUS_FILE"))
	if path == "" {
		path = dataPath("brotli-ref", "tests", "testdata", "alice29.txt")
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}

	type lib struct {
		name   string
		levels []int
		create func(w io.Writer, level int) (compressor, error)
	}

	libs := []lib{
		{
			name:   "go-brrr",
			levels: intRange(0, 11),
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
		{
			name:   "gzip",
			levels: intRange(1, 9),
			create: func(w io.Writer, level int) (compressor, error) {
				return gzip.NewWriterLevel(w, level)
			},
		},
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

					if _, err := w.Write(payload); err != nil {
						b.Fatal(err)
					}

					if err := w.Close(); err != nil {
						b.Fatal(err)
					}

					compressedSize := buf.Len()
					b.SetBytes(int64(len(payload)))
					b.ReportAllocs()

					for b.Loop() {
						w.Reset(io.Discard)

						if _, err := w.Write(payload); err != nil {
							b.Fatal(err)
						}

						if err := w.Close(); err != nil {
							b.Fatal(err)
						}
					}

					b.ReportMetric(float64(len(payload))/float64(compressedSize), "ratio")
				})
			}
		})
	}
}

func BenchmarkCrossLibDecompress(b *testing.B) {
	path := resolveUserPath(os.Getenv("BENCH_CORPUS_FILE"))
	if path == "" {
		path = dataPath("brotli-ref", "tests", "testdata", "alice29.txt")
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}

	type lib struct {
		name     string
		levels   []int
		compress func(data []byte, level int) []byte
		decomp   func(b *testing.B, compressed []byte, origSize int)
	}

	libs := []lib{
		{
			name:   "go-brrr",
			levels: intRange(0, 11),
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
			decomp: func(b *testing.B, compressed []byte, origSize int) {
				r := brrr.NewReader(bytes.NewReader(compressed))
				b.SetBytes(int64(origSize))
				b.ReportAllocs()
				for b.Loop() {
					r.Reset(bytes.NewReader(compressed))
					n, err := io.Copy(io.Discard, r)
					if err != nil {
						b.Fatal(err)
					}
					if int(n) != origSize {
						b.Fatalf("size mismatch: got %d, want %d", n, origSize)
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
			decomp: func(b *testing.B, compressed []byte, origSize int) {
				dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
				if err != nil {
					b.Fatal(err)
				}
				b.SetBytes(int64(origSize))
				b.ReportAllocs()
				for b.Loop() {
					result, err := dec.DecodeAll(compressed, nil)
					if err != nil {
						b.Fatal(err)
					}
					if len(result) != origSize {
						b.Fatalf("size mismatch: got %d, want %d", len(result), origSize)
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
			decomp: func(b *testing.B, compressed []byte, origSize int) {
				r, err := gzip.NewReader(bytes.NewReader(compressed))
				if err != nil {
					b.Fatal(err)
				}
				b.SetBytes(int64(origSize))
				b.ReportAllocs()
				for b.Loop() {
					if err := r.Reset(bytes.NewReader(compressed)); err != nil {
						b.Fatal(err)
					}
					n, err := io.Copy(io.Discard, r)
					if err != nil {
						b.Fatal(err)
					}
					if int(n) != origSize {
						b.Fatalf("size mismatch: got %d, want %d", n, origSize)
					}
				}
			},
		},
	}

	for _, lib := range libs {
		b.Run("lib="+lib.name, func(b *testing.B) {
			for _, level := range lib.levels {
				compressed := lib.compress(payload, level)
				b.Run(fmt.Sprintf("level=%d", level), func(b *testing.B) {
					lib.decomp(b, compressed, len(payload))
				})
			}
		})
	}
}

func intRange(from, to int) []int {
	s := make([]int, 0, to-from+1)
	for i := from; i <= to; i++ {
		s = append(s, i)
	}
	return s
}
