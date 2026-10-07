package encoder

import (
	"errors"
	"fmt"
	"testing"
)

func TestNonZeroASCIIRunStopsAtTheFirstZeroOrHighBitByteInBothTheWordAndTheTailLoop(t *testing.T) {
	var errs []error
	for _, tc := range []struct {
		name string
		data []byte
		want uint
	}{
		{"empty", []byte{}, 0},
		{"one_ascii", []byte{'a'}, 1},
		{"stops_on_leading_nul", []byte{0, 'a', 'b'}, 0},
		{"stops_on_leading_high_bit", []byte{0xC3, 'a'}, 0},
		{"seven_ascii_tail_only", []byte("abcdefg"), 7},
		{"eight_ascii_one_word", []byte("abcdefgh"), 8},
		{"nine_ascii_word_plus_tail", []byte("abcdefghi"), 9},
		{"nul_inside_first_word", []byte{'a', 'b', 0, 'd', 'e', 'f', 'g', 'h', 'i'}, 2},
		{"high_bit_inside_first_word", []byte{'a', 'b', 'c', 0xE2, 'e', 'f', 'g', 'h', 'i'}, 3},
		{"nul_in_second_word", []byte("abcdefgh\x00jklmnopq"), 8},
		{"high_bit_at_word_boundary", []byte("abcdefg\xC3ijklmnop"), 7},
		{"all_ascii_two_words", []byte("abcdefghijklmnop"), 16},
	} {
		if got := nonZeroASCIIRun(tc.data, 0, uint(len(tc.data))); got != tc.want {
			errs = append(errs, fmt.Errorf(
				"a wrong run length silently reclassifies bytes as valid one-byte sequences: case=%s got=%d want=%d",
				tc.name, got, tc.want))
		}
	}
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
}
