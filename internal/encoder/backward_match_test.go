package encoder

import (
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

func TestDictionaryBackwardMatchPacking(t *testing.T) {
	for length := uint(core.DictMinWordLength); length <= maxStaticDictMatchLen; length++ {
		for lenCode := uint(core.DictMinWordLength); lenCode <= core.DictMaxWordLength; lenCode++ {
			m := newDictionaryBackwardMatch(1<<20, length, lenCode)
			if got := m.matchLength(); got != length {
				t.Fatalf("length=%d lenCode=%d: matchLength() = %d", length, lenCode, got)
			}
			if got := m.matchLengthCode(); got != lenCode {
				t.Fatalf("length=%d lenCode=%d: matchLengthCode() = %d", length, lenCode, got)
			}
		}
	}
}
