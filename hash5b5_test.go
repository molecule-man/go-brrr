package brrr

import "testing"

func TestH5b5Hash(t *testing.T) {
	var h h5b5

	// Output should stay within 14-bit range.
	inputs := []string{
		"\x00\x00\x00\x00",
		"\xFF\xFF\xFF\xFF",
		"Hell",
		"abcd",
		"\x01\x02\x03\x04",
	}
	for _, s := range inputs {
		v := h.hash([]byte(s), 0)
		if v >= h5b5BucketSize {
			t.Errorf("h5b5.hash(%q) = %d, exceeds h5b5BucketSize %d", s, v, h5b5BucketSize)
		}
	}

	// Different 4-byte inputs should usually produce different hashes.
	a := h.hash([]byte("ABCD"), 0)
	b := h.hash([]byte("EFGH"), 0)
	if a == b {
		t.Errorf("h5b5.hash collision: %q and %q both hash to %d", "ABCD", "EFGH", a)
	}
}

func TestH5b5StoreAndRetrieve(t *testing.T) {
	// Verify that storing positions and then scanning the bucket finds them.
	var h h5b5
	h.reset(false, 0, nil)

	const bufSize = 128
	data := make([]byte, bufSize)
	copy(data, "ABCDABCDABCDABCD") // repeated pattern
	mask := uint(bufSize - 1)

	// Store several positions.
	for i := range uint(12) {
		h.store(data, mask, i)
	}

	// The key for "ABCD" should have multiple entries.
	key := h.hash(data, 0)
	n := h.num[key]
	if n == 0 {
		t.Fatal("expected at least one entry in the bucket")
	}
}

func TestH5b5FindLongestMatch(t *testing.T) {
	// Create a data buffer with a repeated pattern.
	// "ABCDEFGH" at positions 0..7, then "xxABCDEFGH" at 8..17.
	const bufSize = 128
	data := make([]byte, bufSize)
	copy(data[0:], "ABCDEFGHxxABCDEFGH")

	var h h5b5
	h.reset(false, 0, nil)
	mask := uint(bufSize - 1)

	// Store positions 0..9.
	for i := range uint(10) {
		h.store(data, mask, i)
	}

	distCache := [4]uint{4, 11, 15, 16}

	var lookups, matches uint
	var sr hasherSearchResult
	sr.score = minScore

	h.findLongestMatch(data, mask, &distCache, 10, 18, 10, 0,
		&lookups, &matches, &sr)

	if sr.len < 8 {
		t.Errorf("match length = %d, want >= 8", sr.len)
	}
	if sr.distance != 10 {
		t.Errorf("match distance = %d, want 10", sr.distance)
	}
	if sr.score <= minScore {
		t.Errorf("score = %d, should be > minScore (%d)", sr.score, minScore)
	}
}

func TestH5b5FindLongestMatchCacheHit(t *testing.T) {
	// Verify that a distance cache hit is detected.
	const bufSize = 128
	data := make([]byte, bufSize)
	copy(data, "ABCDABCDxxxxxxxx")

	var h h5b5
	h.reset(false, 0, nil)
	mask := uint(bufSize - 1)

	// Only store positions 0-3; findLongestMatch stores cur=4 itself.
	for i := range uint(4) {
		h.store(data, mask, i)
	}

	distCache := [4]uint{4, 11, 15, 16}

	var lookups, matches uint
	var sr hasherSearchResult
	sr.score = minScore

	h.findLongestMatch(data, mask, &distCache, 4, 16, 4, 0,
		&lookups, &matches, &sr)

	if sr.len < 4 {
		t.Errorf("cache hit match length = %d, want >= 4", sr.len)
	}
	if sr.distance != 4 {
		t.Errorf("cache hit distance = %d, want 4", sr.distance)
	}
}

func TestH5b5NoMatchFallsToDict(t *testing.T) {
	// When no match is found, the static dictionary should be searched.
	const bufSize = 128
	data := make([]byte, bufSize)
	// Fill with non-repeating bytes.
	for i := range data {
		data[i] = byte(i)
	}

	var h h5b5
	h.reset(false, 0, nil)
	mask := uint(bufSize - 1)

	distCache := [4]uint{4, 11, 15, 16}

	var lookups, matches uint
	var sr hasherSearchResult
	sr.score = minScore

	h.findLongestMatch(data, mask, &distCache, 32, 16, 32, 0,
		&lookups, &matches, &sr)

	// The search should have at least tried the dictionary.
	if lookups == 0 {
		t.Error("expected dictionary lookup to be attempted")
	}
}

func TestH5b5Reset(t *testing.T) {
	var h h5b5

	// Store some data.
	data := make([]byte, 64)
	copy(data, "ABCDEFGH")
	h.reset(false, 0, nil)
	h.store(data, 63, 0)

	key := h.hash(data, 0)
	if h.num[key] == 0 {
		t.Fatal("expected non-zero count after store")
	}

	// Full reset should clear everything.
	h.reset(false, 0, nil)
	if h.num[key] != 0 {
		t.Errorf("expected zero count after reset, got %d", h.num[key])
	}
}

func TestH5b5StitchToPreviousBlock(t *testing.T) {
	const bufSize = 128
	data := make([]byte, bufSize)
	copy(data, "ABCDEFGHIJKLMNOPxxxx")

	var h h5b5
	h.reset(false, 0, nil)
	mask := uint(bufSize - 1)

	// Stitch should store positions 13, 14, 15 (= position-3, -2, -1 for position=16).
	h.stitchToPreviousBlock(20, 16, data, mask)

	// Verify at least position 15 got stored.
	key := h.hash(data, 15)
	if h.num[key] == 0 {
		t.Error("expected stitchToPreviousBlock to store position 15")
	}
}
