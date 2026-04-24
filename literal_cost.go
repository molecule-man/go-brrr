// Literal bit-cost estimation for Zopfli optimal parsing.
//
// Estimates the per-byte cost (in bits) of encoding each literal in a
// byte stream, using a sliding-window frequency model. The cost model
// guides the Zopfli DP toward commands that compress well.
//
// Two strategies are used depending on the input:
//   - UTF-8 data: a 3-histogram model (one per UTF-8 byte position)
//     captures the strong byte-position correlations in multi-byte encodings.
//   - Non-UTF-8 data: a single 256-entry histogram with a sliding window.

package brrr

// estimateBitCostsForLiterals estimates the Shannon bit-cost for each literal
// byte in data[position..position+numBytes), writing results to
// cost[0..numBytes). The costs are used by the Zopfli cost model to decide
// whether a literal or a backward reference is cheaper.
//
// The histogram parameter is scratch space: 3*256 entries for the UTF-8
// path, or 256 entries for the non-UTF-8 path.
func estimateBitCostsForLiterals(data []byte, position, numBytes, ringBufferMask uint, histogram []uint, cost []float32) {
	if isMostlyUTF8(data, position, ringBufferMask, numBytes, minUTF8Ratio) {
		estimateBitCostsForLiteralsUTF8(data, position, numBytes, ringBufferMask, histogram, cost)
	} else {
		estimateBitCostsForLiteralsRaw(data, position, numBytes, ringBufferMask, histogram, cost)
	}
}

// utf8Position returns the byte position within a UTF-8 multi-byte sequence
// (0 = start/ASCII, 1 = byte 2, 2 = byte 3), clamped to clamp.
func utf8Position(last, c, clamp uint) uint {
	if c < 128 {
		return 0
	}
	if c >= 192 {
		return min(1, clamp)
	}
	// Continuation byte: check previous byte to decide.
	if last < 0xE0 {
		return 0
	}
	return min(2, clamp)
}

// decideMultiByteStatsLevel determines whether to use 1-histogram (ASCII),
// 2-histogram (2-byte UTF-8), or 3-histogram (3-byte UTF-8) modeling.
func decideMultiByteStatsLevel(data []byte, pos, length, mask uint) uint {
	var counts [3]uint
	maxUTF8 := uint(1)
	lastC := uint(0)
	for i := range length {
		c := uint(data[(pos+i)&mask])
		counts[utf8Position(lastC, c, 2)]++
		lastC = c
	}
	if counts[2] < 500 {
		maxUTF8 = 1
	}
	if counts[1]+counts[2] < 25 {
		maxUTF8 = 0
	}
	return maxUTF8
}

// estimateBitCostsForLiteralsUTF8 estimates per-byte costs using a
// sliding-window UTF-8-aware frequency model with up to 3 histograms
// (one per byte position in a multi-byte sequence).
func estimateBitCostsForLiteralsUTF8(data []byte, pos, length, mask uint, histogram []uint, cost []float32) {
	maxUTF8 := decideMultiByteStatsLevel(data, pos, length, mask)
	windowHalf := uint(495)
	inWindow := min(windowHalf, length)
	var inWindowUTF8 [3]uint

	// Clear histograms: 3 * 256 entries.
	for i := range histogram[:3*256] {
		histogram[i] = 0
	}

	// Bootstrap histograms from the initial window.
	lastC := uint(0)
	utf8Pos := uint(0)
	for i := range inWindow {
		c := uint(data[(pos+i)&mask])
		histogram[256*utf8Pos+c]++
		inWindowUTF8[utf8Pos]++
		utf8Pos = utf8Position(lastC, c, maxUTF8)
		lastC = c
	}

	// Compute bit costs with sliding window.
	for i := range length {
		if i >= windowHalf {
			// Remove a byte in the past.
			var c, lc uint
			if i >= windowHalf+1 {
				c = uint(data[(pos+i-windowHalf-1)&mask])
			}
			if i >= windowHalf+2 {
				lc = uint(data[(pos+i-windowHalf-2)&mask])
			}
			utf8Pos2 := utf8Position(lc, c, maxUTF8)
			histogram[256*utf8Pos2+uint(data[(pos+i-windowHalf)&mask])]--
			inWindowUTF8[utf8Pos2]--
		}
		if i+windowHalf < length {
			// Add a byte in the future.
			c := uint(data[(pos+i+windowHalf-1)&mask])
			lc := uint(data[(pos+i+windowHalf-2)&mask])
			utf8Pos2 := utf8Position(lc, c, maxUTF8)
			histogram[256*utf8Pos2+uint(data[(pos+i+windowHalf)&mask])]++
			inWindowUTF8[utf8Pos2]++
		}

		var c uint
		if i >= 1 {
			c = uint(data[(pos+i-1)&mask])
		}
		var lc uint
		if i >= 2 {
			lc = uint(data[(pos+i-2)&mask])
		}
		curUTF8Pos := utf8Position(lc, c, maxUTF8)
		maskedPos := (pos + i) & mask
		histo := histogram[256*curUTF8Pos+uint(data[maskedPos])]
		if histo == 0 {
			histo = 1
		}
		litCost := fastLog2(int(inWindowUTF8[curUTF8Pos])) - fastLog2(int(histo))
		litCost += 0.02905
		if litCost < 1.0 {
			litCost = litCost*0.5 + 0.5
		}
		// Make the first bytes more expensive to account for the statistical
		// anomaly at the beginning of the data.
		const prologueLength = 2000
		const multiplier = 0.35 / prologueLength
		if i < prologueLength {
			litCost += 0.35 + multiplier*float64(i)
		}
		cost[i] = float32(litCost)
	}
}

