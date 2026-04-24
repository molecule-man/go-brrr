package brrr

import "testing"

// Verify prefix/suffix strings match the C reference (total content bytes and
// spot checks against kPrefixSuffix + kPrefixSuffixMap).
func TestTransformPrefixSuffix(t *testing.T) {
	totalBytes := 0
	for i, s := range transformPrefixSuffix {
		totalBytes += len(s)
		if len(s) > 255 {
			t.Errorf("entry %d length %d exceeds byte", i, len(s))
		}
	}
	// C blob kPrefixSuffix[217] = 50 length-prefix bytes + 167 content bytes.
	const wantTotalContentBytes = 167
	if totalBytes != wantTotalContentBytes {
		t.Errorf("total prefix/suffix content bytes = %d, want %d", totalBytes, wantTotalContentBytes)
	}

	// Spot-check entries against C reference.
	checks := []struct {
		idx  int
		want string
	}{
		{0, " "},
		{1, ", "},
		{2, " of the "},
		{5, "."},
		{8, "\""},
		{11, "\n"},
		{21, "("},
		{22, ". The "},
		{27, "\n\t"},
		{35, ".com/"},
		{45, "\xc2\xa0"},
		{47, " the "},
		{49, ""},
	}
	for _, c := range checks {
		if got := transformPrefixSuffix[c.idx]; got != c.want {
			t.Errorf("transformPrefixSuffix[%d] = %q, want %q", c.idx, got, c.want)
		}
	}
}

// Verify each cutoff index references an ["", omit-last-N, ""] transform.
func TestTransformCutOffs(t *testing.T) {
	for n, idx := range transformCutOffs {
		if idx < 0 {
			continue
		}
		i := int(idx)
		prefix := transformTriplets[i*3]
		ttype := transformTriplets[i*3+1]
		suffix := transformTriplets[i*3+2]

		// Prefix and suffix must be entry 49 (empty string).
		if prefix != 49 || suffix != 49 {
			t.Errorf("cutoff[%d] = transform %d: prefix_id=%d suffix_id=%d, want 49/49",
				n, idx, prefix, suffix)
		}

		// Transform type: identity for n=0, omit-last-N for n>0.
		var wantType byte
		if n == 0 {
			wantType = transformIdentity
		} else {
			wantType = byte(n) // transformOmitLast1..9 == 1..9
		}
		if ttype != wantType {
			t.Errorf("cutoff[%d] = transform %d: type=%d, want %d", n, idx, ttype, wantType)
		}
	}
}

// Test vectors computed from the C reference (brotli-ref/c/common/transform.c).
func TestTransformDictionaryWord(t *testing.T) {
	tests := []struct {
		word         string
		transformIdx int
		want         string
	}{
		// Identity variants.
		{"time", 0, "time"},               // "" + word + ""
		{"time", 1, "time "},              // "" + word + " "
		{"time", 2, " time "},             // " " + word + " "
		{"time", 5, "time the "},          // "" + word + " the "
		{"time", 6, " time"},              // " " + word + ""
		{"time", 7, "s time "},            // "s " + word + " "
		{"time", 8, "time of "},           // "" + word + " of "
		{"time", 13, ", time "},           // ", " + word + " "
		{"time", 18, "e time "},           // "e " + word + " "
		{"time", 33, " time, "},           // " " + word + ", "
		{"time", 41, " the time"},         // " the " + word + ""
		{"time", 72, ".com/time"},         // ".com/" + word + ""
		{"time", 73, " the time of the "}, // " the " + word + " of the "
		{"time", 102, "\xc2\xa0time"},     // U+00A0 + word + ""

		// OmitLast variants.
		{"time", 12, "tim"},     // omit_last_1
		{"time", 27, "ti"},      // omit_last_2
		{"time", 23, "t"},       // omit_last_3
		{"time", 42, ""},        // omit_last_4: whole word omitted
		{"time", 63, ""},        // omit_last_5: clamp to 0
		{"time", 48, ""},        // omit_last_7: clamp to 0
		{"time", 49, "timing "}, // omit_last_1 + "ing " suffix
		{"longer", 12, "longe"}, // omit_last_1 on 6-byte word
		{"longer", 23, "lon"},   // omit_last_3

		// OmitFirst variants.
		{"time", 3, "ime"},     // omit_first_1
		{"time", 11, "me"},     // omit_first_2
		{"time", 26, "e"},      // omit_first_3
		{"time", 34, ""},       // omit_first_4: whole word omitted
		{"time", 39, ""},       // omit_first_5: skip > len
		{"time", 54, ""},       // omit_first_9: skip > len
		{"longer", 3, "onger"}, // omit_first_1 on 6-byte word
		{"longer", 26, "ger"},  // omit_first_3

		// UppercaseFirst variants.
		{"time", 4, "Time "},   // ucFirst + " " suffix
		{"time", 9, "Time"},    // ucFirst + "" suffix
		{"time", 15, " Time "}, // " " prefix + ucFirst + " "
		{"time", 58, "Time, "}, // ucFirst + ", "
		{"time", 66, "Time\""}, // ucFirst + "\""
		{"time", 79, "Time."},  // ucFirst + "."
		{"time", 99, "Time,"},  // ucFirst + ","
		{"Time", 4, "Time "},   // already uppercase
		{"123x", 9, "123x"},    // non-alpha first byte

		// UppercaseAll variants.
		{"time", 44, "TIME"},    // ucAll + ""
		{"time", 68, "TIME "},   // ucAll + " "
		{"time", 83, " TIME "},  // " " + ucAll + " "
		{"time", 85, " TIME"},   // " " + ucAll + ""
		{"time", 87, "TIME\""},  // ucAll + "\""
		{"time", 107, "TIME, "}, // ucAll + ", "
		{"time", 112, "TIME,"},  // ucAll + ","
		{"abc", 44, "ABC"},      // 3-byte word

		// Short/edge-case words.
		{"a", 0, "a"},  // 1-byte word, identity
		{"a", 12, ""},  // 1-byte word, omit_last_1
		{"a", 3, ""},   // 1-byte word, omit_first_1
		{"a", 9, "A"},  // 1-byte word, ucFirst
		{"a", 44, "A"}, // 1-byte word, ucAll
	}

	var buf [256]byte
	for _, tt := range tests {
		n := transformDictionaryWord(buf[:], tt.word, tt.transformIdx)
		got := string(buf[:n])
		if got != tt.want {
			t.Errorf("transformDictionaryWord(%q, %d) = %q, want %q",
				tt.word, tt.transformIdx, got, tt.want)
		}
	}
}

