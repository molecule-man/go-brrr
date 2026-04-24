package brrr

import "testing"

func TestWriteBits(t *testing.T) {
	type write struct {
		nBits uint
		bits  uint64
	}

	tests := []struct {
		name   string
		writes []write
		want   []byte // expected prefix of the buffer
	}{
		{
			name:   "8 bits at pos 0",
			writes: []write{{8, 0b1010_0101}},
			want:   []byte{0b1010_0101},
		},
		{
			name:   "3 bits at pos 0",
			writes: []write{{3, 0b101}},
			want:   []byte{0b0000_0101},
		},
		{
			name: "two writes within same byte",
			writes: []write{
				{3, 0b101},   // bits 0-2
				{5, 0b11011}, // bits 3-7
			},
			want: []byte{0b11011_101},
		},
		{
			name: "write spanning byte boundary",
			writes: []write{
				{4, 0b1010},      // bits 0-3
				{8, 0b1100_0011}, // bits 4-11, spans bytes 0 and 1
			},
			want: []byte{
				0b0011_1010, // low nibble: first write, high nibble: low 4 bits of second
				0b0000_1100, // high 4 bits of second write
			},
		},
		{
			name: "three writes across two bytes",
			writes: []write{
				{3, 0b101},       // bits 0-2
				{5, 0b11011},     // bits 3-7, fills byte 0
				{8, 0b1111_0000}, // bits 8-15, fills byte 1
			},
			want: []byte{
				0b11011_101,
				0b1111_0000,
			},
		},
		{
			name: "write starting at non-zero byte",
			writes: []write{
				{8, 0b1111_1111}, // byte 0
				{8, 0b0000_0000}, // byte 1
				{4, 0b1010},      // bits 16-19
			},
			want: []byte{
				0b1111_1111,
				0b0000_0000,
				0b0000_1010,
			},
		},
		{
			name: "single bit writes",
			writes: []write{
				{1, 0b1},
				{1, 0b0},
				{1, 0b1},
				{1, 0b1},
				{1, 0b0},
				{1, 0b0},
				{1, 0b1},
				{1, 0b0},
			},
			want: []byte{0b0100_1101},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bs := bitWriter{buf: make([]byte, 64)}

			for _, w := range tt.writes {
				bs.writeBits(w.nBits, w.bits)
			}

			for i, b := range tt.want {
				if bs.buf[i] != b {
					t.Errorf("byte %d: got 0b%08b, want 0b%08b", i, bs.buf[i], b)
				}
			}
		})
	}
}

func TestBitstreamWriteHuffmanTree(t *testing.T) {
	tests := []struct {
		name      string
		depths    []byte
		wantBits  uint
		wantBytes []byte
	}{
		// Small alphabets — no RLE (len <= 50), pure literal encoding.
		{"two_equal", []byte{1, 1}, 40, []byte{0x1c, 0x00, 0x00, 0x00, 0x00}},
		{"three_asymmetric", []byte{1, 2, 2}, 13, []byte{0xdc, 0x19}},
		{"four_balanced", []byte{2, 2, 2, 2}, 40, []byte{0x70, 0x00, 0x00, 0x00, 0x00}},
		{"four_varying", []byte{1, 2, 3, 3}, 18, []byte{0x6c, 0xd7, 0x00}},
		{"gaps_with_zeros", []byte{0, 2, 0, 3, 0, 3}, 25, []byte{0xb0, 0x71, 0xb2, 0x01}},
		{"single_active", []byte{0, 0, 1, 0}, 19, []byte{0x1c, 0x70, 0x04}},
		{"single_depth_value", []byte{5}, 34, []byte{0xc3, 0x01, 0x00, 0x00, 0x00}},
		{"eight_uniform", []byte{3, 3, 3, 3, 3, 3, 3, 3}, 36, []byte{0x1e, 0x00, 0x00, 0x00, 0x00}},
		{"all_different", []byte{1, 2, 3, 4, 5, 6, 7}, 43, []byte{0x4c, 0x45, 0x44, 0xe4, 0x74, 0x07}},

		// Large alphabets (len > 50) — may trigger RLE.
		{"initial_repeat_code_8", repeatByte(8, 60), 40, []byte{0x03, 0x70, 0x00, 0x00, 0x58}},
		{"long_uniform", repeatByte(5, 60), 28, []byte{0xc3, 0xc1, 0xe9, 0x02}},
		{"long_zeros", longZerosDepths(70), 28, []byte{0x6e, 0x70, 0xb9, 0x0c}},
		{"mixed_runs", mixedRunsDepths(80), 47, []byte{0x0a, 0x8e, 0xbb, 0xd1, 0xea, 0x0c}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 4096)
			bs := bitWriter{buf: buf}
			tree := make([]huffmanTreeNode, 2*alphabetSizeCodeLengths+1)

			bs.writeHuffmanTree(tt.depths, tree)

			n := (bs.bitOffset + 7) / 8
			assertBitstream(t, buf[:n], bs.bitOffset, tt.wantBytes, tt.wantBits)
		})
	}
}

func repeatByte(v byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = v
	}
	return b
}

func longZerosDepths(n int) []byte {
	d := make([]byte, n)
	d[0] = 3
	d[n-1] = 4
	return d
}

func mixedRunsDepths(n int) []byte {
	d := make([]byte, n)
	for i := range n / 4 {
		d[i] = 5
	}
	// Middle half is zeros.
	for i := 3 * n / 4; i < n; i++ {
		d[i] = 3
	}
	return d
}
