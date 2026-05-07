// Fast compressor: q0 (one-pass) and q1 (two-pass).
// Buffers all input, compresses on Flush/Close in fragments of 1<<lgwin.

package encoder

import (
	"errors"
	"io"
	"sync"
)

var (
	errFastCompoundDict = errors.New("brrr: compound dictionaries require quality >= 2")

	poolOnePassArena = sync.Pool{New: func() any { return new(onePassArena) }}
	poolTwoPassArena = sync.Pool{New: func() any { return new(twoPassArena) }}

	poolFastOutBuf   sync.Pool
	poolFastTable32  sync.Pool
	poolFastCommands sync.Pool
	poolFastLiterals sync.Pool
)

// fastCompressor implements Compressor for q0 and q1. Input is buffered
// in-memory; output is produced in fragments at Flush/Close time.
type fastCompressor struct {
	onePass *onePassArena // non-nil when quality == 0
	twoPass *twoPassArena // non-nil when quality == 1

	buf        []byte // buffered uncompressed input
	outBuf     []byte // scratch for compressed output
	table      []uint32
	commandBuf []uint32 // q1 only
	literalBuf []byte   // q1 only

	quality     int
	lgwin       int
	wroteHeader bool
}

// newFastCompressor returns a Compressor for q0 or q1, acquiring its arena
// from the appropriate pool.
func newFastCompressor(quality, lgwin int) *fastCompressor {
	c := &fastCompressor{quality: quality, lgwin: lgwin}
	switch quality {
	case 0:
		c.onePass = poolOnePassArena.Get().(*onePassArena)
		c.onePass.initCommandPrefixCodes()
	case 1:
		c.twoPass = poolTwoPassArena.Get().(*twoPassArena)
	}
	return c
}

// Write buffers input. Compression runs at Flush/Close time.
func (c *fastCompressor) Write(_ io.Writer, p []byte) (int, error) {
	c.buf = append(c.buf, p...)
	return len(p), nil
}

// Flush compresses buffered input as non-final meta-blocks and emits them to
// dst. After Flush the buffered input is empty.
func (c *fastCompressor) Flush(dst io.Writer) error {
	if len(c.buf) == 0 {
		return nil
	}
	return c.compress(dst, false)
}

// Close compresses any remaining buffered input and emits the final meta-block.
func (c *fastCompressor) Close(dst io.Writer) error {
	return c.compress(dst, true)
}

// Reset clears per-stream state for reuse with the same quality/lgwin.
// The arena is preserved (not returned to pool) and re-initialized.
func (c *fastCompressor) Reset() {
	c.buf = c.buf[:0]
	c.wroteHeader = false
	if c.quality == 0 {
		c.onePass.initCommandPrefixCodes()
	}
}

// AttachDictionary reports that q0/q1 do not support compound dictionaries.
// Writer is expected to validate this at construction so this is a defensive
// path.
func (c *fastCompressor) AttachDictionary(*PreparedDictionary) error {
	return errFastCompoundDict
}

// Release returns the arena and any owned scratch buffers to their pools.
func (c *fastCompressor) Release() {
	if c.onePass != nil {
		poolOnePassArena.Put(c.onePass)
		c.onePass = nil
	}
	if c.twoPass != nil {
		poolTwoPassArena.Put(c.twoPass)
		c.twoPass = nil
	}
	putFastByteBuffer(&poolFastOutBuf, c.outBuf)
	c.outBuf = nil
	putFastUint32Slice(c.table)
	c.table = nil
	putFastCommandBuffer(c.commandBuf)
	c.commandBuf = nil
	putFastByteBuffer(&poolFastLiterals, c.literalBuf)
	c.literalBuf = nil
}

