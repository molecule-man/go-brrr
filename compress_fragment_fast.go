package brrr

// One-pass fast encoder for an input fragment, independent of input history.
// When a backward match is found, the corresponding command and literal codes
// are immediately emitted to the bitstream.

import "math/bits"

// maxDistance is the maximum backward reference distance for window size 18.
// BROTLI_MAX_BACKWARD_LIMIT(18) = (1 << 18) - 16 = 262128.
const maxDistance = (1 << 18) - 16

// hashMul32 is the hash multiplier used for 5-byte match lookups.
const hashMul32 = 0x1E35A7BD

const (
	firstBlockSize   = 3 << 15
	mergeBlockSize   = 1 << 16
	inputMarginBytes = windowGap
	minMatchLen      = 5
)

// minRatio is the acceptable loss for uncompressible speedup (2%).
const minRatio = 980

// cmdHistoSeed is the initial histogram seed for commands. Each compression
// block starts with these counts to provide a non-zero baseline.
var cmdHistoSeed = [128]uint32{
	0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 0, 0, 0, 0,
}

type fragmentCompressor struct {
	arena          *onePassArena
	b              *bitWriter
	input          []byte
	table          []uint32
	tableBits      uint
	blockSize      int
	totalBlockSize int
	pos            int
	shift          uint
	mlenPos        uint
	nextEmit       int
	ipEnd          int
	literalRatio   uint
	metablockStart int
	baseIP         int
	isLast         bool
}

func (c *fragmentCompressor) compress() {
	c.tableBits = uint(bits.Len(uint(len(c.table)))) - 1
	initialBitOffset := c.b.bitOffset

	if len(c.input) == 0 {
		assert(c.isLast)
		c.b.writeBits(1, 1) // islast
		c.b.writeBits(1, 1) // isempty
		c.b.byteAlign()
		return
	}

	c.blockSize = min(len(c.input), firstBlockSize)
	c.totalBlockSize = c.blockSize
	c.shift = 64 - c.tableBits
	// Save the bit position of the MLEN field of the meta-block header, so that
	// we can update it later if we decide to extend this meta-block.
	c.mlenPos = c.b.bitOffset + 3

	c.b.writeMetaBlockHeader(c.blockSize, false, false)
	// No block splits, no contexts.
	c.b.writeBits(13, 0)

	c.literalRatio = c.arena.buildAndWriteLiteralPrefixCode(c.input[:c.blockSize], c.b)

	// Store the pre-compressed command and distance prefix codes.
	{
		i := uint(0)
		for i+7 < c.arena.cmdCodeNumBits {
			c.b.writeBits(8, uint64(c.arena.cmdCode[i/8]))
			i += 8
		}
		c.b.writeBits(c.arena.cmdCodeNumBits&7, uint64(c.arena.cmdCode[c.arena.cmdCodeNumBits/8]))
	}

	c.writeCommands()

	// If output is larger than a single uncompressed block, rewrite it.
	if c.b.bitOffset-initialBitOffset > 31+uint(len(c.input))*8 {
		c.writeUncompressedMetaBlock(c.input, initialBitOffset)
	}

	if c.isLast {
		c.b.writeBits(1, 1) // islast
		c.b.writeBits(1, 1) // isempty
		c.b.byteAlign()
	}
}

