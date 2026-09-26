package encoder

import (
	"bytes"
	"fmt"
	"math/bits"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unsafe"
)

func compressFragmentFastBefore(
	s *onePassArena,
	input []byte,
	isLast bool,
	table []uint32,
	b *bitWriter,
) {
	c := &fragmentCompressor{
		arena:  s,
		b:      b,
		input:  input,
		table:  table,
		isLast: isLast,
	}
	c.compressBefore()
}

func (c *fragmentCompressor) compressBefore() {
	c.tableBits = uint(bits.Len(uint(len(c.table)))) - 1
	initialBitOffset := c.b.bitOffset

	if len(c.input) == 0 {
		assert(c.isLast)
		c.b.writeBits(1, 1)
		c.b.writeBits(1, 1)
		c.b.byteAlign()
		return
	}

	c.blockSize = min(len(c.input), firstBlockSize)
	c.totalBlockSize = c.blockSize
	c.shift = 64 - c.tableBits
	c.mlenPos = c.b.bitOffset + 3

	c.b.writeMetaBlockHeader(c.blockSize, false, false)
	c.b.writeBits(13, 0)

	c.literalRatio = c.arena.buildAndWriteLiteralPrefixCode(c.input[:c.blockSize], c.b)

	{
		i := uint(0)
		for i+7 < c.arena.cmdCodeNumBits {
			c.b.writeBits(8, uint64(c.arena.cmdCode[i/8]))
			i += 8
		}
		c.b.writeBits(c.arena.cmdCodeNumBits&7, uint64(c.arena.cmdCode[c.arena.cmdCodeNumBits/8]))
	}

	c.writeCommandsBefore()

	if c.b.bitOffset-initialBitOffset > 31+uint(len(c.input))*8 {
		c.writeUncompressedMetaBlock(c.input, initialBitOffset)
	}

	if c.isLast {
		c.b.writeBits(1, 1)
		c.b.writeBits(1, 1)
		c.b.byteAlign()
	}
}

func (c *fragmentCompressor) writeCommandsBefore() {
	c.arena.cmdHisto = cmdHistoSeed
	input := c.input
	table := c.table
	ip := c.pos
	shift := c.shift
	lastDistance := -1
	c.ipEnd = c.pos + c.blockSize

	if c.blockSize < inputMarginBytes {
		c.writeRemainderBefore()
		return
	}

	lenLimit := min(c.blockSize-minMatchLen, len(input)-c.pos-inputMarginBytes)
	ipLimit := c.pos + lenLimit

	ip++
	nextHash := hashFragmentBefore(input, uint(ip), shift)

	for {
		skip := uint32(32)
		nextIP := ip
		var candidate int

		for {
			hash := nextHash
			bytesBetweenHashLookups := skip >> 5
			skip++
			ip = nextIP
			nextIP = ip + int(bytesBetweenHashLookups)
			if nextIP > ipLimit {
				c.writeRemainderBefore()
				return
			}
			nextHash = hashFragmentBefore(input, uint(nextIP), shift)

			candidate = ip - lastDistance
			if uint(candidate) < uint(ip) && isMatch(input, uint(ip), uint(candidate)) {
				table[hash] = uint32(ip)
				if ip-candidate <= maxDistance {
					break
				}
				continue
			}

			candidate = int(table[hash])
			table[hash] = uint32(ip)
			if isMatch(input, uint(ip), uint(candidate)) {
				if ip-candidate <= maxDistance {
					break
				}
				continue
			}
		}

		{
			base := ip
			matched := 5 + matchLenAt(
				input, uint(candidate+5), uint(ip+5), c.ipEnd-ip-5)
			distance := base - candidate
			insert := uint(base - c.nextEmit)
			ip += matched

			switch {
			case insert < 6210:
				c.writeInsertLen(insert)
			case c.shouldUseUncompressedMode(insert):
				c.writeUncompressedMetaBlock(
					input[c.metablockStart:base], c.mlenPos-3)
				c.pos = base
				c.nextEmit = c.pos
				c.nextBlockBefore()
				return
			default:
				c.writeLongInsertLen(insert)
			}
			c.writeLiterals(input[c.nextEmit : c.nextEmit+int(insert)])
			if distance == lastDistance {
				c.b.writeBits(uint(c.arena.cmdDepth[64]), uint64(c.arena.cmdBits[64]))
				c.arena.cmdHisto[64]++
				c.writeCopyLenLastDistance(uint(matched))
			} else {
				c.writeDistanceAndCopyLenLastDistance(uint(distance), uint(matched))
				lastDistance = distance
			}

			c.nextEmit = ip
			if ip >= ipLimit {
				c.writeRemainderBefore()
				return
			}

			candidate = updateHashTableBefore(input, table, ip, shift)
		}

		for isMatch(input, uint(ip), uint(candidate)) {
			base := ip
			matched := 5 + matchLenAt(
				input, uint(candidate+5), uint(ip+5), c.ipEnd-ip-5)
			if ip-candidate > maxDistance {
				break
			}
			ip += matched
			lastDistance = base - candidate
			distance := uint(lastDistance)
			copyLen := uint(matched)
			if copyLen >= 134 {
				c.writeCopyLen(copyLen)
				c.writeDistance(distance)
			} else {
				depth := c.arena.cmdDepth[:]
				cmdBits := c.arena.cmdBits[:]
				histo := c.arena.cmdHisto[:]

				dd := distance + 3
				dNbits := (uint(bits.Len(dd)) - 2) & 63
				dPrefix := (dd >> dNbits) & 1
				dOffset := (2 + dPrefix) << dNbits
				distcode := 2*(dNbits-1) + dPrefix + 80
				dDepth := uint(depth[distcode]) & 63
				dBits := uint64(cmdBits[distcode]) | uint64(dd-dOffset)<<dDepth
				dLen := dDepth + dNbits

				info := copyLenCodeInfo[copyLen]
				ccode := uint(info & 0xFF)
				nbits := uint((info >> 8) & 0xFF)
				cextra := uint(info >> 16)
				d := uint(depth[ccode]) & 63
				cBits := uint64(cmdBits[ccode]) | uint64(cextra)<<d
				cLen := d + nbits

				c.b.writeBits(cLen+dLen, cBits|dBits<<(cLen&63))
				histo[ccode]++
				histo[distcode]++
			}

			c.nextEmit = ip
			if ip >= ipLimit {
				c.writeRemainderBefore()
				return
			}

			candidate = updateHashTableBefore(input, table, ip, shift)
		}

		ip++
		nextHash = hashFragmentBefore(input, uint(ip), shift)
	}
}

