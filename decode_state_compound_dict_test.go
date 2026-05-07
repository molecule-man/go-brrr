// Tests for decoder compound-dictionary attachment behavior.

package brrr

import (
	"errors"
	"testing"
)

func TestDecodeStateAttachCompoundDictOverflow(t *testing.T) {
	var s decodeState
	for i := range 15 {
		if err := s.attachCompoundDict([]byte{byte(i + 1)}); err != nil {
			t.Fatalf("attach %d: %v", i, err)
		}
	}
	if err := s.attachCompoundDict([]byte("overflow")); !errors.Is(err, errTooManyDicts) {
		t.Fatalf("expected errTooManyDicts, got %v", err)
	}
}

func TestDecodeStateAttachCompoundDictEmpty(t *testing.T) {
	var s decodeState
	if err := s.attachCompoundDict(nil); !errors.Is(err, errEmptyDict) {
		t.Fatalf("expected errEmptyDict for nil, got %v", err)
	}
	if err := s.attachCompoundDict([]byte{}); !errors.Is(err, errEmptyDict) {
		t.Fatalf("expected errEmptyDict for empty, got %v", err)
	}
}
