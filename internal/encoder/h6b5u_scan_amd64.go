//go:build amd64 && !purego

package encoder

import "unsafe"

// h6b5uFindInBucket scans the bucket of key newest first and stores cur. It
// prefetches the bucket of nextKey and the data at its newest entry.
// It updates out only when the score exceeds bestScore.
// Before the first wrap, mask is ^uint(0).
//
//go:noescape
func h6b5uFindInBucket(h *h6b5u, data unsafe.Pointer, key, cur, curMasked,
	mask, minPrev, maxLength, bestLen, bestScore uint,
	nextKey uint, out *hasherSearchResult)
