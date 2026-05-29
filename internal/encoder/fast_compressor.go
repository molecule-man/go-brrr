// Fast compressor: q0 (one-pass) and q1 (two-pass).
// Compresses incrementally in fragments of 1<<lgwin, retaining at most one
// fragment in memory; remaining data is emitted on Flush/Close.

package encoder

import (
	"errors"
	"io"
	"sync"
)

const maxPooledFastInputBuffer = 64 << 10

var (
	errFastCompoundDict = errors.New("brrr: compound dictionaries require quality >= 2")

	poolOnePassArena = sync.Pool{New: func() any { return new(onePassArena) }}
	poolTwoPassArena = sync.Pool{New: func() any { return new(twoPassArena) }}

	poolFastCompressor = sync.Pool{New: func() any { return new(fastCompressor) }}
	poolFastOutBuf     sync.Pool
	poolFastTable32    sync.Pool
	poolFastCommands   sync.Pool
	poolFastLiterals   sync.Pool
)

// fastCompressor implements Compressor for q0 and q1. Input is compressed
// incrementally; at most one 1<<lgwin fragment is buffered in memory.
type fastCompressor struct {
	onePass *onePassArena // non-nil when quality == 0
	twoPass *twoPassArena // non-nil when quality == 1

	buf        []byte // pending input, at most one fragment (1<<lgwin bytes)
	outBuf     []byte // scratch for compressed output
	table      []uint32
	commandBuf []uint32 // q1 only
	literalBuf []byte   // q1 only

	carryBits uint // number of valid sub-byte bits held in carry (0..7)
	quality   int
	lgwin     int

	carry       byte // trailing output bits (carryBits of them) not yet byte-complete
	wroteHeader bool
}

// newFastCompressor returns a Compressor for q0 or q1, acquiring its arena
// from the appropriate pool.
func newFastCompressor(quality, lgwin int) *fastCompressor {
	c := poolFastCompressor.Get().(*fastCompressor)
	c.quality = quality
	c.lgwin = lgwin
	switch quality {
	case 0:
		c.onePass = poolOnePassArena.Get().(*onePassArena)
		c.onePass.initCommandPrefixCodes()
	case 1:
		c.twoPass = poolTwoPassArena.Get().(*twoPassArena)
	}
	return c
}

// Write compresses input incrementally. Full fragments (1<<lgwin bytes) are
// emitted to dst as they fill; at most one fragment is retained in memory, so
// total buffering is bounded regardless of stream length. The final fragment
// is deferred to Flush/Close, where it may be marked last.
func (c *fastCompressor) Write(dst io.Writer, p []byte) (int, error) {
	total := len(p)
	blockSizeLimit := 1 << c.lgwin
	for len(p) > 0 {
		// With nothing buffered and more than a full fragment available,
		// compress straight from p to avoid copying.
		if len(c.buf) == 0 && len(p) > blockSizeLimit {
			if err := c.emitFragment(dst, p[:blockSizeLimit], false); err != nil {
				return total - len(p), err
			}
			p = p[blockSizeLimit:]
			continue
		}
		room := blockSizeLimit - len(c.buf)
		take := min(room, len(p))
		c.buf = append(c.buf, p[:take]...)
		p = p[take:]
		// Emit only when the fragment is full and more input follows, so the
		// last fragment stays buffered for Flush/Close.
		if len(c.buf) == blockSizeLimit && len(p) > 0 {
			if err := c.emitFragment(dst, c.buf, false); err != nil {
				return total - len(p), err
			}
			c.buf = c.buf[:0]
		}
	}
	return total, nil
}

// Flush emits buffered input as a non-final meta-block and writes all complete
// output bytes to dst. As in the C reference fast path, the trailing sub-byte
// (at most 7 bits) is retained and continues into the next meta-block rather
// than being padded out, so the bitstream stays continuous. The brotli stream
// is not finalized.
func (c *fastCompressor) Flush(dst io.Writer) error {
	if len(c.buf) == 0 {
		return nil
	}
	err := c.emitFragment(dst, c.buf, false)
	c.buf = c.buf[:0]
	return err
}

