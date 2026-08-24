// Streaming brotli writer and public API.

package brrr

import (
	"bytes"
	"errors"
	"io"
	"strconv"

	"github.com/molecule-man/go-brrr/internal/encoder"
)

// Writer compresses data into brotli format.
//
// Callers must Close the Writer to finalize the brotli stream.
type Writer struct {
	dst      io.Writer
	err      error
	c        encoder.Compressor
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

	if len(opts.Dictionaries) > encoder.MaxCompoundDicts {
		return nil, encoder.ErrTooManyDicts
	}
	if len(opts.Dictionaries) > 0 && level < 2 {
		return nil, encoder.ErrQualityTooLow
	}

	w := &Writer{dst: dst, quality: level, lgwin: lgwin, sizeHint: opts.SizeHint, dicts: opts.Dictionaries}
	w.c = encoder.NewCompressor(w.quality, w.lgwin, w.sizeHint)
	for _, pd := range w.dicts {
		_ = w.c.AttachDictionary(pd.impl)
	}
	return w, nil
}

// Compress compresses data at the given quality level and returns the
// brotli-compressed bytes. It is the one-shot counterpart to [Decompress].
// Supported levels are 0 (BestSpeed) through 11 (BestCompression). The exact
// input length is supplied to the encoder as a size hint.
func Compress(data []byte, level int) ([]byte, error) {
	var buf bytes.Buffer
	w, err := NewWriterOptions(&buf, level, WriterOptions{SizeHint: uint(len(data))})
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
	n, err := w.c.Write(w.dst, p)
	if err != nil {
		w.err = err
	}
	return n, err
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

	if w.c == nil {
		// Compressor was released on a previous Close; re-acquire.
		w.c = encoder.NewCompressor(w.quality, w.lgwin, w.sizeHint)
	} else {
		w.c.Reset()
	}
	for _, pd := range w.dicts {
		_ = w.c.AttachDictionary(pd.impl)
	}
}
