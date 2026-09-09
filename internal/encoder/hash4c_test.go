package encoder

import (
	"testing"
	"unsafe"
)

func TestH4cLayout(t *testing.T) {
	var h4 h40
	var h10 h41

	if got, want := unsafe.Sizeof(h4), unsafe.Sizeof(h10); got != want {
		t.Errorf("h40 size = %d, h41 size = %d, want equal", got, want)
	}

	for _, c := range []struct {
		name        string
		got4, got10 uintptr
	}{
		{"maxHops", unsafe.Offsetof(h4.maxHops), unsafe.Offsetof(h10.maxHops)},
		{"addr", unsafe.Offsetof(h4.addr), unsafe.Offsetof(h10.addr)},
		{"head", unsafe.Offsetof(h4.head), unsafe.Offsetof(h10.head)},
		{"tinyHash", unsafe.Offsetof(h4.tinyHash), unsafe.Offsetof(h10.tinyHash)},
		{"slots", unsafe.Offsetof(h4.slots), unsafe.Offsetof(h10.slots)},
	} {
		if c.got4 != c.got10 {
			t.Errorf("%s offset: h40 = %d, h41 = %d, want equal", c.name, c.got4, c.got10)
		}
	}

	if got := unsafe.Offsetof(h4.maxHops); got != 0 {
		t.Errorf("maxHops offset = %d, want 0", got)
	}

	// addr must stay eight-byte aligned for memclr stores.
	if got := unsafe.Offsetof(h4.addr); got%8 != 0 {
		t.Errorf("addr offset = %d, want a multiple of 8", got)
	}
}