func (c *fragmentCompressor) writeCommands() {
	// Initialize the command and distance histograms.
	c.arena.cmdHisto = cmdHistoSeed
	input := c.input
	table := c.table
	ip := c.pos
	baseIP := c.baseIP
	shift := c.shift
	lastDistance := -1
	c.ipEnd = c.pos + c.blockSize

	if c.blockSize < inputMarginBytes {
		c.writeRemainder()
		return
	}

	// For the last block, keep a 16-byte margin so all distances are at
	// most window size - 16. For other blocks, keep a 5-byte margin.
	lenLimit := min(c.blockSize-minMatchLen, len(input)-c.pos-inputMarginBytes)
	ipLimit := c.pos + lenLimit

	ip++
	nextHash := hashFragment(input, uint(ip), shift)

	for {
		// Step 1: Scan forward in the input looking for a 5-byte-long match.
		// If we find one whose distance is too large, keep scanning.
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
				c.writeRemainder()
				return
			}
			nextHash = hashFragment(input, uint(nextIP), shift)

			candidate = ip - lastDistance
			if candidate >= 0 && candidate < ip && isMatch(input, uint(ip), uint(candidate)) {
				table[hash] = uint32(ip - baseIP)
				if ip-candidate <= maxDistance {
					break
				}
				continue
			}

			candidate = baseIP + int(table[hash])
			table[hash] = uint32(ip - baseIP)
			if isMatch(input, uint(ip), uint(candidate)) {
				if ip-candidate <= maxDistance {
					break
				}
				continue
			}
		}

		// Step 2: Emit the found match together with the literal bytes from
		// nextEmit to the bitstream, and then see if we can find a next match
		// immediately afterwards.
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
				c.nextBlock()
				return
			default:
				c.writeLongInsertLen(insert)
			}
			c.writeLiterals(input[c.nextEmit : c.nextEmit+int(insert)])
			if distance == lastDistance {
				c.b.writeBits(uint(c.arena.cmdDepth[64]), uint64(c.arena.cmdBits[64]))
				c.arena.cmdHisto[64]++
			} else {
				c.writeDistance(uint(distance))
				lastDistance = distance
			}
			c.writeCopyLenLastDistance(uint(matched))

			c.nextEmit = ip
			if ip >= ipLimit {
				c.writeRemainder()
				return
			}

			candidate = updateHashTable(input, table, ip, baseIP, shift)
		}

		// Try to find another match immediately.
		for isMatch(input, uint(ip), uint(candidate)) {
			base := ip
			matched := 5 + matchLenAt(
				input, uint(candidate+5), uint(ip+5), c.ipEnd-ip-5)
			if ip-candidate > maxDistance {
				break
			}
			ip += matched
			lastDistance = base - candidate
			c.writeCopyLen(uint(matched))
			c.writeDistance(uint(lastDistance))

			c.nextEmit = ip
			if ip >= ipLimit {
				c.writeRemainder()
				return
			}

			candidate = updateHashTable(input, table, ip, baseIP, shift)
		}

		ip++
		nextHash = hashFragment(input, uint(ip), shift)
	}
}

func (c *fragmentCompressor) writeRemainder() {
	input := c.input
	c.pos += c.blockSize
	c.blockSize = min(len(input)-c.pos, mergeBlockSize)

	// Decide if we want to continue this meta-block instead of emitting the
	// last insert-only command.
	if c.pos < len(input) &&
		c.totalBlockSize+c.blockSize <= (1<<20) &&
		c.arena.shouldMergeBlock(input[c.pos:], c.blockSize, c.arena.litDepth[:]) {
		// Update the size of the current meta-block and continue emitting
		// commands. We can do this because the current size and the new size
		// both have 5 nibbles.
		c.totalBlockSize += c.blockSize
		updateBits(c.b.buf, 20, uint32(c.totalBlockSize-1), c.mlenPos)
		c.writeCommands()
		return
	}

	// Emit the remaining bytes as literals.
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
	c.nextBlock()
}

func (c *fragmentCompressor) nextBlock() {
	input := c.input
	// If we have more data, write a new meta-block header and prefix codes
	// and then continue emitting commands.
	if c.pos < len(input) {
		c.metablockStart = c.pos
		c.blockSize = min(len(input)-c.pos, firstBlockSize)
		c.totalBlockSize = c.blockSize
		c.mlenPos = c.b.bitOffset + 3
		c.b.writeMetaBlockHeader(c.blockSize, false, false)
		// No block splits, no contexts.
		c.b.writeBits(13, 0)
		c.literalRatio = c.arena.buildAndWriteLiteralPrefixCode(
			input[c.pos:c.pos+c.blockSize], c.b)
		c.arena.buildAndWriteCommandPrefixCode(c.b)
		c.writeCommands()
		return
	}

	if !c.isLast {
		// If this is not the last block, update the command and distance
		// prefix codes for the next block and store the compressed forms.
		c.arena.cmdCode[0] = 0
		c.arena.cmdCodeNumBits = 0
		cmdCodeStream := &bitWriter{buf: c.arena.cmdCode[:], bitOffset: 0}
		c.arena.buildAndWriteCommandPrefixCode(cmdCodeStream)
		c.arena.cmdCodeNumBits = cmdCodeStream.bitOffset
	}
}

// writeInsertLen writes an insert length code to the bitstream.
// Requires: insertLen < 6210
func (c *fragmentCompressor) writeInsertLen(insertLen uint) {
	depth := c.arena.cmdDepth[:]
	cmdBits := c.arena.cmdBits[:]
	histo := c.arena.cmdHisto[:]
	switch {
	case insertLen < 6:
		code := insertLen + 40
		c.b.writeBits(uint(depth[code]), uint64(cmdBits[code]))
		histo[code]++
	case insertLen < 130:
		tail := insertLen - 2
		nbits := uint(bits.Len(tail)) - 2
		prefix := tail >> nbits
		inscode := (nbits << 1) + prefix + 42
		c.b.writeBits(uint(depth[inscode]), uint64(cmdBits[inscode]))
		c.b.writeBits(nbits, uint64(tail-(prefix<<nbits)))
		histo[inscode]++
	case insertLen < 2114:
		tail := insertLen - 66
		nbits := uint(bits.Len(tail)) - 1
		code := nbits + 50
		c.b.writeBits(uint(depth[code]), uint64(cmdBits[code]))
		c.b.writeBits(nbits, uint64(tail-(1<<nbits)))
		histo[code]++
	default:
		c.b.writeBits(uint(depth[61]), uint64(cmdBits[61]))
		c.b.writeBits(12, uint64(insertLen-2114))
		histo[61]++
	}
}

