// Streaming brotli writer and public API.

package brrr

import (
	"errors"
	"io"
	"strconv"
	"sync"
)

var (
	poolOnePassArena = sync.Pool{New: func() any { return new(onePassArena) }}
	poolTwoPassArena = sync.Pool{New: func() any { return new(twoPassArena) }}
	poolFastOutBuf   sync.Pool
	poolFastTable32  sync.Pool
	poolFastCommands sync.Pool
	poolFastLiterals sync.Pool
)

// Writer compresses data into brotli format.
//
// Callers must Close the Writer to finalize the brotli stream.
type Writer struct {
	dst         io.Writer
	err         error
	enc         streamEncoder // non-nil for quality >= 2
	onePass     *onePassArena // non-nil when quality == 0
	twoPass     *twoPassArena // non-nil when quality == 1
	buf         []byte        // buffered uncompressed input (quality 0-1)
	outBuf      []byte        // scratch space for compressed output (quality 0-1)
	table       []uint32
	commandBuf  []uint32
	literalBuf  []byte
	quality     int // 0 = one-pass, 1 = two-pass, 2+ = streaming
	lgwin       int
	sizeHint    uint // from WriterOptions, preserved across Reset
	wroteHeader bool
	closed      bool
	reused      bool // true after first Reset; suppresses pool release on Close
}

// NewWriter returns a new Writer compressing data to dst at the given
// quality level. Supported levels are 0 (BestSpeed) through 11
// (BestCompression).
func NewWriter(dst io.Writer, level int) (*Writer, error) {
	return NewWriterOptions(dst, level, WriterOptions{})
}

// NewWriterOptions returns a new Writer compressing data to dst at the given
// quality level with additional tuning options. LGWin range is 10–24; 0
// selects the default (22).
func NewWriterOptions(dst io.Writer, level int, opts WriterOptions) (*Writer, error) {
	if level < 0 || level > 11 {
		return nil, errors.New("brrr: invalid compression level: " + strconv.Itoa(level))
	}

	lgwin := opts.LGWin
	if lgwin == 0 {
		lgwin = defaultLGWin
	}
	if lgwin < minLGWin || lgwin > maxLGWin {
		return nil, errors.New("brrr: invalid window size: lgwin=" + strconv.Itoa(lgwin) +
			" (must be " + strconv.Itoa(minLGWin) + "–" + strconv.Itoa(maxLGWin) + ")")
	}

	w := &Writer{dst: dst, quality: level, lgwin: lgwin, sizeHint: opts.SizeHint}
	w.init()
	return w, nil
}

// Write compresses p and writes it to the underlying writer.
// Data may be buffered internally; call Flush or Close to ensure all
// data is written.
func (w *Writer) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.closed {
		return 0, io.ErrClosedPipe
	}

	if w.enc == nil {
		// Quality 0-1: buffer input for batch compression.
		w.buf = append(w.buf, p...)
		return len(p), nil
	}

	// Quality >= 2: streaming ring-buffer approach.
	w.enc.updateSizeHint(uint(len(p)))
	written := len(p)
	for len(p) > 0 {
		remaining := w.enc.remainingInputBlockSize()
		if remaining == 0 {
			if err := w.writeEncoded(w.enc.encodeData(false, false)); err != nil {
				return 0, err
			}
			continue
		}
		chunk := min(uint(len(p)), remaining)
		w.enc.copyInputToRingBuffer(p[:chunk])
		p = p[chunk:]
	}
	return written, nil
}

// Flush compresses any buffered data and writes it to the underlying
// writer as one or more non-final meta-blocks. Flush does not finalize
// the brotli stream; call Close for that.
func (w *Writer) Flush() error {
	if w.err != nil {
		return w.err
	}
	if w.closed {
		return io.ErrClosedPipe
	}

	if w.enc != nil {
		return w.flushStreaming()
	}

	if len(w.buf) == 0 {
		return nil
	}
	w.err = w.compress(false)
	return w.err
}

// Close flushes remaining data, finalizes the brotli stream by writing
// the final empty meta-block, and writes everything to the underlying writer.
// Close does not close the underlying writer.
func (w *Writer) Close() error {
	if w.err != nil {
		return w.err
	}
	if w.closed {
		return nil
	}
	w.closed = true

	if w.enc != nil {
		w.err = w.closeStreaming()
		if !w.reused {
			w.enc.releaseBuffers()
			switch e := w.enc.(type) {
			case *encoderSplit:
				poolEncoderSplit.Put(e)
				w.enc = nil
			case *encoderArena:
				poolEncoderArena.Put(e)
				w.enc = nil
			}
		}
		return w.err
	}

	w.err = w.compress(true)
	if !w.reused {
		w.releaseFastBuffers()
	}
	return w.err
}

