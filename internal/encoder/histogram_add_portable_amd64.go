//go:build goexperiment.simd && go1.27 && amd64 && !purego

package encoder

import (
	"simd"
	"simd/archsimd"
)

func histogramAdd(dst, src []uint32, alphabetSize int) {
	if simd.VectorBitSize() < 256 {
		histogramAddAsm(dst, src, alphabetSize)
		return
	}
	histogramAddPortable(dst, src, alphabetSize)
}

//go:noescape
func histogramAddAsm(dst, src []uint32, n int)

func histogramAddPortable(dst, src []uint32, n int) {
	d := dst[:n:n]
	s := src[:n:n]
	w := simd.Uint32s{}.Len()
	if n < w {
		for i := range d {
			d[i] += s[i]
		}
		return
	}
	tail := d[n-w:]
	last := simd.LoadUint32s(tail).Add(simd.LoadUint32s(s[n-w:]))
	for len(d) > 4*w && len(s) > 4*w {
		simd.LoadUint32s(d).Add(simd.LoadUint32s(s)).Store(d)
		simd.LoadUint32s(d[w:]).Add(simd.LoadUint32s(s[w:])).Store(d[w:])
		simd.LoadUint32s(d[2*w:]).Add(simd.LoadUint32s(s[2*w:])).Store(d[2*w:])
		simd.LoadUint32s(d[3*w:]).Add(simd.LoadUint32s(s[3*w:])).Store(d[3*w:])
		d, s = d[4*w:], s[4*w:]
	}
	for len(d) > w && len(s) > w {
		simd.LoadUint32s(d).Add(simd.LoadUint32s(s)).Store(d)
		d, s = d[w:], s[w:]
	}
	last.Store(tail)
	if simd.VectorBitSize() > 128 {
		archsimd.ClearAVXUpperBits()
	}
}
