package brrr

import "testing"

func TestStaticDictLUT(t *testing.T) {
	t.Run("BucketsLength", func(t *testing.T) {
		if len(staticDictBuckets) != 32768 {
			t.Fatalf("len(staticDictBuckets) = %d, want 32768", len(staticDictBuckets))
		}
	})

	t.Run("WordsLength", func(t *testing.T) {
		if len(staticDictWords) != 31705 {
			t.Fatalf("len(staticDictWords) = %d, want 31705", len(staticDictWords))
		}
	})

	t.Run("PlaceholderEntry", func(t *testing.T) {
		want := dictWord{0, 0, 0}
		if staticDictWords[0] != want {
			t.Fatalf("staticDictWords[0] = %+v, want %+v", staticDictWords[0], want)
		}
	})

	// Spot-check first bucket values against C static_dict_lut_inc.h.
	t.Run("BucketSpotCheck", func(t *testing.T) {
		want := []uint16{1, 0, 0, 0, 0, 0, 0, 0, 0, 3, 6, 0, 0, 0, 0, 0, 20, 0, 0, 0}
		for i, w := range want {
			if staticDictBuckets[i] != w {
				t.Errorf("staticDictBuckets[%d] = %d, want %d", i, staticDictBuckets[i], w)
			}
		}
	})

	// Spot-check first word entries against C static_dict_lut_inc.h.
	t.Run("WordSpotCheck", func(t *testing.T) {
		want := []dictWord{
			{0, 0, 0},
			{8, 0, 1002},
			{136, 0, 1015},
			{4, 0, 683},
			{4, 10, 325},
			{138, 10, 125},
			{7, 11, 572},
			{9, 11, 592},
			{11, 11, 680},
			{11, 11, 842},
		}
		for i, w := range want {
			if staticDictWords[i] != w {
				t.Errorf("staticDictWords[%d] = %+v, want %+v", i, staticDictWords[i], w)
			}
		}
	})

	// Every non-zero bucket offset must point to a valid chain ending with
	// the end-of-bucket flag (len & 0x80).
	t.Run("BucketChainIntegrity", func(t *testing.T) {
		for i, off := range staticDictBuckets {
			if off == 0 {
				continue
			}
			if int(off) >= len(staticDictWords) {
				t.Fatalf("bucket %d: offset %d out of range", i, off)
			}
			// Walk until end-of-bucket flag.
			found := false
			for j := int(off); j < len(staticDictWords); j++ {
				if staticDictWords[j].len&0x80 != 0 {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("bucket %d: chain starting at %d never terminates", i, off)
			}
		}
	})

	// Count non-placeholder entries: those with word length in [4,24].
	t.Run("EntryCount", func(t *testing.T) {
		count := 0
		for _, w := range staticDictWords {
			l := w.len & 0x1F
			if l >= 4 && l <= 24 {
				count++
			}
		}
		// 31704 real entries + 1 placeholder = 31705 total.
		if count != 31704 {
			t.Errorf("non-placeholder entries = %d, want 31704", count)
		}
	})
}

func TestStaticDictHash(t *testing.T) {
	t.Run("Lengths", func(t *testing.T) {
		if len(staticDictHashWords) != 32768 {
			t.Fatalf("len(staticDictHashWords) = %d, want 32768", len(staticDictHashWords))
		}
		if len(staticDictHashLengths) != 32768 {
			t.Fatalf("len(staticDictHashLengths) = %d, want 32768", len(staticDictHashLengths))
		}
	})

	// Spot-check first values against C dictionary_hash_inc.h.
	t.Run("HashWordsSpotCheck", func(t *testing.T) {
		want := []uint16{
			1002, 0, 0, 0, 0, 0, 0, 0, 0, 683, 0, 0, 0, 0, 0, 0, 0, 1265, 0, 0,
		}
		for i, w := range want {
			if staticDictHashWords[i] != w {
				t.Errorf("staticDictHashWords[%d] = %d, want %d", i, staticDictHashWords[i], w)
			}
		}
	})

	t.Run("HashLengthsSpotCheck", func(t *testing.T) {
		want := []byte{
			8, 0, 0, 0, 0, 0, 0, 0, 0, 4, 0, 0, 0, 0, 0, 0, 0, 6, 0, 0,
		}
		for i, w := range want {
			if staticDictHashLengths[i] != w {
				t.Errorf("staticDictHashLengths[%d] = %d, want %d", i, staticDictHashLengths[i], w)
			}
		}
	})

	// Every non-zero length must be in [4,24].
	t.Run("LengthRange", func(t *testing.T) {
		for i, l := range staticDictHashLengths {
			if l != 0 && (l < 4 || l > 24) {
				t.Errorf("staticDictHashLengths[%d] = %d, out of range [4,24]", i, l)
			}
		}
	})

	// Non-zero length implies non-zero word index consistency: word index
	// must be within the valid range for that length.
	t.Run("WordIndexRange", func(t *testing.T) {
		for i, l := range staticDictHashLengths {
			if l == 0 {
				continue
			}
			maxIdx := uint16(1) << dictSizeBitsByLength[l]
			if staticDictHashWords[i] >= maxIdx {
				t.Errorf("bucket %d: word index %d >= max %d for length %d",
					i, staticDictHashWords[i], maxIdx, l)
			}
		}
	})

	t.Run("FirstByteConsistency", func(t *testing.T) {
		for i, l := range staticDictHashLengths {
			if l == 0 {
				continue
			}
			offset := uint(dictOffsetsByLength[l]) + uint(l)*uint(staticDictHashWords[i])
			want := dictData[offset]
			if staticDictHashFirstBytes[i] != want {
				t.Errorf("bucket %d: first byte %d, want %d (len=%d, word=%d)",
					i, staticDictHashFirstBytes[i], want, l, staticDictHashWords[i])
			}
		}
	})
}
