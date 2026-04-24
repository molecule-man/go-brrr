package brrr

import "testing"

func TestContextBlockSplitterBasic(t *testing.T) {
	// Create a simple context block splitter with 2 contexts and feed it
	// symbols to verify it produces a valid block split.
	var split blockSplit
	cs := newContextBlockSplitter(&split, 256, 2, 4, 400.0, 100)

	// Feed 20 symbols alternating between contexts 0 and 1.
	for i := range 20 {
		cs.addSymbol(i%10, i%2)
	}
	cs.finishBlock(true)

	if split.numTypes < 1 {
		t.Errorf("numTypes = %d, want >= 1", split.numTypes)
	}
	if len(split.types) == 0 {
		t.Error("types is empty")
	}
	if len(split.lengths) == 0 {
		t.Error("lengths is empty")
	}

	// Total block lengths should be at least 20 (the splitter pads the
	// final block to minBlockSize when blockSize < minBlockSize, matching
	// the C reference behavior).
	var total uint32
	for _, l := range split.lengths {
		total += l
	}
	if total < 20 {
		t.Errorf("total block lengths = %d, want >= 20", total)
	}
}

func TestContextBlockSplitterMaxBlockTypes(t *testing.T) {
	// With numContexts=2, maxBlockTypes should be 128 (256/2).
	var split blockSplit
	cs := newContextBlockSplitter(&split, 256, 2, 4, 400.0, 100)
	if cs.maxBlockTypes != 128 {
		t.Errorf("maxBlockTypes = %d, want 128", cs.maxBlockTypes)
	}

	cs2 := newContextBlockSplitter(&split, 256, 13, 4, 400.0, 100)
	if cs2.maxBlockTypes != 256/13 {
		t.Errorf("maxBlockTypes = %d, want %d", cs2.maxBlockTypes, 256/13)
	}
}

func TestBuildMetaBlockGreedyWithContext(t *testing.T) {
	// Build a metablock with context modeling and verify the output.
	data := []byte("Hello, World! This is a test of context-aware block splitting in brotli. " +
		"The quick brown fox jumps over the lazy dog. Pack my box with five dozen liquor jugs.")
	// Pad to power of 2.
	buf := make([]byte, 256)
	copy(buf, data)
	mask := uint(len(buf) - 1)

	// Create a simple command sequence: one big insert.
	commands := []command{
		{insertLen: uint32(len(data)), copyLen: 0, cmdPrefix: 10},
	}

	var bufs splitBufs
	var mb metaBlockSplit

	buildMetaBlockGreedy(buf, 0, mask, 0, 0,
		2, staticContextMapSimpleUTF8[:],
		commands, &bufs, &mb)

	if mb.litSplit.numTypes < 1 {
		t.Errorf("litSplit.numTypes = %d, want >= 1", mb.litSplit.numTypes)
	}
	if mb.literalContextMap == nil {
		t.Error("literalContextMap is nil, want non-nil for context modeling")
	}
	if len(mb.literalContextMap) != mb.litSplit.numTypes*(1<<literalContextBits) {
		t.Errorf("literalContextMap length = %d, want %d",
			len(mb.literalContextMap), mb.litSplit.numTypes*(1<<literalContextBits))
	}
	// Verify context map entries are valid.
	maxHisto := uint32(mb.litSplit.numTypes * 2) // 2 contexts
	for i, v := range mb.literalContextMap {
		if v >= maxHisto {
			t.Errorf("literalContextMap[%d] = %d, exceeds max %d", i, v, maxHisto-1)
		}
	}
}

func TestBuildMetaBlockGreedySingleContext(t *testing.T) {
	// With numContexts=1, should produce nil context map (same as Q4 path).
	data := []byte("abcdefghijklmnopqrstuvwxyz")
	buf := make([]byte, 32)
	copy(buf, data)
	mask := uint(len(buf) - 1)

	commands := []command{
		{insertLen: uint32(len(data)), copyLen: 0, cmdPrefix: 10},
	}

	var bufs splitBufs
	var mb metaBlockSplit

	buildMetaBlockGreedy(buf, 0, mask, 0, 0,
		1, nil, commands, &bufs, &mb)

	if mb.literalContextMap != nil {
		t.Error("literalContextMap should be nil for single-context path")
	}
	if mb.litSplit.numTypes < 1 {
		t.Errorf("litSplit.numTypes = %d, want >= 1", mb.litSplit.numTypes)
	}
}

func TestMapStaticContexts(t *testing.T) {
	var mb metaBlockSplit
	mb.litSplit.numTypes = 2

	mapStaticContexts(&mb, 2, staticContextMapSimpleUTF8[:])

	wantLen := 2 * (1 << literalContextBits) // 2 * 64 = 128
	if len(mb.literalContextMap) != wantLen {
		t.Fatalf("literalContextMap length = %d, want %d", len(mb.literalContextMap), wantLen)
	}

	// For block type 0: entries should be offset 0 + staticContextMap[j].
	// For block type 1: entries should be offset 2 + staticContextMap[j].
	for j := range 64 {
		want0 := staticContextMapSimpleUTF8[j]
		got0 := mb.literalContextMap[j]
		if got0 != want0 {
			t.Errorf("literalContextMap[0<<6 + %d] = %d, want %d", j, got0, want0)
		}

		want1 := 2 + staticContextMapSimpleUTF8[j]
		got1 := mb.literalContextMap[64+j]
		if got1 != want1 {
			t.Errorf("literalContextMap[1<<6 + %d] = %d, want %d", j, got1, want1)
		}
	}
}
