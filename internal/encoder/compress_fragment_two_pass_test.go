package encoder

import (
	"bytes"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/molecule-man/go-brrr/internal/creftest"
)

// compressTwoPassForTest is a helper that compresses input with
// compressFragmentTwoPass and returns the bitstream result (including
// the brotli stream header for window size 18).
func compressTwoPassForTest(t *testing.T, input []byte, tableBits uint) []byte {
	t.Helper()
	var s twoPassArena
	tableSize := 1 << tableBits
	table := make([]uint32, tableSize)
	commandBuf := make([]uint32, twoPassBlockSize)
	literalBuf := make([]byte, twoPassBlockSize)
	bufSize := len(input)*2 + 1024
	buf := make([]byte, bufSize)
	b := bitWriter{buf: buf}

	// Write the brotli stream header for window size 18.
	b.writeBits(4, 3)

	compressFragmentTwoPass(&s, input, true, commandBuf, literalBuf, table, &b)
	n := (b.bitOffset + 7) / 8
	return buf[:n]
}

func TestCompressFragmentTwoPassRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		input     []byte
		tableBits uint
	}{
		{
			name:      "hello_world",
			input:     []byte("Hello, World!"),
			tableBits: 9,
		},
		{
			name:      "repeated_a_1000",
			input:     bytes.Repeat([]byte("a"), 1000),
			tableBits: 11,
		},
		{
			name:      "repeated_pattern_2000",
			input:     bytes.Repeat([]byte("abcdefghij"), 200),
			tableBits: 11,
		},
		{
			name:      "sequential_512",
			input:     sequentialBytesRT(512),
			tableBits: 11,
		},
		{
			name:      "pseudo_random_2048",
			input:     pseudoRandomBytesRT(2048, 42),
			tableBits: 13,
		},
		{
			name:      "fox_sentence_100x",
			input:     bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog. "), 100),
			tableBits: 15,
		},
		{
			name:      "single_byte",
			input:     []byte("X"),
			tableBits: 9,
		},
		{
			name:      "five_bytes_exact",
			input:     []byte("exact"),
			tableBits: 9,
		},
		{
			name:      "table_bits_8",
			input:     bytes.Repeat([]byte("test8 "), 100),
			tableBits: 8,
		},
		{
			name:      "table_bits_10",
			input:     bytes.Repeat([]byte("test10 "), 100),
			tableBits: 10,
		},
		{
			name:      "table_bits_12",
			input:     bytes.Repeat([]byte("test12 "), 100),
			tableBits: 12,
		},
		{
			name:      "table_bits_14",
			input:     bytes.Repeat([]byte("test14 "), 100),
			tableBits: 14,
		},
		{
			name:      "table_bits_16",
			input:     bytes.Repeat([]byte("test16 "), 500),
			tableBits: 16,
		},
		{
			name:      "table_bits_17",
			input:     bytes.Repeat([]byte("test17 "), 500),
			tableBits: 17,
		},
		{
			name:      "large_block",
			input:     bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz"), 6000),
			tableBits: 13,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed := compressTwoPassForTest(t, tt.input, tt.tableBits)
			decompressed := creftest.BrotliDecompress(t, compressed)
			if !bytes.Equal(decompressed, tt.input) {
				t.Errorf("round-trip mismatch: got %d bytes, want %d bytes",
					len(decompressed), len(tt.input))
				if len(decompressed) < 200 && len(tt.input) < 200 {
					t.Errorf("got  %q", decompressed)
					t.Errorf("want %q", tt.input)
				}
			}
		})
	}
}

type q1Input struct {
	name string
	data []byte
}

func loadQ1Corpus(tb testing.TB) []q1Input {
	tb.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "brotli-ref", "tests", "testdata", "*.txt"))
	if err != nil {
		tb.Fatal(err)
	}
	ins := make([]q1Input, 0, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			tb.Fatal(err)
		}
		ins = append(ins, q1Input{filepath.Base(p), data})
	}
	return ins
}