func (c *fragmentCompressor) writeRemainderBefore() {
	input := c.input
	c.pos += c.blockSize
	c.blockSize = min(len(input)-c.pos, mergeBlockSize)

	if c.pos < len(input) &&
		c.totalBlockSize+c.blockSize <= (1<<20) &&
		c.arena.shouldMergeBlock(input[c.pos:], c.blockSize, c.arena.litDepth[:]) {
		c.totalBlockSize += c.blockSize
		updateBits(c.b.buf, 20, uint32(c.totalBlockSize-1), c.mlenPos)
		c.writeCommandsBefore()
		return
	}

	if c.nextEmit < c.ipEnd {
		insert := uint(c.ipEnd - c.nextEmit)
		switch {
		case insert < 6210:
			c.writeInsertLen(insert)
			c.writeLiterals(input[c.nextEmit:c.ipEnd])
		case c.shouldUseUncompressedMode(insert):
			c.writeUncompressedMetaBlock(
				input[c.metablockStart:c.ipEnd], c.mlenPos-3)
		default:
			c.writeLongInsertLen(insert)
			c.writeLiterals(input[c.nextEmit:c.ipEnd])
		}
	}
	c.nextEmit = c.ipEnd
	c.nextBlockBefore()
}

func (c *fragmentCompressor) nextBlockBefore() {
	input := c.input
	if c.pos < len(input) {
		c.metablockStart = c.pos
		c.blockSize = min(len(input)-c.pos, firstBlockSize)
		c.totalBlockSize = c.blockSize
		c.mlenPos = c.b.bitOffset + 3
		c.b.writeMetaBlockHeader(c.blockSize, false, false)
		c.b.writeBits(13, 0)
		c.literalRatio = c.arena.buildAndWriteLiteralPrefixCode(
			input[c.pos:c.pos+c.blockSize], c.b)
		c.arena.buildAndWriteCommandPrefixCode(c.b)
		c.writeCommandsBefore()
		return
	}

	if !c.isLast {
		c.arena.cmdCode[0] = 0
		c.arena.cmdCodeNumBits = 0
		cmdCodeStream := &bitWriter{buf: c.arena.cmdCode[:], bitOffset: 0}
		c.arena.buildAndWriteCommandPrefixCode(cmdCodeStream)
		c.arena.cmdCodeNumBits = cmdCodeStream.bitOffset
	}
}

