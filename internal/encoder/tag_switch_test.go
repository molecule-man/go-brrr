package encoder

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"testing"
)

func TestLargeInputHasherSwitchesToTagsOnlyForBinaryInput(t *testing.T) {
	binary := make([]byte, 2<<20)
	rng := rand.New(rand.NewPCG(5, 6))
	for i := range binary {
		binary[i] = byte(rng.Uint32())
	}
	text := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), (2<<20)/45)

	tests := []struct {
		name    string
		quality int
		input   []byte
		want    string
	}{
		{"q5 binary", 5, binary, "*encoder.h6"},
		{"q6 binary", 6, binary, "*encoder.h6b5"},
		{"q5 text", 5, text, "*encoder.h6u"},
		{"q6 text", 6, text, "*encoder.h6b5u"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewCompressor(tt.quality, 22, uint(len(tt.input)), false).(*encoderSplit)
			if _, err := e.Write(io.Discard, tt.input); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if got := fmt.Sprintf("%T", e.hasher); got != tt.want {
				t.Errorf("hasher after %d bytes = %s, want %s", len(tt.input), got, tt.want)
			}
			if err := e.Close(io.Discard); err != nil {
				t.Fatalf("Close: %v", err)
			}
		})
	}
}

func TestLargeInputHasherFromPoolDoesNotSwitchOnOldCounters(t *testing.T) {
	for _, h := range []streamHasher{&h6u{busyCalls: h6uBusyCalls}, &h6b5u{busyCalls: h6b5uBusyCalls}} {
		e := NewCompressor(5, 22, 2<<20, false).(*encoderSplit)
		e.hasher = h
		e.resetHasher()
		e.inputPos = 1 << 20

		e.maybePromoteHasher()

		if e.hasher != h {
			t.Errorf("%T: converted to %T before reset", h, e.hasher)
		}
	}
}