func loadQ1Inputs(tb testing.TB) []q1Input {
	tb.Helper()
	var ins []q1Input
	for _, name := range []string{"gh_172KB.html", "github_events_2k.json", "github_events_5k.json", "github_events_8k.json", "reactcore_187KB.js"} {
		data, err := os.ReadFile(filepath.Join("../../testdata", name))
		if err != nil {
			tb.Fatal(err)
		}
		ins = append(ins, q1Input{name, data})
	}
	ins = append(ins, loadQ1Corpus(tb)...)
	text := bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog. "), 20000)
	for _, n := range []int{inputMarginBytes - 1, inputMarginBytes, inputMarginBytes + 1, inputMarginBytes + 7, 40, 100, 2*twoPassBlockSize + 1, 2*twoPassBlockSize + inputMarginBytes + 3} {
		ins = append(ins, q1Input{fmt.Sprintf("text_%d", n), text[:n]})
	}
	windowEdge := pseudoRandomBytesRT(2*maxDistance+8192, 99)
	for _, end := range []int{4096, 4096 + maxDistance + 1, 4096 + 2*maxDistance + 1} {
		clear(windowEdge[end-1024 : end])
		copy(windowEdge[end:], "MARKER!!")
	}
	return append(ins,
		q1Input{"zeros_300000", make([]byte, 300000)},
		q1Input{"period7_200000", bytes.Repeat([]byte("abcdefg"), 200000/7)},
		q1Input{"random_300000", pseudoRandomBytesRT(300000, 7)},
		q1Input{"marker_after_zero_run_repeated_just_past_and_at_max_distance", windowEdge},
	)
}

type createCommandsFunc func(c *twoPassCompressor, input []byte, pos, blockSize, inputSize int, commands []uint32, literals []byte) (int, int)

type minMatch6Fixture struct {
	arena    twoPassArena
	c        twoPassCompressor
	commands []uint32
	literals []byte
}

func newMinMatch6Fixture(tb testing.TB, input []byte, tableBits uint) *minMatch6Fixture {
	tb.Helper()
	f := &minMatch6Fixture{
		commands: make([]uint32, twoPassBlockSize),
		literals: make([]byte, twoPassBlockSize),
	}
	f.c = twoPassCompressor{
		arena:     &f.arena,
		input:     input,
		table:     make([]uint32, 1<<tableBits),
		tableBits: tableBits,
		minMatch:  6,
	}
	return f
}

func (f *minMatch6Fixture) run(fn createCommandsFunc, block func(numCommands, numLiterals int)) {
	clear(f.c.table)
	input := f.c.input
	for pos := 0; pos < len(input); pos += twoPassBlockSize {
		blockSize := min(len(input)-pos, twoPassBlockSize)
		f.arena.cmdHisto = [128]uint32{}
		numCommands, numLiterals := fn(&f.c, input, pos, blockSize, len(input)-pos, f.commands, f.literals)
		block(numCommands, numLiterals)
	}
}

type minMatch6Block struct {
	commands []uint32
	literals []byte
	histo    [128]uint32
}

func (f *minMatch6Fixture) record(fn createCommandsFunc) []minMatch6Block {
	var blocks []minMatch6Block
	f.run(fn, func(numCommands, numLiterals int) {
		blocks = append(blocks, minMatch6Block{
			slices.Clone(f.commands[:numCommands]),
			slices.Clone(f.literals[:numLiterals]),
			f.arena.cmdHisto,
		})
	})
	return blocks
}

