// Huffman-coded meta-block data: commands, literals, and distances.

package brrr

// huffmanBlock holds input data, commands, and Huffman codes for the three
// prefix code alphabets (literal, insert-and-copy, distance).
type huffmanBlock struct {
	input    []byte
	commands []command

	litDepth  []byte
	litBits   []uint16
	cmdDepth  []byte
	cmdBits   []uint16
	distDepth []byte
	distBits  []uint16

	startPos uint
	mask     uint
}

// writeData encodes a sequence of commands into the bitstream using the
// provided Huffman codes. For each command it writes:
//  1. The insert-and-copy length symbol + extra bits
//  2. The literal bytes covered by the insert length
//  3. The distance symbol + extra bits (if the command has a backward reference)
func (block huffmanBlock) writeData(b *bitWriter) { //nolint:gocritic // stack copy avoids heap escape; called once per metablock
	pos := block.startPos
	for _, cmd := range block.commands {
		cmdCode := cmd.cmdPrefix
		b.writeBits(uint(block.cmdDepth[cmdCode]), uint64(block.cmdBits[cmdCode]))
		// Use the command prefix lookup table to avoid recomputing the
		// insert/copy length prefix classes for every command.
		{
			lut := cmdLut[cmdCode]
			copyLenCode := cmd.copyLen
			// Most copy commands have no encoded length delta; avoid the
			// sign-extension path unless the high delta bits are present.
			if copyLenCode>>25 != 0 {
				copyLenCode = cmd.copyLenCode()
			}
			effCopyLen := uint(copyLenCode)
			insNumExtra := lut.insertLenExtraBits
			b.writeBits(uint(insNumExtra+lut.copyLenExtraBits),
				((uint64(effCopyLen)-uint64(lut.copyLenOffset))<<insNumExtra)|
					(uint64(cmd.insertLen)-uint64(lut.insertLenOffset)))
		}
		// Literal encoding loop: three consecutive literals are packed into a
		// single writeBits call. Each Brotli literal code is at most 15 bits,
		// so three codes total at most 45 bits — well within the 56-bit limit.
		// This reduces writeBits call count ~3x in the common case.
		j := cmd.insertLen
		for j != 0 {
			lit0 := block.input[pos&block.mask]
			n0 := uint(block.litDepth[lit0])
			v0 := uint64(block.litBits[lit0])
			pos++
			j--
			if j == 0 {
				b.writeBits(n0, v0)
				break
			}
			lit1 := block.input[pos&block.mask]
			n1 := uint(block.litDepth[lit1])
			v1 := uint64(block.litBits[lit1])
			pos++
			j--
			if j == 0 {
				b.writeBits(n0+n1, v0|v1<<n0)
				break
			}
			lit2 := block.input[pos&block.mask]
			n2 := uint(block.litDepth[lit2])
			pos++
			j--
			b.writeBits(n0+n1+n2, v0|v1<<n0|uint64(block.litBits[lit2])<<(n0+n1))
		}
		copyLen := cmd.copyLength()
		pos += uint(copyLen)
		if copyLen != 0 && !cmd.usesLastDistance() {
			distCode := cmd.distPrefixCode()
			distBitsLen := uint(block.distDepth[distCode])
			distNumExtra := uint(cmd.distExtraBitsLen())
			// Merge the distance Huffman code and its extra bits into one
			// writeBits call. distBitsLen ≤ 15, distNumExtra ≤ ~24, total ≤ 39 bits.
			b.writeBits(distBitsLen+distNumExtra,
				uint64(block.distBits[distCode])|(uint64(cmd.distExtra)<<distBitsLen))
		}
	}
}
