package brrr

import (
	"bytes"
	"strings"
	"testing"
)

func TestDecideOverLiteralContextModelingShortInput(t *testing.T) {
	// Short input (< 64 bytes) should return 1 context regardless of quality.
	data := []byte("Hello, World!")
	n, cmap := decideOverLiteralContextModeling(data, 0, uint(len(data))-1, uint(len(data)), 5, uint(len(data)))
	if n != 1 {
		t.Errorf("short input: numContexts = %d, want 1", n)
	}
	if cmap != nil {
		t.Errorf("short input: context map should be nil")
	}
}

func TestDecideOverLiteralContextModelingLowQuality(t *testing.T) {
	// Quality < 5 should return 1 context.
	data := bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog. "), 100)
	n, cmap := decideOverLiteralContextModeling(data, 0, uint(len(data))-1, uint(len(data)), 4, uint(len(data)))
	if n != 1 {
		t.Errorf("low quality: numContexts = %d, want 1", n)
	}
	if cmap != nil {
		t.Errorf("low quality: context map should be nil")
	}
}

func TestDecideOverLiteralContextModelingASCIIText(t *testing.T) {
	// ASCII English text at quality 5 should typically get 2 contexts
	// (SimpleUTF8) since it has good ASCII bigram structure.
	text := strings.Repeat("The quick brown fox jumps over the lazy dog. Pack my box with five dozen liquor jugs. ", 200)
	data := []byte(text)
	mask := uint(len(data)) - 1
	if len(data)&(len(data)-1) != 0 {
		// Ensure power-of-2 size for proper masking.
		buf := make([]byte, 1<<17) // 128 KB
		copy(buf, data)
		data = buf
		mask = uint(len(data)) - 1
	}

	n, cmap := decideOverLiteralContextModeling(data, 0, mask, uint(len(text)), 5, uint(len(text)))
	if n < 1 || n > 13 {
		t.Errorf("ASCII text: numContexts = %d, want 1..13", n)
	}
	if n > 1 && cmap == nil {
		t.Errorf("ASCII text: numContexts=%d but context map is nil", n)
	}
	if n > 1 && len(cmap) != 64 {
		t.Errorf("ASCII text: context map length = %d, want 64", len(cmap))
	}
}

func TestDecideOverLiteralContextModelingReturnsValidContextMap(t *testing.T) {
	// Verify that when a context map is returned, all entries are < numContexts.
	text := strings.Repeat("Hello world! This is a test of context modeling for brotli. ", 200)
	data := make([]byte, 1<<17)
	copy(data, text)
	mask := uint(len(data)) - 1

	n, cmap := decideOverLiteralContextModeling(data, 0, mask, uint(len(text)), 5, uint(len(text)))
	if n > 1 {
		for i, v := range cmap {
			if v >= uint32(n) {
				t.Errorf("contextMap[%d] = %d, exceeds numContexts-1 (%d)", i, v, n-1)
			}
		}
	}
}

func TestEstimateEntropy(t *testing.T) {
	// Uniform distribution: all symbols equally likely.
	hist := [4]uint32{10, 10, 10, 10}
	e := estimateEntropy(hist[:])
	// 40 symbols, 4 equally likely → 2 bits/symbol → 80 bits total.
	if e < 79 || e > 81 {
		t.Errorf("uniform: estimateEntropy = %.2f, want ~80", e)
	}

	// Single symbol: zero entropy.
	hist2 := [4]uint32{100, 0, 0, 0}
	e2 := estimateEntropy(hist2[:])
	if e2 != 0 {
		t.Errorf("single symbol: estimateEntropy = %.2f, want 0", e2)
	}

	// Empty histogram.
	hist3 := [4]uint32{0, 0, 0, 0}
	e3 := estimateEntropy(hist3[:])
	if e3 != 0 {
		t.Errorf("empty: estimateEntropy = %.2f, want 0", e3)
	}
}

func TestByteCategoryClassification(t *testing.T) {
	tests := []struct {
		b    byte
		want uint32
	}{
		{0, 0},   // control: 0x00..0x3F → category 0
		{' ', 0}, // space is 0x20 → category 0
		{'?', 0}, // 0x3F → category 0
		{'@', 0}, // 0x40 → top 2 bits = 01 → category 0
		{'A', 0}, // 0x41 → top 2 bits = 01 → category 0
		{'z', 0}, // 0x7A → top 2 bits = 01 → category 0
		{127, 0}, // 0x7F → top 2 bits = 01 → category 0
		{128, 1}, // 0x80 → top 2 bits = 10 → category 1
		{191, 1}, // 0xBF → top 2 bits = 10 → category 1
		{192, 2}, // 0xC0 → top 2 bits = 11 → category 2
		{255, 2}, // 0xFF → top 2 bits = 11 → category 2
	}
	for _, tt := range tests {
		got := byteCategory(tt.b)
		if got != tt.want {
			t.Errorf("byteCategory(0x%02X) = %d, want %d", tt.b, got, tt.want)
		}
	}
}

func TestStaticContextMapValues(t *testing.T) {
	// Verify the three static context maps have 64 entries and valid cluster indices.
	check := func(name string, m [64]uint32, maxCluster uint32) {
		for i, v := range m {
			if v > maxCluster {
				t.Errorf("%s[%d] = %d, exceeds max cluster %d", name, i, v, maxCluster)
			}
		}
	}
	check("staticContextMapSimpleUTF8", staticContextMapSimpleUTF8, 1)
	check("staticContextMapContinuation", staticContextMapContinuation, 2)
	check("staticContextMapComplexUTF8", staticContextMapComplexUTF8, 12)
}

func TestChooseContextMapNoGain(t *testing.T) {
	// When all bigram categories are uniform, entropy gain is minimal → 1 context.
	var histo [9]uint32
	for i := range histo {
		histo[i] = 100
	}
	var numCtx uint
	var cmap []uint32
	numCtx = 1
	chooseContextMap(5, &histo, &numCtx, &cmap)
	if numCtx != 1 {
		t.Errorf("uniform bigrams: numContexts = %d, want 1", numCtx)
	}
}

func TestShouldUseComplexStaticContextMapSmallInput(t *testing.T) {
	// Input < 1 MB should not trigger complex context map.
	data := make([]byte, 500_000)
	for i := range data {
		data[i] = byte(i % 256)
	}
	var numCtx uint
	var cmap []uint32
	ok := shouldUseComplexStaticContextMap(data, 0, uint(len(data))-1, uint(len(data)), uint(len(data)), &numCtx, &cmap)
	if ok {
		t.Error("small input should not use complex context map")
	}
}