func TestToUpperCase(t *testing.T) {
	tests := []struct {
		in       []byte
		wantByte []byte
		wantStep int
	}{
		// ASCII lowercase → uppercase.
		{[]byte("abc"), []byte("Abc"), 1},
		{[]byte("z"), []byte("Z"), 1},
		// ASCII uppercase → unchanged.
		{[]byte("A"), []byte("A"), 1},
		// ASCII non-alpha → unchanged.
		{[]byte("1"), []byte("1"), 1},
		{[]byte(" "), []byte(" "), 1},
		// 2-byte UTF-8: flip bit 5 of second byte.
		{[]byte{0xC3, 0xA9}, []byte{0xC3, 0x89}, 2}, // é → É
		// 3-byte UTF-8: XOR third byte with 5.
		{[]byte{0xE4, 0xB8, 0xAD}, []byte{0xE4, 0xB8, 0xA8}, 3},
	}

	for _, tt := range tests {
		buf := make([]byte, len(tt.in))
		copy(buf, tt.in)
		step := toUpperCase(buf)
		if step != tt.wantStep {
			t.Errorf("toUpperCase(%x) step = %d, want %d", tt.in, step, tt.wantStep)
		}
		for i, b := range tt.wantByte {
			if buf[i] != b {
				t.Errorf("toUpperCase(%x) result = %x, want %x", tt.in, buf[:len(tt.wantByte)], tt.wantByte)
				break
			}
		}
	}
}

func TestShiftUTF8(t *testing.T) {
	tests := []struct {
		in       []byte
		param    uint16
		wantByte []byte
		wantStep int
	}{
		// ASCII: shift 'A' (0x41) by +1 → 'B' (0x42).
		{[]byte("A"), 1, []byte("B"), 1},
		// ASCII: shift 'a' (0x61) by +0 → 'a'.
		{[]byte("a"), 0, []byte("a"), 1},
		// Continuation byte: no modification, step=1.
		{[]byte{0x80}, 1, []byte{0x80}, 1},
		// 2-byte: shift U+00E9 (é = C3 A9) by +1 → U+00EA (ê = C3 AA).
		{[]byte{0xC3, 0xA9}, 1, []byte{0xC3, 0xAA}, 2},
		// 2-byte: insufficient length → step=1.
		{[]byte{0xC3}, 1, []byte{0xC3}, 1},
		// 3-byte: shift U+4E2D (中 = E4 B8 AD) by +1 → U+4E2E (丮 = E4 B8 AE).
		{[]byte{0xE4, 0xB8, 0xAD}, 1, []byte{0xE4, 0xB8, 0xAE}, 3},
		// 3-byte: insufficient length (2 bytes) → step=2.
		{[]byte{0xE4, 0xB8}, 1, []byte{0xE4, 0xB8}, 2},
		// 4-byte: shift U+1F600 (😀 = F0 9F 98 80) by +1 → U+1F601 (😁 = F0 9F 98 81).
		{[]byte{0xF0, 0x9F, 0x98, 0x80}, 1, []byte{0xF0, 0x9F, 0x98, 0x81}, 4},
		// 4-byte: insufficient length (3 bytes) → step=3.
		{[]byte{0xF0, 0x9F, 0x98}, 1, []byte{0xF0, 0x9F, 0x98}, 3},
		// >=0xF8: invalid leading byte, no modification, step=1.
		{[]byte{0xF8}, 1, []byte{0xF8}, 1},
		// Negative shift via high bit: param=0x8001 means shift by -32767.
		// ASCII 'z' (0x7A) + (0x1000000 - 0x8000) + 0x0001 = large, masked to 7 bits.
		{[]byte("z"), 0x8001, []byte{byte((uint32(0x7A) + 0x1000000 - 0x8000 + 1) & 0x7F)}, 1},
	}

	for _, tt := range tests {
		buf := make([]byte, len(tt.in))
		copy(buf, tt.in)
		step := shiftUTF8(buf, len(buf), tt.param)
		if step != tt.wantStep {
			t.Errorf("shiftUTF8(%x, %d) step = %d, want %d", tt.in, tt.param, step, tt.wantStep)
		}
		for i, b := range tt.wantByte {
			if buf[i] != b {
				t.Errorf("shiftUTF8(%x, %d) result = %x, want %x", tt.in, tt.param, buf[:len(tt.wantByte)], tt.wantByte)
				break
			}
		}
	}
}
