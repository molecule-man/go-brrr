// Benchmark helpers for caching pre-compressed payloads to disk.
package brrr

import (
	"bytes"
	"testing"
)

// BenchCompressPayloads compresses each payload using go-brrr at the given
// quality/lgwin and caches the results on disk to avoid slow re-compression
// on repeated benchmark runs. It is accessible from the brrr_test package.
func BenchCompressPayloads(tb testing.TB, payloads [][]byte, quality, lgwin int) [][]byte {
	tb.Helper()
	compressed := make([][]byte, len(payloads))
	for i, data := range payloads {
		key := crefCacheKey(data, quality, lgwin, 0, nil)
		if cached, ok := diskCacheLookup(key); ok {
			compressed[i] = cached
			continue
		}
		var buf bytes.Buffer
		w, err := NewWriterOptions(&buf, quality, WriterOptions{LGWin: lgwin})
		if err != nil {
			tb.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			tb.Fatal(err)
		}
		if err := w.Close(); err != nil {
			tb.Fatal(err)
		}
		out := bytes.Clone(buf.Bytes())
		diskCacheStore(key, out)
		compressed[i] = out
	}
	return compressed
}
