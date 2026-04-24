package brrr

import (
	"hash/crc32"
	"testing"
)

func TestDictionaryCRC32(t *testing.T) {
	if len(dictData) != dictDataSize {
		t.Fatalf("dictData length = %d, want %d", len(dictData), dictDataSize)
	}

	const wantCRC = 0x5136cb04
	got := crc32.ChecksumIEEE([]byte(dictData))
	if got != wantCRC {
		t.Fatalf("dictData CRC-32 = %#08x, want %#08x", got, wantCRC)
	}
}
