package core

import (
	"math/bits"
	"testing"
)

// makeSymbolList builds a SymbolList and count histogram from per-symbol code
// lengths, mimicking how the brotli decoder populates these during
// ReadHuffmanCode.
func makeSymbolList(codeLengths []byte) (SymbolList, []uint16) {
	n := len(codeLengths)
	storage := make([]uint16, HuffmanMaxCodeLength+1+n)
	offset := HuffmanMaxCodeLength + 1

	// Initialize linked list heads to sentinel.
	for i := 0; i <= HuffmanMaxCodeLength; i++ {
		storage[i] = 0xFFFF
	}

	count := make([]uint16, HuffmanMaxCodeLength+1)
	nextSym := make([]int, HuffmanMaxCodeLength+1)
	for i := 0; i <= HuffmanMaxCodeLength; i++ {
		nextSym[i] = i - (HuffmanMaxCodeLength + 1)
	}

	for sym, cl := range codeLengths {
		if cl != 0 {
			storage[offset+nextSym[cl]] = uint16(sym)
			nextSym[cl] = sym
			count[cl]++
		}
	}

	return SymbolList{Storage: storage, Offset: offset}, count
}

// lookupHuffman decodes one symbol from the table given raw stream bits.
// Returns the decoded symbol and the number of bits consumed.
func lookupHuffman(table []HuffmanCode, rootBits int, streamBits uint32) (symbol uint16, bitsUsed int) {
	rootMask := uint32((1 << uint(rootBits)) - 1)
	idx := streamBits & rootMask
	entry := table[idx]
	if int(entry.Bits) <= rootBits {
		return entry.Value, int(entry.Bits)
	}
	// 2nd level table: p += p->value + extra
	extraBits := int(entry.Bits) - rootBits
	extra := (streamBits >> uint(rootBits)) & uint32((1<<uint(extraBits))-1)
	entry2 := table[idx+uint32(entry.Value)+extra]
	return entry2.Value, rootBits + int(entry2.Bits)
}

// canonicalCode returns the bit-reversed canonical Huffman code for lookup in the table.
// Given a canonical code and its length, it reverses the bits so it matches
// how the table is indexed (LSB-first stream order).
func canonicalCode(code uint32, codeLen int) uint32 {
	var rev uint32
	for range codeLen {
		rev = (rev << 1) | (code & 1)
		code >>= 1
	}
	return rev
}

// assignCanonical takes per-symbol code lengths and returns the canonical Huffman codes.
// Returns a map from symbol → (code, codeLen).
func assignCanonical(codeLengths []byte) map[int][2]int {
	// Find max length.
	maxLen := 0
	for _, cl := range codeLengths {
		if int(cl) > maxLen {
			maxLen = int(cl)
		}
	}
	if maxLen == 0 {
		return nil
	}

	// Count symbols per length.
	blCount := make([]int, maxLen+1)
	for _, cl := range codeLengths {
		if cl != 0 {
			blCount[cl]++
		}
	}

	// Compute starting code for each length (standard canonical Huffman).
	nextCode := make([]int, maxLen+1)
	code := 0
	for bl := 1; bl <= maxLen; bl++ {
		code = (code + blCount[bl-1]) << 1
		nextCode[bl] = code
	}

	// Assign codes to symbols in symbol order.
	result := make(map[int][2]int)
	for sym, cl := range codeLengths {
		if cl != 0 {
			result[sym] = [2]int{nextCode[cl], int(cl)}
			nextCode[cl]++
		}
	}
	return result
}

func TestBuildSimpleHuffmanTable_1Symbol(t *testing.T) {
	const rootBits = 8
	table := make([]HuffmanCode, 1<<rootBits)
	val := []uint16{42}
	size := BuildSimpleHuffmanTable(table, rootBits, val, 0)

	if size != 1<<rootBits {
		t.Fatalf("size = %d, want %d", size, 1<<rootBits)
	}

	for i := 0; i < int(size); i++ {
		if table[i].Bits != 0 || table[i].Value != 42 {
			t.Fatalf("table[%d] = {%d, %d}, want {0, 42}", i, table[i].Bits, table[i].Value)
		}
	}
}

func TestBuildSimpleHuffmanTable_2Symbols(t *testing.T) {
	const rootBits = 8
	table := make([]HuffmanCode, 1<<rootBits)
	val := []uint16{10, 20}
	size := BuildSimpleHuffmanTable(table, rootBits, val, 1)

	if size != 1<<rootBits {
		t.Fatalf("size = %d, want %d", size, 1<<rootBits)
	}

	for i := 0; i < int(size); i++ {
		got := table[i]
		if got.Bits != 1 {
			t.Fatalf("table[%d].Bits = %d, want 1", i, got.Bits)
		}
		wantVal := uint16(10)
		if i&1 == 1 {
			wantVal = 20
		}
		if got.Value != wantVal {
			t.Fatalf("table[%d].Value = %d, want %d", i, got.Value, wantVal)
		}
	}
}

