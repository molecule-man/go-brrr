// Tests for the encoder core: wrapPosition, writeUncompressedMetaBlock,
// and writeMetaBlockInternal.

package brrr

import (
	"bytes"
	"testing"
)

func TestWrapPosition(t *testing.T) {
	tests := []struct {
		name string
		pos  uint64
		want uint
	}{
		{"zero", 0, 0},
		{"small", 42, 42},
		{"just_under_1GB", 1<<30 - 1, 1<<30 - 1},
		{"exactly_1GB", 1 << 30, 1 << 30},
		{"exactly_2GB", 2 << 30, 2 << 30},
		// First 3 GiB are continuous; wrapping starts at 3 GiB.
		{"exactly_3GB", 3 << 30, 1 << 30},
		{"3GB_plus_42", 3<<30 + 42, 1<<30 + 42},
		{"exactly_4GB", 4 << 30, 2 << 30},
		{"4GB_plus_99", 4<<30 + 99, 2<<30 + 99},
		// Wraps every 2 GiB: 5 GiB maps like 3 GiB.
		{"exactly_5GB", 5 << 30, 1 << 30},
		{"exactly_6GB", 6 << 30, 2 << 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapPosition(tt.pos)
			if got != tt.want {
				t.Errorf("wrapPosition(%d) = %d, want %d", tt.pos, got, tt.want)
			}
		})
	}
}

func TestWriteUncompressedMetaBlock(t *testing.T) {
	t.Run("linear", func(t *testing.T) {
		data := []byte("Hello, World!")
		buf := make([]byte, len(data)*2+64)
		e := encodeState{
			b:    bitWriter{buf: buf},
			data: data,
			mask: uint32(len(data)) - 1,
		}

		e.writeUncompressedMetaBlock(len(data), false)

		// Verify the data bytes appear in the output after the header.
		out := buf[:e.b.bitOffset/8]
		if !bytes.Contains(out, data) {
			t.Errorf("output does not contain original data")
		}
	})

	t.Run("ring_buffer_wrapping", func(t *testing.T) {
		// 16-byte ring buffer, position 14, length 5 → wraps around.
		ring := make([]byte, 16)
		ring[14] = 'A'
		ring[15] = 'B'
		ring[0] = 'C'
		ring[1] = 'D'
		ring[2] = 'E'

		buf := make([]byte, 128)
		e := encodeState{
			b:            bitWriter{buf: buf},
			data:         ring,
			mask:         15,
			lastFlushPos: 14,
		}

		e.writeUncompressedMetaBlock(5, false)

		out := buf[:e.b.bitOffset/8]
		if !bytes.Contains(out, []byte("ABCDE")) {
			t.Errorf("output should contain wrapped data ABCDE, got %x", out)
		}
	})

	t.Run("isLast_appends_empty_block", func(t *testing.T) {
		data := []byte("test")
		buf := make([]byte, 64)
		e := encodeState{
			b:    bitWriter{buf: buf},
			data: data,
			mask: 0xFF,
		}

		e.writeUncompressedMetaBlock(len(data), false)
		sizeWithout := e.b.bitOffset

		buf2 := make([]byte, 64)
		e2 := encodeState{
			b:    bitWriter{buf: buf2},
			data: data,
			mask: 0xFF,
		}
		e2.writeUncompressedMetaBlock(len(data), true)
		sizeWith := e2.b.bitOffset

		// isLast adds ISLAST(1) + ISEMPTY(1) + byte-alignment.
		if sizeWith <= sizeWithout {
			t.Errorf("isLast output (%d bits) should be larger than non-isLast (%d bits)",
				sizeWith, sizeWithout)
		}
	})

	t.Run("roundtrip", func(t *testing.T) {
		data := []byte("Hello, Brotli uncompressed block!")
		padded := make([]byte, 64)
		copy(padded, data)

		buf := make([]byte, 256)
		b := &bitWriter{buf: buf}
		// Stream header for lgwin=18.
		b.writeBits(4, 3)

		e := encodeState{
			b:    *b,
			data: padded,
			mask: 63,
		}
		e.writeUncompressedMetaBlock(len(data), true)

		compressed := buf[:(e.b.bitOffset+7)/8]
		decompressed := brotliDecompress(t, compressed)
		if !bytes.Equal(decompressed, data) {
			t.Errorf("roundtrip failed:\n  got  %q\n  want %q", decompressed, data)
		}
	})

	t.Run("roundtrip_wrapping", func(t *testing.T) {
		// Ring buffer with wrap-around.
		ring := make([]byte, 16)
		ring[12] = 'W'
		ring[13] = 'R'
		ring[14] = 'A'
		ring[15] = 'P'
		ring[0] = '!'

		buf := make([]byte, 256)
		b := &bitWriter{buf: buf}
		b.writeBits(4, 3) // lgwin=18

		e := encodeState{
			b:            *b,
			data:         ring,
			mask:         15,
			lastFlushPos: 12,
		}
		e.writeUncompressedMetaBlock(5, true)

		compressed := buf[:(e.b.bitOffset+7)/8]
		decompressed := brotliDecompress(t, compressed)
		if !bytes.Equal(decompressed, []byte("WRAP!")) {
			t.Errorf("roundtrip wrapping failed:\n  got  %q\n  want %q", decompressed, "WRAP!")
		}
	})
}

