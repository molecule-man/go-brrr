package encoder

import (
	"encoding/binary"
	"math/rand/v2"
	"testing"
)

func TestTagEqualMask8MatchesByteCompare(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for tag := range 256 {
		// Bytes near tag catch borrow and high-bit errors in the SWAR compare.
		near := []byte{
			byte(tag), byte(tag ^ 0x01), byte(tag ^ 0x80), byte(tag ^ 0xFF),
			byte(tag + 1), byte(tag - 1), 0x00, 0x80, 0xFF, 0x7F,
		}
		for range 2000 {
			var tags [8]byte
			for k := range tags {
				if rng.IntN(4) == 0 {
					tags[k] = byte(rng.Uint32())
				} else {
					tags[k] = near[rng.IntN(len(near))]
				}
			}
			var want uint32
			for k, b := range tags {
				if b == byte(tag) {
					want |= 1 << k
				}
			}
			c := binary.LittleEndian.Uint64(tags[:]) ^ uint64(tag)*0x0101010101010101
			if got := tagEqualMask8(c); got != want {
				t.Fatalf("tag %#02x, tags %x: got %08b, want %08b", tag, tags, got, want)
			}
		}
	}
}
