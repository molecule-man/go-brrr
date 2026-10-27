//go:build amd64 && !purego

package encoder

import (
	"errors"
	"fmt"
	"testing"
)

var histogramTotalCountSink uint32

func histogramTotalCountScalarReference(h []uint32, alphabetSize int) uint32 {
	var total uint32
	for i := range alphabetSize {
		total += h[i]
	}
	return total
}

func histogramTotalCountFixture(tb testing.TB, n int, base uint32) []uint32 {
	tb.Helper()
	h := make([]uint32, n)
	for i := range h {
		h[i] = base + uint32(i*2654435761)%100000
	}
	return h
}

func TestHistogramTotalCountKernelMatchesScalarAtEveryLengthAndWrapsLikeTheScalarLoop(t *testing.T) {
	lengths := make([]int, 0, 303)
	for n := range 301 {
		lengths = append(lengths, n)
	}
	lengths = append(lengths, 704, 2816)
	var errs []error
	for _, base := range []uint32{0, 0xfff00000} {
		for _, n := range lengths {
			h := histogramTotalCountFixture(t, n, base)
			want := histogramTotalCountScalarReference(h, n)
			if got := histogramTotalCountAsm(h, n); got != want {
				errs = append(errs, fmt.Errorf("n=%d base=%#x: the kernel summed to %d but the scalar loop gives %d; "+
					"a wrong tail or fold here silently corrupts every cost estimate", n, base, got, want))
			}
			if got := histogramTotalCount(h, n); got != want {
				errs = append(errs, fmt.Errorf("n=%d base=%#x: histogramTotalCount summed to %d but the scalar loop gives "+
					"%d, so the drop-in replacement is not equivalent", n, base, got, want))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		t.Error(err)
	}
}

func BenchmarkHistogramTotalCount(b *testing.B) {
	for _, n := range []int{256, 704, 2816} {
		h := histogramTotalCountFixture(b, n, 0)
		b.Run(fmt.Sprintf("symbols=%d/impl=scalar_loop", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(4 * n))
			for range b.N {
				histogramTotalCountSink = histogramTotalCountScalarReference(h, n)
			}
		})
		b.Run(fmt.Sprintf("symbols=%d/impl=kernel", n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(4 * n))
			for range b.N {
				histogramTotalCountSink = histogramTotalCountAsm(h, n)
			}
		})
	}
}
