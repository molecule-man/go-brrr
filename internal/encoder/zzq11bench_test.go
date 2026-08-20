package encoder

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

func q11Corpus(tb testing.TB) []byte {
	tb.Helper()
	b, err := os.ReadFile("../../testdata/reactcore_187KB.js")
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

func compressQ(tb testing.TB, q int, data []byte) []byte {
	var out bytes.Buffer
	c := NewCompressor(q, 22, uint(len(data)))
	if _, err := c.Write(&out, data); err != nil {
		tb.Fatal(err)
	}
	if err := c.Close(&out); err != nil {
		tb.Fatal(err)
	}
	c.Release()
	return out.Bytes()
}

func TestQ11Digest(t *testing.T) {
	for _, q := range []int{10, 11} {
		for _, f := range []string{"../../testdata/reactcore_187KB.js", "../../testdata/gh_172KB.html", "../../testdata/github_events_8k.json"} {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(compressQ(t, q, data))
			t.Logf("q=%d %s len=%d sha=%s", q, f, len(compressQ(t, q, data)), hex.EncodeToString(sum[:8]))
		}
	}
}

func BenchmarkQ11JS(b *testing.B) {
	data := q11Corpus(b)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		compressQ(b, 11, data)
	}
}

func BenchmarkQ10JS(b *testing.B) {
	data := q11Corpus(b)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		compressQ(b, 10, data)
	}
}
