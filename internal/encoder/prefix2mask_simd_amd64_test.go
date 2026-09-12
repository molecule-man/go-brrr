//go:build amd64 && !purego

package encoder

import (
	"math/bits"
	"testing"
)

var prefix2MaskSink uint64

// prefix2Mask64Scalar is the loop the kernel replaces: bit j set when the
// two-byte prefix at p[j] matches.
func prefix2Mask64Scalar(data []byte, off int, c0, c1 byte) uint64 {
	var m uint64
	for j := range 64 {
		if data[off+j] == c0 && data[off+j+1] == c1 {
			m |= 1 << uint(j)
		}
	}
	return m
}

func prefix2MaskFixture(tb testing.TB, seed int) []byte {
	tb.Helper()
	d := make([]byte, 256)
	for i := range d {
		d[i] = byte((i*seed + i/3) % 7)
	}
	return d
}

func TestPrefix2Mask64MatchesScalarForEveryBytePair(t *testing.T) {
	for _, seed := range []int{1, 3, 11, 97} {
		data := prefix2MaskFixture(t, seed)
		for _, off := range []int{0, 1, 7, 64, 100} {
			for c0 := range byte(8) {
				for c1 := range byte(8) {
					got := prefix2Mask64(&data[off], c0, c1)
					want := prefix2Mask64Scalar(data, off, c0, c1)
					if got != want {
						t.Fatalf("seed=%d off=%d c0=%d c1=%d: kernel %064b, scalar %064b; a "+
							"wrong bit changes which candidate the encoder tries and so the output",
							seed, off, c0, c1, got, want)
					}
				}
			}
		}
	}
}

func TestPrefix2Mask64AllOnesAndAllZeroes(t *testing.T) {
	same := make([]byte, 128)
	if got := prefix2Mask64(&same[0], 0, 0); got != ^uint64(0) {
		t.Fatalf("uniform buffer: got %064b, want all ones", got)
	}
	if got := prefix2Mask64(&same[0], 1, 1); got != 0 {
		t.Fatalf("no match anywhere: got %064b, want zero", got)
	}
}

func BenchmarkPrefix2Mask64(b *testing.B) {
	data := prefix2MaskFixture(b, 11)
	c0, c1 := data[64], data[65]
	b.Run("impl=OLD_scalar_scan", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			prefix2MaskSink = prefix2Mask64Scalar(data, 0, c0, c1)
		}
	})
	b.Run("impl=sse2_mask", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			prefix2MaskSink = prefix2Mask64(&data[0], c0, c1)
		}
	})
	b.Run("impl=sse2_mask_plus_walk", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			m := prefix2Mask64(&data[0], c0, c1)
			var n uint64
			for m != 0 {
				j := uint(63 - bits.LeadingZeros64(m))
				m &^= 1 << j
				n += uint64(j)
			}
			prefix2MaskSink = n
		}
	})
}
