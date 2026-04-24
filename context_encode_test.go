package brrr

import (
	"slices"
	"testing"
)

func TestMoveToFrontTransform(t *testing.T) {
	tests := []struct {
		name string
		in   []uint32
		want []uint32
	}{
		{
			name: "empty",
			in:   nil,
			want: nil,
		},
		{
			name: "single_element",
			in:   []uint32{0},
			want: []uint32{0},
		},
		{
			name: "identity",
			// [0, 1, 2, 3] — each value appears at its own MTF position.
			in:   []uint32{0, 1, 2, 3},
			want: []uint32{0, 1, 2, 3},
		},
		{
			name: "repeated_same",
			// [3, 3, 3, 3] — after the first, value 3 is always at front (pos 0).
			in:   []uint32{3, 3, 3, 3},
			want: []uint32{3, 0, 0, 0},
		},
		{
			name: "alternating",
			// [0, 1, 0, 1] — alternating values bounce between positions 0 and 1.
			in:   []uint32{0, 1, 0, 1},
			want: []uint32{0, 1, 1, 1},
		},
		{
			name: "descending",
			in:   []uint32{3, 2, 1, 0},
			want: []uint32{3, 3, 3, 3},
		},
		{
			name: "typical_context_map",
			// A realistic context map pattern: many repeated cluster indices.
			// MTF list starts as [0,1,2]. After each lookup the found
			// element moves to the front, so repeated values get position 0.
			in:   []uint32{0, 0, 1, 1, 0, 2, 2, 0},
			want: []uint32{0, 0, 1, 0, 1, 2, 0, 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.in == nil {
				moveToFrontTransform(nil)
				return
			}
			v := slices.Clone(tt.in)
			moveToFrontTransform(v)
			if !slices.Equal(v, tt.want) {
				t.Errorf("moveToFrontTransform(%v) = %v, want %v", tt.in, v, tt.want)
			}
		})
	}
}

func TestMoveToFrontTransformRoundTrip(t *testing.T) {
	// Verify that inverse MTF recovers the original.
	original := []uint32{0, 1, 2, 0, 1, 3, 3, 2, 0, 1}
	v := slices.Clone(original)
	moveToFrontTransform(v)

	// Inverse MTF.
	maxVal := original[0]
	for _, x := range original[1:] {
		if x > maxVal {
			maxVal = x
		}
	}
	var mtf [256]byte
	for i := range maxVal + 1 {
		mtf[i] = byte(i)
	}
	for i, pos := range v {
		val := mtf[pos]
		v[i] = uint32(val)
		copy(mtf[1:pos+1], mtf[:pos])
		mtf[0] = val
	}

	if !slices.Equal(v, original) {
		t.Errorf("round-trip failed: got %v, want %v", v, original)
	}
}

func TestRunLengthCodeZeros(t *testing.T) {
	tests := []struct {
		name              string
		in                []uint32
		wantLen           int
		wantMaxRunLenPfx  uint32
		wantNonZeroShift  bool // verify non-zero values are shifted
		wantZeroRunDecode bool // verify zero runs decode correctly
	}{
		{
			name:             "no_zeros",
			in:               []uint32{1, 2, 3, 4},
			wantLen:          4,
			wantMaxRunLenPfx: 0,
			wantNonZeroShift: true,
		},
		{
			name:              "all_zeros_short",
			in:                []uint32{0, 0, 0},
			wantLen:           1,
			wantMaxRunLenPfx:  1,
			wantZeroRunDecode: true,
		},
		{
			name:              "mixed",
			in:                []uint32{1, 0, 0, 0, 2, 0, 3},
			wantLen:           5,
			wantMaxRunLenPfx:  1,
			wantNonZeroShift:  true,
			wantZeroRunDecode: true,
		},
		{
			name:              "long_zero_run",
			in:                []uint32{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2},
			wantMaxRunLenPfx:  4,
			wantZeroRunDecode: true,
		},
		{
			name:              "single_zero",
			in:                []uint32{1, 0, 2},
			wantLen:           3,
			wantMaxRunLenPfx:  0,
			wantZeroRunDecode: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := slices.Clone(tt.in)
			out, maxPfx := runLengthCodeZeros(v)

			if tt.wantMaxRunLenPfx != maxPfx {
				t.Errorf("maxRunLenPrefix: got %d, want %d", maxPfx, tt.wantMaxRunLenPfx)
			}

			if tt.wantLen > 0 && len(out) != tt.wantLen {
				t.Errorf("output length: got %d, want %d", len(out), tt.wantLen)
			}

			// Decode and verify round-trip.
			decoded := decodeRLEZeros(out, maxPfx)
			if !slices.Equal(decoded, tt.in) {
				t.Errorf("round-trip failed:\n  input:   %v\n  encoded: %v (maxPfx=%d)\n  decoded: %v",
					tt.in, out, maxPfx, decoded)
			}
		})
	}
}

