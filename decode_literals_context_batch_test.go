package brrr

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unsafe"

	"github.com/molecule-man/go-brrr/internal/core"
)

func decodeLiteralsContextBatchBefore(
	dst []byte, n int,
	ptrs *[64]unsafe.Pointer,
	contextLookup []byte, p1, p2 byte,
	br *bitReader,
) (byte, byte) {
	val := br.val
	bitPos := br.bitPos
	brPos := br.pos
	inputBase := br.inputBase
	ctxBase := unsafe.Pointer(unsafe.SliceData(contextLookup))
	dstPtr := unsafe.Pointer(unsafe.SliceData(dst))

	for n > 0 {
		n--
		if bitPos < core.HuffmanMaxCodeLength {
			val |= *(*uint64)(unsafe.Add(inputBase, brPos)) << (bitPos & 63)
			brPos += int((63 - bitPos) >> 3)
			bitPos |= 56
		}

		ctx := *(*byte)(unsafe.Add(ctxBase, int(p1))) | *(*byte)(unsafe.Add(ctxBase, 256+int(p2)))
		tableBase := ptrs[ctx&63]

		idx := val & huffmanTableMask
		raw := *(*uint32)(unsafe.Add(tableBase, idx*4))
		drop := uint(raw & 0xFF)
		value := uint(raw >> 16)
		if drop > huffmanTableBits {
			nbits := drop - huffmanTableBits
			idx2 := idx + uint64(value) + ((val >> huffmanTableBits) & bitMask(nbits))
			raw = *(*uint32)(unsafe.Add(tableBase, idx2*4))
			drop = huffmanTableBits + uint(raw&0xFF)
			value = uint(raw >> 16)
		}
		bitPos -= drop
		val >>= drop & 63

		p2 = p1
		p1 = byte(value)
		*(*byte)(dstPtr) = p1
		dstPtr = unsafe.Add(dstPtr, 1)
	}

	br.val = val
	br.bitPos = bitPos
	br.pos = brPos
	return p1, p2
}

type contextLiteralBatchCase struct {
	stream string
	wrap   int
	codes  []core.HuffmanCode
	ptrs   [64]unsafe.Pointer
	lookup []byte
	br     bitReader
	p1, p2 byte
	n      int
}

func captureContextLiteralBatchCases(tb testing.TB, stream string, compressed []byte) []contextLiteralBatchCase {
	tb.Helper()
	s := new(decodeState)
	s.init()
	s.br.setInput(compressed)
	var out []byte
	var cases []contextLiteralBatchCase
	for {
		switch s.decompressStream(&out) {
		case decoderResultSuccess:
			return cases
		case decoderResultNeedsMoreOutput:
			n := min(1<<16, (s.br.availIn()-8)/2)
			if s.trivialLiteralContext == 0 && n > 0 {
				c := contextLiteralBatchCase{
					stream: stream,
					wrap:   len(cases),
					codes:  slices.Clone(s.literalHGroup.codes),
					lookup: s.contextLookup,
					br:     s.br,
					p1:     s.ringbuffer[(s.pos-1)&s.ringbufferMask],
					p2:     s.ringbuffer[(s.pos-2)&s.ringbufferMask],
					n:      n,
				}
				for ctx, off := range s.literalCodesOffsets {
					c.ptrs[ctx] = unsafe.Pointer(&c.codes[off])
				}
				cases = append(cases, c)
			}
			out = s.flushOutput(out[:0])
		default:
			tb.Fatalf("%s: decoding the freshly compressed stream failed (%v), so no literal batch inputs can be captured", stream, s.err)
		}
	}
}

func contextLiteralBatchCases(tb testing.TB, qualities ...int) []contextLiteralBatchCase {
	tb.Helper()
	paths := []string{
		"testdata/gh_172KB.html",
		"testdata/reactcore_187KB.js",
		"brotli-ref/tests/testdata/plrabn12.txt",
		"brotli-ref/tests/testdata/lcet10.txt",
		"brotli-ref/tests/testdata/mapsdatazrh",
	}
	cases := make([]contextLiteralBatchCase, 0, len(paths)*len(qualities))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			tb.Fatal(err)
		}
		for _, q := range qualities {
			var buf bytes.Buffer
			w, err := NewWriterOptions(&buf, q, WriterOptions{LGWin: 16})
			if err != nil {
				tb.Fatal(err)
			}
			if _, err := w.Write(data); err != nil {
				tb.Fatal(err)
			}
			if err := w.Close(); err != nil {
				tb.Fatal(err)
			}
			name := fmt.Sprintf("%s/q=%d", filepath.Base(path), q)
			cases = append(cases, captureContextLiteralBatchCases(tb, name, buf.Bytes())...)
		}
	}
	return cases
}

