// Test helpers that wrap the CGo C-reference encode/decode functions.
package brrr

import (
	"testing"

	"github.com/molecule-man/go-brrr/internal/benchcache"
	"github.com/molecule-man/go-brrr/internal/cref"
)

func brotliCompress(t *testing.T, input []byte, quality, lgwin int, sizeHint uint) []byte {
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

func brotliDecompress(t *testing.T, compressed []byte) []byte {
	t.Helper()
	out, err := cref.Decode(compressed)
	if err != nil {
		t.Fatalf("C brotli decompression failed: %v", err)
	}
	return out
}

func brotliCompressDict(t *testing.T, input, dict []byte, quality, lgwin int, sizeHint uint) []byte {
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
