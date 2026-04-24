// Unit tests for static dictionary search.

package brrr

import "testing"

// lookupTestEntry returns a test dictionary entry from the hash tables.
// It finds the first non-zero entry and returns the hash14 key, word length,
// word index, and the raw dictionary word bytes.
func lookupTestEntry() (key uint32, wordLen, wordIndex uint, word string) {
	for i := range uint32(len(staticDictHashLengths)) {
		if staticDictHashLengths[i] != 0 {
			wl := uint(staticDictHashLengths[i])
			wi := uint(staticDictHashWords[i])
			offset := uint(dictOffsetsByLength[wl]) + wl*wi
			return i, wl, wi, dictData[offset : offset+wl]
		}
	}
	panic("no non-zero hash table entry")
}

func TestHash14(t *testing.T) {
	// The first non-zero hash table entry should round-trip: hash14 of the
	// dictionary word's first 4 bytes, shifted left by 1, should equal the
	// bucket index.
	key, _, _, word := lookupTestEntry()

	// hash14 needs at least 4 bytes; pad with zeros if needed.
	buf := make([]byte, 8)
	copy(buf, word)
	got := hash14(buf) << 1

	// The key should be even (hash14 << 1), and our entry is at an even index.
	// The hash may map to key or key^1 (two buckets per hash14 value).
	if got != key&^1 {
		t.Errorf("hash14(word) << 1 = %d, want %d (bucket pair base)", got, key&^1)
	}
}

func TestFindMatchLenDict(t *testing.T) {
	t.Run("FullMatch", func(t *testing.T) {
		data := []byte("abcdef")
		if n := findMatchLenDict(data, "abcdef"); n != 6 {
			t.Errorf("findMatchLenDict full match = %d, want 6", n)
		}
	})

	t.Run("PartialMatch", func(t *testing.T) {
		data := []byte("abcXef")
		if n := findMatchLenDict(data, "abcdef"); n != 3 {
			t.Errorf("findMatchLenDict partial match = %d, want 3", n)
		}
	})

	t.Run("NoMatch", func(t *testing.T) {
		data := []byte("Xbcdef")
		if n := findMatchLenDict(data, "abcdef"); n != 0 {
			t.Errorf("findMatchLenDict no match = %d, want 0", n)
		}
	})
}

func TestMatchStaticDictEntry(t *testing.T) {
	_, wordLen, wordIndex, word := lookupTestEntry()

	// Build input that matches the dictionary word exactly.
	data := make([]byte, wordLen+8) // extra bytes for safety
	copy(data, word)

	maxBackward := uint(1024)
	maxDistance := uint(maxBackwardDistance)

	out, ok := matchStaticDictEntry(data, wordLen, wordIndex, maxBackward, maxDistance, minScore)
	if !ok {
		t.Fatal("matchStaticDictEntry returned false for exact dictionary word")
	}

	if out.len != wordLen {
		t.Errorf("match len = %d, want %d", out.len, wordLen)
	}
	if out.lenCodeDelta != 0 {
		t.Errorf("lenCodeDelta = %d, want 0 (full match)", out.lenCodeDelta)
	}

	// For a full match: cut=0, transformID=0, backward = maxBackward + 1 + wordIndex.
	wantBackward := maxBackward + 1 + wordIndex
	if out.distance != wantBackward {
		t.Errorf("distance = %d, want %d", out.distance, wantBackward)
	}
	if out.score <= minScore {
		t.Errorf("score = %d, should be > minScore (%d)", out.score, minScore)
	}
}

func TestSearchStaticDictionary_TooShort(t *testing.T) {
	_, wordLen, _, word := lookupTestEntry()

	data := make([]byte, wordLen+8)
	copy(data, word)

	// maxLength < wordLen → caller skips the entry.
	var lookups, matches uint
	_, ok := searchStaticDictionary(data, wordLen-1, 1024, maxBackwardDistance, &lookups, &matches, minScore)

	if ok {
		t.Error("expected no match when maxLength < wordLen")
	}
}

func TestMatchStaticDictEntry_PartialMatch(t *testing.T) {
	_, wordLen, wordIndex, word := lookupTestEntry()

	// Match all but the last byte.
	data := make([]byte, wordLen+8)
	copy(data, word)
	data[wordLen-1] ^= 0xFF // corrupt last byte

	out, ok := matchStaticDictEntry(data, wordLen, wordIndex, 1024, maxBackwardDistance, minScore)
	if !ok {
		t.Fatal("matchStaticDictEntry returned false for partial match within cutoff range")
	}

	if out.len != wordLen-1 {
		t.Errorf("match len = %d, want %d", out.len, wordLen-1)
	}
	if out.lenCodeDelta != 1 {
		t.Errorf("lenCodeDelta = %d, want 1", out.lenCodeDelta)
	}
}

func TestSearchStaticDictionary(t *testing.T) {
	_, wordLen, _, word := lookupTestEntry()

	// Build input data from the dictionary word.
	data := make([]byte, wordLen+8)
	copy(data, word)

	maxBackward := uint(1024)
	maxDistance := uint(maxBackwardDistance)

	t.Run("FindsMatch", func(t *testing.T) {
		var lookups, matches uint

		out, ok := searchStaticDictionary(data, wordLen, maxBackward, maxDistance, &lookups, &matches, minScore)

		if lookups == 0 {
			t.Fatal("expected at least 1 lookup")
		}
		if !ok {
			t.Fatal("expected a match")
		}
		if out.score <= minScore {
			t.Errorf("score = %d, should be > minScore (%d)", out.score, minScore)
		}
		if matches == 0 {
			t.Fatal("expected dictNumMatches to be incremented")
		}
	})

	t.Run("Deep", func(t *testing.T) {
		var lookups, matches uint
		var sr hasherSearchResult
		sr.score = minScore

		searchStaticDictionaryDeep(data, wordLen, maxBackward, maxDistance,
			&lookups, &matches, &sr)

		if lookups == 0 {
			t.Fatal("expected at least 1 lookup")
		}
		// Deep search checks 2 entries, so lookups should be 2.
		if lookups != 2 {
			t.Errorf("lookups = %d, want 2", lookups)
		}
		if sr.score <= minScore {
			t.Errorf("score = %d, should be > minScore (%d)", sr.score, minScore)
		}
	})

	t.Run("AdaptiveHeuristic", func(t *testing.T) {
		// After many lookups with no matches, the heuristic should skip the search.
		lookups := uint(256) // 256 >> 7 = 2, so matches must be >= 2
		matches := uint(0)

		_, ok := searchStaticDictionary(data, wordLen, maxBackward, maxDistance, &lookups, &matches, minScore)

		if ok {
			t.Error("expected no match when adaptive heuristic triggers early return")
		}
		if lookups != 256 {
			t.Errorf("lookups = %d, want 256 (should not have been incremented)", lookups)
		}
	})

	t.Run("DeepAdaptiveHeuristic", func(t *testing.T) {
		lookups := uint(256)
		matches := uint(0)
		var sr hasherSearchResult
		sr.score = minScore

		searchStaticDictionaryDeep(data, wordLen, maxBackward, maxDistance,
			&lookups, &matches, &sr)

		if sr.score != minScore {
			t.Error("expected no improvement when adaptive heuristic triggers early return")
		}
		if lookups != 256 {
			t.Errorf("lookups = %d, want 256 (should not have been incremented)", lookups)
		}
	})
}