// writeLongInsertLen writes a long insert length code to the bitstream.
func (c *fragmentCompressor) writeLongInsertLen(insertLen uint) {
	depth := c.arena.cmdDepth[:]
	cmdBits := c.arena.cmdBits[:]
	histo := c.arena.cmdHisto[:]
	if insertLen < 22594 {
		c.b.writeBits(uint(depth[62]), uint64(cmdBits[62]))
		c.b.writeBits(14, uint64(insertLen-6210))
		histo[62]++
	} else {
		c.b.writeBits(uint(depth[63]), uint64(cmdBits[63]))
		c.b.writeBits(24, uint64(insertLen-22594))
		histo[63]++
	}
}

// writeCopyLen writes a copy length code to the bitstream.
func (c *fragmentCompressor) writeCopyLen(copyLen uint) {
	depth := c.arena.cmdDepth[:]
	cmdBits := c.arena.cmdBits[:]
	histo := c.arena.cmdHisto[:]
	switch {
	case copyLen < 10:
		c.b.writeBits(uint(depth[copyLen+14]), uint64(cmdBits[copyLen+14]))
		histo[copyLen+14]++
	case copyLen < 134:
		tail := copyLen - 6
		nbits := uint(bits.Len(tail)) - 2
		prefix := tail >> nbits
		code := (nbits << 1) + prefix + 20
		c.b.writeBits(uint(depth[code]), uint64(cmdBits[code]))
		c.b.writeBits(nbits, uint64(tail-(prefix<<nbits)))
		histo[code]++
	case copyLen < 2118:
		tail := copyLen - 70
		nbits := uint(bits.Len(tail)) - 1
		code := nbits + 28
		c.b.writeBits(uint(depth[code]), uint64(cmdBits[code]))
		c.b.writeBits(nbits, uint64(tail-(1<<nbits)))
		histo[code]++
	default:
		c.b.writeBits(uint(depth[39]), uint64(cmdBits[39]))
		c.b.writeBits(24, uint64(copyLen-2118))
		histo[39]++
	}
}

// writeCopyLenLastDistance writes a copy length code with last distance reuse.
func (c *fragmentCompressor) writeCopyLenLastDistance(copyLen uint) {
	depth := c.arena.cmdDepth[:]
	cmdBits := c.arena.cmdBits[:]
	histo := c.arena.cmdHisto[:]
	switch {
	case copyLen < 12:
		c.b.writeBits(uint(depth[copyLen-4]), uint64(cmdBits[copyLen-4]))
		histo[copyLen-4]++
	case copyLen < 72:
		tail := copyLen - 8
		nbits := uint(bits.Len(tail)) - 2
		prefix := tail >> nbits
		code := (nbits << 1) + prefix + 4
		c.b.writeBits(uint(depth[code]), uint64(cmdBits[code]))
		c.b.writeBits(nbits, uint64(tail-(prefix<<nbits)))
		histo[code]++
	case copyLen < 136:
		tail := copyLen - 8
		code := (tail >> 5) + 30
		c.b.writeBits(uint(depth[code]), uint64(cmdBits[code]))
		c.b.writeBits(5, uint64(tail&31))
		c.b.writeBits(uint(depth[64]), uint64(cmdBits[64]))
		histo[code]++
		histo[64]++
	case copyLen < 2120:
		tail := copyLen - 72
		nbits := uint(bits.Len(tail)) - 1
		code := nbits + 28
		c.b.writeBits(uint(depth[code]), uint64(cmdBits[code]))
		c.b.writeBits(nbits, uint64(tail-(1<<nbits)))
		c.b.writeBits(uint(depth[64]), uint64(cmdBits[64]))
		histo[code]++
		histo[64]++
	default:
		c.b.writeBits(uint(depth[39]), uint64(cmdBits[39]))
		c.b.writeBits(24, uint64(copyLen-2120))
		c.b.writeBits(uint(depth[64]), uint64(cmdBits[64]))
		histo[39]++
		histo[64]++
	}
}

