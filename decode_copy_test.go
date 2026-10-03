package brrr

import (
	"testing"
	"unsafe"
)

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