// Close emits any remaining buffered input as the final meta-block, finalizing
// the brotli stream.
func (c *fastCompressor) Close(dst io.Writer) error {
	if err := c.emitFragment(dst, c.buf, true); err != nil {
		return err
	}
	c.buf = c.buf[:0]
	return nil
}

// Reset clears per-stream state for reuse with the same quality/lgwin.
// The arena is preserved (not returned to pool) and re-initialized.
func (c *fastCompressor) Reset() {
	c.buf = c.buf[:0]
	c.carry = 0
	c.carryBits = 0
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
	if cap(c.buf) > maxPooledFastInputBuffer {
		c.buf = nil
	} else {
		c.buf = c.buf[:0]
	}
	c.carry = 0
	c.carryBits = 0
	c.quality = 0
	c.lgwin = 0
	c.wroteHeader = false
	poolFastCompressor.Put(c)
}

// emitFragment compresses a single fragment (at most 1<<lgwin bytes) and writes
// the resulting whole bytes to dst. Trailing sub-byte bits are retained in
// c.carry and seeded into the next fragment, so consecutive fragments form one
// continuous bitstream. If isLast is true the fragment finalizes the stream and
// the output is byte-aligned.
func (c *fastCompressor) emitFragment(dst io.Writer, block []byte, isLast bool) error {
	// Worst case output: uncompressed meta-block = header + data + padding.
	needed := len(block)*2 + 1024
	if len(c.outBuf) < needed {
		c.outBuf = getFastByteBuffer(&poolFastOutBuf, needed)
	}
	c.outBuf[0] = c.carry
	b := bitWriter{buf: c.outBuf, bitOffset: c.carryBits}

	if !c.wroteHeader {
		// For quality 0-1 the C reference clamps the header lgwin to at
		// least 18 since these modes don't use a sliding window.
		headerLGWin := max(c.lgwin, 18)
		lastBytes, lastBytesBits := encodeWindowBits(headerLGWin)
		b.writeBits(uint(lastBytesBits), uint64(lastBytes))
		c.wroteHeader = true
	}

	// Size and clear the hash table for this fragment, matching the C reference.
	htsize := fastHashTableSize(c.quality, len(block))
	switch c.quality {
	case 0:
		var smallTable32 [1024]uint32
		var table []uint32
		if htsize <= len(smallTable32) {
			table = smallTable32[:htsize]
		} else {
			if len(c.table) < htsize {
				c.table = getFastUint32Slice(htsize)
			}
			table = c.table[:htsize]
		}
		clear(table)
		compressFragmentFast(c.onePass, block, isLast, table, &b)
	case 1:
		bufSize := min(len(block), twoPassBlockSize)
		if len(c.commandBuf) < bufSize {
			c.commandBuf = getFastCommandBuffer(bufSize)
		}
		if len(c.literalBuf) < bufSize {
			c.literalBuf = getFastByteBuffer(&poolFastLiterals, bufSize)
		}
		if len(c.table) < htsize {
			c.table = getFastUint32Slice(htsize)
		}
		table := c.table[:htsize]
		clear(table)
		compressFragmentTwoPass(c.twoPass, block, isLast,
			c.commandBuf[:bufSize], c.literalBuf[:bufSize], table, &b)
	}

	// Emit whole bytes; retain any trailing sub-byte bits for the next fragment.
	n := b.bitOffset / 8
	if n > 0 {
		if _, err := dst.Write(c.outBuf[:n]); err != nil {
			return err
		}
	}
	// Retain any trailing sub-byte bits to seed the next fragment. A last
	// fragment is byte-aligned by compressFragment*, so n*8 == bitOffset and
	// carryBits is 0.
	c.carry = c.outBuf[n]
	c.carryBits = b.bitOffset & 7
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
