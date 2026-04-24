package brrr

import (
	"math/rand"
	"testing"
)

func TestCountLiterals(t *testing.T) {
	cmds := []command{
		{insertLen: 5},
		{insertLen: 3},
		{insertLen: 0},
		{insertLen: 10},
	}
	got := countLiterals(cmds)
	if got != 18 {
		t.Errorf("countLiterals = %d, want 18", got)
	}
}

func TestCountLiteralsEmpty(t *testing.T) {
	got := countLiterals(nil)
	if got != 0 {
		t.Errorf("countLiterals(nil) = %d, want 0", got)
	}
}

func TestCopyLiteralsToByteArray(t *testing.T) {
	// Set up a ring buffer with known data.
	data := []byte("Hello, World! This is a test of the ring buffer extraction.")
	mask := uint(63) // buffer size 64
	pos := uint(0)

	// Commands that insert consecutive literals from the buffer.
	// insertLen=5 → "Hello", then copyLen=0 so pos advances by 5.
	// insertLen=8 → ", World!", then copyLen=0 so pos advances by 8.
	cmds := []command{
		{insertLen: 5, copyLen: 0},
		{insertLen: 8, copyLen: 0},
	}

	got := copyLiteralsToByteArray(cmds, data, pos, mask)
	want := "Hello, World!"
	if string(got) != want {
		t.Errorf("copyLiteralsToByteArray = %q, want %q", string(got), want)
	}
}

func TestCopyLiteralsToByteArrayWrapAround(t *testing.T) {
	// Small ring buffer that wraps around.
	bufSize := 16
	mask := uint(bufSize - 1)
	data := make([]byte, bufSize)
	// Fill buffer: positions 12..15 = "WRAP", positions 0..3 = "PING"
	copy(data[12:], []byte("WRAP"))
	copy(data[0:], []byte("PING"))

	// Command starts at position 12, insert length 8 → wraps around.
	cmds := []command{
		{insertLen: 8, copyLen: 0},
	}
	got := copyLiteralsToByteArray(cmds, data, 12, mask)
	if string(got) != "WRAPPING" {
		t.Errorf("wrap-around: got %q, want %q", string(got), "WRAPPING")
	}
}

func TestSplitRand(t *testing.T) {
	seed := uint32(7)
	// Verify deterministic sequence.
	v1 := splitRand(&seed)
	v2 := splitRand(&seed)
	if v1 == v2 {
		t.Error("splitRand produced same value twice")
	}
	// Verify same seed produces same sequence.
	seed2 := uint32(7)
	if splitRand(&seed2) != v1 {
		t.Error("splitRand not deterministic")
	}
}

func TestSymbolBitCost(t *testing.T) {
	if got := symbolBitCost(0); got != -2.0 {
		t.Errorf("symbolBitCost(0) = %f, want -2.0", got)
	}
	if got := symbolBitCost(1); got != 0.0 {
		t.Errorf("symbolBitCost(1) = %f, want 0.0", got)
	}
	if got := symbolBitCost(4); got < 1.9 || got > 2.1 {
		t.Errorf("symbolBitCost(4) = %f, want ~2.0", got)
	}
}

func TestFindBlocksTrivial(t *testing.T) {
	// Single histogram → all block IDs should be 0.
	data := make([]uint16, 100)
	for i := range data {
		data[i] = uint16(i % 10)
	}
	blockID := make([]byte, 100)
	numBlocks := findBlocks(data, nil, nil, nil, nil, blockID, 100, 1, 256, 28.0)
	if numBlocks != 1 {
		t.Errorf("findBlocks with 1 histogram: numBlocks = %d, want 1", numBlocks)
	}
	for i, id := range blockID {
		if id != 0 {
			t.Errorf("blockID[%d] = %d, want 0", i, id)
			break
		}
	}
}

func TestFindBlocksTwoDistributions(t *testing.T) {
	// Create data from two very different distributions.
	length := 2000
	data := make([]uint16, length)
	// First half: symbols 0-9
	for i := 0; i < length/2; i++ {
		data[i] = uint16(i % 10)
	}
	// Second half: symbols 200-209
	for i := length / 2; i < length; i++ {
		data[i] = uint16(200 + i%10)
	}

	alphabetSize := 256
	numHistograms := 2
	histograms := make([]uint32, numHistograms*alphabetSize)

	// Build histograms from each half.
	for i := 0; i < length/2; i++ {
		histograms[0*alphabetSize+int(data[i])]++
	}
	for i := length / 2; i < length; i++ {
		histograms[1*alphabetSize+int(data[i])]++
	}

	insertCost := make([]float64, alphabetSize*numHistograms)
	cost := make([]float64, numHistograms)
	bitmapLen := (numHistograms + 7) >> 3
	switchSignal := make([]byte, length*bitmapLen)
	blockID := make([]byte, length)

	numBlocks := findBlocks(data,
		histograms, insertCost, cost, switchSignal, blockID,
		length, numHistograms, alphabetSize, 28.0)

	if numBlocks < 2 {
		t.Errorf("findBlocks on two distributions: numBlocks = %d, want >= 2", numBlocks)
	}

	// Verify the two halves are assigned to different block types.
	firstType := blockID[0]
	secondType := blockID[length-1]
	if firstType == secondType {
		t.Error("findBlocks: both halves assigned to same block type")
	}
}