// estimateBitCostsForLiteralsRaw estimates per-byte costs using a single
// 256-entry sliding-window frequency model for non-UTF-8 data.
func estimateBitCostsForLiteralsRaw(data []byte, pos, length, mask uint, histogram []uint, cost []float32) {
	windowHalf := uint(2000)
	inWindow := min(windowHalf, length)

	for i := range histogram[:256] {
		histogram[i] = 0
	}

	// Bootstrap histogram.
	for i := range inWindow {
		histogram[data[(pos+i)&mask]]++
	}

	// Compute bit costs with sliding window.
	for i := range length {
		if i >= windowHalf {
			histogram[data[(pos+i-windowHalf)&mask]]--
			inWindow--
		}
		if i+windowHalf < length {
			histogram[data[(pos+i+windowHalf)&mask]]++
			inWindow++
		}
		histo := histogram[data[(pos+i)&mask]]
		if histo == 0 {
			histo = 1
		}
		litCost := fastLog2(int(inWindow)) - fastLog2(int(histo))
		litCost += 0.029
		if litCost < 1.0 {
			litCost = litCost*0.5 + 0.5
		}
		cost[i] = float32(litCost)
	}
}

// isMostlyUTF8 returns true if at least minFraction of the data bytes form
// valid UTF-8 sequences.
func isMostlyUTF8(data []byte, pos, mask, length uint, minFraction float64) bool {
	sizeUTF8 := uint(0)
	i := uint(0)
	for i < length {
		bytesRead, isUTF8 := parseAsUTF8(data, (pos+i)&mask, length-i, mask)
		i += bytesRead
		if isUTF8 {
			sizeUTF8 += bytesRead
		}
	}
	return float64(sizeUTF8) > minFraction*float64(length)
}

// parseAsUTF8 attempts to parse a UTF-8 sequence starting at data[pos].
// Returns the number of bytes consumed and whether it was valid UTF-8.
func parseAsUTF8(data []byte, pos, remaining, mask uint) (bytesRead uint, valid bool) {
	b0 := data[pos]

	// ASCII
	if b0&0x80 == 0 {
		if b0 > 0 {
			return 1, true
		}
	}

	// 2-byte UTF-8
	if remaining > 1 {
		b1 := data[(pos+1)&mask]
		if b0&0xE0 == 0xC0 && b1&0xC0 == 0x80 {
			symbol := (uint(b0&0x1F) << 6) | uint(b1&0x3F)
			if symbol > 0x7F {
				return 2, true
			}
		}
	}

	// 3-byte UTF-8
	if remaining > 2 {
		b1 := data[(pos+1)&mask]
		b2 := data[(pos+2)&mask]
		if b0&0xF0 == 0xE0 && b1&0xC0 == 0x80 && b2&0xC0 == 0x80 {
			symbol := (uint(b0&0x0F) << 12) | (uint(b1&0x3F) << 6) | uint(b2&0x3F)
			if symbol > 0x7FF {
				return 3, true
			}
		}
	}

	// 4-byte UTF-8
	if remaining > 3 {
		b1 := data[(pos+1)&mask]
		b2 := data[(pos+2)&mask]
		b3 := data[(pos+3)&mask]
		if b0&0xF8 == 0xF0 && b1&0xC0 == 0x80 && b2&0xC0 == 0x80 && b3&0xC0 == 0x80 {
			symbol := (uint(b0&0x07) << 18) | (uint(b1&0x3F) << 12) |
				(uint(b2&0x3F) << 6) | uint(b3&0x3F)
			if symbol > 0xFFFF && symbol <= 0x10FFFF {
				return 4, true
			}
		}
	}

	return 1, false
}
