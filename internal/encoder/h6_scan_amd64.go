//go:build amd64 && !purego

package encoder

import "unsafe"

// h6FindInBucket runs phase 2 of h6 findLongestMatch. keyTag is
// key<<8 | tag. It compares the tags of the block of key with tag, scans
// the matching entries newest first and writes the best match to out when
// its score beats bestScore. Then it stores cur in the bucket. mask is the
// ring buffer mask, or ^uint(0) before the first wrap. It also prefetches
// next, the block of the next position.
//
//go:noescape
func h6FindInBucket(h *h6, data unsafe.Pointer, keyTag, cur, curMasked,
	mask, minPrev, maxLength, bestLen, bestScore uint,
	next *h6Block, out *hasherSearchResult)
