package brrr

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"unsafe"

	"github.com/molecule-man/go-brrr/internal/core"
)

func (br *bitReader) fillBitWindowBefore(nBits uint) {
	_ = nBits
	if br.bitPos <= 32 {
		br.val |= uint64(*(*uint32)(unsafe.Add(br.inputBase, br.pos))) << br.bitPos
		br.bitPos += 32
		br.pos += 4
	}
}

func (s *decodeState) readSymbolCodeLengthsBefore(alphabetSize uint) decoderResult {
	br := &s.br
	h := &s.headerArena

	if !br.warmup() {
		return decoderResultNeedsMoreInput
	}

	if br.checkInputAmount() {
		val := br.val
		bitPos := br.bitPos
		brPos := br.pos
		inputBase := br.inputBase
		tableBase := unsafe.Pointer(&h.table[0])
		symbol := h.symbol

		for symbol < alphabetSize && h.space > 0 {
			if bitPos <= 32 {
				if brPos > br.fastEnd {
					break
				}
				val |= uint64(*(*uint32)(unsafe.Add(inputBase, brPos))) << bitPos
				bitPos += 32
				brPos += 4
			}

			raw := *(*uint32)(unsafe.Add(tableBase, (val&0x1F)*4))
			drop := uint(raw & 0xFF)
			codeLen := uint(raw >> 16)

			bitPos -= drop
			val >>= drop & 63

			if codeLen >= core.RepeatPreviousCodeLength {
				extraBits := uint(2)
				newLen := uint(0)
				if codeLen == core.RepeatPreviousCodeLength {
					newLen = h.prevCodeLen
				} else {
					extraBits = 3
				}
				repeatDelta := uint(val & bitMask(extraBits))
				bitPos -= extraBits
				val >>= extraBits & 63

				if h.repeatCodeLen != newLen {
					h.repeat = 0
					h.repeatCodeLen = newLen
				}
				oldRepeat := h.repeat
				if h.repeat > 0 {
					h.repeat -= 2
					h.repeat <<= extraBits
				}
				h.repeat += repeatDelta + 3
				delta := h.repeat - oldRepeat
				switch {
				case symbol+delta > alphabetSize:
					symbol = alphabetSize
					h.space = 0xFFFFF
				case newLen != 0:
					last := symbol + delta
					next := h.nextSymbol[newLen]
					for symbol < last {
						h.symbolListsArray[next+symListBase] = uint16(symbol)
						next = int(symbol)
						symbol++
					}
					h.nextSymbol[newLen] = next
					h.space -= delta << (15 - newLen)
					h.codeLengthHisto[newLen] += uint16(delta)
				default:
					symbol += delta
				}

				if brPos > br.fastEnd {
					h.symbol = symbol
					br.val = val
					br.bitPos = bitPos
					br.pos = brPos
					goto slow
				}
				continue
			}

			h.repeat = 0
			if codeLen != 0 {
				h.symbolListsArray[h.nextSymbol[codeLen]+symListBase] = uint16(symbol)
				h.nextSymbol[codeLen] = int(symbol)
				h.prevCodeLen = codeLen
				h.space -= 32768 >> codeLen
				h.codeLengthHisto[codeLen]++
			}
			symbol++
		}

		h.symbol = symbol
		br.val = val
		br.bitPos = bitPos
		br.pos = brPos
	}

slow:
	for h.symbol < alphabetSize && h.space > 0 {
		p := h.table[:]
		if br.checkInputAmount() {
			br.fillBitWindow16()
			entry := p[br.bitsUnmasked()&bitMask(core.HuffmanMaxCodeLengthCodeLength)]
			br.dropBits(uint(entry.Bits))
			codeLen := uint(entry.Value)
			if codeLen < core.RepeatPreviousCodeLength {
				h.processSingleCodeLength(codeLen)
			} else {
				extraBits := uint(2)
				if codeLen != core.RepeatPreviousCodeLength {
					extraBits = 3
				}
				repeatDelta := uint(br.bitsUnmasked() & bitMask(extraBits))
				br.dropBits(extraBits)
				h.processRepeatedCodeLength(codeLen, repeatDelta, alphabetSize)
			}
		} else {
			availBits := br.availBits()
			var bits uint64
			if availBits != 0 {
				br.normalize()
				bits = br.bitsUnmasked()
			}
			entry := p[bits&bitMask(core.HuffmanMaxCodeLengthCodeLength)]
			if uint(entry.Bits) > availBits {
				if !br.pullByte() {
					return decoderResultNeedsMoreInput
				}
				continue
			}
			codeLen := uint(entry.Value)
			if codeLen < core.RepeatPreviousCodeLength {
				br.dropBits(uint(entry.Bits))
				h.processSingleCodeLength(codeLen)
			} else {
				extraBits := codeLen - 14
				repeatDelta := uint((bits >> uint(entry.Bits)) & bitMask(extraBits))
				if availBits < uint(entry.Bits)+extraBits {
					if !br.pullByte() {
						return decoderResultNeedsMoreInput
					}
					continue
				}
				br.dropBits(uint(entry.Bits) + extraBits)
				h.processRepeatedCodeLength(codeLen, repeatDelta, alphabetSize)
			}
		}
	}
	return decoderResultSuccess
}

