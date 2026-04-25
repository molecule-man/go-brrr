// Streaming encoder interface, window-bit encoding, and shared utility functions.

package brrr

import (
	"math/bits"
	"sync"
)

// Hasher pools keyed by concrete type. Using sync.Pool avoids the expensive
// zero-initialization of large bucket arrays (up to 32 MB for h5b8/h6b8) on
// every oneshot compression. The hasher's reset() method only zeroes the small
// num[] counter array, not the full bucket storage.
//
// poolEncoderSplit recycles encoderSplit instances across oneshot calls. The
// encoder's reusable slice buffers (commands, outBuf, splitBufs, etc.) retain
// their capacity, so subsequent calls skip the large per-call allocations that
// would otherwise dominate GC pressure for small inputs.
var (
	poolEncoderArena = sync.Pool{New: func() any { return new(encoderArena) }}
	poolEncoderSplit = sync.Pool{New: func() any { return new(encoderSplit) }}
	poolH2           = sync.Pool{New: func() any { return new(h2) }}
	poolH3           = sync.Pool{New: func() any { return new(h3) }}
	poolH4           = sync.Pool{New: func() any { return new(h4) }}
	poolH5           = sync.Pool{New: func() any { return new(h5) }}
	poolH54          = sync.Pool{New: func() any { return new(h54) }}
	poolH5b5         = sync.Pool{New: func() any { return new(h5b5) }}
	poolH5b6         = sync.Pool{New: func() any { return new(h5b6) }}
	poolH5b7         = sync.Pool{New: func() any { return new(h5b7) }}
	poolH5b8         = sync.Pool{New: func() any { return new(h5b8) }}
	poolH6           = sync.Pool{New: func() any { return new(h6) }}
	poolH6b5         = sync.Pool{New: func() any { return new(h6b5) }}
	poolH6b6         = sync.Pool{New: func() any { return new(h6b6) }}
	poolH6b7         = sync.Pool{New: func() any { return new(h6b7) }}
	poolH6b8         = sync.Pool{New: func() any { return new(h6b8) }}
)

// streamEncoder is the interface connecting the Writer to quality-specific
// streaming encoders.
type streamEncoder interface {
	encodeData(isLast, forceFlush bool) []byte
	copyInputToRingBuffer(p []byte)
	remainingInputBlockSize() uint
	trailingBits() (lastBytes uint16, lastBytesBits uint8)
	clearTrailingBits()
	reset(quality, lgwin int, sizeHint uint)
	updateSizeHint(availableIn uint)
	attachDictionary(data []byte) error
	releaseBuffers()
}

// encoderCore holds state and methods shared by all streaming quality levels
// (Q2–Q10). Quality-specific structs embed this and provide their own encoding
// buffers and compressed-block writers.
type encoderCore struct {
	hasher streamHasher
	encodeState
}

// encoderArena is the Q2/Q3 streaming encoder. It uses a fixed-size arena for
// histogram accumulation and Huffman code building.
type encoderArena struct {
	encoderCore
	arena metablockArena
}

// q10Bufs holds reusable buffers for the Q10 slow path (DP block splitting,
// histogram clustering, Zopfli backward references). All slices use
// grow-and-reuse semantics: grown on demand, never shrunk. After the first
// metablock they typically never allocate again.
type q10Bufs struct {
	// splitByteVector scratch (reused across 3 calls per splitBlock).
	svHistograms []uint32
	svBlockIDs   []byte
	svFloat      []float64 // combined: insertCost + cost
	svSwitchSig  []byte
	svNewID      []uint16

	// clusterBlocks scratch.
	cbHistSymbols   []uint32
	cbAllHistograms []uint32
	cbClusterSizes  []uint32
	cbBatchHist     []uint32
	cbPairs         []histogramPair
	cbTmpHist       []uint32
	cbBatchU32      []uint32 // combined: sizes + newClusters + symbols + remap (4×64)
	cbBlockLengths  []uint32
	cbBatchFloat    []float64 // combined: batchBitCosts + allBitCosts
	cbBatchTotals   []uint32  // combined: batchTotals + allTotals
	cbClusters      []uint32
	cbNewIndex      []uint32

	// splitBlock scratch.
	sbLiteralBytes []byte
	sbUint16       []uint16 // shared for literals, cmdPrefixes, distPrefixes

	// buildMetaBlock scratch.
	bmTmpHist      []uint32
	bmContextModes []byte
	bmLitHist      []uint32
	bmDistHist     []uint32
	bmLitOutHist   []uint32
	bmDistOutHist  []uint32

	// clusterHistograms scratch.
	chClusterSize []uint32
	chClusters    []uint32
	chBitCosts    []float64
	chTotalCounts []uint32
	chSymbols     []uint32
	chTmpHist     []uint32
	chPairs       []histogramPair

	// histogramReindex scratch.
	hrNewIndex    []uint32
	hrTmpData     []uint32
	hrTmpBitCosts []float64
	hrTmpTotals   []uint32

	// Zopfli backward references scratch.
	zNodes   []zopfliNode
	zMatches []backwardMatch

	// Q11 HQ Zopfli scratch.
	hqNumMatchesArr []uint32
	hqMatches       []backwardMatch

	zCostModel zopfliCostModel // large value type; keep last to minimize pointer bytes
}

// encoderSplit is the Q4–Q10 streaming encoder. It uses greedy block splitting,
// histogram optimization, and optional literal context modeling (Q5+).
// Q10 uses the Zopfli optimal parsing algorithm for backward references.
type encoderSplit struct {
	rleSymBuf  []uint32 // reusable buffer for encodeContextMapBuf
	litDepths  []byte
	litBits    []uint16
	cmdDepths  []byte
	cmdBits    []uint16
	distDepths []byte
	distBits   []uint16
	goodForRLE []bool
	prevHasher streamHasher // stashed hasher from previous reset for reuse in chooseHasher
	splitBufs  splitBufs
	mb         metaBlockSplit
	encoderCore
	q10  q10Bufs
	tree [2*alphabetSizeInsertAndCopyLength + 1]huffmanTreeNode
}

// releaseBuffers extends encoderCore.releaseBuffers to also return the stashed
// prevHasher to its pool.
func (e *encoderSplit) releaseBuffers() {
	releaseHasher(e.prevHasher)
	e.prevHasher = nil
	e.encoderCore.releaseBuffers()
}

// resetHasher marks the hasher as needing re-initialization and updates the
// hashers tracking slice.
func (c *encoderCore) resetHasher() {
	c.hasher.common().ready = false
	if c.hashers == nil {
		c.hashers = []*hasherCommon{c.hasher.common()}
	} else {
		c.hashers[0] = c.hasher.common()
	}
}

func (c *encoderCore) updateSizeHint(availableIn uint) {
	c.encodeState.updateSizeHint(availableIn)
}

// releaseBuffers returns poolable buffers (ring buffer, hasher) for reuse.
func (c *encoderCore) releaseBuffers() {
	c.releaseRingBuffer()
	releaseHasher(c.hasher)
	c.hasher = nil
}