func TestBuildSimpleHuffmanTable_3Symbols(t *testing.T) {
	const rootBits = 8
	table := make([]HuffmanCode, 1<<rootBits)
	val := []uint16{5, 15, 25}
	size := BuildSimpleHuffmanTable(table, rootBits, val, 2)

	if size != 1<<rootBits {
		t.Fatalf("size = %d, want %d", size, 1<<rootBits)
	}

	sym, bl := lookupHuffman(table, rootBits, 0b0)
	if sym != 5 || bl != 1 {
		t.Fatalf("lookup(0b0) = (%d, %d), want (5, 1)", sym, bl)
	}

	sym1, bl1 := lookupHuffman(table, rootBits, 0b01)
	sym2, bl2 := lookupHuffman(table, rootBits, 0b11)
	if bl1 != 2 || bl2 != 2 {
		t.Fatalf("2-bit codes: bits = (%d, %d), want (2, 2)", bl1, bl2)
	}
	if sym1 != 15 || sym2 != 25 {
		t.Fatalf("2-bit symbols = (%d, %d), want (15, 25)", sym1, sym2)
	}
}

func TestBuildSimpleHuffmanTable_4SymbolsEqual(t *testing.T) {
	const rootBits = 8
	table := make([]HuffmanCode, 1<<rootBits)
	val := []uint16{30, 10, 40, 20}
	size := BuildSimpleHuffmanTable(table, rootBits, val, 3)

	if size != 1<<rootBits {
		t.Fatalf("size = %d, want %d", size, 1<<rootBits)
	}

	seen := map[uint16]bool{}
	for code := range uint32(4) {
		sym, bl := lookupHuffman(table, rootBits, code)
		if bl != 2 {
			t.Fatalf("lookup(%d): bits = %d, want 2", code, bl)
		}
		seen[sym] = true
	}
	for _, want := range []uint16{10, 20, 30, 40} {
		if !seen[want] {
			t.Fatalf("symbol %d not found in table", want)
		}
	}
}

func TestBuildSimpleHuffmanTable_4SymbolsUnequal(t *testing.T) {
	const rootBits = 8
	table := make([]HuffmanCode, 1<<rootBits)
	val := []uint16{100, 200, 300, 400}
	size := BuildSimpleHuffmanTable(table, rootBits, val, 4)

	if size != 1<<rootBits {
		t.Fatalf("size = %d, want %d", size, 1<<rootBits)
	}

	sym, bl := lookupHuffman(table, rootBits, 0b0)
	if sym != 100 || bl != 1 {
		t.Fatalf("lookup(0b0) = (%d, %d), want (100, 1)", sym, bl)
	}
	sym, bl = lookupHuffman(table, rootBits, 0b01)
	if sym != 200 || bl != 2 {
		t.Fatalf("lookup(0b01) = (%d, %d), want (200, 2)", sym, bl)
	}
	sym, bl = lookupHuffman(table, rootBits, 0b011)
	if sym != 300 || bl != 3 {
		t.Fatalf("lookup(0b011) = (%d, %d), want (300, 3)", sym, bl)
	}
	sym, bl = lookupHuffman(table, rootBits, 0b111)
	if sym != 400 || bl != 3 {
		t.Fatalf("lookup(0b111) = (%d, %d), want (400, 3)", sym, bl)
	}
}

func TestBuildCodeLengthsHuffmanTable(t *testing.T) {
	var codeLengths [AlphabetSizeCodeLengths]byte
	codeLengths[0] = 1
	codeLengths[1] = 2
	codeLengths[2] = 2

	var count [HuffmanMaxCodeLengthCodeLength + 1]uint16
	for _, cl := range codeLengths {
		if cl != 0 {
			count[cl]++
		}
	}

	table := make([]HuffmanCode, 1<<HuffmanMaxCodeLengthCodeLength)
	BuildCodeLengthsHuffmanTable(table, codeLengths[:], count[:])

	codes := assignCanonical(codeLengths[:])

	for sym, codeInfo := range codes {
		code := codeInfo[0]
		codeLen := codeInfo[1]
		reversed := canonicalCode(uint32(code), codeLen)
		gotSym, gotBits := lookupHuffman(table, HuffmanMaxCodeLengthCodeLength, reversed)
		if gotSym != uint16(sym) || gotBits != codeLen {
			t.Errorf("symbol %d: code=%d len=%d reversed=%d → got (%d, %d), want (%d, %d)",
				sym, code, codeLen, reversed, gotSym, gotBits, sym, codeLen)
		}
	}

	validSyms := map[uint16]bool{0: true, 1: true, 2: true}
	for i := range table {
		if !validSyms[table[i].Value] {
			t.Errorf("table[%d].Value = %d, not a valid symbol", i, table[i].Value)
		}
	}
}