func (s *decodeState) processCommandsBefore() decoderResult {
	br := &s.br
	pos := s.pos
	i := s.loopCounter
	var cmdCode uint
	var ok bool
	var v core.CmdLutElement
	var insertLenExtra uint64
	var literal uint
	var p1, p2 byte

	switch s.state {
	case decoderStateCommandInner:
		goto commandInner
	case decoderStateCommandPostDecodeLiterals:
		goto commandPostDecodeLiterals
	case decoderStateCommandPostWrapCopy:
		goto commandPostWrapCopy
	}

commandBegin:
	if s.blockLength[1] == 0 && s.numBlockTypes[1] > 1 {
		if br.checkInputAmount() {
			s.decodeBlockTypeAndLength(false, 1)
		} else if !s.decodeBlockTypeAndLength(true, 1) {
			s.state = decoderStateCommandBegin
			s.pos = pos
			return decoderResultNeedsMoreInput
		}
		s.htreeCommand = s.insertCopyHGroup.codes[s.insertCopyHGroup.htrees[s.blockTypeRB[3]]:]
	}

	if br.checkInputAmount() {
		br.fillBitWindow(16)
		val := br.val
		cmdTableBase := unsafe.Pointer(unsafe.SliceData(s.htreeCommand))
		idx := val & huffmanTableMask
		raw := *(*uint32)(unsafe.Add(cmdTableBase, idx*4))
		cmdDrop := uint(raw & 0xFF)
		cmdCode = uint(raw >> 16)
		if cmdDrop > huffmanTableBits {
			nbits := cmdDrop - huffmanTableBits
			idx2 := idx + uint64(cmdCode) + ((val >> huffmanTableBits) & bitMask(nbits))
			raw = *(*uint32)(unsafe.Add(cmdTableBase, idx2*4))
			cmdDrop = huffmanTableBits + uint(raw&0xFF)
			cmdCode = uint(raw >> 16)
		}

		v = *(*core.CmdLutElement)(unsafe.Add(unsafe.Pointer(&core.CmdLut[0]), uintptr(cmdCode)*unsafe.Sizeof(core.CmdLutElement{})))
		s.distanceCode = int(v.DistanceCode)
		s.distanceContext = int(v.Context)
		s.distCodesOffset = s.distCodesCache[s.distanceContext&3]
		i = int(v.InsertLenOffset)

		val >>= cmdDrop & 63
		bitPos := br.bitPos - cmdDrop

		insertLenExtra = 0
		if v.InsertLenExtraBits != 0 {
			if bitPos < 56 {
				val |= *(*uint64)(unsafe.Add(br.inputBase, br.pos)) << (bitPos & 63)
				br.pos += int((63 - bitPos) >> 3)
				bitPos |= 56
			}
			insertLenExtra = val & bitMask(uint(v.InsertLenExtraBits))
			val >>= uint(v.InsertLenExtraBits) & 63
			bitPos -= uint(v.InsertLenExtraBits)
		}

		if bitPos < 56 {
			val |= *(*uint64)(unsafe.Add(br.inputBase, br.pos)) << (bitPos & 63)
			br.pos += int((63 - bitPos) >> 3)
			bitPos |= 56
		}
		copyExtra := val & bitMask(uint(v.CopyLenExtraBits))
		val >>= uint(v.CopyLenExtraBits) & 63
		bitPos -= uint(v.CopyLenExtraBits)

		br.val = val
		br.bitPos = bitPos

		s.copyLength = int(v.CopyLenOffset) + int(copyExtra)
	} else {
		cmdMemento := br.saveState()
		cmdCode, ok = safeReadSymbol(s.htreeCommand, br)
		if !ok {
			s.state = decoderStateCommandBegin
			s.pos = pos
			return decoderResultNeedsMoreInput
		}
		s.safeCmdMemento = cmdMemento

		v = core.CmdLut[cmdCode]
		s.distanceCode = int(v.DistanceCode)
		s.distanceContext = int(v.Context)
		s.distCodesOffset = s.distCodesCache[s.distanceContext]
		i = int(v.InsertLenOffset)

		insertLenExtra = 0
		if v.InsertLenExtraBits != 0 {
			insertLenExtra, ok = br.safeReadBits(uint(v.InsertLenExtraBits))
			if !ok {
				br.restoreState(s.safeCmdMemento)
				s.state = decoderStateCommandBegin
				s.pos = pos
				return decoderResultNeedsMoreInput
			}
		}
		var val uint64
		val, ok = br.safeReadBits(uint(v.CopyLenExtraBits))
		if !ok {
			br.restoreState(s.safeCmdMemento)
			s.state = decoderStateCommandBegin
			s.pos = pos
			return decoderResultNeedsMoreInput
		}
		s.copyLength = int(v.CopyLenOffset) + int(val)
	}
	s.blockLength[1]--
	i += int(insertLenExtra)

	s.metaBlockRemainingLen -= i
	if i == 0 {
		goto commandPostDecodeLiterals
	}

commandInner:
	if s.trivialLiteralContext != 0 {
		for i > 0 {
			if s.blockLength[0] == 0 && s.numBlockTypes[0] > 1 {
				if br.checkInputAmount() {
					s.decodeBlockTypeAndLength(false, 0)
				} else if !s.decodeBlockTypeAndLength(true, 0) {
					s.state = decoderStateCommandInner
					s.pos = pos
					s.loopCounter = i
					return decoderResultNeedsMoreInput
				}
				s.prepareLiteralDecoding()
				if s.trivialLiteralContext == 0 {
					goto commandInner
				}
			}

			n := min(i, int(s.blockLength[0]), s.ringbufferSize-pos, (br.availIn()-8)/2)
			if n == 1 {
				br.fillBitWindow(16)
				s.ringbuffer[pos] = byte(decodeSymbol(br.val, s.literalHTree, br))
				pos++
				i--
				s.blockLength[0]--
				if pos == s.ringbufferSize {
					s.state = decoderStateCommandInnerWrite
					s.pos = pos
					s.loopCounter = i
					return decoderResultNeedsMoreOutput
				}
				continue
			} else if n > 1 {
				decodeLiteralsBatch(s.ringbuffer[pos:], n, s.literalHTree, br)
				pos += n
				i -= n
				s.blockLength[0] -= uint(n)
				if pos == s.ringbufferSize {
					s.state = decoderStateCommandInnerWrite
					s.pos = pos
					s.loopCounter = i
					return decoderResultNeedsMoreOutput
				}
				continue
			}

			if br.checkInputAmount() {
				br.fillBitWindow(16)
				literal = decodeSymbol(br.val, s.literalHTree, br)
			} else {
				literal, ok = safeReadSymbol(s.literalHTree, br)
				if !ok {
					s.state = decoderStateCommandInner
					s.pos = pos
					s.loopCounter = i
					return decoderResultNeedsMoreInput
				}
			}
			s.ringbuffer[pos] = byte(literal)
			s.blockLength[0]--
			pos++
			if pos == s.ringbufferSize {
				s.state = decoderStateCommandInnerWrite
				i--
				s.pos = pos
				s.loopCounter = i
				return decoderResultNeedsMoreOutput
			}
			i--
		}
	} else {
		p1 = s.ringbuffer[(pos-1)&s.ringbufferMask]
		p2 = s.ringbuffer[(pos-2)&s.ringbufferMask]
		for i > 0 {
			if s.blockLength[0] == 0 && s.numBlockTypes[0] > 1 {
				if br.checkInputAmount() {
					s.decodeBlockTypeAndLength(false, 0)
				} else if !s.decodeBlockTypeAndLength(true, 0) {
					s.state = decoderStateCommandInner
					s.pos = pos
					s.loopCounter = i
					return decoderResultNeedsMoreInput
				}
				s.prepareLiteralDecoding()
				if s.trivialLiteralContext != 0 {
					goto commandInner
				}
			}

			n := min(i, int(s.blockLength[0]), s.ringbufferSize-pos, (br.availIn()-8)/2)
			if n > 0 {
				p1, p2 = decodeLiteralsContextBatch(
					s.ringbuffer[pos:], n,
					&s.literalCodesPtrs,
					s.contextLookup, p1, p2, br,
				)
				pos += n
				i -= n
				s.blockLength[0] -= uint(n)
				if pos == s.ringbufferSize {
					s.state = decoderStateCommandInnerWrite
					s.pos = pos
					s.loopCounter = i
					return decoderResultNeedsMoreOutput
				}
				continue
			}

			ctx := s.contextLookup[p1] | s.contextLookup[256+int(p2)]
			hc := s.literalHGroup.codes[s.literalCodesOffsets[ctx]:]
			p2 = p1
			if br.checkInputAmount() {
				br.fillBitWindow(16)
				p1 = byte(decodeSymbol(br.val, hc, br))
			} else {
				literal, ok = safeReadSymbol(hc, br)
				if !ok {
					s.state = decoderStateCommandInner
					s.pos = pos
					s.loopCounter = i
					return decoderResultNeedsMoreInput
				}
				p1 = byte(literal)
			}
			s.ringbuffer[pos] = p1
			s.blockLength[0]--
			pos++
			if pos == s.ringbufferSize {
				s.state = decoderStateCommandInnerWrite
				i--
				s.pos = pos
				s.loopCounter = i
				return decoderResultNeedsMoreOutput
			}
			i--
		}
	}

	if s.metaBlockRemainingLen <= 0 {
		s.state = decoderStateMetablockDone
		s.pos = pos
		s.loopCounter = i
		return decoderResultSuccess
	}

commandPostDecodeLiterals:
	if s.distanceCode >= 0 {
		if s.distanceCode != 0 {
			s.distanceContext = 0
		} else {
			s.distanceContext = 1
		}
		s.distRBIdx--
		s.distanceCode = s.distRB[s.distRBIdx&3]
	} else {
		if s.blockLength[2] == 0 && s.numBlockTypes[2] > 1 {
			if br.checkInputAmount() {
				s.decodeBlockTypeAndLength(false, 2)
			} else if !s.decodeBlockTypeAndLength(true, 2) {
				s.state = decoderStateCommandPostDecodeLiterals
				s.pos = pos
				s.loopCounter = i
				return decoderResultNeedsMoreInput
			}
			s.distContextMapSliceIdx = int(s.blockTypeRB[5]) << core.DistanceContextBits
			s.updateDistCodesCache()
			s.distCodesOffset = s.distCodesCache[s.distanceContext]
		}
		if br.checkInputAmount() {
			br.fillBitWindow(16)
			val := br.val
			raw := distanceSymbolEntryFast(val, s.distanceHGroup.codes, s.distCodesOffset)
			drop := uint(raw & 0xFF)
			code := uint(raw >> 16)
			if drop > huffmanTableBits {
				code, drop = decodeDistanceSymbolSecondLevel(val, s.distanceHGroup.codes, s.distCodesOffset, code, drop)
			}
			val >>= drop & 63
			bitPos := br.bitPos - drop
			s.blockLength[2]--
			s.distanceContext = 0
			if code&^0xF == 0 {
				br.val = val
				br.bitPos = bitPos
				s.distanceCode = int(code)
				s.takeDistanceFromRingBuffer()
			} else {
				b := &s.bodyArena
				nExtra := uint(*(*byte)(unsafe.Add(unsafe.Pointer(&b.distExtraBits[0]), uintptr(code))))
				offset := *(*uint)(unsafe.Add(unsafe.Pointer(&b.distOffset[0]), uintptr(code)*unsafe.Sizeof(uint(0))))
				if bitPos < 56 {
					val |= *(*uint64)(unsafe.Add(br.inputBase, br.pos)) << (bitPos & 63)
					br.pos += int((63 - bitPos) >> 3)
					bitPos |= 56
				}
				bits := val & bitMask(nExtra)
				br.val = val >> (nExtra & 63)
				br.bitPos = bitPos - nExtra
				s.distanceCode = int(offset + uint(bits)<<s.distancePostfixBits)
			}
		} else {
			result := s.readDistance(br)
			if result != decoderResultSuccess {
				s.state = decoderStateCommandPostDecodeLiterals
				s.pos = pos
				s.loopCounter = i
				return result
			}
		}
	}

	if s.maxDistance != s.maxBackwardDistance {
		s.maxDistance = min(pos, s.maxBackwardDistance)
	}

	i = s.copyLength
	if s.distanceCode > s.maxDistance {
		if s.distanceCode > maxAllowedDistance {
			s.err = decompressError("invalid backward reference distance")
			return decoderResultError
		}

		compoundSize := s.compoundDictSize()

		switch {
		case s.distanceCode-s.maxDistance <= compoundSize:
			address := compoundSize - (s.distanceCode - s.maxDistance)
			if !s.compoundDict.initCopy(s, address, i) {
				s.err = decompressError("invalid compound dictionary reference")
				return decoderResultError
			}
			pos += s.compoundDict.copyTo(s, pos)
			if pos >= s.ringbufferSize {
				s.state = decoderStateCommandPostWrite1
				s.pos = pos
				s.loopCounter = 0
				return decoderResultNeedsMoreOutput
			}

		case i >= core.DictMinWordLength && i <= core.DictMaxWordLength:
			address := s.distanceCode - s.maxDistance - 1 - compoundSize
			shift := uint(core.DictSizeBitsByLength[i])
			if shift == 0 {
				s.err = decompressError("invalid static dictionary reference")
				return decoderResultError
			}
			wordIdx := address & ((1 << shift) - 1)
			transformIdx := address >> shift
			if transformIdx >= core.NumTransforms {
				s.err = decompressError("invalid dictionary transform")
				return decoderResultError
			}
			offset := int(core.DictOffsetsByLength[i]) + wordIdx*i
			word := core.DictData[offset : offset+i]

			s.distRBIdx += s.distanceContext

			if transformIdx == int(core.TransformCutOffs[0]) {
				copy(s.ringbuffer[pos:], word)
			} else {
				i = core.TransformDictionaryWord(s.ringbuffer[pos:], word, transformIdx)
				if i == 0 && s.distanceCode <= 120 {
					s.err = decompressError("invalid dictionary transform")
					return decoderResultError
				}
			}
			pos += i
			s.metaBlockRemainingLen -= i
			if pos >= s.ringbufferSize {
				s.state = decoderStateCommandPostWrite1
				s.pos = pos
				s.loopCounter = 0
				return decoderResultNeedsMoreOutput
			}

		default:
			s.err = decompressError("invalid dictionary word length")
			return decoderResultError
		}

		if s.metaBlockRemainingLen <= 0 {
			s.state = decoderStateMetablockDone
			s.pos = pos
			s.loopCounter = 0
			return decoderResultSuccess
		}
		goto commandBegin
	}

	s.distRB[s.distRBIdx&3] = s.distanceCode
	s.distRBIdx++
	s.metaBlockRemainingLen -= i

	{
		srcStart := (pos - s.distanceCode) & s.ringbufferMask
		dstEnd := pos + i
		srcEnd := srcStart + i

		base := unsafe.Pointer(unsafe.SliceData(s.ringbuffer))
		*(*[16]byte)(unsafe.Add(base, pos)) = *(*[16]byte)(unsafe.Add(base, srcStart))

		if dstEnd >= s.ringbufferSize || srcEnd >= s.ringbufferSize {
			goto commandPostWrapCopy
		}
		if srcEnd > pos && dstEnd > srcStart {
			if dist := pos - srcStart; dist > 0 {
				copyOverlappingPattern(base, pos, dist, i)
				pos += i
				goto postCopy
			}
			goto commandPostWrapCopy
		}

		pos += i
		if i > 16 {
			src16 := srcStart + 16
			dst16 := pos - i + 16
			*(*[16]byte)(unsafe.Add(base, dst16)) = *(*[16]byte)(unsafe.Add(base, src16))
			if i > 32 {
				*(*[16]byte)(unsafe.Add(base, dst16+16)) = *(*[16]byte)(unsafe.Add(base, src16+16))
				if i > 48 {
					*(*[16]byte)(unsafe.Add(base, dst16+32)) = *(*[16]byte)(unsafe.Add(base, src16+32))
					if i > 64 {
						*(*[16]byte)(unsafe.Add(base, dst16+48)) = *(*[16]byte)(unsafe.Add(base, src16+48))
						if i > 80 {
							*(*[16]byte)(unsafe.Add(base, dst16+64)) = *(*[16]byte)(unsafe.Add(base, src16+64))
							if i > 96 {
								copy(s.ringbuffer[dst16+80:pos], s.ringbuffer[src16+80:srcEnd])
							}
						}
					}
				}
			}
		}

		goto postCopy
	}

commandPostWrapCopy:
	for i > 0 {
		srcStart := (pos - s.distanceCode) & s.ringbufferMask
		n := min(i, s.ringbufferSize-pos, s.ringbufferSize-srcStart)
		if dist := pos - srcStart; dist > 0 {
			if dist >= n {
				copy(s.ringbuffer[pos:pos+n], s.ringbuffer[srcStart:srcStart+n])
			} else {
				copyOverlappingPattern(unsafe.Pointer(unsafe.SliceData(s.ringbuffer)),
					pos, dist, n)
			}
			pos += n
			i -= n
		} else {
			s.ringbuffer[pos] = s.ringbuffer[srcStart]
			pos++
			i--
		}
		if pos == s.ringbufferSize {
			s.state = decoderStateCommandPostWrite2
			s.pos = pos
			s.loopCounter = i
			return decoderResultNeedsMoreOutput
		}
	}

postCopy:

	if s.metaBlockRemainingLen <= 0 {
		s.state = decoderStateMetablockDone
		s.pos = pos
		s.loopCounter = i
		return decoderResultSuccess
	}
	goto commandBegin
}

