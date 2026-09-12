package encoder

import "testing"

// extendMatchSink keeps the benchmarked calls from being elided.
var extendMatchSink uint32

// extendMatchBefore is extendLastCommand's match-extension loop as it was
// before matchLenAt replaced the byte-at-a-time compare.
func extendMatchBefore(data []byte, length, wrappedPos, mask, cmdDist uint32) uint32 {
	var copyLen uint32
	for length != 0 && data[wrappedPos&mask] == data[(wrappedPos-cmdDist)&mask] {
		copyLen++
		length--
		wrappedPos++
	}
	return copyLen
}

// extendMatchAfter is the loop as it stands now.
func extendMatchAfter(data []byte, length, wrappedPos, mask, cmdDist uint32) uint32 {
	var copyLen uint32
	for length != 0 {
		dst := wrappedPos & mask
		src := (wrappedPos - cmdDist) & mask
		n := min(length, mask+1-dst, mask+1-src)
		m := uint32(matchLenAt(data, uint(src), uint(dst), int(n)))
		copyLen += m
		length -= m
		wrappedPos += m
		if m != n {
			break
		}
	}
	return copyLen
}

func extendMatchFixture(tb testing.TB, sizeLog, period int) (data []byte, mask uint32) {
	tb.Helper()
	n := 1 << sizeLog
	data = make([]byte, n)
	for i := range data {
		data[i] = byte(i % period)
	}
	return data, uint32(n - 1)
}

func TestExtendMatchWideCompareAgreesWithTheByteLoop(t *testing.T) {
	for _, period := range []int{1, 2, 3, 7, 16, 61, 256} {
		data, mask := extendMatchFixture(t, 16, period)
		for _, dist := range []uint32{1, 2, 3, 8, 16, 17, 64, 1000} {
			for _, length := range []uint32{0, 1, 2, 7, 8, 9, 31, 32, 33, 1000, 5000} {
				for _, pos := range []uint32{1024, 40000, uint32(mask) - 100} {
					want := extendMatchBefore(data, length, pos, mask, dist)
					got := extendMatchAfter(data, length, pos, mask, dist)
					if got != want {
						t.Fatalf("period=%d dist=%d length=%d pos=%d: extended %d bytes, byte "+
							"loop extends %d; a wrong copy length changes the command stream",
							period, dist, length, pos, got, want)
					}
				}
			}
		}
	}
}

func benchmarkExtendMatch(b *testing.B, period int, dist, length uint32) {
	data, mask := extendMatchFixture(b, 16, period)
	const pos = 1024
	b.Run("impl=before_byte_loop", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(length))
		for range b.N {
			extendMatchSink = extendMatchBefore(data, length, pos, mask, dist)
		}
	})
	b.Run("impl=after_matchlen_8byte", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(length))
		for range b.N {
			extendMatchSink = extendMatchAfter(data, length, pos, mask, dist)
		}
	})
}

func BenchmarkExtendMatchDist1Len4096(b *testing.B)  { benchmarkExtendMatch(b, 1, 1, 4096) }
func BenchmarkExtendMatchDist16Len4096(b *testing.B) { benchmarkExtendMatch(b, 16, 16, 4096) }
func BenchmarkExtendMatchDist61Len256(b *testing.B)  { benchmarkExtendMatch(b, 61, 61, 256) }
func BenchmarkExtendMatchDist8Len32(b *testing.B)    { benchmarkExtendMatch(b, 8, 8, 32) }
