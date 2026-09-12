// Scalar histogramAdd for every target the SSE2 version does not cover:
// non-amd64 and purego builds. Byte-for-byte identical results; this is the
// original loop unchanged.

//go:build !amd64 || purego

package encoder

// histogramAdd adds src histogram into dst histogram element-wise.
func histogramAdd(dst, src []uint32, alphabetSize int) {
	for i := range alphabetSize {
		dst[i] += src[i]
	}
}
