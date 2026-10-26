//go:build cgo

package cref

import (
	"bytes"
	"errors"
	"testing"
)

func TestDecodeOutputLimit(t *testing.T) {
	for _, n := range []int{100, 3 << 20} {
		plain := bytes.Repeat([]byte("abc"), n/3)
		encoded, err := Encode(plain, 5, 22, uint(len(plain)))
		if err != nil {
			t.Fatal(err)
		}

		out, consumed, err := DecodeConsumed(encoded, len(plain))
		if err != nil || consumed != len(encoded) || !bytes.Equal(out, plain) {
			t.Fatalf("n=%d: DecodeConsumed at limit = (%d bytes, %d consumed, %v), want (%d, %d, nil)",
				n, len(out), consumed, err, len(plain), len(encoded))
		}
		if _, _, err := DecodeConsumed(encoded, len(plain)-1); !errors.Is(err, ErrOutputLimit) {
			t.Fatalf("n=%d: DecodeConsumed below limit: err = %v, want ErrOutputLimit", n, err)
		}

		out, err = DecodePrefix(encoded, len(plain))
		if err != nil || !bytes.Equal(out, plain) {
			t.Fatalf("n=%d: DecodePrefix at limit = (%d bytes, %v), want (%d, nil)", n, len(out), err, len(plain))
		}
		if _, err := DecodePrefix(encoded, len(plain)-1); !errors.Is(err, ErrOutputLimit) {
			t.Fatalf("n=%d: DecodePrefix below limit: err = %v, want ErrOutputLimit", n, err)
		}
	}
}
