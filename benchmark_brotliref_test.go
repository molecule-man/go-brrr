// Benchmark using the brotli-ref pure-Go decoder.
//go:build bench

package brrr_test

import brotliref "github.com/google/brotli/go/brotli"

func init() {
	oneshotBytesDecompressors = append(oneshotBytesDecompressors, struct {
		name    string
		factory oneshotBytesDecompressor
	}{
		name: "brotli-ref",
		factory: brotliref.Decode,
	})
}