func TestBuildCodeLengthsHuffmanTable_SingleSymbol(t *testing.T) {
	var codeLengths [AlphabetSizeCodeLengths]byte
	codeLengths[5] = 1

	var count [HuffmanMaxCodeLengthCodeLength + 1]uint16
	count[1] = 1

	table := make([]HuffmanCode, 1<<HuffmanMaxCodeLengthCodeLength)
	BuildCodeLengthsHuffmanTable(table, codeLengths[:], count[:])

	for i := range table {
		if table[i].Value != 5 || table[i].Bits != 0 {
			t.Fatalf("table[%d] = {%d, %d}, want {0, 5}", i, table[i].Bits, table[i].Value)
		}
	}
}

func TestBuildCodeLengthsHuffmanTable_AllLengths(t *testing.T) {
	var codeLengths [AlphabetSizeCodeLengths]byte
	codeLengths[0] = 1
	codeLengths[1] = 2
	codeLengths[2] = 3
	codeLengths[3] = 4
	codeLengths[4] = 5
	codeLengths[5] = 5

	var count [HuffmanMaxCodeLengthCodeLength + 1]uint16
	for _, cl := range codeLengths {
		if cl != 0 {
			count[cl]++
		}
	}

	table := make([]HuffmanCode, 1<<HuffmanMaxCodeLengthCodeLength)
	BuildCodeLengthsHuffmanTable(table, codeLengths[:], count[:])

	codes := assignCanonical(codeLengths[:])

	for sym, codeInfo := range codes {
		code := codeInfo[0]
		codeLen := codeInfo[1]
		reversed := canonicalCode(uint32(code), codeLen)
		gotSym, gotBits := lookupHuffman(table, HuffmanMaxCodeLengthCodeLength, reversed)
		if gotSym != uint16(sym) || gotBits != codeLen {
			t.Errorf("symbol %d: code=%d len=%d reversed=%d → got (%d, %d), want (%d, %d)",
				sym, code, codeLen, reversed, gotSym, gotBits, sym, codeLen)
		}
	}
}

func TestBuildHuffmanTable_SmallAlphabet(t *testing.T) {
	codeLengths := []byte{1, 2, 3, 3}
	symbols, count := makeSymbolList(codeLengths)

	const rootBits = 8
	table := make([]HuffmanCode, 1<<rootBits)
	size := BuildHuffmanTable(table, rootBits, symbols, count)

	if size != 1<<rootBits {
		t.Fatalf("size = %d, want %d", size, 1<<rootBits)
	}

	codes := assignCanonical(codeLengths)
	for sym, codeInfo := range codes {
		code := codeInfo[0]
		codeLen := codeInfo[1]
		reversed := canonicalCode(uint32(code), codeLen)
		gotSym, gotBits := lookupHuffman(table[:size], rootBits, reversed)
		if gotSym != uint16(sym) || gotBits != codeLen {
			t.Errorf("symbol %d: code=%d len=%d reversed=%d → got (%d, %d), want (%d, %d)",
				sym, code, codeLen, reversed, gotSym, gotBits, sym, codeLen)
		}
	}
}

func TestBuildHuffmanTable_256Symbols(t *testing.T) {
	codeLengths := make([]byte, 256)
	for i := range codeLengths {
		codeLengths[i] = 8
	}

	symbols, count := makeSymbolList(codeLengths)

	const rootBits = 8
	table := make([]HuffmanCode, 1<<rootBits)
	size := BuildHuffmanTable(table, rootBits, symbols, count)

	if size != 256 {
		t.Fatalf("size = %d, want 256", size)
	}

	seen := make(map[uint16]bool)
	for i := range 256 {
		if table[i].Bits != 8 {
			t.Errorf("table[%d].Bits = %d, want 8", i, table[i].Bits)
		}
		seen[table[i].Value] = true
	}
	if len(seen) != 256 {
		t.Fatalf("expected 256 distinct symbols, got %d", len(seen))
	}
}

