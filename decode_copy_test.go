package brrr

import (
	"testing"
	"unsafe"
)

// copyOverlappingSink keeps the benchmarked stores from being elided.
var copyOverlappingSink byte

// copyOverlappingReference is the byte-at-a-time loop the wide path replaces.
func copyOverlappingReference(rb []byte, pos, dist, n int) {
	for k := range n {
		rb[pos+k] = rb[pos+k-dist]
	}
}

func copyOverlappingFixture(tb testing.TB, pos, n int) []byte {
	tb.Helper()
	// 542 bytes of trailing slack, mirroring ringBufferWriteAheadSlack, because
	// the wide path may store up to 15 bytes past pos+n.
	rb := make([]byte, pos+n+542)
	for i := range pos {
		rb[i] = byte(i*31 + 7)
	}
	return rb
}

func TestCopyOverlappingPatternMatchesTheByteLoopAtEveryDistanceAndLength(t *testing.T) {
	const pos = 256
	for dist := 1; dist <= 40; dist++ {
		for _, n := range []int{1, 2, 3, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 65, 100, 255, 1000} {
			if dist >= n {
				continue // not an overlapping copy; a different path handles it
			}
			got := copyOverlappingFixture(t, pos, n)
			want := copyOverlappingFixture(t, pos, n)

			base := unsafe.Pointer(unsafe.SliceData(got))
			copyOverlappingPattern(base, pos, dist, n)
			copyOverlappingReference(want, pos, dist, n)

			for k := range n {
				if got[pos+k] != want[pos+k] {
					t.Fatalf("dist=%d n=%d byte %d: got %#02x, byte loop gives %#02x; a wrong "+
						"back-reference byte silently corrupts the decoded output",
						dist, n, k, got[pos+k], want[pos+k])
				}
			}
		}
	}
}

func TestCopyOverlappingPatternNeverWritesBeforePos(t *testing.T) {
	const pos = 256
	for dist := 1; dist <= 20; dist++ {
		for _, n := range []int{1, 16, 17, 100, 1000} {
			if dist >= n {
				continue
			}
			rb := copyOverlappingFixture(t, pos, n)
			before := append([]byte(nil), rb[:pos]...)

			copyOverlappingPattern(unsafe.Pointer(unsafe.SliceData(rb)), pos, dist, n)

			for i := range pos {
				if rb[i] != before[i] {
					t.Fatalf("dist=%d n=%d: byte %d before pos was modified; the source window "+
						"is already-final output and must never be rewritten", dist, n, i)
				}
			}
		}
	}
}

func TestCopyOverlappingPatternStaysWithinTheRingBufferSlack(t *testing.T) {
	// The wide stores are allowed to run past pos+n, but only into the slack the
	// ring buffer guarantees. 15 is the most a 16-byte store can overshoot.
	const pos, maxOvershoot = 256, 15
	for dist := 1; dist <= 20; dist++ {
		for _, n := range []int{1, 5, 16, 17, 100} {
			if dist >= n {
				continue
			}
			rb := copyOverlappingFixture(t, pos, n)
			const canary = 0xC7
			for i := pos + n; i < len(rb); i++ {
				rb[i] = canary
			}

			copyOverlappingPattern(unsafe.Pointer(unsafe.SliceData(rb)), pos, dist, n)

			for i := pos + n + maxOvershoot; i < len(rb); i++ {
				if rb[i] != canary {
					t.Fatalf("dist=%d n=%d: wrote %d bytes past pos+n, more than the %d a 16-byte "+
						"store can overshoot; the ring buffer slack is a fixed 542 bytes",
						dist, n, i-(pos+n), maxOvershoot)
				}
			}
		}
	}
}

func benchmarkCopyOverlapping(b *testing.B, dist, n int) {
	const pos = 4096
	rb := copyOverlappingFixture(b, pos, n)
	base := unsafe.Pointer(unsafe.SliceData(rb))

	b.Run("impl=before_byte_loop", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(n))
		for range b.N {
			copyOverlappingReference(rb, pos, dist, n)
		}
		copyOverlappingSink = rb[pos]
	})
	b.Run("impl=after_wide_pattern", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(n))
		for range b.N {
			copyOverlappingPattern(base, pos, dist, n)
		}
		copyOverlappingSink = rb[pos]
	})
}

func BenchmarkCopyOverlappingDist1Len64(b *testing.B)    { benchmarkCopyOverlapping(b, 1, 64) }
func BenchmarkCopyOverlappingDist4Len64(b *testing.B)    { benchmarkCopyOverlapping(b, 4, 64) }
func BenchmarkCopyOverlappingDist12Len256(b *testing.B)  { benchmarkCopyOverlapping(b, 12, 256) }
func BenchmarkCopyOverlappingDist32Len256(b *testing.B)  { benchmarkCopyOverlapping(b, 32, 256) }
func BenchmarkCopyOverlappingDist64Len1024(b *testing.B) { benchmarkCopyOverlapping(b, 64, 1024) }
