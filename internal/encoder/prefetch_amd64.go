//go:build amd64 && !purego

package encoder

import "unsafe"

//go:noescape
func prefetch2(a, b unsafe.Pointer)
