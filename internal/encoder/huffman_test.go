package encoder

import (
	"cmp"
	"math"
	"slices"
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

// --- setDepth ---

func TestSetDepth_SingleLeaf(t *testing.T) {
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
	pool := []huffmanTreeNode{
		{left: -1, rightOrValue: 0},
		{left: -1, rightOrValue: 1},
		{left: 0, rightOrValue: 1},
		{left: -1, rightOrValue: 2},
		{left: -1, rightOrValue: 3},
		{left: 3, rightOrValue: 4},
		{left: 2, rightOrValue: 5},
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
	pool := []huffmanTreeNode{
		{left: -1, rightOrValue: 0},
		{left: -1, rightOrValue: 1},
		{left: -1, rightOrValue: 2},
		{left: 0, rightOrValue: 1},
		{left: 3, rightOrValue: 2},
		{left: -1, rightOrValue: 3},
		{left: 4, rightOrValue: 5},
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
	ok := setDepth(pool, depth, 6, 2)
	if ok {
		t.Fatal("setDepth should return false when tree exceeds maxDepth")
	}
}

func TestSetDepth_ExactlyAtMaxDepth(t *testing.T) {
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
	data := []uint32{1000, 1, 1, 1}
	tree := make([]huffmanTreeNode, 2*len(data)+1)
	depth := make([]byte, len(data))
	createHuffmanTree(data, 15, tree, depth)

	if depth[0] >= depth[1] {
		t.Errorf("expected depth[0] (%d) < depth[1] (%d) for skewed frequencies", depth[0], depth[1])
	}
	verifyKraftInequality(t, depth, data)
}

func TestCreateHuffmanTree_RespectsTreeLimit(t *testing.T) {
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
	counts := make([]uint32, 40)
	for i := range counts {
		counts[i] = 3
	}
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
	counts := make([]uint32, 64)
	for i := range counts {
		counts[i] = 100
	}
	counts[10] = 101
	counts[11] = 99
	counts[12] = 100
	counts[13] = 102

	orig := make([]uint32, len(counts))
	copy(orig, counts)

	optimizeHuffmanCountsForRLE(counts, new([]bool))

	for i := range 60 {
		if counts[i] == 0 {
			t.Errorf("counts[%d] became 0 after smoothing", i)
		}
	}
}

// --- encodeHuffmanTree ---

func TestEncodeHuffmanTree_ShortNonZero(t *testing.T) {
	depth := []byte{1, 2, 3, 3}
	tree := make([]byte, 256)
	extraBits := make([]byte, 256)

	treeSize := encodeHuffmanTree(depth, tree, extraBits)

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

	if treeSize != 0 {
		t.Fatalf("treeSize = %d, want 0", treeSize)
	}
}

func TestEncodeHuffmanTree_RepeatPreviousCode(t *testing.T) {
	depth := make([]byte, 60)
	for i := range depth {
		depth[i] = 5
	}
	tree := make([]byte, 256)
	extraBits := make([]byte, 256)

	treeSize := encodeHuffmanTree(depth, tree, extraBits)

	found16 := false
	for i := range treeSize {
		if tree[i] == core.RepeatPreviousCodeLength {
			found16 = true
			break
		}
	}
	if !found16 {
		t.Error("expected repeat-previous code (16) for long run of same nonzero value")
	}
	if treeSize >= 60 {
		t.Errorf("treeSize = %d, expected compression below 60", treeSize)
	}
}

func TestEncodeHuffmanTree_RepeatZeroCode(t *testing.T) {
	depth := make([]byte, 70)
	depth[0] = 3
	depth[69] = 4
	tree := make([]byte, 512)
	extraBits := make([]byte, 512)

	treeSize := encodeHuffmanTree(depth, tree, extraBits)

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
	depth := make([]byte, 80)
	for i := range 20 {
		depth[i] = 5
	}
	for i := 60; i < 80; i++ {
		depth[i] = 3
	}
	tree := make([]byte, 512)
	extraBits := make([]byte, 512)

	treeSize := encodeHuffmanTree(depth, tree, extraBits)

	if treeSize == 0 {
		t.Fatal("treeSize = 0")
	}
	if treeSize >= 80 {
		t.Errorf("treeSize = %d, expected compression below 80", treeSize)
	}
}

// --- convertBitDepthsToSymbols ---

func TestConvertBitDepthsToSymbols_TwoSymbolsDepth1(t *testing.T) {
	depth := []byte{1, 1}
	bitsOut := make([]uint16, 2)
	convertBitDepthsToSymbols(depth, bitsOut)

	if bitsOut[0] != 0 {
		t.Errorf("bits[0] = %d, want 0", bitsOut[0])
	}
	if bitsOut[1] != 1 {
		t.Errorf("bits[1] = %d, want 1", bitsOut[1])
	}
}

func TestConvertBitDepthsToSymbols_ThreeSymbols(t *testing.T) {
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
	if bitsOut[1] != 0 {
		t.Errorf("bits[1] = %d, want 0 (reversed 00)", bitsOut[1])
	}
	if bitsOut[3] != 2 {
		t.Errorf("bits[3] = %d, want 2 (reversed 01 = 10)", bitsOut[3])
	}
}

func TestConvertBitDepthsToSymbols_VaryingDepths(t *testing.T) {
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
	tree := make([]byte, 32)
	extraBitsData := make([]byte, 32)
	treeSize := encodeHuffmanTreeRepetitions(tree, extraBitsData, 5, 5, 10)

	found := false
	for i := range treeSize {
		if tree[i] == core.RepeatPreviousCodeLength {
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