func TestCreateCommandsMinMatch6WithHashUpdateFoldedIntoRepeatLoopMatchesTheTwoCallSiteVersion(t *testing.T) {
	for _, in := range loadQ1Inputs(t) {
		for _, tableBits := range []uint{16, 17} {
			t.Run(fmt.Sprintf("%s/table_bits=%d", in.name, tableBits), func(t *testing.T) {
				before := newMinMatch6Fixture(t, in.data, tableBits)
				after := newMinMatch6Fixture(t, in.data, tableBits)
				want := before.record((*twoPassCompressor).createCommandsMinMatch6Before)
				got := after.record((*twoPassCompressor).createCommandsMinMatch6)
				for i := range want {
					if !slices.Equal(got[i].commands, want[i].commands) {
						t.Errorf("block %d: commands differ (got %d, want %d entries); q1 output would no longer be byte-identical to the C reference", i, len(got[i].commands), len(want[i].commands))
					}
					if !bytes.Equal(got[i].literals, want[i].literals) {
						t.Errorf("block %d: literals differ (got %d, want %d bytes); q1 output would no longer be byte-identical to the C reference", i, len(got[i].literals), len(want[i].literals))
					}
					if got[i].histo != want[i].histo {
						t.Errorf("block %d: command histogram differs; the prefix codes written for this block would change", i)
					}
				}
				if !slices.Equal(after.c.table, before.c.table) {
					t.Errorf("final hash table differs: the folded update must store the same positions in the same slots and order as updateHashTableTwoPass6 did")
				}
			})
		}
	}
}