type refillCorpusFile struct {
	name string
	data []byte
}

func refillCorpus(tb testing.TB) []refillCorpusFile {
	tb.Helper()
	paths := []string{
		filepath.Join("testdata", "gh_172KB.html"),
		filepath.Join("testdata", "github_events_8k.json"),
		filepath.Join("testdata", "reactcore_187KB.js"),
		filepath.Join("brotli-ref", "tests", "testdata", "plrabn12.txt"),
		filepath.Join("brotli-ref", "tests", "testdata", "lcet10.txt"),
		filepath.Join("brotli-ref", "tests", "testdata", "mapsdatazrh"),
	}
	files := make([]refillCorpusFile, 0, len(paths))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			tb.Fatal(err)
		}
		files = append(files, refillCorpusFile{name: filepath.Base(p), data: data})
	}
	return files
}

func primedBitReader(data []byte, pos int, avail uint) bitReader {
	var br bitReader
	br.setInput(data)
	br.pos = pos
	br.bitPos = avail
	first := uint(pos)*8 - avail
	for i := range avail {
		bit := first + i
		br.val |= uint64(data[bit/8]>>(bit%8)&1) << i
	}
	return br
}

func consumedBits(br *bitReader) int {
	return br.pos*8 - int(br.bitPos)
}

func bitReaderDifference(after, before *bitReader) error {
	var errs []error
	if after.bitPos > 63 {
		errs = append(errs, fmt.Errorf("%d bits buffered; the unconditional refills compute (63-bitPos)>>3, which underflows at 64", after.bitPos))
	}
	if consumedBits(after) != consumedBits(before) {
		errs = append(errs, fmt.Errorf("reader sits at stream bit %d, before the change at %d, so it would skip or repeat input", consumedBits(after), consumedBits(before)))
	}
	n := min(after.bitPos, before.bitPos, 63)
	if after.val&bitMask(n) != before.val&bitMask(n) {
		errs = append(errs, fmt.Errorf("the next %d buffered bits are %#x, before the change %#x", n, after.val&bitMask(n), before.val&bitMask(n)))
	}
	return errors.Join(errs...)
}

