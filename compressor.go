// Compressor interface used by Writer to drive a brotli stream regardless of
// quality level. Each implementation owns its scratch buffers and pool
// lifecycle: acquisition is via the newCompressor factory, return-to-pool is
// via Release.

package brrr

import "io"

// compressor is the unified backend interface for Writer. Quality, lgwin, and
// sizeHint are immutable after construction; Reset clears per-stream state but
// keeps those parameters. AttachDictionary may return an error for backends
// that do not support compound dictionaries (q0/q1).
type compressor interface {
	// Write enqueues input. Implementations may emit compressed output to dst
	// during this call (q>=2) or buffer until Flush/Close (q0/q1).
	Write(dst io.Writer, p []byte) (int, error)

	// Flush emits a non-final meta-block boundary so that everything written
	// so far is decodable from the output stream.
	Flush(dst io.Writer) error

	// Close finalizes the stream by writing the last meta-block.
	Close(dst io.Writer) error

	// Reset discards per-stream state for reuse with the same parameters.
	Reset()

	// AttachDictionary attaches a compound dictionary to the encoder.
	AttachDictionary(pd *PreparedDictionary) error

	// Release returns the compressor and any owned scratch buffers to their
	// pools. The compressor must not be used after Release.
	Release()
}