// AttachDictionary registers raw dictionary bytes as a compound dictionary
// chunk. The encoder will reference these bytes as backward distances beyond
// the ring buffer. May be called multiple times (up to 15 dictionaries).
// Returns an error if quality < 2 or max dictionaries exceeded.
func (w *Writer) AttachDictionary(data []byte) error {
	if w.enc == nil {
		return errQualityTooLow
	}
	return w.enc.attachDictionary(data)
}

// Reset discards internal state and switches to writing to dst.
// This permits reusing a Writer rather than allocating a new one.
func (w *Writer) Reset(dst io.Writer) {
	w.dst = dst
	w.err = nil
	w.closed = false
	w.reused = true

	if w.enc != nil {
		w.enc.reset(w.quality, w.lgwin, w.sizeHint)
		return
	}

	if w.quality >= 4 {
		// enc was returned to pool on a previous Close; re-acquire.
		e := poolEncoderSplit.Get().(*encoderSplit)
		e.reset(w.quality, w.lgwin, w.sizeHint)
		w.enc = e
		return
	}

	if w.quality >= 2 {
		// enc was returned to pool on a previous Close; re-acquire.
		e := poolEncoderArena.Get().(*encoderArena)
		e.reset(w.quality, w.lgwin, w.sizeHint)
		w.enc = e
		return
	}

	w.buf = w.buf[:0]
	w.wroteHeader = false
	// No need to clear w.table here: compress() clears table[:htsize]
	// per block, so only the entries actually used are zeroed.
	if w.quality == 0 {
		if w.onePass == nil {
			w.onePass = poolOnePassArena.Get().(*onePassArena)
		}
		w.onePass.initCommandPrefixCodes()
	} else if w.twoPass == nil {
		w.twoPass = poolTwoPassArena.Get().(*twoPassArena)
	}
}

func (w *Writer) init() {
	if w.quality >= 4 {
		e := poolEncoderSplit.Get().(*encoderSplit)
		e.reset(w.quality, w.lgwin, w.sizeHint)
		w.enc = e
		return
	}
	if w.quality >= 2 {
		e := poolEncoderArena.Get().(*encoderArena)
		e.reset(w.quality, w.lgwin, w.sizeHint)
		w.enc = e
		return
	}
	// Quality 0-1: table, commandBuf, and literalBuf are lazily allocated
	// in compress() based on actual block size, avoiding large upfront
	// allocations that dominate cost for small inputs.
	switch w.quality {
	case 0:
		w.onePass = poolOnePassArena.Get().(*onePassArena)
		w.onePass.initCommandPrefixCodes()
	case 1:
		w.twoPass = poolTwoPassArena.Get().(*twoPassArena)
	}
}

