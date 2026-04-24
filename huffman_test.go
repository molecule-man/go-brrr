package brrr

import (
	"cmp"
	"math"
	"math/bits"
	"slices"
	"testing"
)

// makeSymbolList builds a symbolList and count histogram from per-symbol code lengths,
// mimicking how the brotli decoder populates these during ReadHuffmanCode.
func makeSymbolList(codeLengths []byte) (symbolList, []uint16) {
	n := len(codeLengths)
	storage := make([]uint16, huffmanMaxCodeLength+1+n)
	offset := huffmanMaxCodeLength + 1

	// Initialize linked list heads to sentinel.
	for i := 0; i <= huffmanMaxCodeLength; i++ {
		storage[i] = 0xFFFF
	}

	count := make([]uint16, huffmanMaxCodeLength+1)
	nextSym := make([]int, huffmanMaxCodeLength+1)
	for i := 0; i <= huffmanMaxCodeLength; i++ {
		nextSym[i] = i - (huffmanMaxCodeLength + 1)
	}

	for sym, cl := range codeLengths {
		if cl != 0 {
			storage[offset+nextSym[cl]] = uint16(sym)
			nextSym[cl] = sym
			count[cl]++
		}
	}

	return symbolList{storage: storage, offset: offset}, count
}

// lookupHuffman decodes one symbol from the table given raw stream bits.
// Returns the decoded symbol and the number of bits consumed.
func lookupHuffman(table []huffmanCode, rootBits int, streamBits uint32) (symbol uint16, bitsUsed int) {
	rootMask := uint32((1 << uint(rootBits)) - 1)
	idx := streamBits & rootMask
	entry := table[idx]
	if int(entry.bits) <= rootBits {
		return entry.value, int(entry.bits)
	}
	// 2nd level table: p += p->value + extra
	extraBits := int(entry.bits) - rootBits
	extra := (streamBits >> uint(rootBits)) & uint32((1<<uint(extraBits))-1)
	entry2 := table[idx+uint32(entry.value)+extra]
	return entry2.value, rootBits + int(entry2.bits)
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
	table := make([]huffmanCode, 1<<rootBits)
	val := []uint16{42}
	size := buildSimpleHuffmanTable(table, rootBits, val, 0)

	if size != 1<<rootBits {
		t.Fatalf("size = %d, want %d", size, 1<<rootBits)
	}

	// Every entry should decode to symbol 42, consuming 0 bits.
	for i := 0; i < int(size); i++ {
		if table[i].bits != 0 || table[i].value != 42 {
			t.Fatalf("table[%d] = {%d, %d}, want {0, 42}", i, table[i].bits, table[i].value)
		}
	}
}

func TestBuildSimpleHuffmanTable_2Symbols(t *testing.T) {
	const rootBits = 8
	table := make([]huffmanCode, 1<<rootBits)
	val := []uint16{10, 20}
	size := buildSimpleHuffmanTable(table, rootBits, val, 1)

	if size != 1<<rootBits {
		t.Fatalf("size = %d, want %d", size, 1<<rootBits)
	}

	// Code 0 → smaller symbol (10), code 1 → larger symbol (20). Both 1 bit.
	for i := 0; i < int(size); i++ {
		got := table[i]
		if got.bits != 1 {
			t.Fatalf("table[%d].bits = %d, want 1", i, got.bits)
		}
		wantVal := uint16(10)
		if i&1 == 1 {
			wantVal = 20
		}
		if got.value != wantVal {
			t.Fatalf("table[%d].value = %d, want %d", i, got.value, wantVal)
		}
	}
}

func TestBuildSimpleHuffmanTable_3Symbols(t *testing.T) {
	const rootBits = 8
	table := make([]huffmanCode, 1<<rootBits)
	val := []uint16{5, 15, 25}
	size := buildSimpleHuffmanTable(table, rootBits, val, 2)

	if size != 1<<rootBits {
		t.Fatalf("size = %d, want %d", size, 1<<rootBits)
	}

	// Code lengths: [1, 2, 2]. val[0]=5 gets 1 bit, val[1]=15 and val[2]=25 get 2 bits.
	// Verify via lookup.
	sym, bl := lookupHuffman(table, rootBits, 0b0) // 0 → symbol 5
	if sym != 5 || bl != 1 {
		t.Fatalf("lookup(0b0) = (%d, %d), want (5, 1)", sym, bl)
	}

	// The 2-bit codes: figure out assignment by checking the table.
	sym1, bl1 := lookupHuffman(table, rootBits, 0b01) // low bit 1, then 0
	sym2, bl2 := lookupHuffman(table, rootBits, 0b11) // low bit 1, then 1
	if bl1 != 2 || bl2 != 2 {
		t.Fatalf("2-bit codes: bits = (%d, %d), want (2, 2)", bl1, bl2)
	}
	// Both 15 and 25 should appear, smaller first.
	if sym1 != 15 || sym2 != 25 {
		t.Fatalf("2-bit symbols = (%d, %d), want (15, 25)", sym1, sym2)
	}
}

