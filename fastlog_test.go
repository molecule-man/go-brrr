package brrr

import (
	"math"
	"testing"
)

func TestFastLog2(t *testing.T) {
	// fastLog2(0) == 0 by convention (not math.Log2 which returns -Inf).
	if got := fastLog2(0); got != 0 {
		t.Errorf("fastLog2(0) = %v, want 0", got)
	}

	// Table range: 1..255 has float32-precision values (matching the C
	// reference's kLog2Table which uses f-suffix literals). Verify they
	// match float32(math.Log2(v)) promoted back to float64.
	for v := 1; v < len(log2Table); v++ {
		got := fastLog2(v)
		want := float64(float32(math.Log2(float64(v))))
		if got != want {
			t.Errorf("fastLog2(%d) = %v, want %v", v, got, want)
		}
	}

	// Above table range: falls back to math.Log2.
	for _, v := range []int{256, 1000, 65536, 1 << 20} {
		got := fastLog2(v)
		want := math.Log2(float64(v))
		if got != want {
			t.Errorf("fastLog2(%d) = %v, want %v", v, got, want)
		}
	}
}

var sink float64

func BenchmarkMathLog2(b *testing.B) {
	b.SkipNow()
	b.Run("math.Log2", func(b *testing.B) {
		for b.Loop() {
			for v := 1; v <= 256; v++ {
				sink = math.Log2(float64(v))
			}
		}
	})
	b.Run("fastLog2", func(b *testing.B) {
		for b.Loop() {
			for v := 1; v <= 256; v++ {
				sink = fastLog2(v)
			}
		}
	})
}