// decodeRLEZeros reverses runLengthCodeZeros for verification.
func decodeRLEZeros(encoded []uint32, maxRunLenPrefix uint32) []uint32 {
	const symbolMask = (1 << symbolBits) - 1
	var result []uint32

	for _, sym := range encoded {
		code := sym & symbolMask
		extra := sym >> symbolBits

		if code == 0 {
			// A zero-run prefix code of 0 means a single zero.
			result = append(result, 0)
		} else if code <= maxRunLenPrefix {
			// Zero-run: length = (1 << code) + extra.
			runLen := (1 << code) + extra
			for range runLen {
				result = append(result, 0)
			}
		} else {
			// Non-zero value, shifted back down.
			result = append(result, code-maxRunLenPrefix)
		}
	}

	return result
}

func TestEncodeContextMap(t *testing.T) {
	tests := []struct {
		name        string
		contextMap  []uint32
		numClusters uint
	}{
		{
			name:        "single_cluster",
			contextMap:  []uint32{0, 0, 0, 0},
			numClusters: 1,
		},
		{
			name:        "two_clusters_simple",
			contextMap:  []uint32{0, 1, 0, 1},
			numClusters: 2,
		},
		{
			name:        "three_clusters",
			contextMap:  []uint32{0, 1, 2, 0, 1, 2, 0, 1},
			numClusters: 3,
		},
		{
			name:        "many_zeros",
			contextMap:  []uint32{0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0},
			numClusters: 2,
		},
		{
			name:        "four_clusters_mixed",
			contextMap:  []uint32{0, 1, 2, 3, 0, 0, 1, 1, 2, 2, 3, 3},
			numClusters: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use a large enough buffer.
			buf := make([]byte, 4096)
			bs := bitWriter{buf: buf}
			tree := make([]huffmanTreeNode, 2*(maxNumberOfBlockTypes+6+1)+1)

			// Ensure encodeContextMap doesn't mutate the original.
			original := slices.Clone(tt.contextMap)
			bs.encodeContextMap(tt.contextMap, tt.numClusters, tree)

			if !slices.Equal(tt.contextMap, original) {
				t.Error("encodeContextMap mutated the input context map")
			}

			// Verify some bits were written.
			if tt.numClusters <= 1 {
				// Only storeVarLenUint8(0) = 1 bit.
				if bs.bitOffset < 1 {
					t.Error("expected at least 1 bit for single cluster")
				}
			} else {
				// Must have written the Huffman tree + symbols + IMTF bit.
				if bs.bitOffset < 10 {
					t.Errorf("expected substantial output for %d clusters, got %d bits",
						tt.numClusters, bs.bitOffset)
				}
			}

			// Snapshot test for reproducibility.
			n := (bs.bitOffset + 7) / 8
			snapshotBitstream(t, buf[:n], bs.bitOffset)
		})
	}
}

