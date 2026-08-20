package encoder

import (
	"strconv"
	"sync"
	"testing"
)

func clearOriginalUint32(h []uint32, n int) {
	for i := range n {
		h[i] = 0
	}
}

func clearGenericUint32(h []uint32, n int) {
	clear(h[:n])
}

func clearOriginalUint(h []uint, n int) {
	for i := range h[:n] {
		h[i] = 0
	}
}

func clearGenericUint(h []uint, n int) {
	clear(h[:n])
}

func clearOriginalBytes(b []byte, n int) {
	for i := range n {
		b[i] = 0
	}
}

func clearGenericBytes(b []byte, n int) {
	clear(b[:n])
}

func BenchmarkHistogramClear(b *testing.B) {
	for _, n := range []int{256, 544, 704} {
		h := make([]uint32, n)
		b.Run("n="+strconv.Itoa(n)+"/original", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(n * 4))
			for range b.N {
				clearOriginalUint32(h, n)
			}
			benchFinish(b, "hc"+strconv.Itoa(n), true)
		})
		b.Run("n="+strconv.Itoa(n)+"/generic", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(n * 4))
			for range b.N {
				clearGenericUint32(h, n)
			}
			benchFinish(b, "hc"+strconv.Itoa(n), false)
		})
	}
}

func BenchmarkLiteralCostHistogramClear(b *testing.B) {
	for _, n := range []int{256, 768} {
		h := make([]uint, n)
		b.Run("n="+strconv.Itoa(n)+"/original", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				clearOriginalUint(h, n)
			}
			benchFinish(b, "lc"+strconv.Itoa(n), true)
		})
		b.Run("n="+strconv.Itoa(n)+"/generic", func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				clearGenericUint(h, n)
			}
			benchFinish(b, "lc"+strconv.Itoa(n), false)
		})
	}
}

func BenchmarkContextMapZeroFill(b *testing.B) {
	for _, n := range []int{1024, 16384} {
		m := make([]byte, n)
		b.Run("n="+strconv.Itoa(n)+"/original", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(n))
			for range b.N {
				clearOriginalBytes(m, n)
			}
			benchFinish(b, "cm"+strconv.Itoa(n), true)
		})
		b.Run("n="+strconv.Itoa(n)+"/generic", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(n))
			for range b.N {
				clearGenericBytes(m, n)
			}
			benchFinish(b, "cm"+strconv.Itoa(n), false)
		})
	}
}

var benchOriginalNs sync.Map

func benchFinish(b *testing.B, key string, isOriginal bool) {
	b.Helper()
	own := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	if own <= 0 {
		return
	}
	if isOriginal {
		benchOriginalNs.Store(key, own)
	}
	if v, ok := benchOriginalNs.Load(key); ok {
		b.ReportMetric(v.(float64)/own, "speedup")
	}
}
