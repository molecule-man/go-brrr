package brrr

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

type cmdLutTestInput struct {
	name string
	data []byte
}

func cmdLutTestInputs(tb testing.TB) []cmdLutTestInput {
	tb.Helper()
	var inputs []cmdLutTestInput
	paths := []string{
		"testdata/gh_172KB.html",
		"testdata/github_events_2k.json",
		"testdata/github_events_5k.json",
		"testdata/github_events_8k.json",
		"testdata/reactcore_187KB.js",
		"brotli-ref/tests/testdata/plrabn12.txt",
		"brotli-ref/tests/testdata/lcet10.txt",
		"brotli-ref/tests/testdata/mapsdatazrh",
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			tb.Fatalf("read equivalence input %s: %v", p, err)
		}
		inputs = append(inputs, cmdLutTestInput{filepath.Base(p), data})
	}
	r := rand.New(rand.NewSource(7))
	var mixed []byte
	for len(mixed) < 1<<19 {
		for n := r.Intn(1 << uint(r.Intn(17))); n > 0; n-- {
			mixed = append(mixed, byte('a'+r.Intn(20)))
		}
		if len(mixed) == 0 {
			continue
		}
		dist := 1 + r.Intn(len(mixed))
		for n := 4 + r.Intn(1<<uint(r.Intn(13))); n > 0; n-- {
			mixed = append(mixed, mixed[len(mixed)-dist])
		}
	}
	return append(inputs,
		cmdLutTestInput{"empty", nil},
		cmdLutTestInput{"one_byte", []byte{'x'}},
		cmdLutTestInput{"long_inserts_and_copies", mixed},
	)
}

func decompressInChunksWith(data []byte, chunk int, step func(s *decodeState, output *[]byte) decoderResult) ([]byte, error) {
	var s decodeState
	s.initForReuse()
	var got, out []byte
	for {
		br := &s.br
		br.unload()
		rest := br.availIn()
		n := min(chunk, len(data))
		if n == 0 {
			return got, decompressError("truncated input")
		}
		in := append(append(make([]byte, 0, rest+n), br.input[br.pos:br.pos+rest]...), data[:n]...)
		data = data[n:]
		br.setInput(in)
		for {
			result := step(&s, &out)
			got = append(got, out...)
			out = out[:0]
			if result == decoderResultError {
				return got, s.err
			}
			pending := s.pendingOutput()
			got = append(got, pending...)
			s.consumeOutput(len(pending))
			if result == decoderResultSuccess {
				return got, nil
			}
			if result == decoderResultNeedsMoreInput {
				break
			}
		}
	}
}

func compareProcessCommandsWithBefore(name string, stream []byte, chunk int) error {
	got, gotErr := decompressInChunksWith(stream, chunk, (*decodeState).decompressStream)
	want, wantErr := decompressInChunksWith(stream, chunk, (*decodeState).decompressStreamUsingProcessCommandsBefore)
	var errs []error
	if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
		errs = append(errs, fmt.Errorf("%s chunk=%d: error %w, processCommandsBefore gave %w", name, chunk, gotErr, wantErr))
	}
	if !bytes.Equal(got, want) {
		errs = append(errs, fmt.Errorf("%s chunk=%d: decoded %d bytes that differ from the %d bytes of processCommandsBefore", name, chunk, len(got), len(want)))
	}
	return errors.Join(errs...)
}

