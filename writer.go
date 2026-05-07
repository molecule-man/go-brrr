// Streaming brotli writer and public API.

package brrr

import (
	"errors"
	"io"
	"strconv"
	"sync"
)

var poolOnePassArena = sync.Pool{New: func() any { return new(onePassArena) }}
var poolTwoPassArena = sync.Pool{New: func() any { return new(twoPassArena) }}

// Writer compresses data into brotli format.
//
// Callers must Close the Writer to finalize the brotli stream.
type Writer struct {
	dst      io.Writer
	err      error
	enc      streamEncoder         // non-nil for quality >= 2
	c        compressor            // non-nil for quality <= 1 (q0/q1 backend)
	dicts    []*PreparedDictionary // from WriterOptions, preserved across Reset
	quality  int                   // 0 = one-pass, 1 = two-pass, 2+ = streaming
	lgwin    int
	sizeHint uint // from WriterOptions, preserved across Reset
	closed   bool
	reused   bool // true after first Reset; suppresses pool release on Close
}

// NewWriter returns a new Writer compressing data to dst at the given
// quality level. Supported levels are 0 (BestSpeed) through 11
// (BestCompression).
func NewWriter(dst io.Writer, level int) (*Writer, error) {
	return NewWriterOptions(dst, level, WriterOptions{})
}

// NewWriterOptions returns a new Writer compressing data to dst at the given
// quality level with additional tuning options. LGWin range is 10–24; 0
// selects the default (22). Compound dictionaries supplied via opts.Dictionaries
// require level >= 2.
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

	if len(opts.Dictionaries) > maxCompoundDicts {
		return nil, errTooManyDicts
	}
	if len(opts.Dictionaries) > 0 && level < 2 {
		return nil, errQualityTooLow
	}

	w := &Writer{dst: dst, quality: level, lgwin: lgwin, sizeHint: opts.SizeHint, dicts: opts.Dictionaries}
	w.init()
	for _, pd := range w.dicts {
		_ = w.enc.attachDictionary(pd)
	}
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

	if w.c != nil {
		return w.c.Write(w.dst, p)
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

	w.err = w.c.Flush(w.dst)
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

	w.err = w.c.Close(w.dst)
	if !w.reused {
		w.c.Release()
		w.c = nil
	}
	return w.err
}

// Reset discards internal state and switches to writing to dst.
// This permits reusing a Writer rather than allocating a new one.
// Compound dictionaries supplied via WriterOptions are preserved.
func (w *Writer) Reset(dst io.Writer) {
	w.dst = dst
	w.err = nil
	w.closed = false
	w.reused = true

	if w.enc != nil {
		w.enc.reset(w.quality, w.lgwin, w.sizeHint)
		for _, pd := range w.dicts {
			_ = w.enc.attachDictionary(pd)
		}
		return
	}

	if w.quality >= 4 {
		// enc was returned to pool on a previous Close; re-acquire.
		e := poolEncoderSplit.Get().(*encoderSplit)
		e.reset(w.quality, w.lgwin, w.sizeHint)
		w.enc = e
		for _, pd := range w.dicts {
			_ = w.enc.attachDictionary(pd)
		}
		return
	}

	if w.quality >= 2 {
		// enc was returned to pool on a previous Close; re-acquire.
		e := poolEncoderArena.Get().(*encoderArena)
		e.reset(w.quality, w.lgwin, w.sizeHint)
		w.enc = e
		for _, pd := range w.dicts {
			_ = w.enc.attachDictionary(pd)
		}
		return
	}

	if w.c == nil {
		w.c = newFastCompressor(w.quality, w.lgwin)
		return
	}
	w.c.Reset()
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
	// Quality 0-1: fastCompressor lazily allocates table/commandBuf/literalBuf
	// in compress() based on actual block size, avoiding large upfront
	// allocations that dominate cost for small inputs.
	w.c = newFastCompressor(w.quality, w.lgwin)
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
