// Tests for the decode_mem.go / decode_mem_checked.go primitives. These run in
// both builds, so they also check that both versions return the same values.

package brrr

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/molecule-man/go-brrr/internal/core"
)

func TestDecodeMemLoadsAreLittleEndian(t *testing.T) {
	b := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9}
	if got, want := load32(b, 5), uint32(0x09080706); got != want {
		t.Errorf("load32(b, 5) = %#x, want %#x", got, want)
	}
	if got, want := load64(b, 1), uint64(0x0908070605040302); got != want {
		t.Errorf("load64(b, 1) = %#x, want %#x", got, want)
	}
}

func TestDecodeMemHuffmanEntryPacksBitsAndValue(t *testing.T) {
	table := []core.HuffmanCode{{Bits: 1, Value: 2}, {Bits: 3, Value: 0x1234}}
	want := uint32(3) | 0x1234<<16
	if got := huffmanEntry(table, 1, 0); got != want {
		t.Errorf("huffmanEntry(table, 1, 0) = %#x, want %#x", got, want)
	}
	if got := huffmanEntry(table, 0, 1); got != want {
		t.Errorf("huffmanEntry(table, 0, 1) = %#x, want %#x", got, want)
	}
}

func TestDecodeMemCopy16Overlap(t *testing.T) {
	// The load completes before the store, so a forward overlap copies the
	// original bytes, not bytes that the same copy wrote.
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i)
	}
	copy16(b, 4, 0)
	for i := range 16 {
		if b[4+i] != byte(i) {
			t.Fatalf("b[%d] = %d, want %d", 4+i, b[4+i], i)
		}
	}
}

func TestClearScalarFieldsZeroesEveryField(t *testing.T) {
	var s decodeState
	start := unsafe.Pointer(&s.state)
	end := uintptr(unsafe.Pointer(&s.distCodesCache)) + unsafe.Sizeof(s.distCodesCache)
	region := unsafe.Slice((*byte)(start), end-uintptr(start))
	for i := range region {
		region[i] = 0xFF
	}

	s.clearScalarFields()

	v := reflect.ValueOf(&s).Elem()
	typ := v.Type()
	first, _ := typ.FieldByName("state")
	last, _ := typ.FieldByName("distCodesCache")
	for i := first.Index[0]; i <= last.Index[0]; i++ {
		if !v.Field(i).IsZero() {
			t.Errorf("clearScalarFields did not zero %s", typ.Field(i).Name)
		}
	}
}