func TestBuildSimpleHuffmanTable_4SymbolsEqual(t *testing.T) {
	const rootBits = 8
	table := make([]huffmanCode, 1<<rootBits)
	val := []uint16{30, 10, 40, 20}
	size := buildSimpleHuffmanTable(table, rootBits, val, 3)

	if size != 1<<rootBits {
		t.Fatalf("size = %d, want %d", size, 1<<rootBits)
	}

	// Code lengths [2,2,2,2]. Symbols are sorted by value: 10,20,30,40.
	// 2-bit codes, 4 entries in root pattern.
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
	table := make([]huffmanCode, 1<<rootBits)
	val := []uint16{100, 200, 300, 400}
	size := buildSimpleHuffmanTable(table, rootBits, val, 4)

	if size != 1<<rootBits {
		t.Fatalf("size = %d, want %d", size, 1<<rootBits)
	}

	// Code lengths [1, 2, 3, 3].
	// val[0]=100 → 1 bit, val[1]=200 → 2 bits, val[2]=300 → 3 bits, val[3]=400 → 3 bits.
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
	// Build a code-length Huffman table for a small set of active code-length symbols.
	// Use symbols 0, 1, 2 with code lengths 1, 2, 2.
	// Remaining 15 symbols (3..17) have code length 0 (unused).
	var codeLengths [alphabetSizeCodeLengths]byte
	codeLengths[0] = 1
	codeLengths[1] = 2
	codeLengths[2] = 2

	var count [huffmanMaxCodeLengthCodeLength + 1]uint16
	for _, cl := range codeLengths {
		if cl != 0 {
			count[cl]++
		}
	}

	table := make([]huffmanCode, 1<<huffmanMaxCodeLengthCodeLength)
	buildCodeLengthsHuffmanTable(table, codeLengths[:], count[:])

	// Compute canonical codes for the active symbols.
	codes := assignCanonical(codeLengths[:])

	// Verify each active symbol decodes correctly.
	for sym, codeInfo := range codes {
		code := codeInfo[0]
		codeLen := codeInfo[1]
		// Reverse the canonical code for table lookup (table is indexed LSB-first).
		reversed := canonicalCode(uint32(code), codeLen)
		gotSym, gotBits := lookupHuffman(table, huffmanMaxCodeLengthCodeLength, reversed)
		if gotSym != uint16(sym) || gotBits != codeLen {
			t.Errorf("symbol %d: code=%d len=%d reversed=%d → got (%d, %d), want (%d, %d)",
				sym, code, codeLen, reversed, gotSym, gotBits, sym, codeLen)
		}
	}

	// Verify all table entries point to valid symbols.
	validSyms := map[uint16]bool{0: true, 1: true, 2: true}
	for i := range table {
		if !validSyms[table[i].value] {
			t.Errorf("table[%d].value = %d, not a valid symbol", i, table[i].value)
		}
	}
}

func TestBuildCodeLengthsHuffmanTable_SingleSymbol(t *testing.T) {
	// Only symbol 5 is active (code length doesn't matter, it's the only one).
	var codeLengths [alphabetSizeCodeLengths]byte
	codeLengths[5] = 1

	var count [huffmanMaxCodeLengthCodeLength + 1]uint16
	count[1] = 1

	table := make([]huffmanCode, 1<<huffmanMaxCodeLengthCodeLength)
	buildCodeLengthsHuffmanTable(table, codeLengths[:], count[:])

	// Every entry should decode to symbol 5, consuming 0 bits (special case).
	for i := range table {
		if table[i].value != 5 || table[i].bits != 0 {
			t.Fatalf("table[%d] = {%d, %d}, want {0, 5}", i, table[i].bits, table[i].value)
		}
	}
}

func TestBuildCodeLengthsHuffmanTable_AllLengths(t *testing.T) {
	// Use all 5 code lengths: 1@1, 1@2, 1@3, 1@4, 2@5 = 6 active symbols.
	// Kraft: 16/32 + 8/32 + 4/32 + 2/32 + 2/32 = 1.0.
	var codeLengths [alphabetSizeCodeLengths]byte
	codeLengths[0] = 1
	codeLengths[1] = 2
	codeLengths[2] = 3
	codeLengths[3] = 4
	codeLengths[4] = 5
	codeLengths[5] = 5
	// symbols 6..17 have length 0 (unused)

	var count [huffmanMaxCodeLengthCodeLength + 1]uint16
	for _, cl := range codeLengths {
		if cl != 0 {
			count[cl]++
		}
	}

	table := make([]huffmanCode, 1<<huffmanMaxCodeLengthCodeLength)
	buildCodeLengthsHuffmanTable(table, codeLengths[:], count[:])

	codes := assignCanonical(codeLengths[:])

	for sym, codeInfo := range codes {
		code := codeInfo[0]
		codeLen := codeInfo[1]
		reversed := canonicalCode(uint32(code), codeLen)
		gotSym, gotBits := lookupHuffman(table, huffmanMaxCodeLengthCodeLength, reversed)
		if gotSym != uint16(sym) || gotBits != codeLen {
			t.Errorf("symbol %d: code=%d len=%d reversed=%d → got (%d, %d), want (%d, %d)",
				sym, code, codeLen, reversed, gotSym, gotBits, sym, codeLen)
		}
	}
}

func TestBuildHuffmanTable_SmallAlphabet(t *testing.T) {
	// 4 symbols with code lengths [1, 2, 3, 3].
	// Fits entirely in the root table (max length 3 <= rootBits 8).
	codeLengths := []byte{1, 2, 3, 3}
	symbols, count := makeSymbolList(codeLengths)

	const rootBits = 8
	table := make([]huffmanCode, 1<<rootBits)
	size := buildHuffmanTable(table, rootBits, symbols, count)

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
	// Simulate a typical literal byte Huffman code: 256 symbols,
	// lengths distributed so everything fits in root table (max length <= 8).
	// Use a simple scheme: 128 symbols at length 7, 128 at length 8.
	// Kraft sum: 128/128 + 128/256 = 1.0 + 0.5 = 1.5... that's > 1, not valid.
	// Let's use: 2 at length 1... no, for 256 symbols we need exactly Kraft = 1.
	// Simplest valid: all 256 symbols at length 8. Kraft: 256/256 = 1.
	codeLengths := make([]byte, 256)
	for i := range codeLengths {
		codeLengths[i] = 8
	}

	symbols, count := makeSymbolList(codeLengths)

	const rootBits = 8
	table := make([]huffmanCode, 1<<rootBits)
	size := buildHuffmanTable(table, rootBits, symbols, count)

	if size != 256 {
		t.Fatalf("size = %d, want 256", size)
	}

	// Every entry should be 8 bits, and all 256 symbols should appear exactly once.
	seen := make(map[uint16]bool)
	for i := range 256 {
		if table[i].bits != 8 {
			t.Errorf("table[%d].bits = %d, want 8", i, table[i].bits)
		}
		seen[table[i].value] = true
	}
	if len(seen) != 256 {
		t.Fatalf("expected 256 distinct symbols, got %d", len(seen))
	}
}