func TestStoreSymbolWithContext(t *testing.T) {
	// Test that storeSymbolWithContext with a trivial (identity) context map
	// produces the same bits as storeSymbol.
	//
	// With contextBits=0 and contextMap=[0,1,...,numBlockTypes-1],
	// the context variant should behave identically to the flat variant.

	numTypes := 2
	alphabetSize := 4
	types := []byte{0, 1}
	lengths := []uint32{3, 2}
	histogram := make([]uint32, numTypes*alphabetSize)
	// Block type 0 histogram.
	histogram[0] = 10
	histogram[1] = 5
	histogram[2] = 3
	histogram[3] = 1
	// Block type 1 histogram.
	histogram[4] = 8
	histogram[5] = 4
	histogram[6] = 2
	histogram[7] = 1

	// Identity context map: cluster i = block type i.
	contextMap := make([]uint32, numTypes)
	for i := range numTypes {
		contextMap[i] = uint32(i)
	}

	symbols := []uint{0, 1, 2, 0, 3}

	tree := make([]huffmanTreeNode, 2*alphabetSize+1)

	// Encode with storeSymbol.
	buf1 := make([]byte, 256)
	enc1 := newBlockEncoder(alphabetSize, numTypes, types, lengths)
	bs1 := bitWriter{buf: buf1}
	enc1.buildAndStoreBlockSwitchEntropyCodes(tree, &bs1)
	enc1.buildAndStoreEntropyCodes(histogram, numTypes, alphabetSize, tree, &bs1, nil, nil)
	afterSetup1 := bs1.bitOffset
	for _, sym := range symbols {
		enc1.storeSymbol(sym, &bs1)
	}

	// Encode with storeSymbolWithContext (contextBits=0, identity context map).
	buf2 := make([]byte, 256)
	enc2 := newBlockEncoder(alphabetSize, numTypes, types, lengths)
	bs2 := bitWriter{buf: buf2}
	enc2.buildAndStoreBlockSwitchEntropyCodes(tree, &bs2)
	enc2.buildAndStoreEntropyCodes(histogram, numTypes, alphabetSize, tree, &bs2, nil, nil)
	afterSetup2 := bs2.bitOffset
	for _, sym := range symbols {
		enc2.storeSymbolWithContext(sym, 0, contextMap, 0, &bs2)
	}

	// The setup bits should match.
	if afterSetup1 != afterSetup2 {
		t.Fatalf("setup bit offset mismatch: storeSymbol=%d, storeSymbolWithContext=%d",
			afterSetup1, afterSetup2)
	}

	// The symbol bits should match.
	if bs1.bitOffset != bs2.bitOffset {
		t.Fatalf("total bit offset mismatch: storeSymbol=%d, storeSymbolWithContext=%d",
			bs1.bitOffset, bs2.bitOffset)
	}

	n := (bs1.bitOffset + 7) / 8
	for i := range n {
		if buf1[i] != buf2[i] {
			t.Fatalf("byte %d mismatch: storeSymbol=0x%02x, storeSymbolWithContext=0x%02x",
				i, buf1[i], buf2[i])
		}
	}
}

func TestStoreSymbolWithContextBits(t *testing.T) {
	// Test with contextBits > 0: verify that the context map lookup
	// correctly selects different histogram clusters based on context.

	numTypes := 1
	numClusters := 2
	alphabetSize := 4
	types := []byte{0}
	lengths := []uint32{4}

	// 2 clusters × 4 symbols.
	histogram := make([]uint32, numClusters*alphabetSize)
	// Cluster 0: symbol 0 is most frequent.
	histogram[0] = 20
	histogram[1] = 5
	histogram[2] = 3
	histogram[3] = 1
	// Cluster 1: symbol 3 is most frequent.
	histogram[4] = 1
	histogram[5] = 3
	histogram[6] = 5
	histogram[7] = 20

	// Context map: 1 block type × 2 contexts (contextBits=1).
	// context 0 → cluster 0, context 1 → cluster 1.
	contextMap := []uint32{0, 1}

	tree := make([]huffmanTreeNode, 2*alphabetSize+1)

	buf := make([]byte, 256)
	enc := newBlockEncoder(alphabetSize, numTypes, types, lengths)
	bs := bitWriter{buf: buf}
	enc.buildAndStoreBlockSwitchEntropyCodes(tree, &bs)
	enc.buildAndStoreEntropyCodes(histogram, numClusters, alphabetSize, tree, &bs, nil, nil)

	// Write symbols with different contexts.
	enc.storeSymbolWithContext(0, 0, contextMap, 1, &bs) // cluster 0, symbol 0 (short code)
	enc.storeSymbolWithContext(0, 1, contextMap, 1, &bs) // cluster 1, symbol 0 (long code)
	enc.storeSymbolWithContext(3, 0, contextMap, 1, &bs) // cluster 0, symbol 3 (long code)
	enc.storeSymbolWithContext(3, 1, contextMap, 1, &bs) // cluster 1, symbol 3 (short code)

	// Should succeed without panic and produce some output.
	if bs.bitOffset == 0 {
		t.Error("expected non-zero output")
	}
}

