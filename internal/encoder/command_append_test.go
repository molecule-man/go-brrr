package encoder

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func newCommandSimpleDistBefore(insertLen, copyLen uint, copyLenDelta int, distanceCode uint) command {
	delta := uint32(uint8(int8(copyLenDelta)))
	distPrefix, distExtra := prefixEncodeSimpleDistance(distanceCode)
	effectiveCopyLen := uint(int(copyLen) + copyLenDelta)
	insCode := getInsertLenCode(insertLen)
	copyCode := getCopyLenCode(effectiveCopyLen)
	cmdPrefix := combineLengthCodes(insCode, copyCode, (distPrefix&0x3FF) == 0)
	return command{
		insertLen:  uint32(insertLen),
		copyLen:    uint32(copyLen) | (delta << 25),
		distExtra:  distExtra,
		cmdPrefix:  cmdPrefix,
		distPrefix: distPrefix,
	}
}

type simpleDistCommandArgs struct {
	insertLen    uint
	copyLen      uint
	copyLenDelta int
	distanceCode uint
}

func simpleDistCommandArgsFromCorpus(tb testing.TB) []simpleDistCommandArgs {
	tb.Helper()
	var paths []string
	for _, pattern := range []string{"../../testdata/*", "../../brotli-ref/tests/testdata/*.txt"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			tb.Fatalf("glob %s: %v", pattern, err)
		}
		paths = append(paths, matches...)
	}
	const lgwin, blockSize = 22, 1 << 16
	var s encodeState
	h := new(h54)
	var args []simpleDistCommandArgs
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			tb.Fatalf("stat %s: %v", p, err)
		}
		if fi.IsDir() {
			continue
		}
		input, err := os.ReadFile(p)
		if err != nil {
			tb.Fatalf("read %s: %v", p, err)
		}
		s.reset(4, lgwin, uint(len(input)))
		for off := 0; off < len(input); off += blockSize {
			block := input[off:min(off+blockSize, len(input))]
			s.copyInputToRingBuffer(block)
			if off == 0 {
				h.reset(len(block) == len(input), uint(len(input)), s.data)
			}
			pos := wrapPosition(uint64(off))
			h.stitchToPreviousBlock(uint(len(block)), pos, s.data, uint(s.mask))
			h.createBackwardReferences(&s, uint32(len(block)), uint32(pos))
		}
		for _, c := range s.commands {
			args = append(args, simpleDistCommandArgs{
				insertLen:    uint(c.insertLen),
				copyLen:      uint(c.copyLength()),
				copyLenDelta: int(c.copyLenCode()) - int(c.copyLength()),
				distanceCode: uint(c.distanceCode(0, 0)),
			})
		}
	}
	return args
}

func simpleDistCommandEdgeArgs() []simpleDistCommandArgs {
	var args []simpleDistCommandArgs
	for _, insertLen := range []uint{0, 1, 5, 6, 7, 129, 130, 2113, 2114, 6209, 6210, 22593, 22594, 1<<24 - 1} {
		for _, copyLen := range []uint{2, 3, 9, 10, 133, 134, 2117, 2118, 1<<24 - 1} {
			for _, copyLenDelta := range []int{0, -1, 1, -6, 63, -64} {
				if int(copyLen)+copyLenDelta < 2 {
					continue
				}
				for _, distanceCode := range []uint{0, 1, 15, 16, 17, 19, 20, 1000, 1<<24 + 15, 1<<25 - 1} {
					args = append(args, simpleDistCommandArgs{insertLen, copyLen, copyLenDelta, distanceCode})
				}
			}
		}
	}
	return args
}

func TestPushCommandSimpleDistAppendsExactlyTheCommandsTheRemovedValueConstructorBuiltForCorpusAndEdgeCaseArguments(t *testing.T) {
	corpus := simpleDistCommandArgsFromCorpus(t)
	if len(corpus) < 1000 {
		t.Fatalf("the corpus yielded only %d commands; the comparison needs real h54 command arguments to mean anything", len(corpus))
	}
	var s encodeState
	var want []command //nolint:prealloc
	for _, a := range append(corpus, simpleDistCommandEdgeArgs()...) {
		want = append(want, newCommandSimpleDistBefore(a.insertLen, a.copyLen, a.copyLenDelta, a.distanceCode))
		s.pushCommandSimpleDist(a.insertLen, a.copyLen, a.copyLenDelta, a.distanceCode)
	}
	var errs []error
	if len(s.commands) != len(want) || cap(s.commands) != cap(want) {
		errs = append(errs, fmt.Errorf("len/cap = %d/%d, want %d/%d: writing into the appended slot must grow the slice exactly like appending a finished command", len(s.commands), cap(s.commands), len(want), cap(want)))
	}
	mismatches := 0
	for i := range min(len(s.commands), len(want)) {
		if s.commands[i] != want[i] {
			mismatches++
			if mismatches <= 10 {
				errs = append(errs, fmt.Errorf("command %d: got %+v, want %+v", i, s.commands[i], want[i]))
			}
		}
	}
	if mismatches > 0 {
		errs = append(errs, fmt.Errorf("%d of %d commands differ: every field must be stored exactly as before or the encoder stops being byte-identical with the C reference", mismatches, len(want)))
	}
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkCommandAppend(b *testing.B) {
	args := simpleDistCommandArgsFromCorpus(b)
	var s encodeState
	b.Run("simpledist/impl=before", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			s.commands = s.commands[:0]
			for _, a := range args {
				s.commands = append(s.commands, newCommandSimpleDistBefore(a.insertLen, a.copyLen, a.copyLenDelta, a.distanceCode))
			}
		}
	})
	b.Run("simpledist/impl=after", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			s.commands = s.commands[:0]
			for _, a := range args {
				s.pushCommandSimpleDist(a.insertLen, a.copyLen, a.copyLenDelta, a.distanceCode)
			}
		}
	})
	b.Run("literal/impl=before", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			s.commands = s.commands[:0]
			for _, a := range args {
				delta := uint32(uint8(int8(a.copyLenDelta)))
				distPrefix, distExtra := prefixEncodeSimpleDistance(a.distanceCode)
				effectiveCopyLen := uint(int(a.copyLen) + a.copyLenDelta)
				insCode := getInsertLenCode(a.insertLen)
				copyCode := getCopyLenCode(effectiveCopyLen)
				cmdPrefix := combineLengthCodes(insCode, copyCode, (distPrefix&0x3FF) == 0)
				s.commands = append(s.commands, command{
					insertLen:  uint32(a.insertLen),
					copyLen:    uint32(a.copyLen) | (delta << 25),
					distExtra:  distExtra,
					cmdPrefix:  cmdPrefix,
					distPrefix: distPrefix,
				})
			}
		}
	})
	b.Run("literal/impl=after", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			s.commands = s.commands[:0]
			for _, a := range args {
				delta := uint32(uint8(int8(a.copyLenDelta)))
				distPrefix, distExtra := prefixEncodeSimpleDistance(a.distanceCode)
				effectiveCopyLen := uint(int(a.copyLen) + a.copyLenDelta)
				insCode := getInsertLenCode(a.insertLen)
				copyCode := getCopyLenCode(effectiveCopyLen)
				cmdPrefix := combineLengthCodes(insCode, copyCode, (distPrefix&0x3FF) == 0)
				s.appendCommand(uint32(a.insertLen), uint32(a.copyLen)|(delta<<25), distExtra, cmdPrefix, distPrefix)
			}
		}
	})
}