// releaseHasher returns a hasher to its type-specific pool.
func releaseHasher(h streamHasher) {
	switch h := h.(type) {
	case *h2:
		poolH2.Put(h)
	case *h3:
		poolH3.Put(h)
	case *h4:
		poolH4.Put(h)
	case *h5:
		poolH5.Put(h)
	case *h54:
		poolH54.Put(h)
	case *h5b5:
		poolH5b5.Put(h)
	case *h5b6:
		poolH5b6.Put(h)
	case *h5b7:
		poolH5b7.Put(h)
	case *h5b8:
		poolH5b8.Put(h)
	case *h6:
		poolH6.Put(h)
	case *h6b5:
		poolH6b5.Put(h)
	case *h6b6:
		poolH6b6.Put(h)
	case *h6b7:
		poolH6b7.Put(h)
	case *h6b8:
		poolH6b8.Put(h)
	}
}

// stitchHasher resets the hash table on first use (or after a 32-bit position
// wrap) and stitches to the previous block.
func (c *encoderCore) stitchHasher(isLast bool) {
	s := &c.encodeState
	if !c.hasher.common().ready {
		oneShot := s.lastProcessedPos == 0 && isLast
		inputSize := s.unprocessedInputSize()
		c.hasher.reset(oneShot, uint(inputSize), s.data)
	}

	position := wrapPosition(s.lastProcessedPos)
	inputSize := uint(s.unprocessedInputSize())
	c.hasher.stitchToPreviousBlock(inputSize, position, s.data, uint(s.mask))
}

// prepareMetaBlock runs backward-reference search and flush heuristics.
// Returns the metablock size to encode, or 0 if the caller should return
// earlyResult (which may be nil).
func (c *encoderCore) prepareMetaBlock(isLast, forceFlush bool) (metablockSize uint32, earlyResult []byte, ready bool) {
	s := &c.encodeState
	delta := s.unprocessedInputSize()
	bytes := uint32(delta)

	if delta == 0 {
		if !isLast && !forceFlush {
			return 0, nil, false
		}
		if s.inputPos == s.lastFlushPos {
			if isLast {
				// Empty stream: emit ISLAST + ISEMPTY.
				s.lastBytes |= uint16(3) << s.lastBytesBits
				s.lastBytesBits += 2
				n := (s.lastBytesBits + 7) / 8
				out := make([]byte, n)
				out[0] = byte(s.lastBytes)
				if n > 1 {
					out[1] = byte(s.lastBytes >> 8)
				}
				s.lastBytes = 0
				s.lastBytesBits = 0
				return 0, out, false
			}
			return 0, nil, false
		}
		// Fall through: flush accumulated commands from prior merged blocks.
	}

	// Grow command buffer if needed.
	needed := int(s.numCommands) + int(bytes)/2 + 1
	if needed > cap(s.commands) {
		newCap := needed + int(bytes)/4 + 16
		newCmds := make([]command, len(s.commands), newCap)
		copy(newCmds, s.commands)
		s.commands = newCmds
	}

	wrappedPos := uint32(wrapPosition(s.lastProcessedPos))

	if s.numCommands > 0 && s.lastInsertLen == 0 {
		if s.cmdHisto != nil {
			// Save cmdPrefix before potential modification by extendLastCommand,
			// so the inline histogram can be corrected if it changes.
			oldPrefix := s.commands[s.numCommands-1].cmdPrefix
			bytes, wrappedPos = s.extendLastCommand(bytes, wrappedPos)
			lastCmd := &s.commands[s.numCommands-1]
			if newPrefix := lastCmd.cmdPrefix; newPrefix != oldPrefix {
				s.cmdHisto[oldPrefix]--
				s.cmdHisto[newPrefix]++
				// combineLengthCodes takes the "short" path (< 128) when
				// useLastDistance && insCode < 8 && copyCode < 16. Extending
				// copyLen can push copyCode across 16, flipping cmdPrefix from
				// < 128 to >= 128. The decoder then expects a distance symbol
				// for this command, so distHisto must account for it.
				if oldPrefix < 128 && newPrefix >= 128 {
					s.distHisto[lastCmd.distPrefixCode()]++
				}
			}
		} else {
			bytes, wrappedPos = s.extendLastCommand(bytes, wrappedPos)
		}
	}

	c.stitchHasher(isLast)
	c.hasher.createBackwardReferences(s, bytes, wrappedPos)

	// Flush heuristic: emit the metablock if it's full enough.
	maxLength := s.maxMetablockSize()
	processedBytes := s.inputPos - s.lastFlushPos
	nextInputFits := processedBytes+uint64(s.inputBlockSize()) <= uint64(maxLength)

	shouldFlush := false
	if s.quality <= 3 {
		const maxNumDelayedSymbols = 0x2FFF
		shouldFlush = s.numLiterals+s.numCommands >= maxNumDelayedSymbols
	}

	if !isLast && !forceFlush && !shouldFlush &&
		nextInputFits &&
		s.numLiterals < maxLength/8 &&
		s.numCommands < maxLength/8 {
		// Merge with next input block.
		s.updateLastProcessedPos()
		return 0, nil, false
	}

	// Create the final insert-only command for trailing literals.
	if s.lastInsertLen > 0 {
		trailingCmd := newInsertCommand(s.lastInsertLen)
		s.commands = append(s.commands, trailingCmd)
		s.numCommands++
		s.numLiterals += s.lastInsertLen
		if s.cmdHisto != nil {
			// Tally the trailing literal command into the inline histograms.
			s.cmdHisto[trailingCmd.cmdPrefix]++
			startPos := s.inputPos - uint64(s.lastInsertLen)
			for j := uint64(0); j < uint64(s.lastInsertLen); j++ {
				s.litHisto[s.data[uint32(startPos+j)&s.mask]]++
			}
		}
		s.lastInsertLen = 0
	}

	if !isLast && s.inputPos == s.lastFlushPos {
		return 0, nil, false
	}

	return uint32(s.inputPos - s.lastFlushPos), nil, true
}

// initMetaBlockOutput allocates and seeds the output buffer for a metablock.
func (c *encoderCore) initMetaBlockOutput(metablockSize uint32) {
	s := &c.encodeState

	outSize := 2*int(metablockSize) + 503
	if cap(s.outBuf) < outSize {
		s.outBuf = make([]byte, outSize)
	} else {
		s.outBuf = s.outBuf[:outSize]
	}

	s.outBuf[0] = byte(s.lastBytes)
	s.outBuf[1] = byte(s.lastBytes >> 8)
	s.b = bitWriter{buf: s.outBuf, bitOffset: uint(s.lastBytesBits)}
}

// finishMetaBlock extracts trailing bits and resets per-metablock state.
func (c *encoderCore) finishMetaBlock() []byte {
	s := &c.encodeState

	s.lastBytes = uint16(s.outBuf[s.b.bitOffset/8])
	s.lastBytesBits = uint8(s.b.bitOffset & 7)

	s.lastFlushPos = s.inputPos
	s.updateLastProcessedPos()

	if s.lastFlushPos > 0 {
		s.prevByte = s.data[(uint32(s.lastFlushPos)-1)&s.mask]
	}
	if s.lastFlushPos > 1 {
		s.prevByte2 = s.data[(uint32(s.lastFlushPos)-2)&s.mask]
	}

	s.numCommands = 0
	s.numLiterals = 0
	s.commands = s.commands[:0]
	s.savedDistCache = s.distCache

	return s.outBuf[:s.b.bitOffset/8]
}

