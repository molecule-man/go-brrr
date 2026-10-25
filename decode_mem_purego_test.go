//go:build purego

// Bounds tests for the checked decoder primitives.

package brrr

import (
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

const ringCopyPanic = "brrr: ring buffer access past the logical end"

func expectBoundsPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		got := recover()
		err, ok := got.(runtime.Error)
		if !ok || !strings.Contains(err.Error(), "out of range") {
			t.Errorf("%s: panic = %v, want a bounds panic", name, got)
		}
	}()
	f()
}

func expectRingCopyPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if got := recover(); got != ringCopyPanic {
			t.Errorf("%s: panic = %v, want %q", name, got, ringCopyPanic)
		}
	}()
	f()
}

func TestCheckedLoadsStopAtTheSliceEnd(t *testing.T) {
	b := make([]byte, 9)
	expectBoundsPanic(t, "load32 at len-3", func() { load32(b, 6) })
	expectBoundsPanic(t, "load64 at len-7", func() { load64(b, 2) })
	expectBoundsPanic(t, "load32 at -1", func() { load32(b, -1) })
	expectBoundsPanic(t, "load64 at -1", func() { load64(b, -1) })
}

func TestCheckedCopy16StopsAtTheSliceEnd(t *testing.T) {
	b := make([]byte, 40)
	for i := range b {
		b[i] = byte(i)
	}
	copy16(b, 24, 0)
	for i := range 16 {
		if b[24+i] != byte(i) {
			t.Fatalf("b[%d] = %d, want %d", 24+i, b[24+i], i)
		}
		b[24+i] = byte(80 + i)
	}
	copy16(b, 0, 24)
	for i := range 16 {
		if b[i] != byte(80+i) {
			t.Fatalf("b[%d] = %d, want %d", i, b[i], 80+i)
		}
	}
	expectBoundsPanic(t, "dst at len-15", func() { copy16(b, 25, 0) })
	expectBoundsPanic(t, "src at len-15", func() { copy16(b, 0, 25) })
	expectBoundsPanic(t, "dst at -1", func() { copy16(b, -1, 0) })
	expectBoundsPanic(t, "src at -1", func() { copy16(b, 0, -1) })
}

func TestCheckedHuffmanEntryStopsAtTheTableEnd(t *testing.T) {
	table := make([]core.HuffmanCode, 2)
	expectBoundsPanic(t, "offset+i at len", func() { huffmanEntry(table, 1, 1) })
	expectBoundsPanic(t, "offset past len", func() { huffmanEntry(table, 3, 0) })
	// uint64(-1)+1 wraps to 0, which is a valid index.
	expectBoundsPanic(t, "negative offset", func() { huffmanEntry(table, -1, 1) })
}

func TestCheckedUint64AtAndDistExtraStopAtTheEnd(t *testing.T) {
	lut := make([]uint64, 4)
	*uint64At(lut, 3) = 42
	if lut[3] != 42 {
		t.Fatalf("lut[3] = %d, want 42", lut[3])
	}
	expectBoundsPanic(t, "uint64At at len", func() { uint64At(lut, 4) })

	var b metablockBodyArena
	last := len(b.distOffset) - 1
	b.distExtraBits[last] = 3
	b.distOffset[last] = 17
	if bits, offset := distExtra(&b, uint(last)); bits != 3 || offset != 17 {
		t.Fatalf("distExtra(last) = (%d, %d), want (3, 17)", bits, offset)
	}
	expectBoundsPanic(t, "distExtra at len", func() { distExtra(&b, uint(len(b.distOffset))) })
}

func TestCheckRingCopy(t *testing.T) {
	const size = 64
	for _, tc := range []struct{ pos, n int }{
		{0, 0}, {0, size}, {size - 1, 0}, {size - 1, 1}, {10, size - 10},
	} {
		checkRingCopy(size, tc.pos, tc.n)
	}
	for _, tc := range []struct{ pos, n int }{
		{0, size + 1}, {size, 0}, {size, 1}, {size + 1, 0}, {-1, 0}, {0, -1}, {1, math.MaxInt},
	} {
		expectRingCopyPanic(t, "checkRingCopy", func() { checkRingCopy(size, tc.pos, tc.n) })
	}
}

// An injected pos at or past the logical end must stop the copy before it
// writes. Slice bounds alone allow it, because the slack is part of the
// allocation.
func TestCheckedRingCopyRejectsPosAtTheLogicalEnd(t *testing.T) {
	for _, extra := range []int{0, 1} {
		var s decodeState
		s.state = decoderStateCommandPostWrapCopy
		s.ringbufferSize = 64
		s.ringbufferMask = s.ringbufferSize - 1
		s.ringbuffer = make([]byte, s.ringbufferSize+ringBufferWriteAheadSlack)
		s.pos = s.ringbufferSize + extra
		s.loopCounter = 8
		s.distanceCode = 1
		const canary = 0xC7
		for i := s.ringbufferSize; i < len(s.ringbuffer); i++ {
			s.ringbuffer[i] = canary
		}

		var got any
		func() {
			defer func() { got = recover() }()
			s.processCommands()
		}()

		if got != ringCopyPanic {
			t.Fatalf("pos=size+%d: processCommands panic = %v, want %q", extra, got, ringCopyPanic)
		}
		for i := s.ringbufferSize; i < len(s.ringbuffer); i++ {
			if s.ringbuffer[i] != canary {
				t.Fatalf("pos=size+%d: ringbuffer[%d] = %#x, want canary %#x", extra, i, s.ringbuffer[i], canary)
			}
		}
	}
}