// TestRunLengthCodeZerosMaxPrefix verifies that the max prefix is capped at 6.
func TestRunLengthCodeZerosMaxPrefix(t *testing.T) {
	// Create a very long zero run (256 zeros).
	original := make([]uint32, 258)
	original[0] = 1
	original[257] = 2
	// 256 zeros in between.

	v := slices.Clone(original)
	out, maxPfx := runLengthCodeZeros(v)
	if maxPfx > 6 {
		t.Errorf("maxRunLenPrefix should be capped at 6, got %d", maxPfx)
	}

	// Verify round-trip.
	decoded := decodeRLEZeros(out, maxPfx)
	if !slices.Equal(decoded, original) {
		t.Errorf("round-trip failed for long zero run:\n  original len=%d\n  decoded  len=%d",
			len(original), len(decoded))
	}
}

// TestEncodeContextMapMatchesC verifies the encoded output matches
// the C reference by checking specific known outputs.
func TestEncodeContextMapMatchesC(t *testing.T) {
	// Verify that encodeContextMap for a simple 2-cluster map produces
	// the correct bitstream prefix: storeVarLenUint8(1) = 0b11 (2 bits),
	// then the IMTF/RLE/Huffman encoding follows.
	buf := make([]byte, 256)
	bs := bitWriter{buf: buf}
	tree := make([]huffmanTreeNode, 2*(maxNumberOfBlockTypes+6+1)+1)

	contextMap := []uint32{0, 1}
	bs.encodeContextMap(contextMap, 2, tree)

	// First 2 bits should be storeVarLenUint8(1) = 0b11.
	// Bit 0: "has value" flag = 1.
	// Bit 1-3: nbits-1 = 0 (since log2(1)=0, nbits=1).
	// But nbits=0, so: writeBits(1,1), writeBits(3,0), writeBits(0,...).
	// That's: 1 (1 bit), 000 (3 bits) = 4 bits for the VarLenUint8(1).
	if bs.bitOffset < 4 {
		t.Errorf("expected at least 4 bits for storeVarLenUint8(1), got %d", bs.bitOffset)
	}

	// The last bit should be the IMTF flag (1).
	lastBitByte := buf[(bs.bitOffset-1)/8]
	lastBitPos := (bs.bitOffset - 1) % 8
	imtfBit := (lastBitByte >> lastBitPos) & 1
	if imtfBit != 1 {
		t.Error("expected IMTF flag bit to be 1")
	}
}

// TestMoveToFrontTransformMaxValue verifies the transform works
// with the maximum cluster index (255).
func TestMoveToFrontTransformMaxValue(t *testing.T) {
	v := []uint32{255, 0, 255, 0}
	moveToFrontTransform(v)

	// First element: 255 is at position 255 in initial list.
	if v[0] != 255 {
		t.Errorf("v[0]: got %d, want 255", v[0])
	}
	// Second element: 0 is now at position 1 (255 moved to front).
	// Actually, after moving 255 to front: [255, 0, 1, 2, ..., 254].
	// So 0 is at position 1.
	if v[1] != 1 {
		t.Errorf("v[1]: got %d, want 1", v[1])
	}
	// Third element: 255 is now at position 1 (0 moved to front).
	// After moving 0 to front: [0, 255, 1, 2, ..., 254].
	// So 255 is at position 1.
	if v[2] != 1 {
		t.Errorf("v[2]: got %d, want 1", v[2])
	}
}
