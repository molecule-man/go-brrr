//go:build amd64 && !purego

package encoder

import "unsafe"

// tagMask32 sets bit k when tags[k] equals tag and prefetches next0 and next1.
//
//go:noescape
func tagMask32(tags *[32]uint8, tag uint8, next0, next1 unsafe.Pointer) uint32