func BenchmarkCreateCommandsMinMatch6(b *testing.B) {
	impls := []struct {
		name string
		fn   createCommandsFunc
	}{
		{"before", (*twoPassCompressor).createCommandsMinMatch6Before},
		{"after", (*twoPassCompressor).createCommandsMinMatch6},
	}
	corpus := loadQ1Corpus(b)
	if len(corpus) == 0 {
		b.Fatal("no reference text files found under brotli-ref/tests/testdata")
	}
	for _, in := range corpus {
		for _, impl := range impls {
			b.Run(in.name+"/impl="+impl.name, func(b *testing.B) {
				f := newMinMatch6Fixture(b, in.data, 17)
				b.ReportAllocs()
				b.SetBytes(int64(len(in.data)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					f.run(impl.fn, func(int, int) {})
				}
			})
		}
	}
}

func (c *twoPassCompressor) createCommandsMinMatch6Before(
	input []byte, pos, blockSize, inputSize int,
	commands []uint32, literals []byte,
) (numCommands, numLiterals int) {
	const minMatch = 6

	cmdHisto := &c.arena.cmdHisto
	ip := pos
	shift := 64 - c.tableBits
	ipEnd := pos + blockSize
	nextEmit := pos
	lastDistance := -1
	table := c.table
	cmdPos := 0
	litPos := 0
	var nextHash uint32

	if blockSize < inputMarginBytes {
		goto encodeRemainder
	}

	{
		lenLimit := min(blockSize-minMatch, inputSize-inputMarginBytes)
		ipLimit := pos + lenLimit

		ip++
		nextLoad := loadU64LE(input, uint(ip))
		nextHash = uint32(((nextLoad << 16) * hashMul32) >> (shift & 63))

		for {
			skip := uint32(32)
			nextIP := ip
			var candidate int

			for {
				hash := nextHash
				ipBytes := nextLoad
				bytesBetweenHashLookups := skip >> 5
				skip++
				ip = nextIP
				nextIP = ip + int(bytesBetweenHashLookups)
				if nextIP > ipLimit {
					goto encodeRemainder
				}
				nextLoad = loadU64LE(input, uint(nextIP))
				nextHash = uint32(((nextLoad << 16) * hashMul32) >> (shift & 63))

				candidate = ip - lastDistance
				if candidate >= 0 && candidate < ip &&
					(loadU64LE(input, uint(candidate))^ipBytes)<<16 == 0 {
					table[hash] = uint32(ip)
					if ip-candidate <= maxDistance {
						break
					}
					continue
				}

				candidate = int(table[hash])
				table[hash] = uint32(ip)
				if (loadU64LE(input, uint(candidate))^ipBytes)<<16 == 0 {
					if ip-candidate <= maxDistance {
						break
					}
					continue
				}
			}

			{
				base := ip
				matched := minMatch + matchLenAt(
					input, uint(candidate+minMatch), uint(ip+minMatch), ipEnd-ip-minMatch)
				distance := base - candidate
				insert := base - nextEmit
				ip += matched

				if u := uint(insert); u < 6 {
					commands[cmdPos] = uint32(u)
					cmdHisto[u]++
					cmdPos++
				} else {
					cmdPos += encodeInsertLen(commands[cmdPos:], u, cmdHisto)
				}
				copy(literals[litPos:], input[nextEmit:nextEmit+insert])
				litPos += insert
				if distance == lastDistance {
					commands[cmdPos] = 64
					cmdHisto[64]++
					cmdPos++
				} else {
					cmdPos += encodeDistance(commands[cmdPos:], uint(distance), cmdHisto)
					lastDistance = distance
				}
				cmdPos += encodeCopyLenLastDistance(commands[cmdPos:], uint(matched), cmdHisto)

				nextEmit = ip
				if ip >= ipLimit {
					goto encodeRemainder
				}

				candidate = c.updateHashTableTwoPass6Before(input, table, ip, shift)
			}

			for ip-candidate <= maxDistance &&
				isMatchTwoPass6AtBefore(input, uint(ip), uint(candidate)) {
				base := ip
				matched := minMatch + matchLenAt(
					input, uint(candidate+minMatch), uint(ip+minMatch), ipEnd-ip-minMatch)
				ip += matched
				lastDistance = base - candidate

				var ccode uint
				if cl := uint(matched); cl < 134 {
					cmd := copyLenCodeQ1Small[cl]
					commands[cmdPos] = cmd
					ccode = uint(cmd & 0x7F)
				} else if cl < 2118 {
					tail := cl - 70
					nbits := uint(bits.Len(tail)) - 1
					ccode = nbits + 52
					extra := tail - (1 << nbits)
					commands[cmdPos] = uint32(ccode) | uint32(extra)<<8
				} else {
					ccode = 63
					extra := cl - 2118
					commands[cmdPos] = uint32(ccode) | uint32(extra)<<8
				}
				cmdHisto[ccode]++
				cmdPos++

				cmdPos += encodeDistance(commands[cmdPos:], uint(lastDistance), cmdHisto)

				nextEmit = ip
				if ip >= ipLimit {
					goto encodeRemainder
				}

				candidate = c.updateHashTableTwoPass6Before(input, table, ip, shift)
			}

			ip++
			nextLoad = loadU64LE(input, uint(ip))
			nextHash = uint32(((nextLoad << 16) * hashMul32) >> (shift & 63))
		}
	}

encodeRemainder:
	if nextEmit < ipEnd {
		insert := ipEnd - nextEmit
		cmdPos += encodeInsertLen(commands[cmdPos:], uint(insert), cmdHisto)
		copy(literals[litPos:], input[nextEmit:ipEnd])
		litPos += insert
	}
	return cmdPos, litPos
}

func (c *twoPassCompressor) updateHashTableTwoPass6Before(input []byte, table []uint32, ip int, shift uint) int {
	inputBytes := loadU64LE(input, uint(ip-5))
	prevHash := hashBytesAtOffsetTwoPass6(inputBytes, 0, shift)
	table[prevHash] = uint32(ip - 5)
	prevHash = hashBytesAtOffsetTwoPass6(inputBytes, 1, shift)
	table[prevHash] = uint32(ip - 4)
	prevHash = hashBytesAtOffsetTwoPass6(inputBytes, 2, shift)
	table[prevHash] = uint32(ip - 3)
	inputBytes = loadU64LE(input, uint(ip-2))
	curHash := hashBytesAtOffsetTwoPass6(inputBytes, 2, shift)
	prevHash = hashBytesAtOffsetTwoPass6(inputBytes, 0, shift)
	table[prevHash] = uint32(ip - 2)
	prevHash = hashBytesAtOffsetTwoPass6(inputBytes, 1, shift)
	table[prevHash] = uint32(ip - 1)

	candidate := int(table[curHash])
	table[curHash] = uint32(ip)
	return candidate
}

func isMatchTwoPass6AtBefore(input []byte, a, b uint) bool {
	return (loadU64LE(input, a)^loadU64LE(input, b))<<16 == 0
}