func TestBuildHuffmanTable_TwoLevel(t *testing.T) {
	// Force a 2-level table: rootBits=8, some codes at length 9.
	// 1@1, 1@2, 1@3, 1@4, 1@5, 1@6, 1@7, 1@8, 2@9.
	// Kraft: sum(1/2^i, i=1..8) + 2/512 = (1 - 1/256) + 1/256 = 1.0.
	codeLengths := make([]byte, 10)
	for i := range 8 {
		codeLengths[i] = byte(i + 1)
	}
	codeLengths[8] = 9
	codeLengths[9] = 9

	symbols, count := makeSymbolList(codeLengths)

	const rootBits = 8
	table := make([]huffmanCode, 512)
	size := buildHuffmanTable(table, rootBits, symbols, count)

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
	// Use the maximum code length (15) with rootBits=8.
	// 2 symbols at length 1, rest (14 symbols) at length 15 would violate Kraft.
	// Valid scheme: 1 at len 1, 1 at len 2, ..., 1 at len 14, 2 at len 15.
	// Kraft: sum(1/2^i, i=1..14) + 2/2^15 = (1 - 1/2^14) + 1/2^14 = 1.0.
	n := 16
	codeLengths := make([]byte, n)
	for i := range 14 {
		codeLengths[i] = byte(i + 1)
	}
	codeLengths[14] = 15
	codeLengths[15] = 15

	symbols, count := makeSymbolList(codeLengths)

	const rootBits = 8
	table := make([]huffmanCode, 1024) // generous
	size := buildHuffmanTable(table, rootBits, symbols, count)

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
	code := huffmanCode{bits: 3, value: 42}
	table := make([]huffmanCode, 8)
	replicateValue(table, code, 2, 8)

	for i := 0; i < 8; i += 2 {
		if table[i] != code {
			t.Errorf("table[%d] = %+v, want %+v", i, table[i], code)
		}
	}
	zero := huffmanCode{}
	for i := 1; i < 8; i += 2 {
		if table[i] != zero {
			t.Errorf("table[%d] = %+v, want zero", i, table[i])
		}
	}
}

func TestNextTableBitSize(t *testing.T) {
	// count[9] = 4, count[10] = 2. rootBits = 8.
	// At length 9: left = 1 << (9-8) = 2. left -= count[9] = 2 - 4 = -2 ≤ 0 → break.
	// Returns 9 - 8 = 1.
	count := make([]uint16, huffmanMaxCodeLength+1)
	count[9] = 4
	count[10] = 2
	got := nextTableBitSize(count, 9, 8)
	if got != 1 {
		t.Fatalf("nextTableBitSize = %d, want 1", got)
	}

	// At length 9: left = 2, left -= 0 = 2 > 0 → continue.
	// At length 10: left = 4, left -= 4 = 0 ≤ 0 → break.
	// Returns 10 - 8 = 2.
	count2 := make([]uint16, huffmanMaxCodeLength+1)
	count2[10] = 4
	got2 := nextTableBitSize(count2, 9, 8)
	if got2 != 2 {
		t.Fatalf("nextTableBitSize = %d, want 2", got2)
	}
}

