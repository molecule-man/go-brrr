package core

import (
	"testing"
	"unsafe"
)

// The decoder reads HuffmanCode as fields and as a native uint32.
// Both views must agree on every byte order.
func TestHuffmanCodeRaw(t *testing.T) {
	if size := unsafe.Sizeof(HuffmanCode{}); size != 4 {
		t.Fatalf("HuffmanCode size = %d, want 4", size)
	}
	codes := []HuffmanCode{
		{Bits: 0, Value: 0}, {Bits: 1, Value: 0x0102}, {Bits: 7, Value: 0xABCD},
		{Bits: 15, Value: 0xFFFF}, {Bits: 0xFF, Value: 0x8001},
	}

	for _, code := range codes {
		lit := []HuffmanCode{code}
		raw := *(*uint32)(unsafe.Pointer(&lit[0]))
		if byte(raw) != code.Bits || uint16(raw>>16) != code.Value {
			t.Errorf("raw(%+v) = %#x: Bits %#x, Value %#x", code, raw, byte(raw), uint16(raw>>16))
		}

		table := make([]HuffmanCode, 8)
		replicateValue(table, code, 2, len(table))
		for i, got := range table {
			if i%2 == 1 {
				if got != (HuffmanCode{}) {
					t.Errorf("table[%d] = %+v, want zero", i, got)
				}
				continue
			}
			if got != code {
				t.Errorf("replicateValue: table[%d] = %+v, want %+v", i, got, code)
			}
		}
	}
}