func (e *encoderArena) reset(quality, lgwin int, sizeHint uint) {
	e.encodeState.reset(quality, lgwin, sizeHint)

	if e.hasher == nil {
		if quality <= 2 {
			e.hasher = poolH2.Get().(*h2)
		} else {
			e.hasher = poolH3.Get().(*h3)
		}
	}

	e.resetHasher()
}

func (e *encoderArena) encodeData(isLast, forceFlush bool) []byte {
	s := &e.encodeState
	// For qualities 2 and 3, initialize histogram slices on the first block of
	// each metablock so that createBackwardReferences can accumulate them
	// inline, skipping the separate tally pass in writeMetaBlockFast /
	// writeMetaBlockTrivial.
	if s.quality <= 3 && s.cmdHisto == nil {
		e.arena.resetHistograms()
		s.litHisto = e.arena.litHisto[:]
		s.cmdHisto = e.arena.cmdHisto[:]
		s.distHisto = e.arena.distHisto[:]
	}
	metablockSize, earlyResult, ready := e.prepareMetaBlock(isLast, forceFlush)
	if !ready {
		return earlyResult
	}
	e.initMetaBlockOutput(metablockSize)
	e.writeMetaBlockInternal(int(metablockSize), int(e.numLiterals), int(e.numCommands), isLast)
	return e.finishMetaBlock()
}

// writeMetaBlockInternal decides whether to compress or emit uncompressed,
// and falls back to uncompressed when compression expands the data.
func (e *encoderArena) writeMetaBlockInternal(length, numLiterals, numCommands int, isLast bool) {
	s := &e.encodeState
	b := &s.b

	if length == 0 {
		b.writeBits(2, 3)
		b.byteAlign()
		return
	}

	if !s.shouldCompress(length, numLiterals, numCommands) {
		s.distCache = s.savedDistCache
		// Discard pre-built histograms: uncompressed block means the next
		// metablock must start with a fresh histogram accumulation.
		s.litHisto = nil
		s.cmdHisto = nil
		s.distHisto = nil
		s.writeUncompressedMetaBlock(length, isLast)
		return
	}

	savedBuf0 := b.buf[0]
	savedBuf1 := b.buf[1]
	savedBitOffset := b.bitOffset

	if s.quality <= 2 {
		e.writeMetaBlockFast(length, isLast)
	} else {
		e.writeMetaBlockTrivial(length, isLast)
	}

	if uint(length)+4 < b.bitOffset/8 {
		s.distCache = s.savedDistCache
		b.buf[0] = savedBuf0
		b.buf[1] = savedBuf1
		b.bitOffset = savedBitOffset
		// Histograms were discarded by writeMetaBlockTrivial/writeMetaBlockFast;
		// no inline state to clean up here.
		s.writeUncompressedMetaBlock(length, isLast)
	}
}

// writeMetaBlockFast encodes commands into a compressed meta-block using the
// fast Huffman tree builder. For <= 128 commands, uses static command/distance
// prefix codes. For more commands, builds all three prefix codes dynamically.
func (e *encoderArena) writeMetaBlockFast(length int, isLast bool) {
	s := &e.encodeState
	b := &s.b
	arena := &e.arena
	input := s.data
	startPos := wrapPosition(s.lastFlushPos)
	mask := uint(s.mask)
	commands := s.commands
	distAlphabetBits := uint(bits.Len(s.distAlphabetSizeMax - 1))

	b.writeMetaBlockHeader(length, isLast, false)
	// No block splits, no context maps.
	b.writeBits(13, 0)

	prebuilt := s.cmdHisto != nil
	if prebuilt {
		// Histograms pre-built by createBackwardReferences; skip the tally pass.
		s.litHisto = nil
		s.cmdHisto = nil
		s.distHisto = nil
	}

	if len(commands) <= 128 {
		// Fast path: build literal codes only, use static cmd/dist codes.
		litHisto := &arena.litHisto
		var numLiterals uint
		if prebuilt {
			numLiterals = s.numLiterals
		} else {
			*litHisto = [alphabetSizeLiteral]uint32{}
			pos := startPos
			for i := range commands {
				cmd := &commands[i]
				for j := cmd.insertLen; j != 0; j-- {
					litHisto[input[pos&mask]]++
					pos++
				}
				numLiterals += uint(cmd.insertLen)
				pos += uint(cmd.copyLength())
			}
		}

		b.buildAndWriteHuffmanTreeFast(arena.tree[:], litHisto[:], numLiterals,
			8, arena.litDepth[:], arena.litBits[:])
		b.writeStaticCommandHuffmanTree()
		b.writeStaticDistanceHuffmanTree()
		huffmanBlock{
			input:     input,
			commands:  commands,
			litDepth:  arena.litDepth[:],
			litBits:   arena.litBits[:],
			cmdDepth:  staticCommandCodeDepth[:],
			cmdBits:   staticCommandCodeBits[:],
			distDepth: staticDistanceCodeDepth[:],
			distBits:  staticDistanceCodeBits[:],
			startPos:  startPos,
			mask:      mask,
		}.writeData(b)
	} else {
		// Normal path: build all three prefix codes from histogram data.
		var litTotal, distTotal uint
		if prebuilt {
			litTotal = s.numLiterals
			for _, c := range &arena.distHisto {
				distTotal += uint(c)
			}
		} else {
			arena.resetHistograms()

			hist := blockHistograms{
				lit:  arena.litHisto[:],
				cmd:  arena.cmdHisto[:],
				dist: arena.distHisto[:],
			}
			pos := startPos
			for i := range commands {
				cmd := commands[i]
				posDelta, distDelta := hist.tally(input, pos, mask, cmd)
				pos += posDelta
				litTotal += uint(cmd.insertLen)
				distTotal += distDelta
			}
		}

		b.buildAndWriteHuffmanTreeFast(arena.tree[:], arena.litHisto[:], litTotal,
			8, arena.litDepth[:], arena.litBits[:])
		b.buildAndWriteHuffmanTreeFast(arena.tree[:], arena.cmdHisto[:], uint(len(commands)),
			10, arena.cmdDepth[:], arena.cmdBits[:])
		b.buildAndWriteHuffmanTreeFast(arena.tree[:], arena.distHisto[:], distTotal,
			distAlphabetBits, arena.distDepth[:], arena.distBits[:])
		huffmanBlock{
			input:     input,
			commands:  commands,
			litDepth:  arena.litDepth[:],
			litBits:   arena.litBits[:],
			cmdDepth:  arena.cmdDepth[:],
			cmdBits:   arena.cmdBits[:],
			distDepth: arena.distDepth[:],
			distBits:  arena.distBits[:],
			startPos:  startPos,
			mask:      mask,
		}.writeData(b)
	}

	if isLast {
		b.byteAlign()
	}
}