func updateHashTableBefore(input []byte, table []uint32, ip int, shift uint) int {
	tbl := unsafe.Pointer(unsafe.SliceData(table))
	inputBytes := loadU64LE(input, uint(ip-3))
	prevHash := hashBytesAtOffset(inputBytes, 0, shift)
	curHash := hashBytesAtOffset(inputBytes, 3, shift)
	*(*uint32)(unsafe.Add(tbl, uintptr(prevHash)*4)) = uint32(ip - 3)
	prevHash = hashBytesAtOffset(inputBytes, 1, shift)
	*(*uint32)(unsafe.Add(tbl, uintptr(prevHash)*4)) = uint32(ip - 2)
	prevHash = hashBytesAtOffset(inputBytes, 2, shift)
	*(*uint32)(unsafe.Add(tbl, uintptr(prevHash)*4)) = uint32(ip - 1)
	curPtr := (*uint32)(unsafe.Add(tbl, uintptr(curHash)*4))
	candidate := int(*curPtr)
	*curPtr = uint32(ip)
	return candidate
}

func hashFragmentBefore(input []byte, i, shift uint) uint32 {
	h := (loadU64LE(input, i) << 24) * hashMul32
	return uint32(h >> (shift & 63))
}

type fragmentFastInput struct {
	name string
	data []byte
}

func fragmentFastRealInputs(tb testing.TB) []fragmentFastInput {
	tb.Helper()
	var inputs []fragmentFastInput
	paths, err := filepath.Glob(filepath.Join("..", "..", "testdata", "*.*"))
	if err != nil {
		tb.Fatal(err)
	}
	corpus, err := filepath.Glob(filepath.Join("..", "..", "brotli-ref", "tests", "testdata", "*.txt"))
	if err != nil {
		tb.Fatal(err)
	}
	for _, p := range append(paths, corpus...) {
		if filepath.Ext(p) == ".sh" {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			tb.Fatal(err)
		}
		inputs = append(inputs, fragmentFastInput{name: filepath.Base(p), data: data})
	}
	if len(inputs) == 0 {
		tb.Fatal("no real inputs found in testdata; the equivalence check would compare nothing")
	}
	return inputs
}

func fragmentFastRandomBytes(n int, seed uint64) []byte {
	rng := rand.New(rand.NewPCG(seed, 0))
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(rng.IntN(256))
	}
	return out
}

func fragmentFastEdgeInputs() []fragmentFastInput {
	text := bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog; pack my box with five dozen liquor jugs. "), 400)
	far := fragmentFastRandomBytes(300000, 7)
	farRepeat := slices.Concat(far[:60000], fragmentFastRandomBytes(240000, 8), far[:60000], text)
	shortInserts := make([]byte, 0, 200000)
	rng := rand.New(rand.NewPCG(99, 0))
	for len(shortInserts) < 200000 {
		shortInserts = append(shortInserts, "abcdefghij"[rng.IntN(10)])
		if rng.IntN(3) == 0 {
			shortInserts = append(shortInserts, "0123456789"[:5+rng.IntN(6)]...)
		}
	}
	return []fragmentFastInput{
		{name: "empty", data: nil},
		{name: "one_byte", data: []byte("X")},
		{name: "five_bytes", data: []byte("exact")},
		{name: "fifteen_bytes_below_margin", data: []byte("0123456789abcde")},
		{name: "sixteen_bytes_at_margin", data: []byte("0123456789abcdef")},
		{name: "seventeen_repeated", data: bytes.Repeat([]byte("a"), 17)},
		{name: "twenty_two_repeated", data: bytes.Repeat([]byte("ab"), 11)},
		{name: "repeated_a_300k_long_copies_and_merges", data: bytes.Repeat([]byte("a"), 300000)},
		{name: "random_300k_uncompressed_mode", data: fragmentFastRandomBytes(300000, 42)},
		{name: "random_20k_then_text_long_insert", data: slices.Concat(fragmentFastRandomBytes(20000, 3), text)},
		{name: "random_95k_then_run_switches_to_uncompressed_mid_block", data: slices.Concat(fragmentFastRandomBytes(95000, 11), bytes.Repeat([]byte("a"), 3000), text)},
		{name: "text_then_random_40k_then_text", data: slices.Concat(text, fragmentFastRandomBytes(40000, 5), text)},
		{name: "far_repeat_beyond_max_distance", data: farRepeat},
		{name: "short_inserts_between_repeats", data: shortInserts},
		{name: "text_35k", data: text},
	}
}

