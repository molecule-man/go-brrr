// Public package-level constants and WriterOptions.

package brrr

// Compression level constants.
const (
	BestSpeed       = 0
	BestCompression = 11
)

// Window size limits (RFC 7932 Section 9.1).
const (
	minLGWin     = 10
	maxLGWin     = 24
	defaultLGWin = 22
)

// WriterOptions configures the brotli encoder.
type WriterOptions struct {
	// Quality controls the compression level (0–11).
	// 0 is fastest, 11 is best compression.
	Quality int

	// LGWin sets the base-2 logarithm of the sliding window size (10–24).
	// 0 selects the default (22).
	LGWin int

	// SizeHint is the expected total input size in bytes. When set, the
	// encoder uses it to make better decisions about context modeling and
	// hasher selection for large inputs. 0 means unknown; the encoder will
	// auto-estimate from the first Write call.
	SizeHint uint
}
