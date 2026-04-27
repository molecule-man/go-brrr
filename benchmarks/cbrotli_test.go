// Benchmark using Google's reference C brotli via CGo.
//go:build cgo && bench

package benchmarks

import (
	"io"
	"testing"

	"github.com/google/brotli/go/cbrotli"
)

func init() {
	oneshotOnlyCompressors = append(oneshotOnlyCompressors, struct {
		name    string
		factory oneshotCompressorFactory
	}{
		name: "cbrotli",
		factory: func(w io.Writer, quality, lgwin int) (io.WriteCloser, error) {
			return cbrotli.NewWriter(w, cbrotli.WriterOptions{Quality: quality, LGWin: lgwin}), nil
		},
	})

	extraDictCompressBenches = append(extraDictCompressBenches, struct {
		name string
		fn   func(b *testing.B, input []byte, quality int, dict []byte)
	}{
		name: "cbrotli",
		fn: func(b *testing.B, input []byte, quality int, dict []byte) {
			pd := cbrotli.NewPreparedDictionary(dict, cbrotli.DtRaw, quality)
			b.Cleanup(func() { pd.Close() })
			benchCompressDictOneshot(b, func(w io.Writer, q int, _ []byte) (io.WriteCloser, error) {
				return cbrotli.NewWriter(w, cbrotli.WriterOptions{Quality: q, Dictionary: pd}), nil
			}, input, quality, dict)
		},
	})

	oneshotBytesDecompressors = append(oneshotBytesDecompressors, struct {
		name    string
		factory oneshotBytesDecompressor
	}{
		name: "cbrotli",
		factory: func(src []byte) ([]byte, error) {
			return cbrotli.Decode(src)
		},
	})

	oneshotOnlyDictDecompressors = append(oneshotOnlyDictDecompressors, struct {
		name    string
		factory oneshotDictDecompressorFactory
	}{
		name: "cbrotli",
		factory: func(src io.Reader, dict []byte) io.ReadCloser {
			return cbrotli.NewReaderWithRawDictionary(src, dict)
		},
	})
}