func TestWriteMetaBlockInternal(t *testing.T) {
	const distAlphabetSizeMax = 64 // NDIRECT=0, NPOSTFIX=0, MAXNBITS=24 → 16 + 2*24 = 64

	t.Run("empty_last", func(t *testing.T) {
		buf := make([]byte, 16)
		e := encoderArena{encoderCore: encoderCore{encodeState: encodeState{
			b:                   bitWriter{buf: buf},
			quality:             2,
			distAlphabetSizeMax: distAlphabetSizeMax,
			distCache:           [4]uint{4, 11, 15, 16},
			savedDistCache:      [4]uint{4, 11, 15, 16},
		}}}

		e.writeMetaBlockInternal(0, 0, 0, true)

		// ISLAST=1 + ISEMPTY=1 = 2 bits, value 0b11 = 3, then byte-aligned.
		if e.b.bitOffset != 8 {
			t.Errorf("bitOffset = %d, want 8", e.b.bitOffset)
		}
		if buf[0] != 0x03 {
			t.Errorf("buf[0] = %#02x, want 0x03", buf[0])
		}
	})

	t.Run("incompressible", func(t *testing.T) {
		// Cycling 0-255 data has near-uniform entropy, which shouldCompress rejects
		// for large enough inputs.
		const dataLen = 50000
		data := make([]byte, 1<<16) // 64K ring buffer
		for i := range data {
			data[i] = byte(i)
		}
		mask := uint32(len(data) - 1)

		bufSize := dataLen*2 + 1024
		buf := make([]byte, bufSize)
		e := encoderArena{encoderCore: encoderCore{encodeState: encodeState{
			b:                   bitWriter{buf: buf},
			quality:             2,
			distAlphabetSizeMax: distAlphabetSizeMax,
			data:                data,
			mask:                mask,
			commands:            []command{newInsertCommand(dataLen)},
			distCache:           [4]uint{4, 11, 15, 16},
			savedDistCache:      [4]uint{1, 2, 3, 4},
		}}}

		// Verify precondition: shouldCompress should return false.
		if e.shouldCompress(dataLen, dataLen, 1) {
			t.Skip("shouldCompress unexpectedly returned true for cycling data")
		}

		e.writeMetaBlockInternal(dataLen, dataLen, 1, false)

		// distCache must be restored from savedDistCache.
		if e.distCache != e.savedDistCache {
			t.Errorf("distCache not restored: got %v, want %v", e.distCache, e.savedDistCache)
		}

		// Output must be non-empty (uncompressed meta-block).
		if e.b.bitOffset == 0 {
			t.Error("expected non-empty output for incompressible path")
		}
	})

	t.Run("incompressible_roundtrip", func(t *testing.T) {
		const dataLen = 50000
		data := make([]byte, 1<<16)
		for i := range data {
			data[i] = byte(i)
		}
		mask := uint32(len(data) - 1)

		bufSize := dataLen*2 + 1024
		buf := make([]byte, bufSize)
		b := &bitWriter{buf: buf}
		b.writeBits(4, 3) // stream header lgwin=18

		dc := [4]uint{4, 11, 15, 16}
		e := encoderArena{encoderCore: encoderCore{encodeState: encodeState{
			b:                   *b,
			quality:             2,
			distAlphabetSizeMax: distAlphabetSizeMax,
			data:                data,
			mask:                mask,
			commands:            []command{newInsertCommand(dataLen)},
			distCache:           dc,
			savedDistCache:      dc,
		}}}

		if e.shouldCompress(dataLen, dataLen, 1) {
			t.Skip("shouldCompress unexpectedly returned true for cycling data")
		}

		e.writeMetaBlockInternal(dataLen, dataLen, 1, true)

		compressed := buf[:(e.b.bitOffset+7)/8]
		decompressed := brotliDecompress(t, compressed)
		if !bytes.Equal(decompressed, data[:dataLen]) {
			t.Errorf("roundtrip failed: got %d bytes, want %d", len(decompressed), dataLen)
		}
	})

	t.Run("compressible_roundtrip", func(t *testing.T) {
		// Highly compressible: repeated 'a' bytes.
		const dataLen = 1000
		data := make([]byte, 1024) // power-of-2 ring buffer
		for i := range data {
			data[i] = 'a'
		}

		bufSize := dataLen*2 + 1024
		buf := make([]byte, bufSize)
		b := &bitWriter{buf: buf}
		b.writeBits(4, 3) // stream header lgwin=18

		dc := [4]uint{4, 11, 15, 16}
		e := encoderArena{encoderCore: encoderCore{encodeState: encodeState{
			b:                   *b,
			quality:             2,
			distAlphabetSizeMax: distAlphabetSizeMax,
			data:                data,
			mask:                uint32(len(data) - 1),
			commands:            []command{newInsertCommand(dataLen)},
			distCache:           dc,
			savedDistCache:      dc,
		}}}

		e.writeMetaBlockInternal(dataLen, dataLen, 1, true)

		compressed := buf[:(e.b.bitOffset+7)/8]
		// Compression must actually reduce size.
		if len(compressed) >= dataLen {
			t.Errorf("compressed size %d should be less than original %d", len(compressed), dataLen)
		}

		decompressed := brotliDecompress(t, compressed)
		if !bytes.Equal(decompressed, bytes.Repeat([]byte{'a'}, dataLen)) {
			t.Errorf("roundtrip failed: got %d bytes, want %d", len(decompressed), dataLen)
		}
	})

	t.Run("nonzero_flush_pos", func(t *testing.T) {
		// Verify that lastFlushPos offsets the data window correctly.
		const dataLen = 500
		const flushPos = 512
		data := make([]byte, 2048)
		for i := range data {
			data[i] = 'z'
		}

		buf := make([]byte, dataLen*2+1024)
		b := &bitWriter{buf: buf}
		b.writeBits(4, 3) // stream header lgwin=18

		dc := [4]uint{4, 11, 15, 16}
		e := encoderArena{encoderCore: encoderCore{encodeState: encodeState{
			b:                   *b,
			quality:             2,
			distAlphabetSizeMax: distAlphabetSizeMax,
			data:                data,
			mask:                uint32(len(data) - 1),
			lastFlushPos:        flushPos,
			commands:            []command{newInsertCommand(dataLen)},
			distCache:           dc,
			savedDistCache:      dc,
		}}}

		e.writeMetaBlockInternal(dataLen, dataLen, 1, true)

		compressed := buf[:(e.b.bitOffset+7)/8]
		decompressed := brotliDecompress(t, compressed)
		if !bytes.Equal(decompressed, bytes.Repeat([]byte{'z'}, dataLen)) {
			t.Errorf("roundtrip failed: got %d bytes, want %d", len(decompressed), dataLen)
		}
	})

	t.Run("fallback_to_uncompressed", func(t *testing.T) {
		// 3 distinct bytes: shouldCompress returns true (low sample entropy),
		// but compression overhead exceeds data size, triggering fallback.
		data := make([]byte, 8) // small power-of-2 buffer
		data[0] = 0xAA
		data[1] = 0xBB
		data[2] = 0xCC
		mask := uint32(len(data) - 1)
		dataLen := 3

		buf := make([]byte, 1024)
		e := encoderArena{encoderCore: encoderCore{encodeState: encodeState{
			b:                   bitWriter{buf: buf},
			quality:             2,
			distAlphabetSizeMax: distAlphabetSizeMax,
			data:                data,
			mask:                mask,
			commands:            []command{newInsertCommand(uint(dataLen))},
			distCache:           [4]uint{4, 11, 15, 16},
			savedDistCache:      [4]uint{1, 2, 3, 4},
		}}}

		// Verify precondition: shouldCompress returns true.
		if !e.shouldCompress(dataLen, dataLen, 1) {
			t.Fatal("shouldCompress unexpectedly returned false for 3-byte data")
		}

		e.writeMetaBlockInternal(dataLen, dataLen, 1, false)

		// Fallback should have restored distCache.
		if e.distCache != e.savedDistCache {
			t.Errorf("distCache not restored on fallback: got %v, want %v", e.distCache, e.savedDistCache)
		}

		// Output should contain the original 3 bytes (uncompressed meta-block).
		out := buf[:e.b.bitOffset/8]
		if !bytes.Contains(out, data[:dataLen]) {
			t.Errorf("fallback output should contain original bytes %x, got %x", data[:dataLen], out)
		}
	})

	t.Run("fallback_roundtrip", func(t *testing.T) {
		data := make([]byte, 8)
		data[0] = 0xAA
		data[1] = 0xBB
		data[2] = 0xCC
		dataLen := 3

		buf := make([]byte, 1024)
		b := &bitWriter{buf: buf}
		b.writeBits(4, 3) // stream header lgwin=18

		dc := [4]uint{4, 11, 15, 16}
		e := encoderArena{encoderCore: encoderCore{encodeState: encodeState{
			b:                   *b,
			quality:             2,
			distAlphabetSizeMax: distAlphabetSizeMax,
			data:                data,
			mask:                uint32(len(data) - 1),
			commands:            []command{newInsertCommand(uint(dataLen))},
			distCache:           dc,
			savedDistCache:      dc,
		}}}

		e.writeMetaBlockInternal(dataLen, dataLen, 1, true)

		compressed := buf[:(e.b.bitOffset+7)/8]
		decompressed := brotliDecompress(t, compressed)
		if !bytes.Equal(decompressed, data[:dataLen]) {
			t.Errorf("roundtrip failed:\n  got  %x\n  want %x", decompressed, data[:dataLen])
		}
	})
}