func TestRemapBlockIDs(t *testing.T) {
	blockIDs := []byte{3, 3, 7, 7, 3, 0}
	newID := make([]uint16, 8) // numHistograms = 8
	n := remapBlockIDs(blockIDs, len(blockIDs), newID, 8)
	if n != 3 {
		t.Errorf("remapBlockIDs: got %d types, want 3", n)
	}
	// First appearing ID (3) should map to 0.
	if blockIDs[0] != 0 {
		t.Errorf("blockIDs[0] = %d, want 0", blockIDs[0])
	}
	// Second appearing ID (7) should map to 1.
	if blockIDs[2] != 1 {
		t.Errorf("blockIDs[2] = %d, want 1", blockIDs[2])
	}
	// Third appearing ID (0) should map to 2.
	if blockIDs[5] != 2 {
		t.Errorf("blockIDs[5] = %d, want 2", blockIDs[5])
	}
}

func TestSplitByteVectorShort(t *testing.T) {
	// Input shorter than minLengthForBlockSplitting → single block.
	data := make([]uint16, 50)
	for i := range data {
		data[i] = uint16(i % 10)
	}
	var split blockSplit
	splitByteVector(&split, &q10Bufs{}, data, len(data), splitVecParams{544, 100, 70, 28.1, 10, 256})

	if split.numTypes != 1 {
		t.Errorf("short input: numTypes = %d, want 1", split.numTypes)
	}
	if len(split.lengths) != 1 || split.lengths[0] != 50 {
		t.Errorf("short input: lengths = %v, want [50]", split.lengths)
	}
}

func TestSplitByteVectorEmpty(t *testing.T) {
	var split blockSplit
	splitByteVector(&split, &q10Bufs{}, nil, 0, splitVecParams{544, 100, 70, 28.1, 10, 256})
	if split.numTypes != 1 {
		t.Errorf("empty input: numTypes = %d, want 1", split.numTypes)
	}
}

func TestSplitByteVectorAlternatingDistributions(t *testing.T) {
	// Create data alternating between two very different distributions
	// in chunks of 500 symbols each.
	rng := rand.New(rand.NewSource(42))
	length := 5000
	data := make([]uint16, length)
	for i := range length {
		chunk := i / 500
		if chunk%2 == 0 {
			data[i] = uint16(rng.Intn(10)) // symbols 0-9
		} else {
			data[i] = uint16(200 + rng.Intn(10)) // symbols 200-209
		}
	}

	var split blockSplit
	splitByteVector(&split, &q10Bufs{}, data, length, splitVecParams{544, 100, 70, 28.1, 10, 256})

	// Should detect multiple block types.
	if split.numTypes < 2 {
		t.Errorf("alternating distributions: numTypes = %d, want >= 2", split.numTypes)
	}
}

func TestSplitBlockEndToEnd(t *testing.T) {
	// Create a simple set of commands and test that splitBlock produces
	// reasonable results.
	data := make([]byte, 4096)
	rng := rand.New(rand.NewSource(123))
	for i := range data {
		data[i] = byte(rng.Intn(256))
	}
	mask := uint(len(data) - 1)

	// Create commands with varying insert lengths and copy lengths.
	var cmds []command
	pos := uint(0)
	for pos < 2000 {
		insertLen := uint(5 + rng.Intn(20))
		if pos+insertLen > 2000 {
			insertLen = 2000 - pos
		}
		copyLen := uint(2 + rng.Intn(10))
		if pos+insertLen+copyLen > uint(len(data)) {
			copyLen = 2
		}
		cmd := newCommand(commandConfig{
			insertLen:      insertLen,
			copyLen:        copyLen,
			distanceCode:   numDistanceShortCodes + 1,
			numDirectCodes: 0,
			postfixBits:    0,
		})
		cmds = append(cmds, cmd)
		pos += insertLen + copyLen
	}

	var litSplit, cmdSplit, distSplit blockSplit
	splitBlock(&litSplit, &cmdSplit, &distSplit, &q10Bufs{}, cmds, data, 0, mask, 10)

	// Basic sanity: all splits should have at least 1 type.
	if litSplit.numTypes < 1 {
		t.Errorf("litSplit.numTypes = %d, want >= 1", litSplit.numTypes)
	}
	if cmdSplit.numTypes < 1 {
		t.Errorf("cmdSplit.numTypes = %d, want >= 1", cmdSplit.numTypes)
	}
	if distSplit.numTypes < 1 {
		t.Errorf("distSplit.numTypes = %d, want >= 1", distSplit.numTypes)
	}

	// Verify block lengths sum correctly.
	litTotal := countLiterals(cmds)
	var litSum uint32
	for _, l := range litSplit.lengths {
		litSum += l
	}
	if int(litSum) != litTotal {
		t.Errorf("literal block lengths sum = %d, want %d", litSum, litTotal)
	}

	var cmdSum uint32
	for _, l := range cmdSplit.lengths {
		cmdSum += l
	}
	if int(cmdSum) != len(cmds) {
		t.Errorf("command block lengths sum = %d, want %d", cmdSum, len(cmds))
	}
}
