// Shared helpers for the benchmark suite: payload caching and path anchoring
// to the repo root so benchmarks work regardless of the test CWD.
package benchmarks

import (
	"bytes"
	"path/filepath"
	"testing"

	brrr "github.com/molecule-man/go-brrr"
	"github.com/molecule-man/go-brrr/internal/benchcache"
)

// dataPath joins parts onto the repository root. Use it for any test asset
// that lives inside the repo (e.g. brotli-ref/, testdata/), so benchmarks
// resolve correctly when run from this subdirectory.
func dataPath(parts ...string) string {
	return filepath.Join(append([]string{benchcache.RepoRoot()}, parts...)...)
}

// resolveUserPath resolves a user-supplied path (e.g. from BENCH_CORPUS_FILE
// or BENCH_CORPUS_DIR) against the repo root when relative, matching the
// historic CWD when these benchmarks lived at the module root. Absolute paths
// are returned unchanged. An empty path is returned unchanged so callers can
// keep their "unset" check.
func resolveUserPath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(benchcache.RepoRoot(), p)
}

// compressPayloads compresses each payload using go-brrr at the given
// quality/lgwin and caches the results on disk to avoid slow re-compression
// on repeated benchmark runs.
func compressPayloads(tb testing.TB, payloads [][]byte, quality, lgwin int) [][]byte {
	tb.Helper()
	compressed := make([][]byte, len(payloads))
	for i, data := range payloads {
		key := benchcache.Key(data, quality, lgwin, 0, nil)
		if cached, ok := benchcache.Lookup(key); ok {
			compressed[i] = cached
			continue
		}
		var buf bytes.Buffer
		w, err := brrr.NewWriterOptions(&buf, quality, brrr.WriterOptions{LGWin: lgwin})
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
		benchcache.Store(key, out)
		compressed[i] = out
	}
	return compressed
}