// writeMetaBlockTrivial encodes commands into a compressed meta-block using
// the full Huffman tree builder for all three prefix codes.
func (e *encoderArena) writeMetaBlockTrivial(length int, isLast bool) {
	s := &e.encodeState
	b := &s.b
	arena := &e.arena
	input := s.data
	startPos := wrapPosition(s.lastFlushPos)
	mask := uint(s.mask)
	commands := s.commands

	b.writeMetaBlockHeader(length, isLast, false)

	if s.cmdHisto != nil {
		// Histograms pre-built by createBackwardReferences; skip the tally pass.
		s.litHisto = nil
		s.cmdHisto = nil
		s.distHisto = nil
	} else {
		arena.resetHistograms()

		hist := blockHistograms{
			lit:  arena.litHisto[:],
			cmd:  arena.cmdHisto[:],
			dist: arena.distHisto[:],
		}
		pos := startPos
		for i := range commands {
			cmd := commands[i]
			posDelta, _ := hist.tally(input, pos, mask, cmd)
			pos += posDelta
		}
	}

	// No block splits, no context maps.
	b.writeBits(13, 0)

	b.buildAndWriteHuffmanTree(
		arena.litHisto[:], alphabetSizeLiteral, arena.tree[:],
		arena.litDepth[:], arena.litBits[:])
	b.buildAndWriteHuffmanTree(
		arena.cmdHisto[:], alphabetSizeInsertAndCopyLength, arena.tree[:],
		arena.cmdDepth[:], arena.cmdBits[:])
	b.buildAndWriteHuffmanTree(
		arena.distHisto[:], s.distAlphabetSizeMax, arena.tree[:],
		arena.distDepth[:], arena.distBits[:])

	huffmanBlock{
		input:     input,
		commands:  commands,
		litDepth:  arena.litDepth[:],
		litBits:   arena.litBits[:],
		cmdDepth:  arena.cmdDepth[:],
		cmdBits:   arena.cmdBits[:],
		distDepth: arena.distDepth[:],
		distBits:  arena.distBits[:],
		startPos:  startPos,
		mask:      mask,
	}.writeData(b)

	if isLast {
		b.byteAlign()
	}
}

// preallocQ10 pre-sizes all q10Bufs slices for a metablock of at most
// blockSize bytes, so that the first compression pass does not trigger
// grow-and-reuse allocations. Buffers that depend on runtime block
// splitting results use conservative upper bounds; any that turn out
// too small will still grow transparently.
func (b *q10Bufs) preallocQ10(blockSizeArg int) {
	// Use 2× block size to cover common cases where a single write
	// exceeds one block (the ring buffer can hold more).
	blockSize := 2 * blockSizeArg
	// Maximum histogram counts (from block_splitter constants).
	const (
		maxLitHist     = 100                             // maxLiteralHistograms
		maxCmdHist     = 50                              // maxCommandHistograms
		maxDistHist    = 50                              // maxCommandHistograms (distances use same limit)
		maxAlpha       = alphabetSizeInsertAndCopyLength // 704, largest alphabet
		litAlpha       = alphabetSizeLiteral             // 256
		distAlpha      = numHistogramDistanceSymbols     // 544
		hpb            = 64                              // histogramsPerBatch
		cpb            = 16                              // clustersPerBatch
		maxPairs       = hpb*hpb/2 + 1
		maxBlockTypes  = maxNumberOfBlockTypes
		litContextMul  = 64 // 1 << literalContextBits
		distContextMul = 4  // 1 << distanceContextBits
	)

	// Upper bound for numBlocks from splitByteVector. In practice much
	// smaller, but we want to avoid first-use allocs.
	estBlocks := min(blockSize/64+1, 4096)
	estClusters := cpb * (estBlocks + hpb - 1) / hpb

	// splitByteVector scratch (largest category: literals with alphabetSize=704).
	b.svHistograms = preallocUint32(b.svHistograms, (maxLitHist+1)*maxAlpha)
	b.svBlockIDs = preallocByte(b.svBlockIDs, blockSize)
	b.svFloat = preallocFloat64(b.svFloat, maxAlpha*maxLitHist+maxLitHist)
	bitmapLen := (maxLitHist + 7) >> 3
	b.svSwitchSig = preallocByte(b.svSwitchSig, blockSize*bitmapLen)
	b.svNewID = preallocUint16(b.svNewID, maxLitHist)

	// clusterBlocks scratch.
	b.cbHistSymbols = preallocUint32(b.cbHistSymbols, estBlocks)
	b.cbAllHistograms = preallocUint32(b.cbAllHistograms, estClusters*maxAlpha)
	b.cbClusterSizes = preallocUint32(b.cbClusterSizes, estClusters)
	b.cbBatchHist = preallocUint32(b.cbBatchHist, hpb*maxAlpha)
	b.cbPairs = preallocHistogramPairs(b.cbPairs, maxPairs)
	b.cbTmpHist = preallocUint32(b.cbTmpHist, 2*maxAlpha)
	b.cbBatchU32 = preallocUint32(b.cbBatchU32, 4*hpb)
	b.cbBlockLengths = preallocUint32(b.cbBlockLengths, estBlocks)
	b.cbBatchFloat = preallocFloat64(b.cbBatchFloat, max(hpb, estClusters))
	b.cbBatchTotals = preallocUint32(b.cbBatchTotals, max(hpb, estClusters))
	b.cbClusters = preallocUint32(b.cbClusters, estClusters)
	b.cbNewIndex = preallocUint32(b.cbNewIndex, estClusters)

	// splitBlock scratch.
	b.sbLiteralBytes = preallocByte(b.sbLiteralBytes, blockSize)
	b.sbUint16 = preallocUint16(b.sbUint16, blockSize)

	// buildMetaBlock scratch.
	b.bmTmpHist = preallocUint32(b.bmTmpHist, maxAlpha)
	b.bmContextModes = preallocByte(b.bmContextModes, maxBlockTypes)
	litHistSize := maxBlockTypes * litContextMul
	b.bmLitHist = preallocUint32(b.bmLitHist, litHistSize*litAlpha)
	distHistSize := maxBlockTypes * distContextMul
	b.bmDistHist = preallocUint32(b.bmDistHist, distHistSize*distAlpha)
	b.bmLitOutHist = preallocUint32(b.bmLitOutHist, litHistSize*litAlpha)
	b.bmDistOutHist = preallocUint32(b.bmDistOutHist, distHistSize*distAlpha)

	// clusterHistograms scratch.
	maxInSize := litHistSize // largest input to clusterHistograms
	b.chClusterSize = preallocUint32(b.chClusterSize, maxInSize)
	b.chClusters = preallocUint32(b.chClusters, maxInSize)
	b.chBitCosts = preallocFloat64(b.chBitCosts, maxInSize)
	b.chTotalCounts = preallocUint32(b.chTotalCounts, maxInSize)
	b.chSymbols = preallocUint32(b.chSymbols, maxInSize)
	b.chTmpHist = preallocUint32(b.chTmpHist, maxAlpha)
	b.chPairs = preallocHistogramPairs(b.chPairs, maxPairs)

	// histogramReindex scratch.
	b.hrNewIndex = preallocUint32(b.hrNewIndex, maxInSize)
	b.hrTmpData = preallocUint32(b.hrTmpData, maxBlockTypes*maxAlpha)
	b.hrTmpBitCosts = preallocFloat64(b.hrTmpBitCosts, maxBlockTypes)
	b.hrTmpTotals = preallocUint32(b.hrTmpTotals, maxBlockTypes)

	// Zopfli backward references scratch.
	b.zNodes = preallocZopfliNodes(b.zNodes, blockSize+1)
	b.zMatches = preallocBackwardMatches(b.zMatches, 2*(h10MaxNumMatches+64))
	if cap(b.zCostModel.literalCosts) < blockSize+2 {
		b.zCostModel.literalCosts = make([]float32, 0, blockSize+2)
	}
	if cap(b.zCostModel.costDist) < 64 {
		b.zCostModel.costDist = make([]float32, 0, 64)
	}

	// Q11 HQ Zopfli scratch.
	b.hqNumMatchesArr = preallocUint32(b.hqNumMatchesArr, blockSize)
	b.hqMatches = preallocBackwardMatches(b.hqMatches, 4*blockSize)
}

