// Reader decompresses a brotli stream incrementally.

package brrr

import (
	"errors"
	"io"
)

var errReaderClosed = errors.New("brrr: reader is closed")

// Reader decompresses brotli-compressed data from an underlying io.Reader.
type Reader struct {
	src          io.Reader
	err          error    // sticky terminal error (io.EOF or decode error)
	srcErr       error    // deferred source error received alongside input bytes
	out          []byte   // decoded output not yet served to caller
	pendingDicts [][]byte // staged compound dictionary chunks, injected on first Read
	state        decodeState
	outPos       int
	started      bool
	buf          [32 << 10]byte
}

// NewReader returns a new Reader reading brotli-compressed data from src.
func NewReader(src io.Reader) *Reader {
	return &Reader{src: src}
}

// AttachDictionary registers raw dictionary bytes as a compound dictionary
// chunk. The decoder will use these bytes when the compressed stream contains
// backward references beyond the ring buffer. Must be called before the first
// Read. May be called up to 15 times for multiple chunks.
func (r *Reader) AttachDictionary(data []byte) error {
	if r.started {
		return errors.New("brrr: cannot attach dictionary after decoding started")
	}
	if r.err != nil {
		return r.err
	}
	if len(data) == 0 {
		return errEmptyDict
	}
	if len(r.pendingDicts) >= 15 {
		return errTooManyDicts
	}
	r.pendingDicts = append(r.pendingDicts, data)
	return nil
}

// Read decompresses data into p.
func (r *Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	// Serve buffered output first.
	if r.outPos < len(r.out) {
		n := copy(p, r.out[r.outPos:])
		r.outPos += n
		if r.outPos == len(r.out) {
			r.out = r.out[:0]
			r.outPos = 0
		}
		return n, nil
	}

	if r.err != nil {
		return 0, r.err
	}

	// Lazy initialization on first Read.
	if !r.started {
		r.state.initForReuse()
		r.started = true
		for _, d := range r.pendingDicts {
			if err := r.state.attachCompoundDict(d); err != nil {
				r.err = err
				return 0, r.err
			}
		}
		if err := r.fill(); err != nil {
			r.err = wrapInputError(err)
			return 0, r.err
		}
	}

	// Drive state machine until we have output or hit a terminal state.
	for {
		result := r.state.decompressStream(&r.out)
		switch result {
		case decoderResultNeedsMoreInput:
			if r.srcErr != nil {
				r.err = wrapInputError(r.srcErr)
				r.srcErr = nil
				return 0, r.err
			}
			if err := r.fill(); err != nil {
				r.err = wrapInputError(err)
				return 0, r.err
			}

		case decoderResultNeedsMoreOutput:
			r.out = r.state.flushOutput(r.out)
			if len(r.out) > 0 {
				r.outPos = 0
				n := copy(p, r.out)
				r.outPos = n
				if r.outPos == len(r.out) {
					r.out = r.out[:0]
					r.outPos = 0
				}
				return n, nil
			}

		case decoderResultSuccess:
			r.out = r.state.flushOutput(r.out)
			r.err = r.terminalReadError()
			if len(r.out) == 0 {
				return 0, r.err
			}
			r.outPos = 0
			n := copy(p, r.out)
			r.outPos = n
			if r.outPos == len(r.out) {
				r.out = r.out[:0]
				r.outPos = 0
			}
			return n, nil

		case decoderResultError:
			r.err = r.state.err
			return 0, r.err
		}
	}
}

// Reset discards internal state and switches to reading from src.
// Compound dictionaries are cleared; call AttachDictionary again if needed.
func (r *Reader) Reset(src io.Reader) {
	r.src = src
	r.err = nil
	r.srcErr = nil
	r.out = r.out[:0]
	r.pendingDicts = nil
	r.outPos = 0
	r.started = false
	// state will be zeroed by initForReuse on first Read;
	// no need to zero it here or touch the 32KB buf.
}

// Close releases resources held by the Reader.
func (r *Reader) Close() error {
	r.src = nil
	putDecRingBuf(r.state.ringbuffer)
	r.state = decodeState{}
	r.out = nil
	r.srcErr = nil
	r.err = errReaderClosed
	r.started = true
	return nil
}

// fill saves unconsumed input bytes and reads more from src.
// After fill, the bitReader's input contains any leftover bytes
// followed by freshly read data.
func (r *Reader) fill() error {
	br := &r.state.br
	br.unload()
	remaining := br.availIn()
	if remaining > 0 {
		copy(r.buf[:remaining], br.input[br.pos:br.pos+remaining])
	}
	n, srcErr := r.src.Read(r.buf[remaining:])
	remaining += n
	if remaining > 0 {
		r.srcErr = srcErr
		br.setInput(r.buf[:remaining])
		return nil
	}
	return srcErr
}

func (r *Reader) terminalReadError() error {
	if r.srcErr != nil && !errors.Is(r.srcErr, io.EOF) {
		err := r.srcErr
		r.srcErr = nil
		return err
	}
	r.srcErr = nil
	return io.EOF
}

// wrapInputError converts an io.EOF from the source into a truncated-input
// decode error, since EOF from the source means the brotli stream was
// incomplete.
func wrapInputError(err error) error {
	if errors.Is(err, io.EOF) {
		return decompressError("truncated input")
	}
	return err
}
