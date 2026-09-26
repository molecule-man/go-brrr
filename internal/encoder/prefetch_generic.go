//go:build !amd64 || purego

package encoder

import "unsafe"

func prefetch2(a, b unsafe.Pointer) {}