func (e *encoderSplit) reset(quality, lgwin int, sizeHint uint) {
	e.encodeState.reset(quality, lgwin, sizeHint)

	// Clear context maps that the Q10+ slow path sets but the Q4–Q9 greedy
	// path does not. A pooled encoder that was previously used at Q10+ would
	// otherwise carry stale non-empty maps into a lower-quality job, causing
	// writeMetaBlock to enable distance/literal context encoding incorrectly.
	e.mb.distanceContextMap = e.mb.distanceContextMap[:0]
	e.mb.literalContextMap = e.mb.literalContextMap[:0]

	// For Q4–Q9, hasher creation is deferred to chooseHasher (called lazily
	// from encodeData on first use). This matches the C reference's
	// HasherSetup, which calls ChooseHasher after UpdateSizeHint has
	// auto-calculated the size hint from the first Write call.
	// Q10+ always uses h10 regardless of sizeHint, so keep eagerly.
	if quality < 10 && sizeHint == 0 {
		// Must re-choose hasher after auto-sizeHint is calculated.
		// chooseHasher will reuse the existing hasher when the type matches.
		if e.hasher != nil {
			e.prevHasher = e.hasher
			e.hasher = nil
		}
	} else if e.hasher != nil {
		e.resetHasher()
	}

	// Pre-allocate Q10 buffers to avoid first-use allocations.
	if quality >= 10 {
		if e.hasher == nil {
			e.hasher = &h10{lgwin: lgwin, quality: quality, bufs: &e.q10}
			e.resetHasher()
		}
		blockSize := 1 << e.lgblock
		e.q10.preallocQ10(blockSize)

		// Pre-allocate h10 forest.
		if h, ok := e.hasher.(*h10); ok {
			numNodes := uint(1) << lgwin
			if cap(h.forest) < int(2*numNodes) {
				h.forest = make([]uint32, 2*numNodes)
			}
		}

		// Pre-allocate metaBlockSplit context maps.
		const (
			maxTypes       = 256
			litContextMul  = 64 // 1 << literalContextBits
			distContextMul = 4  // 1 << distanceContextBits
		)
		e.mb.literalContextMap = preallocUint32(e.mb.literalContextMap, maxTypes*litContextMul)
		e.mb.distanceContextMap = preallocUint32(e.mb.distanceContextMap, maxTypes*distContextMul)
		e.mb.cmdHistograms = preallocUint32(e.mb.cmdHistograms, maxTypes*alphabetSizeInsertAndCopyLength)

		// Pre-allocate blockSplit types/lengths for each category.
		e.mb.litSplit.types = preallocByte(e.mb.litSplit.types, maxTypes)
		e.mb.litSplit.lengths = preallocUint32(e.mb.litSplit.lengths, maxTypes)
		e.mb.cmdSplit.types = preallocByte(e.mb.cmdSplit.types, maxTypes)
		e.mb.cmdSplit.lengths = preallocUint32(e.mb.cmdSplit.lengths, maxTypes)
		e.mb.distSplit.types = preallocByte(e.mb.distSplit.types, maxTypes)
		e.mb.distSplit.lengths = preallocUint32(e.mb.distSplit.lengths, maxTypes)

		// Pre-allocate encoderSplit Huffman code buffers.
		// Sizes are numHistograms * alphabetSize; use maxTypes as a safe bound.
		e.litDepths = preallocByte(e.litDepths, maxTypes*litContextMul*alphabetSizeLiteral)
		e.litBits = preallocUint16(e.litBits, maxTypes*litContextMul*alphabetSizeLiteral)
		e.cmdDepths = preallocByte(e.cmdDepths, maxTypes*alphabetSizeInsertAndCopyLength)
		e.cmdBits = preallocUint16(e.cmdBits, maxTypes*alphabetSizeInsertAndCopyLength)
		e.distDepths = preallocByte(e.distDepths, maxTypes*distContextMul*numHistogramDistanceSymbols)
		e.distBits = preallocUint16(e.distBits, maxTypes*distContextMul*numHistogramDistanceSymbols)
		e.rleSymBuf = preallocUint32(e.rleSymBuf, maxTypes*litContextMul)
		if cap(e.goodForRLE) < alphabetSizeInsertAndCopyLength {
			e.goodForRLE = make([]bool, 0, alphabetSizeInsertAndCopyLength)
		}
	}
}

