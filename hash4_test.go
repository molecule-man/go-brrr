// Tests for H4 and H54 hashers.

package brrr

import "testing"

func TestHash5_17(t *testing.T) {
	// Verify output stays within 17-bit range.
	inputs := []string{
		"\x00\x00\x00\x00\x00\x00\x00\x00",
		"\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF",
		"Hello, W",
		"abcdefgh",
		"\x01\x02\x03\x04\x05\x06\x07\x08",
	}
	var hasher h4
	for _, s := range inputs {
		v := hasher.hash([]byte(s), 0)
		if v >= h4BucketSize {
			t.Errorf("h4.hash(%q) = %d, exceeds h4BucketSize %d", s, v, h4BucketSize)
		}
	}

	// Different 5-byte prefixes should (usually) produce different hashes.
	a := hasher.hash([]byte("ABCDE___"), 0)
	b := hasher.hash([]byte("FGHIJ___"), 0)
	if a == b {
		t.Errorf("h4.hash collision: %q and %q both hash to %d", "ABCDE___", "FGHIJ___", a)
	}
}

func TestHash7_20(t *testing.T) {
	// Verify output stays within 20-bit range.
	inputs := []string{
		"\x00\x00\x00\x00\x00\x00\x00\x00",
		"\xFF\xFF\xFF\xFF\xFF\xFF\xFF\xFF",
		"Hello, W",
		"abcdefgh",
		"\x01\x02\x03\x04\x05\x06\x07\x08",
	}
	var hasher h54
	for _, s := range inputs {
		v := hasher.hash([]byte(s), 0)
		if v >= h54BucketSize {
			t.Errorf("h54.hash(%q) = %d, exceeds h54BucketSize %d", s, v, h54BucketSize)
		}
	}

	// Same 5-byte prefix but different bytes 6-7 should produce different hashes
	// since H54 uses 7 bytes.
	a := hasher.hash([]byte("ABCDEFG_"), 0)
	b := hasher.hash([]byte("ABCDEXY_"), 0)
	if a == b {
		t.Errorf("h54.hash collision: %q and %q both hash to %d", "ABCDEFG_", "ABCDEXY_", a)
	}
}

func TestCreateBackwardReferencesH4(t *testing.T) {
	// Set up an encodeState with data that has an obvious repeated pattern.
	// Verify that h4.createBackwardReferences produces at least one
	// backward reference command.
	const lgwin = 18
	const rbBits = 19 // 1 + max(18, 14)
	const rbSize = 1 << rbBits
	const mask = rbSize - 1

	// "Hello, Hello, Hello, Hello, " — repeated "Hello, " at distance 7.
	input := []byte("Hello, Hello, Hello, Hello, Hello, Hello, Hello, Hello, ______")
	data := make([]byte, rbSize)
	copy(data, input)

	s := encodeState{
		data:                data,
		mask:                mask,
		lgwin:               lgwin,
		lgblock:             14,
		quality:             4,
		distAlphabetSizeMax: 64,
		distCache:           [4]uint{4, 11, 15, 16},
		savedDistCache:      [4]uint{4, 11, 15, 16},
	}
	var h h4
	h.reset(true, uint(len(input)), data)

	h.createBackwardReferences(&s, uint32(len(input)-8), 0)

	if s.numCommands == 0 {
		t.Fatal("expected at least one command from h4.createBackwardReferences")
	}

	// Verify we found at least one backward reference (not just inserts).
	foundBackRef := false
	for _, cmd := range s.commands {
		if cmd.copyLength() > 0 && cmd.distPrefixCode() != 0 {
			foundBackRef = true
			break
		}
	}
	if !foundBackRef {
		t.Error("expected at least one backward reference command")
	}
}

func TestCreateBackwardReferencesH54(t *testing.T) {
	const lgwin = 18
	const rbBits = 19
	const rbSize = 1 << rbBits
	const mask = rbSize - 1

	input := []byte("Hello, Hello, Hello, Hello, Hello, Hello, Hello, Hello, ______")
	data := make([]byte, rbSize)
	copy(data, input)

	s := encodeState{
		data:                data,
		mask:                mask,
		lgwin:               lgwin,
		lgblock:             14,
		quality:             4,
		distAlphabetSizeMax: 64,
		distCache:           [4]uint{4, 11, 15, 16},
		savedDistCache:      [4]uint{4, 11, 15, 16},
	}
	var h h54
	h.reset(true, uint(len(input)), data)

	h.createBackwardReferences(&s, uint32(len(input)-8), 0)

	if s.numCommands == 0 {
		t.Fatal("expected at least one command from h54.createBackwardReferences")
	}

	foundBackRef := false
	for _, cmd := range s.commands {
		if cmd.copyLength() > 0 && cmd.distPrefixCode() != 0 {
			foundBackRef = true
			break
		}
	}
	if !foundBackRef {
		t.Error("expected at least one backward reference command")
	}
}