func compareContextLiteralBatch(c contextLiteralBatchCase, lookup []byte, p1, p2 byte, n int) error {
	brBefore, brAfter := c.br, c.br
	dstBefore, dstAfter := make([]byte, n+ringBufferWriteAheadSlack), make([]byte, n+ringBufferWriteAheadSlack)
	b1, b2 := decodeLiteralsContextBatchBefore(dstBefore, n, &c.ptrs, lookup, p1, p2, &brBefore)
	a1, a2 := decodeLiteralsContextBatch(dstAfter, n, &c.ptrs, lookup, p1, p2, &brAfter)
	var errs []error
	if !bytes.Equal(dstBefore, dstAfter) {
		i := 0
		for dstAfter[i] == dstBefore[i] {
			i++
		}
		errs = append(errs, fmt.Errorf("literal %d decoded as %#x, before %#x", i, dstAfter[i], dstBefore[i]))
	}
	if a1 != b1 || a2 != b2 {
		errs = append(errs, fmt.Errorf("returned context bytes (%#x, %#x), before (%#x, %#x)", a1, a2, b1, b2))
	}
	if brAfter.val != brBefore.val || brAfter.bitPos != brBefore.bitPos || brAfter.pos != brBefore.pos {
		errs = append(errs, fmt.Errorf("bit reader left at (val=%#x bitPos=%d pos=%d), before (val=%#x bitPos=%d pos=%d)",
			brAfter.val, brAfter.bitPos, brAfter.pos, brBefore.val, brBefore.bitPos, brBefore.pos))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("%s wrap=%d p1=%#x p2=%#x n=%d: %w", c.stream, c.wrap, p1, p2, n, err)
	}
	return nil
}

func TestDecodeLiteralsContextBatchWithUintContextBytesAndUnmaskedTableIndexDecodesExactlyLikeTheByteVersion(t *testing.T) {
	cases := contextLiteralBatchCases(t, 5, 9, 11)
	if len(cases) == 0 {
		t.Fatal("no context-modelled literal batch was captured from the q5/q9/q11 streams, so the equivalence check would prove nothing")
	}
	errs := make([]error, 0, 19*len(cases))
	for _, c := range cases {
		errs = append(errs,
			compareContextLiteralBatch(c, c.lookup, c.p1, c.p2, c.n),
			compareContextLiteralBatch(c, c.lookup, c.p1, c.p2, 0),
			compareContextLiteralBatch(c, c.lookup, c.p1, c.p2, 1),
		)
		for mode := range 4 {
			for _, p := range [][2]byte{{0, 0}, {0xFF, 0xFF}, {0, 0xFF}, {0xFF, 0}} {
				errs = append(errs, compareContextLiteralBatch(c, core.ContextLookupTable[mode<<9:], p[0], p[1], min(c.n, 256)))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("decodeLiteralsContextBatch diverged from the pre-change byte-context version on %d captured batches:\n%v", len(cases), err)
	}
	t.Logf("%d captured batches", len(cases))
}

func BenchmarkDecodeLiteralsContextBatch(b *testing.B) {
	cases := contextLiteralBatchCases(b, 5, 9, 11)
	impls := []struct {
		name string
		fn   func([]byte, int, *[64]unsafe.Pointer, []byte, byte, byte, *bitReader) (byte, byte)
	}{
		{"before", decodeLiteralsContextBatchBefore},
		{"after", decodeLiteralsContextBatch},
	}
	benched := map[string]bool{}
	for _, c := range cases {
		if c.n < 1<<16 || benched[c.stream] {
			continue
		}
		benched[c.stream] = true
		dst := make([]byte, c.n+ringBufferWriteAheadSlack)
		br := new(bitReader)
		for _, impl := range impls {
			b.Run(c.stream+"/impl="+impl.name, func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(c.n))
				for i := 0; i < b.N; i++ {
					*br = c.br
					impl.fn(dst, c.n, &c.ptrs, c.lookup, c.p1, c.p2, br)
				}
			})
		}
	}
}