// chooseHasher selects and creates the hasher for this encoder. Called lazily
// from encodeData so that the auto-calculated sizeHint (from the first Write)
// is available. This matches the C reference's HasherSetup → ChooseHasher flow.
// When a previous hasher of the same type is stashed in prevHasher (from a
// prior reset cycle), it is reused to avoid re-allocating large hash tables.
func (e *encoderSplit) chooseHasher() {
	s := &e.encodeState
	prev := e.prevHasher
	e.prevHasher = nil
	switch {
	case s.quality <= 4:
		if s.quality == 4 && s.sizeHint >= 1<<20 {
			if h, ok := prev.(*h54); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = poolH54.Get().(*h54)
			}
		} else {
			if h, ok := prev.(*h4); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = poolH4.Get().(*h4)
			}
		}
	case s.quality <= 5:
		switch {
		case s.lgwin <= 16:
			if h, ok := prev.(*h40); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = &h40{maxHops: 16}
			}
		case s.sizeHint >= 1<<20 && s.lgwin >= 19:
			if h, ok := prev.(*h6); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = poolH6.Get().(*h6)
			}
		default:
			if h, ok := prev.(*h5); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = poolH5.Get().(*h5)
			}
		}
	case s.quality <= 6:
		switch {
		case s.lgwin <= 16:
			if h, ok := prev.(*h40); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = &h40{maxHops: 32}
			}
		case s.sizeHint >= 1<<20 && s.lgwin >= 19:
			if h, ok := prev.(*h6b5); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = poolH6b5.Get().(*h6b5)
			}
		default:
			if h, ok := prev.(*h5b5); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = poolH5b5.Get().(*h5b5)
			}
		}
	case s.quality <= 7:
		switch {
		case s.lgwin <= 16:
			if h, ok := prev.(*h41); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = &h41{maxHops: 56}
			}
		case s.sizeHint >= 1<<20 && s.lgwin >= 19:
			if h, ok := prev.(*h6b6); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = poolH6b6.Get().(*h6b6)
			}
		default:
			if h, ok := prev.(*h5b6); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = poolH5b6.Get().(*h5b6)
			}
		}
	case s.quality <= 8:
		switch {
		case s.lgwin <= 16:
			if h, ok := prev.(*h41); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = &h41{maxHops: 112}
			}
		case s.sizeHint >= 1<<20 && s.lgwin >= 19:
			if h, ok := prev.(*h6b7); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = poolH6b7.Get().(*h6b7)
			}
		default:
			if h, ok := prev.(*h5b7); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = poolH5b7.Get().(*h5b7)
			}
		}
	case s.quality <= 9:
		switch {
		case s.lgwin <= 16:
			if h, ok := prev.(*h42); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = &h42{}
			}
		case s.sizeHint >= 1<<20 && s.lgwin >= 19:
			if h, ok := prev.(*h6b8); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = poolH6b8.Get().(*h6b8)
			}
		default:
			if h, ok := prev.(*h5b8); ok {
				e.hasher = h
			} else {
				releaseHasher(prev)
				e.hasher = poolH5b8.Get().(*h5b8)
			}
		}
	default: // quality 10+ (shouldn't reach here; created eagerly in reset)
		releaseHasher(prev)
		e.hasher = &h10{lgwin: s.lgwin, quality: s.quality, bufs: &e.q10}
	}
	e.resetHasher()
}

func (e *encoderSplit) encodeData(isLast, forceFlush bool) []byte {
	if e.hasher == nil {
		e.chooseHasher()
	}
	metablockSize, earlyResult, ready := e.prepareMetaBlock(isLast, forceFlush)
	if !ready {
		return earlyResult
	}
	e.initMetaBlockOutput(metablockSize)
	e.writeMetaBlockInternal(int(metablockSize), int(e.numLiterals), int(e.numCommands), isLast)
	return e.finishMetaBlock()
}

// writeMetaBlockInternal decides whether to compress or emit uncompressed,
// and falls back to uncompressed when compression expands the data.
func (e *encoderSplit) writeMetaBlockInternal(length, numLiterals, numCommands int, isLast bool) {
	s := &e.encodeState
	b := &s.b

	if length == 0 {
		b.writeBits(2, 3)
		b.byteAlign()
		return
	}

	if !s.shouldCompress(length, numLiterals, numCommands) {
		s.distCache = s.savedDistCache
		s.writeUncompressedMetaBlock(length, isLast)
		return
	}

	savedBuf0 := b.buf[0]
	savedBuf1 := b.buf[1]
	savedBitOffset := b.bitOffset

	e.writeMetaBlockSplit(length, isLast)

	if uint(length)+4 < b.bitOffset/8 {
		s.distCache = s.savedDistCache
		b.buf[0] = savedBuf0
		b.buf[1] = savedBuf1
		b.bitOffset = savedBitOffset
		s.writeUncompressedMetaBlock(length, isLast)
	}
}

// writeMetaBlockSplit runs the block-split metablock pipeline. For Q10+ it
// uses the slow path (DP block splitting + histogram clustering); for Q4–Q9
// it uses the greedy path with optional context modeling.
func (e *encoderSplit) writeMetaBlockSplit(length int, isLast bool) {
	s := &e.encodeState
	startPos := wrapPosition(s.lastFlushPos)

	if s.quality >= 10 {
		// Slow path: DP block splitting + distance parameter optimization
		// + histogram clustering.
		contextMode := chooseContextMode(s.quality, s.data, startPos, uint(s.mask), uint(length))
		distParams := buildMetaBlock(
			s.data, startPos, uint(s.mask),
			s.quality,
			s.prevByte, s.prevByte2,
			s.commands,
			contextMode,
			false, // literal context modeling always enabled for Q10+
			&e.mb,
			&e.q10,
		)
		// Temporarily install the optimized distance params for this
		// metablock's encoding, then restore the defaults. The C reference
		// uses a local copy of params in WriteMetaBlockInternal, so
		// s->params.dist always stays at (0,0). We must do the same to
		// keep extendLastCommand (which reads s.distParams) correct for
		// subsequent metablocks.
		savedDistParams := s.distParams
		savedDistMax := s.distAlphabetSizeMax
		savedDistLimit := s.distAlphabetSizeLimit
		s.distAlphabetSizeMax = uint(distParams.alphabetSizeMax)
		s.distAlphabetSizeLimit = uint(distParams.alphabetSizeLimit)
		s.distParams = distParams
		optimizeHistograms(&e.mb, int(s.distAlphabetSizeLimit), &e.goodForRLE)
		e.writeMetaBlock(length, isLast, &e.mb, e.tree[:])
		s.distParams = savedDistParams
		s.distAlphabetSizeMax = savedDistMax
		s.distAlphabetSizeLimit = savedDistLimit
		return
	}

	// Greedy path for Q4–Q9.
	numContexts := uint(1)
	var staticContextMap []uint32
	if s.quality >= 5 {
		numContexts, staticContextMap = decideOverLiteralContextModeling(
			s.data, startPos, uint(s.mask), uint(length),
			s.quality, s.sizeHint)
	}

	buildMetaBlockGreedy(s.data, startPos, uint(s.mask), s.prevByte, s.prevByte2,
		numContexts, staticContextMap, s.commands, &e.splitBufs, &e.mb)
	optimizeHistograms(&e.mb, int(s.distAlphabetSizeMax), &e.goodForRLE)
	e.writeMetaBlock(length, isLast, &e.mb, e.tree[:])
}