func TestSymbolListGet(t *testing.T) {
	storage := []uint16{100, 200, 300, 400, 500}
	sl := symbolList{storage: storage, offset: 2}

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
	// 3 symbols: sym 0 at length 1, sym 1 at length 2, sym 2 at length 2.
	codeLengths := []byte{1, 2, 2}
	sl, count := makeSymbolList(codeLengths)

	if count[1] != 1 || count[2] != 2 {
		t.Fatalf("count = %v, want [0, 1, 2, ...]", count)
	}

	// Walk the linked list for length 1: should yield symbol 0.
	head := 1 - (huffmanMaxCodeLength + 1) // = -15
	sym := sl.get(head)
	if sym != 0 {
		t.Fatalf("length 1 chain: got symbol %d, want 0", sym)
	}

	// Walk the linked list for length 2: should yield symbols 1 and 2 (in that order).
	head = 2 - (huffmanMaxCodeLength + 1) // = -14
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
	// Verify that bits.Reverse8 matches the removed kReverseBits table
	// for all 256 values.
	for i := range 256 {
		got := bits.Reverse8(byte(i))
		// Manually compute expected reversal.
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

// --- setDepth ---

func TestSetDepth_SingleLeaf(t *testing.T) {
	// A single leaf node: left < 0, value = 7.
	pool := []huffmanTreeNode{{left: -1, rightOrValue: 7}}
	depth := make([]byte, 8)
	ok := setDepth(pool, depth, 0, 15)
	if !ok {
		t.Fatal("setDepth returned false for single leaf")
	}
	if depth[7] != 0 {
		t.Errorf("depth[7] = %d, want 0 (root leaf = level 0)", depth[7])
	}
}

func TestSetDepth_BalancedTree(t *testing.T) {
	// Build a balanced binary tree with 4 leaves at depth 2:
	//        4
	//       / \
	//      2   3
	//     / \ / \
	//    A  B C  D
	// Leaves are symbols 0,1,2,3 at nodes 0,1,2,3.
	// Internal nodes at 4 (root), 2 (left child of root → but we need indices).
	// Let's lay it out:
	//   pool[0]: leaf, value=0
	//   pool[1]: leaf, value=1
	//   pool[2]: internal, left=0, right=1
	//   pool[3]: leaf, value=2
	//   pool[4]: leaf, value=3
	//   pool[5]: internal, left=3, right=4
	//   pool[6]: internal (root), left=2, right=5
	pool := []huffmanTreeNode{
		{left: -1, rightOrValue: 0}, // 0: leaf sym 0
		{left: -1, rightOrValue: 1}, // 1: leaf sym 1
		{left: 0, rightOrValue: 1},  // 2: internal
		{left: -1, rightOrValue: 2}, // 3: leaf sym 2
		{left: -1, rightOrValue: 3}, // 4: leaf sym 3
		{left: 3, rightOrValue: 4},  // 5: internal
		{left: 2, rightOrValue: 5},  // 6: root
	}
	depth := make([]byte, 4)
	ok := setDepth(pool, depth, 6, 15)
	if !ok {
		t.Fatal("setDepth returned false")
	}
	for sym := range 4 {
		if depth[sym] != 2 {
			t.Errorf("depth[%d] = %d, want 2", sym, depth[sym])
		}
	}
}

func TestSetDepth_UnbalancedTree(t *testing.T) {
	// Left-skewed tree producing depths [3, 3, 2, 1]:
	//         root(5)
	//        /       \
	//     n(4)       leaf(3) sym=3  depth=1
	//    /    \
	//  n(3)   leaf(2) sym=2        depth=2
	//  / \
	// leaf(0) leaf(1)  sym=0,1     depth=3
	pool := []huffmanTreeNode{
		{left: -1, rightOrValue: 0}, // 0: leaf sym 0
		{left: -1, rightOrValue: 1}, // 1: leaf sym 1
		{left: -1, rightOrValue: 2}, // 2: leaf sym 2
		{left: 0, rightOrValue: 1},  // 3: internal (left subtree of 4)
		{left: 3, rightOrValue: 2},  // 4: internal (left subtree of root)
		{left: -1, rightOrValue: 3}, // 5: leaf sym 3
		{left: 4, rightOrValue: 5},  // 6: root
	}
	depth := make([]byte, 4)
	ok := setDepth(pool, depth, 6, 15)
	if !ok {
		t.Fatal("setDepth returned false")
	}
	want := []byte{3, 3, 2, 1}
	for sym := range 4 {
		if depth[sym] != want[sym] {
			t.Errorf("depth[%d] = %d, want %d", sym, depth[sym], want[sym])
		}
	}
}

func TestSetDepth_ExceedsMaxDepth(t *testing.T) {
	// Chain of depth 3, but maxDepth=2 → should fail.
	pool := []huffmanTreeNode{
		{left: -1, rightOrValue: 0}, // 0: leaf
		{left: -1, rightOrValue: 1}, // 1: leaf
		{left: 0, rightOrValue: 1},  // 2: internal
		{left: -1, rightOrValue: 2}, // 3: leaf
		{left: 2, rightOrValue: 3},  // 4: internal
		{left: -1, rightOrValue: 3}, // 5: leaf
		{left: 4, rightOrValue: 5},  // 6: root, depth=3 for leaves 0,1
	}
	depth := make([]byte, 4)
	ok := setDepth(pool, depth, 6, 2)
	if ok {
		t.Fatal("setDepth should return false when tree exceeds maxDepth")
	}
}

func TestSetDepth_ExactlyAtMaxDepth(t *testing.T) {
	// Same tree as above but maxDepth=3 → should succeed.
	pool := []huffmanTreeNode{
		{left: -1, rightOrValue: 0},
		{left: -1, rightOrValue: 1},
		{left: 0, rightOrValue: 1},
		{left: -1, rightOrValue: 2},
		{left: 2, rightOrValue: 3},
		{left: -1, rightOrValue: 3},
		{left: 4, rightOrValue: 5},
	}
	depth := make([]byte, 4)
	ok := setDepth(pool, depth, 6, 3)
	if !ok {
		t.Fatal("setDepth returned false for tree exactly at maxDepth")
	}
}

// --- createHuffmanTree ---

// verifyKraftInequality checks that the assigned depths satisfy the Kraft inequality (sum = 1 for complete code).
func verifyKraftInequality(t *testing.T, depth []byte, data []uint32) {
	t.Helper()
	var sum float64
	for i, d := range depth {
		if d > 0 {
			sum += 1.0 / float64(uint(1)<<d)
		} else if data[i] != 0 {
			t.Errorf("symbol %d has count %d but depth 0", i, data[i])
		}
	}
	if math.Abs(sum-1.0) > 1e-9 {
		t.Errorf("Kraft sum = %f, want 1.0", sum)
	}
}

func TestCreateHuffmanTree_TwoSymbols(t *testing.T) {
	data := []uint32{10, 20}
	tree := make([]huffmanTreeNode, 2*len(data)+1)
	depth := make([]byte, len(data))
	createHuffmanTree(data, 15, tree, depth)

	// Two symbols → both get depth 1.
	for sym, d := range depth {
		if d != 1 {
			t.Errorf("depth[%d] = %d, want 1", sym, d)
		}
	}
}

func TestCreateHuffmanTree_SingleNonZero(t *testing.T) {
	data := []uint32{0, 0, 42, 0}
	tree := make([]huffmanTreeNode, 2*len(data)+1)
	depth := make([]byte, len(data))
	createHuffmanTree(data, 15, tree, depth)

	if depth[2] != 1 {
		t.Errorf("depth[2] = %d, want 1 for single active symbol", depth[2])
	}
	for i, d := range depth {
		if i != 2 && d != 0 {
			t.Errorf("depth[%d] = %d, want 0 for inactive symbol", i, d)
		}
	}
}

func TestCreateHuffmanTree_UniformFrequencies(t *testing.T) {
	// 8 symbols with equal frequencies → balanced tree, all depth 3.
	data := make([]uint32, 8)
	for i := range data {
		data[i] = 100
	}
	tree := make([]huffmanTreeNode, 2*len(data)+1)
	depth := make([]byte, len(data))
	createHuffmanTree(data, 15, tree, depth)

	for sym, d := range depth {
		if d != 3 {
			t.Errorf("depth[%d] = %d, want 3", sym, d)
		}
	}
	verifyKraftInequality(t, depth, data)
}

func TestCreateHuffmanTree_SkewedFrequencies(t *testing.T) {
	// Highly skewed: one very frequent symbol, rest rare.
	data := []uint32{1000, 1, 1, 1}
	tree := make([]huffmanTreeNode, 2*len(data)+1)
	depth := make([]byte, len(data))
	createHuffmanTree(data, 15, tree, depth)

	// Most frequent symbol should get shortest code.
	if depth[0] >= depth[1] {
		t.Errorf("expected depth[0] (%d) < depth[1] (%d) for skewed frequencies", depth[0], depth[1])
	}
	verifyKraftInequality(t, depth, data)
}

func TestCreateHuffmanTree_RespectsTreeLimit(t *testing.T) {
	// Many symbols with varied frequencies and a tight tree limit.
	data := make([]uint32, 32)
	for i := range data {
		data[i] = uint32(i + 1)
	}
	tree := make([]huffmanTreeNode, 2*len(data)+1)
	depth := make([]byte, len(data))
	const treeLimit = 7
	createHuffmanTree(data, treeLimit, tree, depth)

	for sym, d := range depth {
		if d > treeLimit {
			t.Errorf("depth[%d] = %d exceeds treeLimit %d", sym, d, treeLimit)
		}
	}
	verifyKraftInequality(t, depth, data)
}

func TestCreateHuffmanTree_ZeroCountsIgnored(t *testing.T) {
	data := []uint32{0, 5, 0, 10, 0, 3}
	tree := make([]huffmanTreeNode, 2*len(data)+1)
	depth := make([]byte, len(data))
	createHuffmanTree(data, 15, tree, depth)

	for i, d := range depth {
		if data[i] == 0 && d != 0 {
			t.Errorf("symbol %d has count 0 but depth %d", i, d)
		}
		if data[i] != 0 && d == 0 {
			t.Errorf("symbol %d has count %d but depth 0", i, data[i])
		}
	}
	verifyKraftInequality(t, depth, data)
}

func TestCreateHuffmanTree_PowerOfTwoSymbols(t *testing.T) {
	// 16 equal symbols → all depth 4.
	data := make([]uint32, 16)
	for i := range data {
		data[i] = 50
	}
	tree := make([]huffmanTreeNode, 2*len(data)+1)
	depth := make([]byte, len(data))
	createHuffmanTree(data, 15, tree, depth)

	for sym, d := range depth {
		if d != 4 {
			t.Errorf("depth[%d] = %d, want 4", sym, d)
		}
	}
}

// --- optimizeHuffmanCountsForRLE ---

func TestOptimizeHuffmanCountsForRLE_FewNonzero(t *testing.T) {
	// Fewer than 16 nonzero → no modification.
	counts := make([]uint32, 20)
	for i := range 10 {
		counts[i] = uint32(i + 1)
	}
	orig := make([]uint32, len(counts))
	copy(orig, counts)

	optimizeHuffmanCountsForRLE(counts, new([]bool))

	for i := range counts {
		if counts[i] != orig[i] {
			t.Errorf("counts[%d] changed from %d to %d with < 16 nonzero", i, orig[i], counts[i])
		}
	}
}

func TestOptimizeHuffmanCountsForRLE_AllZeros(t *testing.T) {
	counts := make([]uint32, 30)
	orig := make([]uint32, len(counts))
	copy(orig, counts)

	optimizeHuffmanCountsForRLE(counts, new([]bool))

	for i := range counts {
		if counts[i] != orig[i] {
			t.Errorf("counts[%d] changed from %d to %d for all-zeros input", i, orig[i], counts[i])
		}
	}
}

func TestOptimizeHuffmanCountsForRLE_FillsIsolatedZeros(t *testing.T) {
	// When smallest_nonzero < 4 and zeros < 6, isolated zeros between
	// nonzero values get filled in. Subsequent stride smoothing may
	// further adjust the value, but the zeros should not survive.
	counts := make([]uint32, 40)
	for i := range counts {
		counts[i] = 3 // smallest_nonzero = 3 (< 4)
	}
	// Create a few isolated zeros (fewer than 6 total zeros).
	counts[5] = 0
	counts[15] = 0
	counts[25] = 0

	optimizeHuffmanCountsForRLE(counts, new([]bool))

	for _, idx := range []int{5, 15, 25} {
		if counts[idx] == 0 {
			t.Errorf("counts[%d] still 0 after optimization; isolated zeros should be filled", idx)
		}
	}
}

func TestOptimizeHuffmanCountsForRLE_PreservesNonzeroTotal(t *testing.T) {
	// After optimization, nonzero symbols should remain nonzero.
	counts := make([]uint32, 64)
	for i := range counts {
		counts[i] = uint32(100 + i*3)
	}

	optimizeHuffmanCountsForRLE(counts, new([]bool))

	for i, c := range counts {
		if c == 0 {
			t.Errorf("counts[%d] became 0 after optimization", i)
		}
	}
}

func TestOptimizeHuffmanCountsForRLE_SmoothsStrides(t *testing.T) {
	// A long run of similar-but-not-identical values should get smoothed.
	counts := make([]uint32, 64)
	for i := range counts {
		counts[i] = 100
	}
	// Introduce a slight variation in a stride.
	counts[10] = 101
	counts[11] = 99
	counts[12] = 100
	counts[13] = 102

	orig := make([]uint32, len(counts))
	copy(orig, counts)

	optimizeHuffmanCountsForRLE(counts, new([]bool))

	// The function should run without panic. The exact smoothing behavior
	// is heuristic; just verify no zeros were introduced in the middle.
	for i := range 60 {
		if counts[i] == 0 {
			t.Errorf("counts[%d] became 0 after smoothing", i)
		}
	}
}

// --- encodeHuffmanTree ---

func TestEncodeHuffmanTree_ShortNonZero(t *testing.T) {
	// Short depth sequence, no RLE (length <= 50).
	depth := []byte{1, 2, 3, 3}
	tree := make([]byte, 256)
	extraBits := make([]byte, 256)

	treeSize := encodeHuffmanTree(depth, tree, extraBits)

	// Each depth value emitted individually.
	if treeSize != 4 {
		t.Fatalf("treeSize = %d, want 4", treeSize)
	}
	want := []byte{1, 2, 3, 3}
	for i := range 4 {
		if tree[i] != want[i] {
			t.Errorf("tree[%d] = %d, want %d", i, tree[i], want[i])
		}
	}
}

func TestEncodeHuffmanTree_TrailingZerosTrimmed(t *testing.T) {
	depth := []byte{3, 2, 1, 0, 0, 0}
	tree := make([]byte, 256)
	extraBits := make([]byte, 256)

	treeSize := encodeHuffmanTree(depth, tree, extraBits)

	// Only the first 3 non-trailing-zero values should be emitted.
	if treeSize != 3 {
		t.Fatalf("treeSize = %d, want 3", treeSize)
	}
	want := []byte{3, 2, 1}
	for i := range 3 {
		if tree[i] != want[i] {
			t.Errorf("tree[%d] = %d, want %d", i, tree[i], want[i])
		}
	}
}

func TestEncodeHuffmanTree_AllZeros(t *testing.T) {
	depth := []byte{0, 0, 0, 0}
	tree := make([]byte, 256)
	extraBits := make([]byte, 256)

	treeSize := encodeHuffmanTree(depth, tree, extraBits)

	// All trailing zeros → nothing emitted.
	if treeSize != 0 {
		t.Fatalf("treeSize = %d, want 0", treeSize)
	}
}

func TestEncodeHuffmanTree_RepeatPreviousCode(t *testing.T) {
	// A sequence of the same nonzero value repeated many times (> 50 to enable RLE).
	depth := make([]byte, 60)
	for i := range depth {
		depth[i] = 5
	}
	tree := make([]byte, 256)
	extraBits := make([]byte, 256)

	treeSize := encodeHuffmanTree(depth, tree, extraBits)

	// Should use repeatPreviousCodeLength (16) to compress the run.
	found16 := false
	for i := range treeSize {
		if tree[i] == repeatPreviousCodeLength {
			found16 = true
			break
		}
	}
	if !found16 {
		t.Error("expected repeat-previous code (16) for long run of same nonzero value")
	}
	// Output should be shorter than the input.
	if treeSize >= 60 {
		t.Errorf("treeSize = %d, expected compression below 60", treeSize)
	}
}

func TestEncodeHuffmanTree_RepeatZeroCode(t *testing.T) {
	// Long run of zeros surrounded by nonzero values (> 50 to enable RLE).
	depth := make([]byte, 70)
	depth[0] = 3
	// 68 zeros in the middle
	depth[69] = 4
	tree := make([]byte, 512)
	extraBits := make([]byte, 512)

	treeSize := encodeHuffmanTree(depth, tree, extraBits)

	// Should use repeatZeroCodeLength (17) for the long zero run.
	found17 := false
	for i := range treeSize {
		if tree[i] == alphabetSizeRepeatZeroCodeLength {
			found17 = true
			break
		}
	}
	if !found17 {
		t.Error("expected repeat-zero code (17) for long run of zeros")
	}
}

func TestEncodeHuffmanTree_MixedDepths(t *testing.T) {
	// Mix of values and zeros (> 50 for RLE).
	depth := make([]byte, 80)
	for i := range 20 {
		depth[i] = 5
	}
	// 40 zeros
	for i := 60; i < 80; i++ {
		depth[i] = 3
	}
	tree := make([]byte, 512)
	extraBits := make([]byte, 512)

	treeSize := encodeHuffmanTree(depth, tree, extraBits)

	if treeSize == 0 {
		t.Fatal("treeSize = 0")
	}
	// Output should be shorter than input.
	if treeSize >= 80 {
		t.Errorf("treeSize = %d, expected compression below 80", treeSize)
	}
}

// --- convertBitDepthsToSymbols ---

func TestConvertBitDepthsToSymbols_TwoSymbolsDepth1(t *testing.T) {
	depth := []byte{1, 1}
	bitsOut := make([]uint16, 2)
	convertBitDepthsToSymbols(depth, bitsOut)

	// Canonical codes: symbol 0 → 0, symbol 1 → 1.
	// Reversed (1-bit): 0→0, 1→1.
	if bitsOut[0] != 0 {
		t.Errorf("bits[0] = %d, want 0", bitsOut[0])
	}
	if bitsOut[1] != 1 {
		t.Errorf("bits[1] = %d, want 1", bitsOut[1])
	}
}

func TestConvertBitDepthsToSymbols_ThreeSymbols(t *testing.T) {
	// Depths [1, 2, 2]: canonical codes = 0, 10, 11.
	// Reversed: 0→0 (1 bit), 10→01=1, 11→11=3.
	depth := []byte{1, 2, 2}
	bitsOut := make([]uint16, 3)
	convertBitDepthsToSymbols(depth, bitsOut)

	if bitsOut[0] != 0 {
		t.Errorf("bits[0] = %d, want 0", bitsOut[0])
	}
	if bitsOut[1] != 1 {
		t.Errorf("bits[1] = %d, want 1 (reversed 10)", bitsOut[1])
	}
	if bitsOut[2] != 3 {
		t.Errorf("bits[2] = %d, want 3 (reversed 11)", bitsOut[2])
	}
}

func TestConvertBitDepthsToSymbols_ZeroDepthSkipped(t *testing.T) {
	depth := []byte{0, 2, 0, 2}
	bitsOut := make([]uint16, 4)
	convertBitDepthsToSymbols(depth, bitsOut)

	if bitsOut[0] != 0 {
		t.Errorf("bits[0] = %d, want 0 for zero-depth symbol", bitsOut[0])
	}
	if bitsOut[2] != 0 {
		t.Errorf("bits[2] = %d, want 0 for zero-depth symbol", bitsOut[2])
	}
	// Symbols 1 and 3 both have depth 2: canonical codes 00, 01 → reversed 00, 10.
	if bitsOut[1] != 0 {
		t.Errorf("bits[1] = %d, want 0 (reversed 00)", bitsOut[1])
	}
	if bitsOut[3] != 2 {
		t.Errorf("bits[3] = %d, want 2 (reversed 01 = 10)", bitsOut[3])
	}
}

func TestConvertBitDepthsToSymbols_VaryingDepths(t *testing.T) {
	// [1, 2, 3, 3]: canonical = 0, 10, 110, 111
	// Reversed: 0→0, 10→01=1, 110→011=3, 111→111=7.
	depth := []byte{1, 2, 3, 3}
	bitsOut := make([]uint16, 4)
	convertBitDepthsToSymbols(depth, bitsOut)

	want := []uint16{0, 1, 3, 7}
	for i := range 4 {
		if bitsOut[i] != want[i] {
			t.Errorf("bits[%d] = %d, want %d", i, bitsOut[i], want[i])
		}
	}
}

func TestConvertBitDepthsToSymbols_AllSameDepth(t *testing.T) {
	// 4 symbols at depth 2: canonical 00, 01, 10, 11.
	// Reversed: 00→0, 01→2, 10→1, 11→3.
	depth := []byte{2, 2, 2, 2}
	bitsOut := make([]uint16, 4)
	convertBitDepthsToSymbols(depth, bitsOut)

	want := []uint16{0, 2, 1, 3}
	for i := range 4 {
		if bitsOut[i] != want[i] {
			t.Errorf("bits[%d] = %d, want %d", i, bitsOut[i], want[i])
		}
	}
}

func TestConvertBitDepthsToSymbols_UniqueCodes(t *testing.T) {
	// Verify all codes are unique per depth group.
	depth := []byte{3, 3, 3, 3, 2, 2, 1, 0}
	bitsOut := make([]uint16, len(depth))
	convertBitDepthsToSymbols(depth, bitsOut)

	type key struct {
		depth byte
		code  uint16
	}
	seen := make(map[key]int)
	for i, d := range depth {
		if d == 0 {
			continue
		}
		k := key{d, bitsOut[i]}
		if prev, ok := seen[k]; ok {
			t.Errorf("symbols %d and %d share code %d at depth %d", prev, i, bitsOut[i], d)
		}
		seen[k] = i
	}
}

// --- Integration: createHuffmanTree + convertBitDepthsToSymbols ---

func TestCreateThenConvert_ProducesValidCodes(t *testing.T) {
	data := []uint32{10, 20, 30, 40, 50, 15, 5, 25}
	tree := make([]huffmanTreeNode, 2*len(data)+1)
	depth := make([]byte, len(data))
	createHuffmanTree(data, 15, tree, depth)

	bitsOut := make([]uint16, len(data))
	convertBitDepthsToSymbols(depth, bitsOut)

	// All active symbols should have unique (depth, code) pairs.
	type key struct {
		depth byte
		code  uint16
	}
	seen := make(map[key]int)
	for i, d := range depth {
		if d == 0 {
			continue
		}
		k := key{d, bitsOut[i]}
		if prev, ok := seen[k]; ok {
			t.Errorf("symbols %d and %d share code %d at depth %d", prev, i, bitsOut[i], d)
		}
		seen[k] = i
	}

	verifyKraftInequality(t, depth, data)
}

// --- encodeHuffmanTreeRepetitions / encodeHuffmanTreeRepetitionsZeros ---

func TestEncodeHuffmanTreeRepetitions_SmallCount(t *testing.T) {
	// Repeating value=5, previous=5, repetitions=2: should emit 2 literal 5s.
	tree := make([]byte, 32)
	extraBitsData := make([]byte, 32)
	treeSize := encodeHuffmanTreeRepetitions(tree, extraBitsData, 5, 5, 2)

	if treeSize != 2 {
		t.Fatalf("treeSize = %d, want 2", treeSize)
	}
	for i := range 2 {
		if tree[i] != 5 {
			t.Errorf("tree[%d] = %d, want 5", i, tree[i])
		}
	}
}

func TestEncodeHuffmanTreeRepetitions_DifferentPrevious(t *testing.T) {
	// previous=3, value=5, reps=1: should emit one literal 5.
	tree := make([]byte, 32)
	extraBitsData := make([]byte, 32)
	treeSize := encodeHuffmanTreeRepetitions(tree, extraBitsData, 3, 5, 1)

	if treeSize != 1 {
		t.Fatalf("treeSize = %d, want 1", treeSize)
	}
	if tree[0] != 5 {
		t.Errorf("tree[0] = %d, want 5", tree[0])
	}
}

func TestEncodeHuffmanTreeRepetitions_UsesRepeatCode(t *testing.T) {
	// Same value repeated 10 times → should use repeatPreviousCodeLength (16).
	tree := make([]byte, 32)
	extraBitsData := make([]byte, 32)
	treeSize := encodeHuffmanTreeRepetitions(tree, extraBitsData, 5, 5, 10)

	found := false
	for i := range treeSize {
		if tree[i] == repeatPreviousCodeLength {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected repeat-previous code (16) for 10 repetitions")
	}
}

func TestEncodeHuffmanTreeRepetitionsZeros_SmallCount(t *testing.T) {
	tree := make([]byte, 32)
	extraBitsData := make([]byte, 32)
	treeSize := encodeHuffmanTreeRepetitionsZeros(tree, extraBitsData, 2)

	if treeSize != 2 {
		t.Fatalf("treeSize = %d, want 2", treeSize)
	}
	for i := range 2 {
		if tree[i] != 0 {
			t.Errorf("tree[%d] = %d, want 0", i, tree[i])
		}
	}
}

func TestEncodeHuffmanTreeRepetitionsZeros_UsesRepeatZeroCode(t *testing.T) {
	tree := make([]byte, 32)
	extraBitsData := make([]byte, 32)
	treeSize := encodeHuffmanTreeRepetitionsZeros(tree, extraBitsData, 20)

	found := false
	for i := range treeSize {
		if tree[i] == alphabetSizeRepeatZeroCodeLength {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected repeat-zero code (17) for 20 zero repetitions")
	}
}

// --- decideOverRLEUse ---

func TestDecideOverRLEUse_ShortRuns(t *testing.T) {
	// No long runs → RLE not useful.
	depth := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	useNonZero, useZero := decideOverRLEUse(depth)
	if useNonZero {
		t.Error("useNonZero should be false for short distinct runs")
	}
	if useZero {
		t.Error("useZero should be false for no zero runs")
	}
}

func TestDecideOverRLEUse_LongZeroRun(t *testing.T) {
	depth := make([]byte, 30)
	depth[0] = 3
	// 28 zeros in middle
	depth[29] = 5
	_, useZero := decideOverRLEUse(depth)
	if !useZero {
		t.Error("useZero should be true for long zero run")
	}
}

func TestDecideOverRLEUse_LongNonZeroRun(t *testing.T) {
	depth := make([]byte, 20)
	for i := range depth {
		depth[i] = 5
	}
	useNonZero, _ := decideOverRLEUse(depth)
	if !useNonZero {
		t.Error("useNonZero should be true for long same-value run")
	}
}

// --- sort huffmanTreeNode ---

func TestSortHuffmanTreeItems_SortsAscending(t *testing.T) {
	items := []huffmanTreeNode{
		{totalCount: 5, rightOrValue: 0},
		{totalCount: 1, rightOrValue: 1},
		{totalCount: 3, rightOrValue: 2},
		{totalCount: 2, rightOrValue: 3},
	}
	slices.SortFunc(items, func(a, b huffmanTreeNode) int {
		if c := cmp.Compare(a.totalCount, b.totalCount); c != 0 {
			return c
		}
		return cmp.Compare(b.rightOrValue, a.rightOrValue)
	})

	for i := 1; i < len(items); i++ {
		if items[i].totalCount < items[i-1].totalCount {
			t.Errorf("items[%d].totalCount = %d < items[%d].totalCount = %d",
				i, items[i].totalCount, i-1, items[i-1].totalCount)
		}
	}
}

func TestSortHuffmanTreeItems_TieBreaking(t *testing.T) {
	// Equal counts: breaks tie by rightOrValue descending.
	items := []huffmanTreeNode{
		{totalCount: 10, rightOrValue: 2},
		{totalCount: 10, rightOrValue: 5},
		{totalCount: 10, rightOrValue: 1},
	}
	slices.SortFunc(items, func(a, b huffmanTreeNode) int {
		if c := cmp.Compare(a.totalCount, b.totalCount); c != 0 {
			return c
		}
		return cmp.Compare(b.rightOrValue, a.rightOrValue)
	})

	// Descending by value for equal counts.
	if items[0].rightOrValue != 5 ||
		items[1].rightOrValue != 2 ||
		items[2].rightOrValue != 1 {
		t.Errorf("tie breaking wrong: got values %d, %d, %d; want 5, 2, 1",
			items[0].rightOrValue,
			items[1].rightOrValue,
			items[2].rightOrValue)
	}
}

// --- reverseBits ---

func TestReverseBits(t *testing.T) {
	tests := []struct {
		numBits int
		bits    uint16
		want    uint16
	}{
		{1, 0, 0},
		{1, 1, 1},
		{2, 0b01, 0b10},
		{2, 0b10, 0b01},
		{3, 0b101, 0b101},
		{3, 0b110, 0b011},
		{4, 0b1010, 0b0101},
		{8, 0b10110001, 0b10001101},
	}
	for _, tc := range tests {
		got := reverseBits(tc.numBits, tc.bits)
		if got != tc.want {
			t.Errorf("reverseBits(%d, 0b%b) = 0b%b, want 0b%b", tc.numBits, tc.bits, got, tc.want)
		}
	}
}