// compress runs the fast encoder on buffered data and writes compressed
// output to dst. If isLast is true the brotli stream is finalized.
func (c *fastCompressor) compress(dst io.Writer, isLast bool) error {
	// The C reference limits each fragment to 1<<lgwin bytes.
	blockSizeLimit := 1 << c.lgwin

	// Ensure the output buffer is large enough for the largest fragment.
	// Worst case: uncompressed meta-block = header + data + padding.
	maxBlock := min(len(c.buf), blockSizeLimit)
	needed := maxBlock*2 + 1024
	if len(c.outBuf) < needed {
		c.outBuf = getFastByteBuffer(&poolFastOutBuf, needed)
	}
	c.outBuf[0] = 0

	b := bitWriter{buf: c.outBuf}

	if !c.wroteHeader {
		// For quality 0-1 the C reference clamps the header lgwin to at
		// least 18 since these modes don't use a sliding window.
		headerLGWin := max(c.lgwin, 18)
		lastBytes, lastBytesBits := encodeWindowBits(headerLGWin)
		b.writeBits(uint(lastBytesBits), uint64(lastBytes))
		c.wroteHeader = true
	}

	// Lazily grow command/literal buffers for q=1, sized to actual need.
	if c.quality == 1 {
		bufSize := min(maxBlock, twoPassBlockSize)
		if len(c.commandBuf) < bufSize {
			c.commandBuf = getFastCommandBuffer(bufSize)
		}
		if len(c.literalBuf) < bufSize {
			c.literalBuf = getFastByteBuffer(&poolFastLiterals, bufSize)
		}
	}

	input := c.buf
	var smallTable32 [1024]uint32
	var table32 []uint32
	var table32Ptr *[]uint32
	for len(input) > 0 || isLast {
		blockSize := min(len(input), blockSizeLimit)
		blockIsLast := isLast && blockSize == len(input)
		block := input[:blockSize]
		input = input[blockSize:]

		// Size and clear hash table per block, matching the C reference.
		htsize := fastHashTableSize(c.quality, blockSize)

		switch c.quality {
		case 0:
			if htsize <= len(smallTable32) {
				table := smallTable32[:htsize]
				clear(table)
				compressFragmentFast(c.onePass, block, blockIsLast, table, &b)
				break
			}
			if len(table32) < htsize {
				if table32Ptr == nil {
					table32Ptr, table32 = getFastUint32Buffer(htsize)
				} else {
					*table32Ptr = make([]uint32, htsize)
					table32 = *table32Ptr
				}
				clear(table32)
			} else {
				clear(table32[:htsize])
			}
			table := table32[:htsize]
			compressFragmentFast(c.onePass, block, blockIsLast, table, &b)
		case 1:
			if len(c.table) < htsize {
				c.table = getFastUint32Slice(htsize)
				clear(c.table)
			} else {
				clear(c.table[:htsize])
			}
			table := c.table[:htsize]
			compressFragmentTwoPass(c.twoPass, block, blockIsLast, c.commandBuf[:min(blockSize, twoPassBlockSize)], c.literalBuf[:min(blockSize, twoPassBlockSize)], table, &b)
		}

		// Flush compressed bytes between blocks to keep memory bounded.
		n := b.bitOffset / 8
		if n > 0 {
			if _, err := dst.Write(c.outBuf[:n]); err != nil {
				putFastUint32Buffer(table32Ptr, table32)
				return err
			}
			// Carry trailing sub-byte bits to the start of the buffer.
			c.outBuf[0] = c.outBuf[n]
			b.bitOffset &= 7
		}

		if blockIsLast {
			break
		}
	}
	c.buf = c.buf[:0]

	// Write any remaining sub-byte bits.
	n := (b.bitOffset + 7) / 8
	if n > 0 {
		if _, err := dst.Write(c.outBuf[:n]); err != nil {
			putFastUint32Buffer(table32Ptr, table32)
			return err
		}
	}
	putFastUint32Buffer(table32Ptr, table32)
	return nil
}

// fastHashTableSize returns the hash table size for a given quality and block
// size, matching the C reference GetHashTable logic.
func fastHashTableSize(quality, blockSize int) int {
	maxTableSize := 1 << 15 // q0
	if quality == 1 {
		maxTableSize = 1 << 17
	}
	htsize := 256
	for htsize < maxTableSize && htsize < blockSize {
		htsize <<= 1
	}
	// Q0 requires odd-bit tables (9, 11, 13, 15).
	if quality == 0 && (htsize&0xAAAAA) == 0 {
		htsize <<= 1
	}
	return htsize
}

// Pool helpers shared by fast paths. Pools live next to the Compressor that
// uses them.

func getFastByteBuffer(pool *sync.Pool, n int) []byte {
	if v := pool.Get(); v != nil {
		buf := *v.(*[]byte)
		if cap(buf) >= n {
			return buf[:n]
		}
	}
	return make([]byte, n)
}

func putFastByteBuffer(pool *sync.Pool, buf []byte) {
	if cap(buf) != 0 {
		buf = buf[:0]
		pool.Put(&buf)
	}
}

func getFastUint32Slice(n int) []uint32 {
	if v := poolFastTable32.Get(); v != nil {
		buf := *v.(*[]uint32)
		if cap(buf) >= n {
			return buf[:n]
		}
	}
	return make([]uint32, n)
}

func putFastUint32Slice(buf []uint32) {
	if cap(buf) != 0 {
		buf = buf[:0]
		poolFastTable32.Put(&buf)
	}
}

func getFastUint32Buffer(n int) (*[]uint32, []uint32) {
	if v := poolFastTable32.Get(); v != nil {
		p := v.(*[]uint32)
		buf := *p
		if cap(buf) >= n {
			return p, buf[:n]
		}
		*p = make([]uint32, n)
		return p, *p
	}
	p := new([]uint32)
	*p = make([]uint32, n)
	return p, *p
}

func putFastUint32Buffer(p *[]uint32, buf []uint32) {
	if p != nil && cap(buf) != 0 {
		*p = buf[:0]
		poolFastTable32.Put(p)
	}
}

func getFastCommandBuffer(n int) []uint32 {
	if v := poolFastCommands.Get(); v != nil {
		buf := *v.(*[]uint32)
		if cap(buf) >= n {
			return buf[:n]
		}
	}
	return make([]uint32, n)
}

func putFastCommandBuffer(buf []uint32) {
	if cap(buf) != 0 {
		buf = buf[:0]
		poolFastCommands.Put(&buf)
	}
}
