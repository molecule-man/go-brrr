// Benchmark using andybalholm/brotli pure-Go implementation.
//go:build bench

package benchmarks

import (
	"io"

	andybalholm "github.com/andybalholm/brotli"
)

type andybalholmDecompressor struct {
	r *andybalholm.Reader
}

func (d *andybalholmDecompressor) Read(p []byte) (int, error) { return d.r.Read(p) }
func (d *andybalholmDecompressor) Close() error               { return nil }
func (d *andybalholmDecompressor) Reset(src io.Reader)        { _ = d.r.Reset(src) }

func init() {
	extraCompressors = append(extraCompressors, struct {
		name    string
		factory compressorFactory
	}{
		name: "andybalholm",
		factory: func(w io.Writer, quality, lgwin int) (compressor, error) {
			return andybalholm.NewWriterOptions(w, andybalholm.WriterOptions{Quality: quality, LGWin: lgwin}), nil
		},
	})

	extraDecompressors = append(extraDecompressors, struct {
		name    string
		factory decompressorFactory
	}{
		name: "andybalholm",
		factory: func(src io.Reader) decompressor {
			return &andybalholmDecompressor{r: andybalholm.NewReader(src)}
		},
	})
}
