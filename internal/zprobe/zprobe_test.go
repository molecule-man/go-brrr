package zprobe

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/molecule-man/go-brrr/internal/encoder"
)

func zcorpus(tb testing.TB, n int) []byte {
	tb.Helper()
	r := rand.New(rand.NewSource(42))
	words := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}
	var sb strings.Builder
	sb.Grow(n + 1024)
	for sb.Len() < n {
		fmt.Fprintf(&sb, "%d,%s,%s,%0.4f,%d,\"%s %s\",%08x\n",
			r.Intn(1000000), words[r.Intn(len(words))], words[r.Intn(len(words))],
			r.Float64()*1000, r.Intn(100), words[r.Intn(len(words))],
			words[r.Intn(len(words))], r.Uint32())
	}
	return []byte(sb.String()[:n])
}

type zsink struct{ n int }

func (w *zsink) Write(p []byte) (int, error) { w.n += len(p); return len(p), nil }

func zrun(tb testing.TB, data []byte, q int) *zsink {
	w := &zsink{}
	c := encoder.NewCompressor(q, 22, uint(len(data)))
	if _, err := c.Write(w, data); err != nil {
		tb.Fatal(err)
	}
	if err := c.Close(w); err != nil {
		tb.Fatal(err)
	}
	c.Release()
	return w
}

func zbench(b *testing.B, q int) {
	data := zcorpus(b, 10<<20)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	var out int
	for i := 0; i < b.N; i++ {
		out = zrun(b, data, q).n
	}
	b.ReportMetric(float64(out), "outbytes")
}

func BenchmarkZQ5(b *testing.B) { zbench(b, 5) }
func BenchmarkZQ6(b *testing.B) { zbench(b, 6) }
func BenchmarkZQ9(b *testing.B) { zbench(b, 9) }

type zbuf struct{ b []byte }

func (w *zbuf) Write(p []byte) (int, error) { w.b = append(w.b, p...); return len(p), nil }

func TestZDigest(t *testing.T) {
	data := zcorpus(t, 10<<20)
	for _, q := range []int{4, 5, 6, 7, 8, 9} {
		w := &zbuf{}
		c := encoder.NewCompressor(q, 22, uint(len(data)))
		if _, err := c.Write(w, data); err != nil {
			t.Fatal(err)
		}
		if err := c.Close(w); err != nil {
			t.Fatal(err)
		}
		c.Release()
		s := sha256.Sum256(w.b)
		fmt.Printf("DIGEST q%d len=%d sha=%s\n", q, len(w.b), hex.EncodeToString(s[:12]))
	}
}
