//go:build amd64 && !purego

package encoder

// histogramAdd adds src histogram into dst histogram element-wise.
func histogramAdd(dst, src []uint32, alphabetSize int) {
	histogramAddSSE2Unrolled8x(dst, src, alphabetSize)
}

// histogramAddSSE2Unrolled8x adds src into dst thirty-two uint32 per iteration
// across eight XMM register pairs. SSE2 is baseline on amd64, so this needs no
// CPU feature check. Implemented in histogramAdd.s.
//
//go:noescape
func histogramAddSSE2Unrolled8x(dst, src []uint32, n int)
