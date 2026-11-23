//go:build goexperiment.simd && go1.27 && amd64 && !purego

package encoder

import (
	"errors"
	"fmt"
	"testing"
)

type histogramAddImpl struct {
	name string
	fn   func(dst, src []uint32, n int)
}

var histogramAddImpls = []histogramAddImpl{
	{"sse2_asm", histogramAddAsm},
	{"portable_simd", histogramAddPortable},
	{"dispatch", histogramAdd},
}

func TestHistogramAddPortableMatchesScalarAtEveryTailLengthAndLeavesEntriesPastNUntouched(t *testing.T) {
	const slack = 33
	var errs []error
	for _, impl := range histogramAddImpls {
		for _, n := range []int{0, 1, 2, 3, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 65, 76, 127, 128, 129, 140, 256, 704} {
			got, src := histogramAddFixture(t, n+slack)
			want := append([]uint32(nil), got...)
			histogramAddScalarReference(want, src, n)
			impl.fn(got, src, n)
			for i := range want {
				if got[i] != want[i] {
					errs = append(errs, fmt.Errorf("%s n=%d index %d: got %d, scalar %d; a wrong lane, a double-added overlap or a write past n corrupts the merged histogram or its neighbour in the arena",
						impl.name, n, i, got[i], want[i]))
				}
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkHistogramAddPortableAgainstSSE2AtAlphabetSizes(b *testing.B) {
	for _, n := range []int{76, 140, 256, 704} {
		for _, impl := range []histogramAddImpl{{"sse2_asm", histogramAddAsm}, {"histogramAdd", histogramAdd}} {
			b.Run(fmt.Sprintf("symbols=%d/impl=%s", n, impl.name), func(b *testing.B) {
				dst, src := histogramAddFixture(b, n)
				b.ReportAllocs()
				b.SetBytes(int64(4 * n))
				for range b.N {
					impl.fn(dst, src, n)
				}
			})
		}
	}
}
