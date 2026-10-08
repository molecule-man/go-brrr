//go:build amd64 && !purego

package encoder

import "unsafe"

// h6FindInBucket runs phase 2 of h6 findLongestMatch. keyTag is
// key<<8 | tag. It compares the tags of the block of key with tag, scans
// the matching entries newest first and returns the best match. score
// equals bestScore when no entry beats it. mask is the ring buffer mask, or
// ^uint(0) before the first wrap. It also prefetches next, the block of the
// next position.
//
//go:noescape
func h6FindInBucket(h *h6, data unsafe.Pointer, keyTag, curMasked, mask,
	minPrev, maxLength, bestLen, bestScore uint,
	next *h6Block) (length, distance, score uint)