// writeDistance writes a distance code to the bitstream.
func (c *fragmentCompressor) writeDistance(distance uint) {
	d := distance + 3
	nbits := uint(bits.Len(d)) - 2
	prefix := (d >> nbits) & 1
	offset := (2 + prefix) << nbits
	distcode := 2*(nbits-1) + prefix + 80
	depth := uint(c.arena.cmdDepth[distcode])
	// Pack Huffman code (depth bits) and extra bits (nbits) into one writeBits
	// call. Max total = 15 + 16 = 31 bits, well under the 56-bit limit.
	c.b.writeBits(depth+nbits, uint64(c.arena.cmdBits[distcode])|uint64(d-offset)<<depth)
	c.arena.cmdHisto[distcode]++
}

// writeLiterals writes literal bytes to the bitstream.
func (c *fragmentCompressor) writeLiterals(input []byte) {
	c.b.writeLiteralBits(input, &c.arena.litDepth, &c.arena.litBits)
}

// writeUncompressedMetaBlock rewrites the output as an uncompressed meta-block.
func (c *fragmentCompressor) writeUncompressedMetaBlock(data []byte, startBitOffset uint) {
	c.b.rewindTo(startBitOffset)
	c.b.writeUncompressedMetaBlock(data)
}

// updateHashTable updates the hash table with positions from the last copy and returns the next candidate.
func updateHashTable(input []byte, table []uint32, ip, baseIP int, shift uint) int {
	inputBytes := loadU64LE(input, uint(ip-3))
	prevHash := hashBytesAtOffset(inputBytes, 0, shift)
	curHash := hashBytesAtOffset(inputBytes, 3, shift)
	table[prevHash] = uint32(ip - baseIP - 3)
	prevHash = hashBytesAtOffset(inputBytes, 1, shift)
	table[prevHash] = uint32(ip - baseIP - 2)
	prevHash = hashBytesAtOffset(inputBytes, 2, shift)
	table[prevHash] = uint32(ip - baseIP - 1)
	candidate := baseIP + int(table[curHash])
	table[curHash] = uint32(ip - baseIP)
	return candidate
}

// shouldUseUncompressedMode returns true when the data so far looks
// incompressible enough to emit an uncompressed meta-block.
func (c *fragmentCompressor) shouldUseUncompressedMode(insertLen uint) bool {
	if uint(c.nextEmit-c.metablockStart)*50 > insertLen {
		return false
	}
	return c.literalRatio > minRatio
}

// TODO: take *encoder instead of *bitWriter
// compressFragmentFast compresses the input as one or more complete meta-blocks
// and writes them to the bitstream. If isLast is true, an additional empty last
// meta-block is emitted.
//
// The arena s is used for scratch space and also carries the command prefix code
// state between calls: cmdDepth, cmdBits, cmdCode, cmdCodeNumBits must be
// properly initialized before the first call.
//
// table must be zeroed on the first call. Its length must be a power of two
// with an odd exponent (9, 11, 13, or 15).
func compressFragmentFast(
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
	c.compress()
}

// hashFragment computes a hash of the 5 bytes at input[i:i+5], shifted right
// by shift bits (64 - tableBits). Taking the raw byte slice and index avoids
// the sub-slice bounds check at the call site in the inner scan loop.
func hashFragment(input []byte, i, shift uint) uint32 {
	h := (loadU64LE(input, i) << 24) * hashMul32
	return uint32(h >> shift)
}

// hashBytesAtOffset computes a hash of 5 bytes within a 64-bit value,
// starting at the given byte offset (0..3).
func hashBytesAtOffset(v uint64, offset, shift uint) uint32 {
	h := ((v >> (8 * offset)) << 24) * hashMul32
	return uint32(h >> shift)
}

// isMatch returns true if the 5 bytes of input at positions a and b are equal.
// Taking the raw byte slice and indices avoids the sub-slice bounds check at
// the call site in the inner scan loop.
func isMatch(input []byte, a, b uint) bool {
	return loadU32LE(input, a) == loadU32LE(input, b) &&
		loadByte(input, a+4) == loadByte(input, b+4)
}

// updateBits overwrites nBits bits at bit position pos in array with the
// given value.
func updateBits(array []byte, nBits uint, value uint32, pos uint) {
	for nBits > 0 {
		bytePos := pos / 8
		unchangedBits := pos & 7
		changedBits := min(nBits, 8-unchangedBits)
		totalBits := unchangedBits + changedBits
		mask := (^((uint32(1) << totalBits) - 1)) | ((uint32(1) << unchangedBits) - 1)
		unchanged := uint32(array[bytePos]) & mask
		changed := value & ((uint32(1) << changedBits) - 1)
		array[bytePos] = byte((changed << unchangedBits) | unchanged)
		nBits -= changedBits
		value >>= changedBits
		pos += changedBits
	}
}
