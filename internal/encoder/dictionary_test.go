package encoder

import (
	"hash/crc32"
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

func TestDictionaryCRC32(t *testing.T) {
	if len(core.DictData) != core.DictDataSize {
		t.Fatalf("core.DictData length = %d, want %d", len(core.DictData), core.DictDataSize)
	}

	const wantCRC = 0x5136cb04
	got := crc32.ChecksumIEEE([]byte(core.DictData))
	if got != wantCRC {
		t.Fatalf("core.DictData CRC-32 = %#08x, want %#08x", got, wantCRC)
	}
}