func TestExtendLastCommand(t *testing.T) {
	// Table-driven cases for basic extension behavior.
	basicTests := []struct {
		name             string
		ringContent      string
		initCopyLen      uint
		distCode         uint
		lastProcessedPos uint64
		distCache0       uint
		inputBytes       uint32
		inputWrappedPos  uint32
		wantCopyLen      uint32
		wantBytes        uint32
		wantWrappedPos   uint32
	}{
		{
			name:             "full_extension",
			ringContent:      "ABCDABCD", // positions 4..7 match 0..3 at distance 4
			initCopyLen:      2,
			distCode:         numDistanceShortCodes - 1 + 4,
			lastProcessedPos: 6,
			distCache0:       4,
			inputBytes:       2,
			inputWrappedPos:  6,
			wantCopyLen:      4, // extended by 2
			wantBytes:        0,
			wantWrappedPos:   8,
		},
		{
			name:             "partial_mismatch",
			ringContent:      "ABCXABCY", // mismatch at position 7 ('Y' vs 'X')
			initCopyLen:      3,
			distCode:         numDistanceShortCodes - 1 + 4,
			lastProcessedPos: 7,
			distCache0:       4,
			inputBytes:       5,
			inputWrappedPos:  7,
			wantCopyLen:      3, // no extension
			wantBytes:        5,
			wantWrappedPos:   7,
		},
	}
	for _, tt := range basicTests {
		t.Run(tt.name, func(t *testing.T) {
			data := make([]byte, 256)
			copy(data, tt.ringContent)
			cmd := newCommand(commandConfig{
				insertLen:    0,
				copyLen:      tt.initCopyLen,
				distanceCode: tt.distCode,
			})
			e := encodeState{
				data:             data,
				mask:             0xFF,
				lgwin:            18,
				lastProcessedPos: tt.lastProcessedPos,
				distCache:        [4]uint{tt.distCache0, 11, 15, 16},
				commands:         []command{cmd},
				numCommands:      1,
			}
			gotBytes, gotPos := e.extendLastCommand(tt.inputBytes, tt.inputWrappedPos)
			if cl := e.commands[0].copyLength(); cl != tt.wantCopyLen {
				t.Errorf("copyLength = %d, want %d", cl, tt.wantCopyLen)
			}
			if gotBytes != tt.wantBytes {
				t.Errorf("bytesLeft = %d, want %d", gotBytes, tt.wantBytes)
			}
			if gotPos != tt.wantWrappedPos {
				t.Errorf("wrappedPos = %d, want %d", gotPos, tt.wantWrappedPos)
			}
		})
	}

	t.Run("distance_exceeds_max", func(t *testing.T) {
		// Distance larger than max backward distance → no extension.
		const lgwin = 10 // max backward = 1024 - 16 = 1008
		const mask = 0x7FF
		data := make([]byte, 2048)
		// Fill with matching bytes so extension would succeed if distance check passed.
		for i := range data {
			data[i] = 'A'
		}

		cmd := newCommand(commandConfig{
			insertLen:    0,
			copyLen:      2,
			distanceCode: numDistanceShortCodes - 1 + 1100, // distance 1100 > 1008
		})

		e := encodeState{
			data:             data,
			mask:             mask,
			lgwin:            lgwin,
			lastProcessedPos: 1200,
			distCache:        [4]uint{1100, 11, 15, 16},
			commands:         []command{cmd},
			numCommands:      1,
		}

		bytesLeft, wrappedPos := e.extendLastCommand(10, uint32(1200&mask))

		// No extension: distance 1100 > max backward 1008.
		if e.commands[0].copyLength() != 2 {
			t.Errorf("copyLength = %d, want 2", e.commands[0].copyLength())
		}
		if bytesLeft != 10 {
			t.Errorf("bytesLeft = %d, want 10", bytesLeft)
		}
		if wrappedPos != uint32(1200&mask) {
			t.Errorf("wrappedPos = %d, want %d", wrappedPos, 1200&mask)
		}
	})

	t.Run("short_distance_code", func(t *testing.T) {
		// Distance code 0 (last distance short code) triggers extension.
		const lgwin = 18
		const mask = 0xFF
		data := make([]byte, 256)
		copy(data[0:], "XYXYXY")

		// Short code 0 = last distance = distCache[0].
		cmd := newCommand(commandConfig{
			insertLen:    0,
			copyLen:      2,
			distanceCode: 0, // short code: last distance
		})

		e := encodeState{
			data:             data,
			mask:             mask,
			lgwin:            lgwin,
			lastProcessedPos: 4,
			distCache:        [4]uint{2, 11, 15, 16},
			commands:         []command{cmd},
			numCommands:      1,
		}

		bytesLeft, wrappedPos := e.extendLastCommand(2, 4)

		// 2 more bytes match at distance 2 ("XY" repeats).
		if e.commands[0].copyLength() != 4 {
			t.Errorf("copyLength = %d, want 4", e.commands[0].copyLength())
		}
		if bytesLeft != 0 {
			t.Errorf("bytesLeft = %d, want 0", bytesLeft)
		}
		if wrappedPos != 6 {
			t.Errorf("wrappedPos = %d, want 6", wrappedPos)
		}
	})

	t.Run("cmdPrefix_updated", func(t *testing.T) {
		// Verify cmdPrefix is recalculated after extension.
		const lgwin = 18
		const mask = 0xFF
		data := make([]byte, 256)
		for i := range data {
			data[i] = 'A'
		}

		cmd := newCommand(commandConfig{
			insertLen:    0,
			copyLen:      2,
			distanceCode: numDistanceShortCodes - 1 + 4,
		})
		originalPrefix := cmd.cmdPrefix

		e := encodeState{
			data:             data,
			mask:             mask,
			lgwin:            lgwin,
			lastProcessedPos: 6,
			distCache:        [4]uint{4, 11, 15, 16},
			commands:         []command{cmd},
			numCommands:      1,
		}

		e.extendLastCommand(50, 6)

		// Copy length increased, so cmdPrefix must differ from original.
		if e.commands[0].cmdPrefix == originalPrefix {
			t.Errorf("cmdPrefix unchanged after extension: %d", originalPrefix)
		}
	})
}