// writeMetaBlock encodes commands into a compressed meta-block using the full
// block encoder with per-block-type Huffman codes, block switch signaling, and
// optional literal and distance context modeling.
//
// When the metablock has a literal context map (numContexts > 1), literals are
// encoded with storeSymbolWithContext using a 6-bit context ID derived from the
// two preceding bytes. The context map routes each (blockType, contextID) pair
// to a histogram cluster, and each cluster gets its own Huffman tree.
//
// When there is no literal context map (numContexts == 1 or short input),
// encoding falls back to one Huffman tree per block type, with trivial
// (identity) context maps.
//
// When a distance context map is present (Q10+ slow path), distance symbols
// are encoded with storeSymbolWithContext using a 2-bit distance context.
// Otherwise distance encoding uses trivial context maps.
func (e *encoderSplit) writeMetaBlock(length int, isLast bool, mb *metaBlockSplit, tree []huffmanTreeNode) {
	s := &e.encodeState
	b := &s.b
	input := s.data
	startPos := wrapPosition(s.lastFlushPos)
	mask := uint(s.mask)
	commands := s.commands
	numDistanceSymbols := s.distAlphabetSizeMax

	b.writeMetaBlockHeader(length, isLast, false)

	litEnc := newBlockEncoder(alphabetSizeLiteral, mb.litSplit.numTypes,
		mb.litSplit.types, mb.litSplit.lengths)
	cmdEnc := newBlockEncoder(alphabetSizeInsertAndCopyLength, mb.cmdSplit.numTypes,
		mb.cmdSplit.types, mb.cmdSplit.lengths)
	distEnc := newBlockEncoder(int(numDistanceSymbols), mb.distSplit.numTypes,
		mb.distSplit.types, mb.distSplit.lengths)

	litEnc.buildAndStoreBlockSwitchEntropyCodes(tree, b)
	cmdEnc.buildAndStoreBlockSwitchEntropyCodes(tree, b)
	distEnc.buildAndStoreBlockSwitchEntropyCodes(tree, b)

	// Distance parameters (RFC 7932 Section 9.2): NPOSTFIX (2 bits) and
	// NDIRECT >> NPOSTFIX (4 bits).
	npostfix := s.distParams.postfixBits
	ndirect := s.distParams.numDirectCodes
	b.writeBits(2, uint64(npostfix))
	b.writeBits(4, uint64(ndirect>>npostfix))

	// Literal context modes (2 bits per literal block type).
	// For Q10+ the mode is determined by chooseContextMode; for Q4–Q9 it
	// is always UTF-8.
	var literalContextMode byte = contextUTF8
	if s.quality >= 10 {
		literalContextMode = chooseContextMode(s.quality, s.data, startPos, mask, uint(length))
	}
	for range mb.litSplit.numTypes {
		b.writeBits(2, uint64(literalContextMode))
	}

	// Literal context map: full encoding when context modeling is active,
	// trivial identity map otherwise.
	useLitContextMap := len(mb.literalContextMap) > 0
	numLitHistograms := len(mb.litHistograms) / alphabetSizeLiteral
	if useLitContextMap {
		e.rleSymBuf = b.encodeContextMapBuf(mb.literalContextMap, uint(numLitHistograms), tree, e.rleSymBuf)
	} else {
		storeTrivialContextMap(uint(mb.litSplit.numTypes), literalContextBits, tree, b)
	}

	// Distance context map: full encoding when the slow path produced a
	// non-trivial mapping, trivial identity map otherwise.
	useDistContextMap := len(mb.distanceContextMap) > 0
	numDistHistograms := mb.distSplit.numTypes
	if useDistContextMap {
		numDistHistograms = len(mb.distHistograms) / int(numDistanceSymbols)
		e.rleSymBuf = b.encodeContextMapBuf(mb.distanceContextMap, uint(numDistHistograms), tree, e.rleSymBuf)
	} else {
		storeTrivialContextMap(uint(mb.distSplit.numTypes), distanceContextBits, tree, b)
	}
	// Tiny metablocks measured better with the smaller storeSymbol path.
	combineDistExtraBits := !useDistContextMap && length >= 4096

	// Build and store entropy codes for each histogram.
	e.litDepths, e.litBits = litEnc.buildAndStoreEntropyCodes(mb.litHistograms,
		numLitHistograms, alphabetSizeLiteral, tree, b, e.litDepths, e.litBits)
	e.cmdDepths, e.cmdBits = cmdEnc.buildAndStoreEntropyCodes(mb.cmdHistograms,
		mb.cmdSplit.numTypes, alphabetSizeInsertAndCopyLength, tree, b, e.cmdDepths, e.cmdBits)
	e.distDepths, e.distBits = distEnc.buildAndStoreEntropyCodes(mb.distHistograms,
		numDistHistograms, int(numDistanceSymbols), tree, b, e.distDepths, e.distBits)

	// Encode commands.
	pos := startPos
	prevByte := s.prevByte
	prevByte2 := s.prevByte2
	for i := range commands {
		cmd := commands[i]

		// Command symbol.
		cmdCode := cmd.cmdPrefix
		cmdEnc.storeSymbol(uint(cmdCode), b)
		// Use the command prefix lookup table to avoid recomputing
		// insert/copy length prefix classes for every command.
		{
			lut := cmdLut[cmdCode]
			effCopyLen := uint(cmd.copyLenCode())
			insNumExtra := lut.insertLenExtraBits
			b.writeBits(uint(insNumExtra+lut.copyLenExtraBits),
				((uint64(effCopyLen)-uint64(lut.copyLenOffset))<<insNumExtra)|
					(uint64(cmd.insertLen)-uint64(lut.insertLenOffset)))
		}

		// Literal bytes.
		if useLitContextMap {
			for j := cmd.insertLen; j != 0; j-- {
				literal := input[pos&mask]
				context := uint(contextLookup(uint(literalContextMode), prevByte, prevByte2))
				litEnc.storeSymbolWithContext(uint(literal), context,
					mb.literalContextMap, literalContextBits, b)
				prevByte2 = prevByte
				prevByte = literal
				pos++
			}
		} else {
			// Hot literal encoding loop — manually inlined storeSymbol
			// fast path to avoid function call overhead per literal byte.
			//
			// When no block switch is imminent (litBlockLen >= 3), three
			// consecutive literals are packed into a single writeBits call.
			// Each Brotli literal code is at most 15 bits, so three codes
			// total at most 45 bits, well within the 56-bit writeBits limit.
			// This reduces writeBits call count ~3x in the common case.
			litDepths := litEnc.depths
			litBits := litEnc.bits
			litEntIdx := litEnc.entropyIdx
			litBlockLen := litEnc.blockLen
			j := cmd.insertLen
			for j != 0 {
				if litBlockLen == 0 {
					litEnc.blockLen = litBlockLen
					litEnc.emitBlockSwitch(b)
					litBlockLen = litEnc.blockLen
					litEntIdx = litEnc.entropyIdx
				}
				litBlockLen--
				j--
				ix0 := litEntIdx + int(input[pos&mask])
				n0 := uint(litDepths[ix0])
				v0 := uint64(litBits[ix0])
				pos++
				if j == 0 || litBlockLen == 0 {
					b.writeBits(n0, v0)
					continue
				}
				litBlockLen--
				j--
				ix1 := litEntIdx + int(input[pos&mask])
				n1 := uint(litDepths[ix1])
				v1 := uint64(litBits[ix1])
				pos++
				if j == 0 || litBlockLen == 0 {
					b.writeBits(n0+n1, v0|v1<<n0)
					continue
				}
				litBlockLen--
				j--
				ix2 := litEntIdx + int(input[pos&mask])
				n2 := uint(litDepths[ix2])
				b.writeBits(n0+n1+n2, v0|v1<<n0|uint64(litBits[ix2])<<(n0+n1))
				pos++
			}
			litEnc.blockLen = litBlockLen
			litEnc.entropyIdx = litEntIdx
		}

		copyLen := cmd.copyLength()
		pos += uint(copyLen)
		if copyLen != 0 && cmd.cmdPrefix >= 128 {
			distPrefix := cmd.distPrefix
			distCode := distPrefix & 0x3FF
			distNumExtra := distPrefix >> 10
			switch {
			case useDistContextMap:
				distContext := uint(cmd.distanceContext())
				distEnc.storeSymbolWithContext(uint(distCode), distContext,
					mb.distanceContextMap, distanceContextBits, b)
				b.writeBits(uint(distNumExtra), uint64(cmd.distExtra))
			case combineDistExtraBits:
				if distEnc.blockLen == 0 {
					distEnc.emitBlockSwitch(b)
				}
				distEnc.blockLen--
				ix := distEnc.entropyIdx + int(distCode)
				distBitsLen := uint(distEnc.depths[ix])
				b.writeBits(distBitsLen+uint(distNumExtra),
					uint64(distEnc.bits[ix])|(uint64(cmd.distExtra)<<distBitsLen))
			default:
				distEnc.storeSymbol(uint(distCode), b)
				b.writeBits(uint(distNumExtra), uint64(cmd.distExtra))
			}
		}

		// Track preceding bytes for context computation across copy runs.
		if useLitContextMap && copyLen != 0 {
			prevByte2 = input[(pos-2)&mask]
			prevByte = input[(pos-1)&mask]
		}
	}

	if isLast {
		b.byteAlign()
	}
}