type fragmentCompressFunc func(s *onePassArena, input []byte, isLast bool, table []uint32, b *bitWriter)

type fragmentFastState struct {
	arena onePassArena
	out   []byte
	b     bitWriter
	table []uint32
}

func newFragmentFastState(inputLen int) *fragmentFastState {
	st := &fragmentFastState{out: make([]byte, 2*inputLen+4096)}
	st.arena.initCommandPrefixCodes()
	st.b = bitWriter{buf: st.out}
	return st
}

func (st *fragmentFastState) compressFragment(fn fragmentCompressFunc, block []byte, isLast bool) {
	st.table = make([]uint32, fastHashTableSize(0, len(block)))
	fn(&st.arena, block, isLast, st.table, &st.b)
}

func fragmentFastCompareStates(before, after *fragmentFastState) []string {
	var diffs []string
	if before.b.bitOffset != after.b.bitOffset {
		diffs = append(diffs, fmt.Sprintf("bit length %d, previous implementation wrote %d", after.b.bitOffset, before.b.bitOffset))
	}
	n := (max(before.b.bitOffset, after.b.bitOffset) + 7) / 8
	if !bytes.Equal(before.out[:n], after.out[:n]) {
		i := 0
		for i < int(n) && before.out[i] == after.out[i] {
			i++
		}
		diffs = append(diffs, fmt.Sprintf("bitstream first differs at byte %d", i))
	}
	if before.arena != after.arena {
		diffs = append(diffs, "arena state (prefix codes, histograms, carried command code) differs, so the next fragment would encode differently")
	}
	if !slices.Equal(before.table, after.table) {
		diffs = append(diffs, "hash table contents differ, so the table slots were not written in the same order")
	}
	return diffs
}

func TestCompressFragmentFastScanLoopAndInlinedHashUpdateMatchPreviousImplementationBitForBit(t *testing.T) {
	inputs := append(fragmentFastRealInputs(t), fragmentFastEdgeInputs()...)
	for _, in := range inputs {
		for _, lgwin := range []uint{10, 16, 18, 24} {
			fragSize := 1 << lgwin
			before := newFragmentFastState(len(in.data))
			after := newFragmentFastState(len(in.data))
			pos := 0
			for {
				end := min(pos+fragSize, len(in.data))
				isLast := end == len(in.data)
				block := in.data[pos:end]
				before.compressFragment(compressFragmentFastBefore, block, isLast)
				after.compressFragment(compressFragmentFast, block, isLast)
				if diffs := fragmentFastCompareStates(before, after); len(diffs) > 0 {
					t.Errorf("%s lgwin=%d fragment at offset %d (len %d, last=%v): q0 output must stay byte-identical to the C reference, but the rewritten writeCommands diverges from the previous implementation: %v",
						in.name, lgwin, pos, len(block), isLast, diffs)
					break
				}
				if isLast {
					break
				}
				pos = end
			}
		}
	}
}

func BenchmarkCompressFragmentFastScanLoop(b *testing.B) {
	impls := []struct {
		name string
		fn   fragmentCompressFunc
	}{
		{"before", compressFragmentFastBefore},
		{"after", compressFragmentFast},
	}
	for _, in := range fragmentFastRealInputs(b) {
		if len(in.data) < 100000 {
			continue
		}
		for _, impl := range impls {
			b.Run(in.name+"/impl="+impl.name, func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(in.data)))
				var arena onePassArena
				out := make([]byte, 2*len(in.data)+1024)
				table := make([]uint32, fastHashTableSize(0, len(in.data)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					arena.initCommandPrefixCodes()
					clear(table)
					out[0] = 0
					bw := bitWriter{buf: out}
					impl.fn(&arena, in.data, true, table, &bw)
				}
			})
		}
	}
}