// compress runs the fast encoder on buffered data and writes the
// compressed output to dst. If isLast is true the brotli stream
// is finalized.
func (w *Writer) compress(isLast bool) error {
	// The C reference limits each fragment to 1<<lgwin bytes.
	blockSizeLimit := 1 << w.lgwin

	// Ensure the output buffer is large enough for the largest fragment.
	// Worst case: uncompressed meta-block = header + data + padding.
	maxBlock := min(len(w.buf), blockSizeLimit)
	needed := maxBlock*2 + 1024
	if len(w.outBuf) < needed {
		w.outBuf = getFastByteBuffer(&poolFastOutBuf, needed)
	}
	w.outBuf[0] = 0

	b := bitWriter{buf: w.outBuf}

	if !w.wroteHeader {
		// For quality 0-1 the C reference clamps the header lgwin to at
		// least 18 since these modes don't use a sliding window.
		headerLGWin := max(w.lgwin, 18)
		lastBytes, lastBytesBits := encodeWindowBits(headerLGWin)
		b.writeBits(uint(lastBytesBits), uint64(lastBytes))
		w.wroteHeader = true
	}

	// Lazily grow command/literal buffers for q=1, sized to actual need.
	if w.quality == 1 {
		bufSize := min(maxBlock, twoPassBlockSize)
		if len(w.commandBuf) < bufSize {
			w.commandBuf = getFastCommandBuffer(bufSize)
		}
		if len(w.literalBuf) < bufSize {
			w.literalBuf = getFastByteBuffer(&poolFastLiterals, bufSize)
		}
	}

	input := w.buf
	var smallTable32 [1024]uint32
	var table32 []uint32
	var table32Ptr *[]uint32
	for len(input) > 0 || isLast {
		blockSize := min(len(input), blockSizeLimit)
		blockIsLast := isLast && blockSize == len(input)
		block := input[:blockSize]
		input = input[blockSize:]

		// Size and clear hash table per block, matching the C reference.
		htsize := fastHashTableSize(w.quality, blockSize)

		switch w.quality {
		case 0:
			if htsize <= len(smallTable32) {
				table := smallTable32[:htsize]
				clear(table)
				compressFragmentFast(w.onePass, block, blockIsLast, table, &b)
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
			compressFragmentFast(w.onePass, block, blockIsLast, table, &b)
		case 1:
			if len(w.table) < htsize {
				w.table = getFastUint32Slice(htsize)
				clear(w.table)
			} else {
				clear(w.table[:htsize])
			}
			table := w.table[:htsize]
			compressFragmentTwoPass(w.twoPass, block, blockIsLast, w.commandBuf[:min(blockSize, twoPassBlockSize)], w.literalBuf[:min(blockSize, twoPassBlockSize)], table, &b)
		}

		// Flush compressed bytes between blocks to keep memory bounded.
		n := b.bitOffset / 8
		if n > 0 {
			if _, err := w.dst.Write(w.outBuf[:n]); err != nil {
				putFastUint32Buffer(table32Ptr, table32)
				return err
			}
			// Carry trailing sub-byte bits to the start of the buffer.
			w.outBuf[0] = w.outBuf[n]
			b.bitOffset &= 7
		}

		if blockIsLast {
			break
		}
	}
	w.buf = w.buf[:0]

	// Write any remaining sub-byte bits.
	n := (b.bitOffset + 7) / 8
	if n > 0 {
		_, err := w.dst.Write(w.outBuf[:n])
		if err != nil {
			putFastUint32Buffer(table32Ptr, table32)
			return err
		}
	}
	putFastUint32Buffer(table32Ptr, table32)
	return nil
}

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

func (w *Writer) releaseFastBuffers() {
	if w.onePass != nil {
		poolOnePassArena.Put(w.onePass)
		w.onePass = nil
	}
	if w.twoPass != nil {
		poolTwoPassArena.Put(w.twoPass)
		w.twoPass = nil
	}
	putFastByteBuffer(&poolFastOutBuf, w.outBuf)
	w.outBuf = nil
	putFastUint32Slice(w.table)
	w.table = nil
	putFastCommandBuffer(w.commandBuf)
	w.commandBuf = nil
	putFastByteBuffer(&poolFastLiterals, w.literalBuf)
	w.literalBuf = nil
}

// writeEncoded writes encoder output to the underlying writer.
func (w *Writer) writeEncoded(out []byte) error {
	if len(out) > 0 {
		_, err := w.dst.Write(out)
		if err != nil {
			w.err = err
			return err
		}
	}
	return nil
}

// flushStreaming flushes the streaming encoder (quality >= 2).
// Emits any accumulated data as a non-final meta-block, followed by a
// byte-padding metadata block so the output is byte-aligned.
func (w *Writer) flushStreaming() error {
	if err := w.writeEncoded(w.enc.encodeData(false, true)); err != nil {
		return err
	}

	// Inject byte-padding metadata block: ISLAST=0, MNIBBLES=11,
	// reserved=0, MSKIPBYTES=00. This flushes any trailing sub-byte
	// bits to the output.
	lastBytes, lastBytesBits := w.enc.trailingBits()
	seal := uint32(lastBytes)
	sealBits := uint(lastBytesBits)
	w.enc.clearTrailingBits()

	seal |= 0x6 << sealBits
	sealBits += 6

	var padding [3]byte
	padding[0] = byte(seal)
	if sealBits > 8 {
		padding[1] = byte(seal >> 8)
	}
	if sealBits > 16 {
		padding[2] = byte(seal >> 16)
	}
	n := (sealBits + 7) / 8
	if _, err := w.dst.Write(padding[:n]); err != nil {
		w.err = err
		return err
	}
	return nil
}

// closeStreaming finalizes the streaming encoder (quality >= 2).
func (w *Writer) closeStreaming() error {
	out := w.enc.encodeData(true, false)
	if err := w.writeEncoded(out); err != nil {
		return err
	}

	// If there are trailing sub-byte bits (edge case: empty stream header
	// bits that weren't flushed by encodeData), emit them.
	lastBytes, lastBytesBits := w.enc.trailingBits()
	if lastBytesBits > 0 {
		var trailing [2]byte
		trailing[0] = byte(lastBytes)
		trailing[1] = byte(lastBytes >> 8)
		n := (lastBytesBits + 7) / 8
		if _, err := w.dst.Write(trailing[:n]); err != nil {
			w.err = err
			return err
		}
	}
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