func TestFillBitWindowReadsTheSameBitsAsBeforeWithoutEverBufferingSixtyFour(t *testing.T) {
	var errs []error
	for _, f := range refillCorpus(t) {
		for avail := range uint(64) {
			for pos := 8; pos+4 <= len(f.data); pos += 331 {
				before := primedBitReader(f.data, pos, avail)
				after := before
				before.fillBitWindowBefore(24)
				after.fillBitWindow(24)
				err := bitReaderDifference(&after, &before)
				if after.bitPos < 25 {
					err = errors.Join(err, fmt.Errorf("%d bits buffered, fewer than the 25 fillBitWindow(24) promises", after.bitPos))
				}
				if err != nil {
					errs = append(errs, fmt.Errorf("%s byte %d with %d bits buffered: %w", f.name, pos, avail, err))
					break
				}
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
}

var refillCodeLengthCodes = []struct {
	name    string
	lengths [core.AlphabetSizeCodeLengths]byte
}{
	{"single_zero_bit_code_length_6", [core.AlphabetSizeCodeLengths]byte{6: 1}},
	{"single_zero_bit_code_length_8", [core.AlphabetSizeCodeLengths]byte{8: 1}},
	{"complete_with_both_repeat_codes", [core.AlphabetSizeCodeLengths]byte{0: 2, 7: 3, 8: 2, 9: 3, 16: 3, 17: 3}},
	{"complete_spread_over_lengths_0_to_8", [core.AlphabetSizeCodeLengths]byte{0: 3, 1: 4, 2: 4, 3: 4, 4: 4, 6: 3, 7: 3, 8: 2, 16: 4, 17: 4}},
}

func codeLengthReadingState(lengths [core.AlphabetSizeCodeLengths]byte, data []byte, pos int, avail uint) *decodeState {
	s := new(decodeState)
	h := &s.headerArena
	h.codeLengthCodeLengths = lengths
	for _, v := range lengths {
		if v != 0 {
			h.codeLengthHisto[v]++
		}
	}
	core.BuildCodeLengthsHuffmanTable(h.table[:], h.codeLengthCodeLengths[:], h.codeLengthHisto[:])
	h.codeLengthHisto = [16]uint16{}
	h.symbolLists = h.symbolListsArray[:]
	for i := 0; i <= core.HuffmanMaxCodeLength; i++ {
		h.nextSymbol[i] = i - symListBase
		h.symbolListsArray[i] = 0xFFFF
	}
	h.prevCodeLen = core.InitialRepeatedCodeLength
	h.space = 32768
	s.br = primedBitReader(data, pos, avail)
	return s
}

func TestReadSymbolCodeLengthsDecodesTheSameCodeLengthsAsBeforeWithoutEverBufferingSixtyFour(t *testing.T) {
	var errs []error
	for _, f := range refillCorpus(t) {
		for _, code := range refillCodeLengthCodes {
			for _, alphabetSize := range []uint{64, 256, core.AlphabetSizeInsertAndCopyLength} {
				for avail := range uint(64) {
					for pos := 8; pos+4 <= len(f.data); pos += len(f.data)/5 + 1 {
						before := codeLengthReadingState(code.lengths, f.data, pos, avail)
						after := codeLengthReadingState(code.lengths, f.data, pos, avail)
						beforeResult := before.readSymbolCodeLengthsBefore(alphabetSize)
						afterResult := after.readSymbolCodeLengths(alphabetSize)
						err := bitReaderDifference(&after.br, &before.br)
						if afterResult != beforeResult {
							err = errors.Join(err, fmt.Errorf("result %d, before the change %d", afterResult, beforeResult))
						}
						if !reflect.DeepEqual(after.headerArena, before.headerArena) {
							err = errors.Join(err, errors.New("decoded code lengths, symbol lists or Huffman space differ, so a different prefix code would be built"))
						}
						if err != nil {
							errs = append(errs, fmt.Errorf("%s %s alphabet %d byte %d with %d bits buffered: %w", f.name, code.name, alphabetSize, pos, avail, err))
							break
						}
					}
				}
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
}

func processCommandsDifference(after, before *decodeState, afterResult, beforeResult decoderResult) error {
	err := bitReaderDifference(&after.br, &before.br)
	if afterResult != beforeResult {
		err = errors.Join(err, fmt.Errorf("result %d, before the change %d", afterResult, beforeResult))
	}
	if !bytes.Equal(after.ringbuffer, before.ringbuffer) {
		err = errors.Join(err, errors.New("ring buffer contents differ, so the decoded output would differ"))
	}
	after.br.unload()
	before.br.unload()
	a, b := *after, *before
	a.ringbuffer, b.ringbuffer = nil, nil
	a.safeCmdMemento, b.safeCmdMemento = bitReaderState{}, bitReaderState{}
	if fmt.Sprint(a.err) != fmt.Sprint(b.err) {
		err = errors.Join(err, fmt.Errorf("decoder error %q, before %q", fmt.Sprint(a.err), fmt.Sprint(b.err)))
	}
	a.err, b.err = nil, nil
	if !sameDecodeState(a, b) {
		err = errors.Join(err, errors.New("decoder state outside the ring buffer and the bit reader differs"))
	}
	return err
}

func decodeComparingProcessCommandsWithBefore(compressed []byte, chunk int) ([]byte, int, error) {
	s := new(decodeState)
	s.init()
	start, end := 0, min(chunk, len(compressed))
	s.br.setInput(compressed[start:end])
	var output []byte
	compared := 0
	for {
		var result decoderResult
		switch s.state {
		case decoderStateCommandBegin, decoderStateCommandInner,
			decoderStateCommandPostDecodeLiterals, decoderStateCommandPostWrapCopy:
			before := *s
			before.ringbuffer = bytes.Clone(s.ringbuffer)
			result = s.processCommands()
			beforeResult := before.processCommandsBefore()
			if err := processCommandsDifference(s, &before, result, beforeResult); err != nil {
				return nil, compared, fmt.Errorf("processCommands call %d, input chunk at byte %d: %w", compared, start, err)
			}
			compared++
		default:
			result = s.decompressStream(&output)
		}
		switch result {
		case decoderResultSuccess:
			if s.state == decoderStateDone {
				return s.flushOutput(output), compared, nil
			}
		case decoderResultNeedsMoreInput:
			if end == len(compressed) {
				return nil, compared, errors.New("stream ended before its last metablock")
			}
			s.br.unload()
			start += s.br.pos
			end = min(end+chunk, len(compressed))
			s.br.setInput(compressed[start:end])
		case decoderResultNeedsMoreOutput:
			output = s.flushOutput(output)
		default:
			return nil, compared, s.err
		}
	}
}

func compressWithWindow(tb testing.TB, data []byte, quality, lgwin int) []byte {
	tb.Helper()
	var buf bytes.Buffer
	w, err := NewWriterOptions(&buf, quality, WriterOptions{LGWin: lgwin})
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		tb.Fatal(err)
	}
	if err := w.Close(); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

func processCommandsMatchesBefore(t *testing.T, compressed, original []byte, requireComparison bool) {
	chunk := min(4093, max(67, len(compressed)/8))
	out, compared, err := decodeComparingProcessCommandsWithBefore(compressed, chunk)
	if err != nil {
		t.Fatal(err)
	}
	var errs []error
	if !bytes.Equal(out, original) {
		errs = append(errs, fmt.Errorf("decoded %d bytes that differ from the %d-byte original", len(out), len(original)))
	}
	if requireComparison && compared == 0 {
		errs = append(errs, errors.New("no processCommands call was compared, so the chunked feed never paused inside a metablock body"))
	}
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
}

func TestProcessCommandsMatchesTheConditionalRefillVersionOnRealStreamsFedInChunks(t *testing.T) {
	for _, f := range refillCorpus(t) {
		for _, quality := range []int{0, 1, 2, 5, 9, 11} {
			for _, lgwin := range []int{16, 22} {
				t.Run(fmt.Sprintf("%s/q%d_lgwin%d", f.name, quality, lgwin), func(t *testing.T) {
					t.Parallel()
					original := f.data
					if quality >= 10 {
						original = original[:min(len(original), 256<<10)]
					}
					processCommandsMatchesBefore(t, compressWithWindow(t, original, quality, lgwin), original, true)
				})
			}
		}
	}
	dir := filepath.Join("brotli-ref", "tests", "testdata")
	vectors, err := filepath.Glob(filepath.Join(dir, "*.compressed*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		t.Run(filepath.Base(vector), func(t *testing.T) {
			t.Parallel()
			compressed, err := os.ReadFile(vector)
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(compressedSuffix.ReplaceAllString(vector, ""))
			if err != nil {
				t.Fatal(err)
			}
			processCommandsMatchesBefore(t, compressed, original, false)
		})
	}
}

func sameDecodeState(a, b any) bool {
	return reflect.DeepEqual(a, b)
}
