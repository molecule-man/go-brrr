package encoder

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

const (
	redirectOld         = 7
	redirectReplacement = 9
)

func histogramCombineRedirectScalarReference(s []uint32, old, replacement uint32) {
	for i, v := range s {
		if v == old {
			s[i] = replacement
		}
	}
}

func histogramCombineRedirectFixture(tb testing.TB, n, oldOneIn int) []uint32 {
	tb.Helper()
	rng := rand.New(rand.NewPCG(uint64(n), uint64(oldOneIn)))
	s := make([]uint32, n)
	for i := range s {
		switch {
		case oldOneIn == 1 || (oldOneIn > 1 && rng.IntN(oldOneIn) == 0):
			s[i] = redirectOld
		default:
			s[i] = redirectOld + 3 + uint32(rng.IntN(60))
		}
	}
	return s
}

func TestHistogramCombineRedirectMatchesTheScalarLoopAtEveryLengthAndMatchDensityAndNeverWritesPastTheSlice(t *testing.T) {
	lengths := make([]int, 0, 304)
	for n := range 301 {
		lengths = append(lengths, n)
	}
	lengths = append(lengths, 704, 4096, 16384)
	var errs []error
	for _, n := range lengths {
		for _, oldOneIn := range []int{0, 64, 2, 1} {
			backing := histogramCombineRedirectFixture(t, n+64, oldOneIn)
			for i := n; i < len(backing); i++ {
				backing[i] = redirectOld
			}
			got, want := slices.Clone(backing), slices.Clone(backing)
			histogramCombineRedirect(got[:n], redirectOld, redirectReplacement)
			histogramCombineRedirectScalarReference(want[:n], redirectOld, redirectReplacement)
			for i := range got {
				if got[i] != want[i] {
					where := "inside the slice, so a symbol would point at the wrong cluster"
					if i >= n {
						where = "past the end of the slice, so the kernel wrote memory it does not own"
					}
					errs = append(errs, fmt.Errorf("len %d, old in 1 of %d: element %d is %d, the scalar loop leaves %d; the difference is %s",
						n, oldOneIn, i, got[i], want[i], where))
					break
				}
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		t.Error(err)
	}
}

func BenchmarkHistogramCombineRedirect(b *testing.B) {
	impls := []struct {
		name string
		f    func([]uint32, uint32, uint32)
	}{
		{"scalar_loop", histogramCombineRedirectScalarReference},
		{"kernel", histogramCombineRedirect},
	}
	for _, n := range []int{64, 256, 1024, 4096, 16384} {
		s := histogramCombineRedirectFixture(b, n, 64)
		for _, impl := range impls {
			b.Run(fmt.Sprintf("symbols=%d/impl=%s", n, impl.name), func(b *testing.B) {
				work := slices.Clone(s)
				b.ReportAllocs()
				b.SetBytes(int64(4 * n))
				b.ResetTimer()
				for i := range b.N {
					if i&1 == 0 {
						impl.f(work, redirectOld, redirectReplacement)
					} else {
						impl.f(work, redirectReplacement, redirectOld)
					}
				}
			})
		}
	}
}