func TestBuildHuffmanTable_TwoLevel(t *testing.T) {
	codeLengths := make([]byte, 10)
	for i := range 8 {
		codeLengths[i] = byte(i + 1)
	}
	codeLengths[8] = 9
	codeLengths[9] = 9

	symbols, count := makeSymbolList(codeLengths)

	const rootBits = 8
	table := make([]HuffmanCode, 512)
	size := BuildHuffmanTable(table, rootBits, symbols, count)

	if size == 0 {
		t.Fatal("size = 0")
	}

	codes := assignCanonical(codeLengths)
	for sym, codeInfo := range codes {
		code := codeInfo[0]
		codeLen := codeInfo[1]
		reversed := canonicalCode(uint32(code), codeLen)
		gotSym, gotBits := lookupHuffman(table[:size], rootBits, reversed)
		if gotSym != uint16(sym) || gotBits != codeLen {
			t.Errorf("symbol %d: code=%d len=%d reversed=%d → got (%d, %d), want (%d, %d)",
				sym, code, codeLen, reversed, gotSym, gotBits, sym, codeLen)
		}
	}
}

func TestBuildHuffmanTable_MaxCodeLength(t *testing.T) {
	n := 16
	codeLengths := make([]byte, n)
	for i := range 14 {
		codeLengths[i] = byte(i + 1)
	}
	codeLengths[14] = 15
	codeLengths[15] = 15

	symbols, count := makeSymbolList(codeLengths)

	const rootBits = 8
	table := make([]HuffmanCode, 1024)
	size := BuildHuffmanTable(table, rootBits, symbols, count)

	if size == 0 {
		t.Fatal("size = 0")
	}

	codes := assignCanonical(codeLengths)
	for sym, codeInfo := range codes {
		code := codeInfo[0]
		codeLen := codeInfo[1]
		reversed := canonicalCode(uint32(code), codeLen)
		gotSym, gotBits := lookupHuffman(table[:size], rootBits, reversed)
		if gotSym != uint16(sym) || gotBits != codeLen {
			t.Errorf("symbol %d: code=%d len=%d reversed=%d → got (%d, %d), want (%d, %d)",
				sym, code, codeLen, reversed, gotSym, gotBits, sym, codeLen)
		}
	}
}

func TestReplicateValue(t *testing.T) {
	code := HuffmanCode{Bits: 3, Value: 42}
	table := make([]HuffmanCode, 8)
	replicateValue(table, code, 2, 8)

	for i := 0; i < 8; i += 2 {
		if table[i] != code {
			t.Errorf("table[%d] = %+v, want %+v", i, table[i], code)
		}
	}
	zero := HuffmanCode{}
	for i := 1; i < 8; i += 2 {
		if table[i] != zero {
			t.Errorf("table[%d] = %+v, want zero", i, table[i])
		}
	}
}

func TestNextTableBitSize(t *testing.T) {
	count := make([]uint16, HuffmanMaxCodeLength+1)
	count[9] = 4
	count[10] = 2
	got := nextTableBitSize(count, 9, 8)
	if got != 1 {
		t.Fatalf("nextTableBitSize = %d, want 1", got)
	}

	count2 := make([]uint16, HuffmanMaxCodeLength+1)
	count2[10] = 4
	got2 := nextTableBitSize(count2, 9, 8)
	if got2 != 2 {
		t.Fatalf("nextTableBitSize = %d, want 2", got2)
	}
}

func TestSymbolListGet(t *testing.T) {
	storage := []uint16{100, 200, 300, 400, 500}
	sl := SymbolList{Storage: storage, Offset: 2}

	if got := sl.get(-2); got != 100 {
		t.Errorf("get(-2) = %d, want 100", got)
	}
	if got := sl.get(0); got != 300 {
		t.Errorf("get(0) = %d, want 300", got)
	}
	if got := sl.get(2); got != 500 {
		t.Errorf("get(2) = %d, want 500", got)
	}
}

func TestMakeSymbolList(t *testing.T) {
	codeLengths := []byte{1, 2, 2}
	sl, count := makeSymbolList(codeLengths)

	if count[1] != 1 || count[2] != 2 {
		t.Fatalf("count = %v, want [0, 1, 2, ...]", count)
	}

	head := 1 - (HuffmanMaxCodeLength + 1)
	sym := sl.get(head)
	if sym != 0 {
		t.Fatalf("length 1 chain: got symbol %d, want 0", sym)
	}

	head = 2 - (HuffmanMaxCodeLength + 1)
	sym = sl.get(head)
	if sym != 1 {
		t.Fatalf("length 2 chain[0]: got symbol %d, want 1", sym)
	}
	sym = sl.get(int(sym))
	if sym != 2 {
		t.Fatalf("length 2 chain[1]: got symbol %d, want 2", sym)
	}
}

func TestReverseBitsConsistency(t *testing.T) {
	for i := range 256 {
		got := bits.Reverse8(byte(i))
		var want byte
		for bit := range 8 {
			if i&(1<<uint(bit)) != 0 {
				want |= 1 << uint(7-bit)
			}
		}
		if got != want {
			t.Errorf("Reverse8(%d) = %d, want %d", i, got, want)
		}
	}
}
