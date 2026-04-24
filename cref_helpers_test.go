// Test helpers that wrap the CGo C-reference encode/decode functions.
package brrr

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/molecule-man/go-brrr/internal/cref"
)

const diskCacheDir = ".cache"

// crefCacheKey returns a hex-encoded SHA-256 digest that uniquely identifies
// a C-reference compression call (input content + all parameters).
func crefCacheKey(input []byte, quality, lgwin int, sizeHint uint, dict []byte) string {
	h := sha256.New()
	// Fixed-size parameter header so different params never collide.
	var hdr [24]byte
	binary.LittleEndian.PutUint64(hdr[0:], uint64(quality))
	binary.LittleEndian.PutUint64(hdr[8:], uint64(lgwin))
	binary.LittleEndian.PutUint64(hdr[16:], uint64(sizeHint))
	h.Write(hdr[:])
	if dict != nil {
		// Length-prefix the dict so (dict="ab", input="cd") differs from
		// (dict="abc", input="d").
		var dlen [8]byte
		binary.LittleEndian.PutUint64(dlen[:], uint64(len(dict)))
		h.Write(dlen[:])
		h.Write(dict)
	}
	h.Write(input)
	return hex.EncodeToString(h.Sum(nil))
}

// diskCacheLookup returns cached output if available.
func diskCacheLookup(key string) ([]byte, bool) {
	data, err := os.ReadFile(filepath.Join(diskCacheDir, key))
	if err != nil {
		return nil, false
	}
	return data, true
}

// diskCacheStore writes output to the cache directory atomically.
func diskCacheStore(key string, data []byte) {
	_ = os.MkdirAll(diskCacheDir, 0o755)
	// Write atomically via temp file to avoid partial reads from parallel tests.
	tmp := filepath.Join(diskCacheDir, key+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, filepath.Join(diskCacheDir, key))
}

func brotliCompress(t *testing.T, input []byte, quality, lgwin int, sizeHint uint) []byte {
	t.Helper()
	key := crefCacheKey(input, quality, lgwin, sizeHint, nil)
	if cached, ok := diskCacheLookup(key); ok {
		return cached
	}
	out, err := cref.Encode(input, quality, lgwin, sizeHint)
	if err != nil {
		t.Fatalf("C brotli compression failed: %v", err)
	}
	diskCacheStore(key, out)
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
	key := crefCacheKey(input, quality, lgwin, sizeHint, dict)
	if cached, ok := diskCacheLookup(key); ok {
		return cached
	}
	out, err := cref.EncodeDict(input, dict, quality, lgwin, sizeHint)
	if err != nil {
		t.Fatalf("C brotli dict compression failed: %v", err)
	}
	diskCacheStore(key, out)
	return out
}
