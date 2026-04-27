// Benchmark using Google's official pure-Go brotli decoder
// (transpiled from the Java reference, decompression only).
//go:build bench

package brrr_test

import googlebrotli "github.com/google/brotli/go/brotli"

func init() {
	oneshotBytesDecompressors = append(oneshotBytesDecompressors, struct {
		name    string
		factory oneshotBytesDecompressor
	}{
		name:    "google-brotli",
		factory: googlebrotli.Decode,
	})
}