func TestProcessCommandsDecodesValidTruncatedAndCorruptStreamsExactlyLikeTheStructCopyingProcessCommandsBefore(t *testing.T) {
	var errs []error
	for _, in := range cmdLutTestInputs(t) {
		for q := 0; q <= 11; q++ {
			data := in.data
			lgwins := []int{22}
			if q >= 10 {
				data = data[:min(len(data), 1<<17)]
			} else if len(data) < 1<<18 {
				lgwins = []int{10, 16, 22, 24}
			}
			for _, lgwin := range lgwins {
				var buf bytes.Buffer
				w, err := NewWriterOptions(&buf, q, WriterOptions{LGWin: lgwin})
				if err != nil {
					t.Fatalf("NewWriterOptions q=%d lgwin=%d: %v", q, lgwin, err)
				}
				if _, err := w.Write(data); err != nil {
					t.Fatalf("compress %s q=%d lgwin=%d: %v", in.name, q, lgwin, err)
				}
				if err := w.Close(); err != nil {
					t.Fatalf("close %s q=%d lgwin=%d: %v", in.name, q, lgwin, err)
				}
				stream := buf.Bytes()
				name := fmt.Sprintf("%s q=%d lgwin=%d", in.name, q, lgwin)
				got, err := decompressInChunksWith(stream, len(stream), (*decodeState).decompressStream)
				if err != nil || !bytes.Equal(got, data) {
					errs = append(errs, fmt.Errorf("%s: the new processCommands does not round-trip the input (err %w)", name, err))
				}
				chunks := []int{len(stream), 61}
				if len(stream) < 1<<16 {
					chunks = append(chunks, 1)
				}
				for _, chunk := range chunks {
					errs = append(errs, compareProcessCommandsWithBefore(name, stream, chunk))
				}
				errs = append(errs,
					compareProcessCommandsWithBefore(name+" truncated to a third", stream[:len(stream)/3], len(stream)),
					compareProcessCommandsWithBefore(name+" missing its last byte", stream[:max(len(stream)-1, 0)], len(stream)),
				)
				for _, at := range []int{len(stream) / 5, len(stream) / 2, len(stream) * 4 / 5} {
					corrupt := bytes.Clone(stream)
					corrupt[at] ^= 0x5a
					errs = append(errs, compareProcessCommandsWithBefore(fmt.Sprintf("%s corrupted at byte %d", name, at), corrupt, len(corrupt)))
				}
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("processCommands must decode every stream, including truncated and corrupted ones, to the same bytes and the same error as the pre-change struct-copying version:\n%v", err)
	}
}

func (s *decodeState) decompressStreamUsingProcessCommandsBefore(output *[]byte) decoderResult {
	for {
		switch s.state {
		case decoderStateUninited:
			if !s.br.warmup() {
				return decoderResultNeedsMoreInput
			}
			if err := s.decodeWindowBits(); err != nil {
				s.err = err
				return decoderResultError
			}
			s.state = decoderStateInitialize
			fallthrough

		case decoderStateInitialize:
			s.maxBackwardDistance = (1 << s.windowBits) - core.WindowGap
			allTrees := reuseHuffmanCodes(s.blockTypeTrees, 3*(huffmanMaxSize258+huffmanMaxSize26))
			s.blockTypeTrees = allTrees[:3*huffmanMaxSize258]
			s.blockLenTrees = allTrees[3*huffmanMaxSize258:]
			s.state = decoderStateMetablockBegin
			fallthrough

		case decoderStateMetablockBegin:
			s.metablockBegin()
			s.state = decoderStateMetablockHeader
			fallthrough

		case decoderStateMetablockHeader:
			result := s.decodeMetaBlockLength()
			if result != decoderResultSuccess {
				return result
			}
			if s.isMetadata || s.isUncompressed {
				if !s.br.jumpToByteBoundary() {
					s.err = errPadding
					return decoderResultError
				}
			}
			if s.isMetadata {
				s.state = decoderStateMetadata
				break
			}
			if s.metaBlockRemainingLen == 0 {
				s.state = decoderStateMetablockDone
				break
			}
			s.calculateRingBufferSize()
			if s.isUncompressed {
				s.state = decoderStateUncompressed
				break
			}
			s.state = decoderStateBeforeCompressedMetablockHeader
			fallthrough

		case decoderStateBeforeCompressedMetablockHeader:
			h := &s.headerArena
			s.loopCounter = 0
			h.subLoopCounter = 0
			h.symbolLists = h.symbolListsArray[core.HuffmanMaxCodeLength+1:]
			h.substateHuffman = huffmanNone
			h.substateTreeGroup = treeGroupNone
			h.substateContextMap = contextMapNone
			s.state = decoderStateHuffmanCode0
			fallthrough

		case decoderStateHuffmanCode0:
			if s.loopCounter >= 3 {
				s.state = decoderStateMetablockHeader2
				break
			}
			result := s.decodeVarLenUint8(&s.numBlockTypes[s.loopCounter])
			if result != decoderResultSuccess {
				return result
			}
			s.numBlockTypes[s.loopCounter]++
			if s.numBlockTypes[s.loopCounter] < 2 {
				s.loopCounter++
				break
			}
			s.state = decoderStateHuffmanCode1
			fallthrough

		case decoderStateHuffmanCode1:
			alphabetSize := s.numBlockTypes[s.loopCounter] + 2
			treeOffset := s.loopCounter * huffmanMaxSize258
			result := s.readHuffmanCode(alphabetSize, alphabetSize,
				s.blockTypeTrees[treeOffset:], nil)
			if result != decoderResultSuccess {
				return result
			}
			s.state = decoderStateHuffmanCode2
			fallthrough

		case decoderStateHuffmanCode2:
			treeOffset := s.loopCounter * huffmanMaxSize26
			result := s.readHuffmanCode(core.AlphabetSizeBlockCount, core.AlphabetSizeBlockCount,
				s.blockLenTrees[treeOffset:], nil)
			if result != decoderResultSuccess {
				return result
			}
			s.state = decoderStateHuffmanCode3
			fallthrough

		case decoderStateHuffmanCode3:
			treeOffset := s.loopCounter * huffmanMaxSize26
			bl, ok := safeReadBlockLength(s, s.blockLenTrees[treeOffset:])
			if !ok {
				return decoderResultNeedsMoreInput
			}
			s.blockLength[s.loopCounter] = bl
			s.loopCounter++
			s.state = decoderStateHuffmanCode0

		case decoderStateMetablockHeader2:
			bits, ok := s.br.safeReadBits(6)
			if !ok {
				return decoderResultNeedsMoreInput
			}
			s.distancePostfixBits = uint(bits & bitMask(2))
			bits >>= 2
			s.numDirectDistanceCodes = uint(bits) << s.distancePostfixBits
			s.contextModes = reuseBytes(s.contextModes, int(s.numBlockTypes[0]))
			s.loopCounter = 0
			s.state = decoderStateContextModes
			fallthrough

		case decoderStateContextModes:
			result := s.readContextModes()
			if result != decoderResultSuccess {
				return result
			}
			s.state = decoderStateContextMap1
			fallthrough

		case decoderStateContextMap1:
			result := s.decodeContextMap(s.numBlockTypes[0]<<core.LiteralContextBits, &s.contextMap, &s.numLiteralHTrees)
			if result != decoderResultSuccess {
				return result
			}
			s.detectTrivialLiteralBlockTypes()
			s.state = decoderStateContextMap2
			fallthrough

		case decoderStateContextMap2:
			npostfix := s.distancePostfixBits
			ndirect := s.numDirectDistanceCodes
			distAlphabetSizeMax := uint(core.NumDistanceShortCodes) + ndirect + (uint(core.MaxDistanceBits) << (npostfix + 1))
			distAlphabetSizeLimit := distAlphabetSizeMax

			result := s.decodeContextMap(s.numBlockTypes[2]<<core.DistanceContextBits, &s.distContextMap, &s.numDistHTrees)
			if result != decoderResultSuccess {
				return result
			}

			s.literalHGroup.init(uint16(core.AlphabetSizeLiteral), uint16(core.AlphabetSizeLiteral), uint16(s.numLiteralHTrees))
			s.insertCopyHGroup.init(uint16(core.AlphabetSizeInsertAndCopyLength), uint16(core.AlphabetSizeInsertAndCopyLength), uint16(s.numBlockTypes[1]))
			s.distanceHGroup.init(uint16(distAlphabetSizeMax), uint16(distAlphabetSizeLimit), uint16(s.numDistHTrees))

			s.loopCounter = 0
			s.state = decoderStateTreeGroup
			fallthrough

		case decoderStateTreeGroup:
			var hgroup *huffmanTreeGroup
			switch s.loopCounter {
			case 0:
				hgroup = &s.literalHGroup
			case 1:
				hgroup = &s.insertCopyHGroup
			case 2:
				hgroup = &s.distanceHGroup
			default:
				s.err = decompressError("unreachable tree group index")
				return decoderResultError
			}
			result := s.huffmanTreeGroupDecode(hgroup)
			if result != decoderResultSuccess {
				return result
			}
			s.loopCounter++
			if s.loopCounter < 3 {
				break
			}
			s.state = decoderStateBeforeCompressedMetablockBody
			fallthrough

		case decoderStateBeforeCompressedMetablockBody:
			s.prepareLiteralDecoding()
			s.distContextMapSliceIdx = 0
			s.updateDistCodesCache()
			s.htreeCommand = s.insertCopyHGroup.codes[s.insertCopyHGroup.htrees[0]:]
			s.ensureRingBuffer()
			s.calculateDistanceLut()
			s.state = decoderStateCommandBegin
			fallthrough

		case decoderStateCommandBegin,
			decoderStateCommandInner,
			decoderStateCommandPostDecodeLiterals,
			decoderStateCommandPostWrapCopy:
			result := s.processCommandsBefore()
			if result != decoderResultSuccess {
				return result
			}

		case decoderStateCommandInnerWrite,
			decoderStateCommandPostWrite1,
			decoderStateCommandPostWrite2:
			s.writeRingBuffer(output)
			switch s.state {
			case decoderStateCommandPostWrite1:
				if cd := s.compoundDict; cd != nil && cd.brLength != cd.brCopied {
					s.pos += cd.copyTo(s, s.pos)
					if s.pos >= s.ringbufferSize {
						continue
					}
				}
				if s.metaBlockRemainingLen == 0 {
					s.state = decoderStateMetablockDone
				} else {
					s.state = decoderStateCommandBegin
				}
			case decoderStateCommandPostWrite2:
				s.state = decoderStateCommandPostWrapCopy
			default:
				if s.loopCounter == 0 {
					if s.metaBlockRemainingLen == 0 {
						s.state = decoderStateMetablockDone
					} else {
						s.state = decoderStateCommandPostDecodeLiterals
					}
				} else {
					s.state = decoderStateCommandInner
				}
			}

		case decoderStateMetadata:
			result := s.skipMetadataBlock()
			if result != decoderResultSuccess {
				return result
			}
			s.state = decoderStateMetablockDone

		case decoderStateUncompressed:
			result := s.copyUncompressedBlock(output)
			if result != decoderResultSuccess {
				return result
			}
			s.state = decoderStateMetablockDone

		case decoderStateMetablockDone:
			if s.metaBlockRemainingLen < 0 {
				s.err = decompressError("negative remaining metablock length")
				return decoderResultError
			}
			if !s.isLastMetablock {
				s.state = decoderStateMetablockBegin
				break
			}
			if !s.br.jumpToByteBoundary() {
				s.err = errPadding
				return decoderResultError
			}
			s.state = decoderStateDone
			fallthrough

		case decoderStateDone:
			return decoderResultSuccess

		default:
			s.err = decompressError("unhandled decoder state")
			return decoderResultError
		}
	}
}

func TestCmdLutPackedHoldsEveryCmdLutFieldAtTheBitOffsetTheDecoderExtracts(t *testing.T) {
	var errs []error
	for code := range core.CmdLut {
		want := core.CmdLut[code]
		p := cmdLutPacked[code]
		got := core.CmdLutElement{
			InsertLenExtraBits: uint8(p),
			CopyLenExtraBits:   uint8(p >> 8),
			DistanceCode:       int8(p >> 16),
			Context:            uint8(p >> 24),
			InsertLenOffset:    uint16(p >> 32),
			CopyLenOffset:      uint16(p >> 48),
		}
		if got != want {
			errs = append(errs, fmt.Errorf("command code %d: packed %#016x unpacks to %+v, want %+v", code, p, got, want))
		}
	}
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("the decoder fast path reads command fields only from cmdLutPacked, so every entry must unpack to its CmdLut element:\n%v", err)
	}
}