// writeUncompressedMetaBlock writes an uncompressed meta-block containing data.
func (b *bitWriter) writeUncompressedMetaBlock(data []byte) {
	b.writeMetaBlockHeader(len(data), false, true)
	b.byteAlign()
	b.writeBytes(data)
}

// writeMetaBlockHeader writes a compressed meta-block header. When isLast is
// true, writes ISLAST=1 and ISEMPTY=0 (no ISUNCOMPRESSED bit per spec). When
// isLast is false, writes ISLAST=0 followed by the ISUNCOMPRESSED flag.
func (b *bitWriter) writeMetaBlockHeader(length int, isLast, uncompressed bool) {
	mlen := encodeMlen(length)
	if isLast {
		b.writeBits(1, 1) // ISLAST = 1
		b.writeBits(1, 0) // ISEMPTY = 0
		b.writeBits(2, mlen.nibbleBits)
		b.writeBits(mlen.numBits, mlen.bits)
		return
	}

	b.writeBits(1, 0) // ISLAST = 0
	b.writeBits(2, mlen.nibbleBits)
	b.writeBits(mlen.numBits, mlen.bits)
	v := uint64(0)
	if uncompressed {
		v = 1
	}
	b.writeBits(1, v) // ISUNCOMPRESSED
}

// writeStaticCommandHuffmanTree writes the precomputed Huffman tree for the
// static command prefix code (704-symbol insert-and-copy alphabet).
func (b *bitWriter) writeStaticCommandHuffmanTree() {
	b.writeBits(56, 0x0092624416307003)
	b.writeBits(3, 0)
}

// writeStaticDistanceHuffmanTree writes the precomputed Huffman tree for the
// static distance prefix code (64-symbol distance alphabet).
func (b *bitWriter) writeStaticDistanceHuffmanTree() {
	b.writeBits(28, 0x0369DC03)
}

// wrapPosition wraps a 64-bit input position to a 32-bit ring-buffer position
// preserving the "not-a-first-lap" feature. The first 3 GiB of input are
// continuous; after that, positions wrap every 2 GiB.
func wrapPosition(position uint64) uint {
	result := uint32(position)
	gb := position >> 30
	if gb > 2 {
		result = (result & ((1 << 30) - 1)) | (uint32((gb-1)&1)+1)<<30
	}
	return uint(result)
}

// encodeWindowBits encodes the brotli stream header for the given window size.
func encodeWindowBits(lgwin int) (uint16, uint8) {
	switch {
	case lgwin == 16:
		return 0, 1
	case lgwin == 17:
		return 1, 7
	case lgwin > 17:
		return uint16(((lgwin - 17) << 1) | 0x01), 4
	default:
		return uint16(((lgwin - 8) << 4) | 0x01), 7
	}
}

// computeDistanceCode maps a raw backward distance to a distance code,
// checking the recent distance cache for short codes (RFC 7932 Section 4).
//
// The 16 distance short codes reference the 4-entry distance cache with
// optional offsets:
//
//	Code  Meaning          Code  Meaning
//	0     dist[0]          8     dist[1] - 1
//	1     dist[1]          9     dist[1] + 1
//	2     dist[2]          10    dist[1] - 2
//	3     dist[3]          11    dist[1] + 2
//	4     dist[0] - 1      12    dist[1] - 3
//	5     dist[0] + 1      13    dist[1] + 3
//	6     dist[0] - 2      14    dist[2] - 1
//	7     dist[0] + 2      15    dist[2] + 1
//
// Offsets from dist[0] (codes 4–7) and dist[1] (codes 8–13) are checked before
// exact matches on dist[2]/dist[3] because offset hits are statistically more
// useful and produce shorter encodings on average. The magic constants 0x9750468
// and 0xFDB1ACE are packed lookup tables that map an offset index to the
// corresponding short code.
//
// Distances beyond the short codes are encoded as distance + 15.
func computeDistanceCode(distance, maxDistance uint, distCache *[4]uint) uint {
	if distance <= maxDistance {
		dp3 := distance + 3
		offset0 := dp3 - distCache[0]
		offset1 := dp3 - distCache[1]
		switch {
		case distance == distCache[0]:
			return 0
		case distance == distCache[1]:
			return 1
		case offset0 < 7:
			return (0x9750468 >> (4 * offset0)) & 0xF
		case offset1 < 7:
			return (0xFDB1ACE >> (4 * offset1)) & 0xF
		case distance == distCache[2]:
			return 2
		case distance == distCache[3]:
			return 3
		}
	}
	return distance + numDistanceShortCodes - 1
}

func preallocUint32(s []uint32, n int) []uint32 {
	if cap(s) < n {
		return make([]uint32, 0, n)
	}
	return s
}

func preallocByte(s []byte, n int) []byte {
	if cap(s) < n {
		return make([]byte, 0, n)
	}
	return s
}

func preallocFloat64(s []float64, n int) []float64 {
	if cap(s) < n {
		return make([]float64, 0, n)
	}
	return s
}

func preallocUint16(s []uint16, n int) []uint16 {
	if cap(s) < n {
		return make([]uint16, 0, n)
	}
	return s
}

func preallocHistogramPairs(s []histogramPair, n int) []histogramPair {
	if cap(s) < n {
		return make([]histogramPair, 0, n)
	}
	return s
}

func preallocZopfliNodes(s []zopfliNode, n int) []zopfliNode {
	if cap(s) < n {
		return make([]zopfliNode, 0, n)
	}
	return s
}

func preallocBackwardMatches(s []backwardMatch, n int) []backwardMatch {
	if cap(s) < n {
		return make([]backwardMatch, 0, n)
	}
	return s
}
