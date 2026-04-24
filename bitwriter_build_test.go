package brrr

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildAndWriteHuffmanTreeFast(t *testing.T) {
	tests := []struct {
		name      string
		histogram []uint32
		maxBits   uint
	}{
		{
			name:      "1-symbol (a=100)",
			histogram: makeHistogram(256, map[int]uint32{97: 100}),
			maxBits:   8,
		},
		{
			name:      "2-symbol (a=60, b=40)",
			histogram: makeHistogram(256, map[int]uint32{97: 60, 98: 40}),
			maxBits:   8,
		},
		{
			name:      "3-symbol (a=50, b=30, c=20)",
			histogram: makeHistogram(256, map[int]uint32{97: 50, 98: 30, 99: 20}),
			maxBits:   8,
		},
		{
			name:      "4-symbol (a=40, b=30, c=20, d=10)",
			histogram: makeHistogram(256, map[int]uint32{97: 40, 98: 30, 99: 20, 100: 10}),
			maxBits:   8,
		},
		{
			name:      "4-symbol equal (0=25, 1=25, 2=25, 3=25)",
			histogram: makeHistogram(256, map[int]uint32{0: 25, 1: 25, 2: 25, 3: 25}),
			maxBits:   8,
		},
		{
			name:      "256-symbol uniform-ish",
			histogram: makeUniformHistogram256(),
			maxBits:   8,
		},
		{
			name:      "sparse (0=500, 10=200, 100=50, 200=30, 255=10)",
			histogram: makeHistogram(256, map[int]uint32{0: 500, 10: 200, 100: 50, 200: 30, 255: 10}),
			maxBits:   8,
		},
		{
			name:      "1-symbol (0=42)",
			histogram: makeHistogram(256, map[int]uint32{0: 42}),
			maxBits:   8,
		},
		{
			name:      "2-symbol (0=70, 1=30)",
			histogram: makeHistogram(256, map[int]uint32{0: 70, 1: 30}),
			maxBits:   8,
		},
		{
			name:      "9-symbol small alphabet (max_bits=4)",
			histogram: makeHistogram(10, map[int]uint32{0: 100, 1: 50, 2: 25, 3: 12, 4: 6, 5: 3, 6: 2, 7: 1, 8: 1}),
			maxBits:   4,
		},
		{
			name:      "5-symbol (0=40, 1=30, 2=20, 3=7, 4=3)",
			histogram: makeHistogram(256, map[int]uint32{0: 40, 1: 30, 2: 20, 3: 7, 4: 3}),
			maxBits:   8,
		},
		{
			name:      "2-symbol far apart (0=50, 255=50)",
			histogram: makeHistogram(256, map[int]uint32{0: 50, 255: 50}),
			maxBits:   8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 256)
			depth := make([]byte, len(tt.histogram))
			bits := make([]uint16, len(tt.histogram))
			tree := make([]huffmanTreeNode, 2*alphabetSizeLiteral+1)

			b := bitWriter{buf: buf}
			b.buildAndWriteHuffmanTreeFast(tree, tt.histogram, histogramTotal(tt.histogram), tt.maxBits, depth, bits)

			n := (b.bitOffset + 7) / 8
			snap := formatHuffmanTreeSnap(buf[:n], b.bitOffset, depth, bits)
			snapshot(t, snap)
		})
	}
}

func formatHuffmanTreeSnap(data []byte, bits uint, depth []byte, codes []uint16) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s", formatHexSnap(data, bits))
	for i, d := range depth {
		if d != 0 {
			fmt.Fprintf(&b, "depth[%d]=%d bits[%d]=0x%04x\n", i, d, i, codes[i])
		}
	}
	return b.String()
}

func histogramTotal(h []uint32) uint {
	var total uint
	for _, v := range h {
		total += uint(v)
	}
	return total
}

func makeHistogram(size int, nonZero map[int]uint32) []uint32 {
	h := make([]uint32, size)
	for k, v := range nonZero {
		h[k] = v
	}
	return h
}

func makeUniformHistogram256() []uint32 {
	h := make([]uint32, 256)
	vals := []uint32{10, 11, 12, 13, 14}
	for i := range 256 {
		h[i] = vals[i%5]
	}
	return h
}

func TestBuildAndWriteHuffmanTree(t *testing.T) {
	tests := []struct {
		name         string
		histogram    []uint32
		alphabetSize uint
	}{
		{
			name:         "1-symbol (a=100)",
			histogram:    makeHistogram(256, map[int]uint32{97: 100}),
			alphabetSize: 256,
		},
		{
			name:         "2-symbol (a=60, b=40)",
			histogram:    makeHistogram(256, map[int]uint32{97: 60, 98: 40}),
			alphabetSize: 256,
		},
		{
			name:         "3-symbol (a=50, b=30, c=20)",
			histogram:    makeHistogram(256, map[int]uint32{97: 50, 98: 30, 99: 20}),
			alphabetSize: 256,
		},
		{
			name:         "4-symbol (a=40, b=30, c=20, d=10)",
			histogram:    makeHistogram(256, map[int]uint32{97: 40, 98: 30, 99: 20, 100: 10}),
			alphabetSize: 256,
		},
		{
			name:         "4-symbol equal (0=25, 1=25, 2=25, 3=25)",
			histogram:    makeHistogram(256, map[int]uint32{0: 25, 1: 25, 2: 25, 3: 25}),
			alphabetSize: 256,
		},
		{
			name:         "5-symbol (0=40, 1=30, 2=20, 3=7, 4=3)",
			histogram:    makeHistogram(256, map[int]uint32{0: 40, 1: 30, 2: 20, 3: 7, 4: 3}),
			alphabetSize: 256,
		},
		{
			name:         "256-symbol uniform-ish",
			histogram:    makeUniformHistogram256(),
			alphabetSize: 256,
		},
		{
			name:         "sparse (0=500, 10=200, 100=50, 200=30, 255=10)",
			histogram:    makeHistogram(256, map[int]uint32{0: 500, 10: 200, 100: 50, 200: 30, 255: 10}),
			alphabetSize: 256,
		},
		{
			name:         "alphabetSize < len(histogram) (distance scenario)",
			histogram:    makeHistogram(140, map[int]uint32{0: 100, 5: 80, 10: 60, 20: 40, 30: 20, 50: 10}),
			alphabetSize: 64,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 256)
			tree := make([]huffmanTreeNode, 2*len(tt.histogram)+1)

			b := bitWriter{buf: buf}
			depth := make([]byte, len(tt.histogram))
			codes := make([]uint16, len(tt.histogram))
			b.buildAndWriteHuffmanTree(tt.histogram, tt.alphabetSize, tree, depth, codes)

			n := (b.bitOffset + 7) / 8
			snap := formatHuffmanTreeSnap(buf[:n], b.bitOffset, depth, codes)
			snapshot(t, snap)
		})
	}
}
