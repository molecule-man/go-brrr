//go:build cgo

// Package creftest provides shared test helpers that wrap the C reference
// brotli encoder/decoder. Used by tests in both the root brrr package and
// internal/encoder.
package creftest

import (
	"testing"

	"github.com/molecule-man/go-brrr/internal/benchcache"
	"github.com/molecule-man/go-brrr/internal/cref"
)

// BrotliCompress encodes input with the C reference brotli encoder, caching
// the result on disk so successive calls with the same parameters are free.
func BrotliCompress(t *testing.T, input []byte, quality, lgwin int, sizeHint uint) []byte {
	t.Helper()
	key := benchcache.Key(input, quality, lgwin, sizeHint, nil)
	if cached, ok := benchcache.Lookup(key); ok {
		return cached
	}
	out, err := cref.Encode(input, quality, lgwin, sizeHint)
	if err != nil {
		t.Fatalf("C brotli compression failed: %v", err)
	}
	benchcache.Store(key, out)
	return out
}

// BrotliDecompress decodes compressed with the C reference brotli decoder.
func BrotliDecompress(t *testing.T, compressed []byte) []byte {
	t.Helper()
	out, err := cref.Decode(compressed)
	if err != nil {
		t.Fatalf("C brotli decompression failed: %v", err)
	}
	return out
}

// BrotliCompressDict encodes input against a compound dictionary, caching the
// result on disk.
func BrotliCompressDict(t *testing.T, input, dict []byte, quality, lgwin int, sizeHint uint) []byte {
	t.Helper()
	key := benchcache.Key(input, quality, lgwin, sizeHint, dict)
	if cached, ok := benchcache.Lookup(key); ok {
		return cached
	}
	out, err := cref.EncodeDict(input, dict, quality, lgwin, sizeHint)
	if err != nil {
		t.Fatalf("C brotli dict compression failed: %v", err)
	}
	benchcache.Store(key, out)
	return out
}
