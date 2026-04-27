// Unit tests for compound dictionary types, construction, and matching.

package brrr

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestNewPreparedDictionary_Small(t *testing.T) {
	// Source too small for hashing (< 8 bytes).
	pd := newPreparedDictionary([]byte("hello"))
	if len(pd.items) != 0 {
		t.Fatal("expected empty items for small source")
	}
	if len(pd.source) != 5 {
		t.Fatalf("expected source length 5, got %d", len(pd.source))
	}
}

func TestNewPreparedDictionary_Medium(t *testing.T) {
	// 256 bytes — enough to build a real hash table.
	src := make([]byte, 256)
	for i := range src {
		src[i] = byte(i)
	}
	pd := newPreparedDictionary(src)

	if pd.bucketBits < 17 {
		t.Fatalf("expected bucketBits >= 17, got %d", pd.bucketBits)
	}
	if pd.slotBits < 7 {
		t.Fatalf("expected slotBits >= 7, got %d", pd.slotBits)
	}
	if len(pd.slotOffsets) != 1<<pd.slotBits {
		t.Fatalf("slotOffsets length mismatch")
	}
	if len(pd.heads) != 1<<pd.bucketBits {
		t.Fatalf("heads length mismatch")
	}
	// Every item has its end-of-chain bit set or is followed by one that does.
	// Just verify items are non-empty.
	if len(pd.items) == 0 {
		t.Fatal("expected non-empty items")
	}
}

func TestNewPreparedDictionary_AutoScale(t *testing.T) {
	// Large enough to trigger auto-scaling (16 << 17 = 2M, so 3M triggers).
	src := make([]byte, 3<<20)
	for i := range src {
		src[i] = byte(i * 7)
	}
	pd := newPreparedDictionary(src)
	if pd.bucketBits <= 17 {
		t.Fatalf("expected bucketBits > 17 for large source, got %d", pd.bucketBits)
	}
}

func TestCompoundDictionary_AttachSingle(t *testing.T) {
	var cd compoundDictionary
	data := []byte("the quick brown fox jumps over the lazy dog")
	if err := cd.attach(newPreparedDictionary(data)); err != nil {
		t.Fatal(err)
	}
	if cd.numChunks != 1 {
		t.Fatalf("expected 1 chunk, got %d", cd.numChunks)
	}
	if cd.totalSize != uint(len(data)) {
		t.Fatalf("expected totalSize %d, got %d", len(data), cd.totalSize)
	}
	if cd.chunkOffsets[0] != 0 {
		t.Fatalf("expected chunkOffsets[0] = 0, got %d", cd.chunkOffsets[0])
	}
	if cd.chunkOffsets[1] != uint(len(data)) {
		t.Fatalf("expected chunkOffsets[1] = %d, got %d", len(data), cd.chunkOffsets[1])
	}
}

func TestCompoundDictionary_AttachMultiple(t *testing.T) {
	var cd compoundDictionary
	data1 := []byte("chunk one data here!")
	data2 := []byte("chunk two data here!")
	if err := cd.attach(newPreparedDictionary(data1)); err != nil {
		t.Fatal(err)
	}
	if err := cd.attach(newPreparedDictionary(data2)); err != nil {
		t.Fatal(err)
	}
	if cd.numChunks != 2 {
		t.Fatalf("expected 2 chunks, got %d", cd.numChunks)
	}
	if cd.totalSize != uint(len(data1)+len(data2)) {
		t.Fatalf("unexpected totalSize")
	}
	if cd.chunkOffsets[1] != uint(len(data1)) {
		t.Fatalf("unexpected chunkOffsets[1]")
	}
	if cd.chunkOffsets[2] != uint(len(data1)+len(data2)) {
		t.Fatalf("unexpected chunkOffsets[2]")
	}
}

func TestCompoundDictionary_AttachOverflow(t *testing.T) {
	var cd compoundDictionary
	for i := range maxCompoundDicts {
		data := []byte{byte(i), 1, 2, 3, 4, 5, 6, 7, 8}
		if err := cd.attach(newPreparedDictionary(data)); err != nil {
			t.Fatalf("attach %d: %v", i, err)
		}
	}
	if err := cd.attach(newPreparedDictionary([]byte("overflow"))); !errors.Is(err, errTooManyDicts) {
		t.Fatalf("expected errTooManyDicts, got %v", err)
	}
}

func TestPrepareDictionaryEmpty(t *testing.T) {
	if _, err := PrepareDictionary(nil); !errors.Is(err, errEmptyDict) {
		t.Fatalf("expected errEmptyDict for nil, got %v", err)
	}
	if _, err := PrepareDictionary([]byte{}); !errors.Is(err, errEmptyDict) {
		t.Fatalf("expected errEmptyDict for empty, got %v", err)
	}
}

func TestDecodeStateAttachCompoundDictOverflow(t *testing.T) {
	var s decodeState
	for i := range 15 {
		if err := s.attachCompoundDict([]byte{byte(i + 1)}); err != nil {
			t.Fatalf("attach %d: %v", i, err)
		}
	}
	if err := s.attachCompoundDict([]byte("overflow")); !errors.Is(err, errTooManyDicts) {
		t.Fatalf("expected errTooManyDicts, got %v", err)
	}
}

func TestDecodeStateAttachCompoundDictEmpty(t *testing.T) {
	var s decodeState
	if err := s.attachCompoundDict(nil); !errors.Is(err, errEmptyDict) {
		t.Fatalf("expected errEmptyDict for nil, got %v", err)
	}
	if err := s.attachCompoundDict([]byte{}); !errors.Is(err, errEmptyDict) {
		t.Fatalf("expected errEmptyDict for empty, got %v", err)
	}
}

func TestFindCompoundDictionaryMatch_HashChainHit(t *testing.T) {
	// Place a known 8+ byte pattern in the dict, then the same pattern in the ring buffer.
	pattern := []byte("ABCDEFGHIJKLMNOP")
	// Dict: padding + pattern (enough bytes for hash table building).
	dictData := make([]byte, 128)
	copy(dictData[64:], pattern)

	pd := newPreparedDictionary(dictData)

	// Ring buffer: same pattern.
	ringSize := 1024
	ring := make([]byte, ringSize)
	copy(ring[256:], pattern)
	mask := uint(ringSize - 1)

	// Position in ring buffer where the pattern starts.
	cur := uint(256)
	maxLength := uint(len(pattern))
	// distanceOffset: for a single-chunk compound dict, this is
	// maxRingBufferDistance + 1 + totalSize - 1 - chunkOffset[0].
	// Simplify: we set distanceOffset = cur + len(dictData) to place the dict
	// just beyond the ring buffer.
	distanceOffset := cur + uint(len(dictData))

	distCache := [4]uint{1, 2, 3, 4}
	var sr hasherSearchResult
	sr.score = minScore

	pd.findCompoundMatch(ring, mask,
		&distCache, cur, maxLength, distanceOffset, &sr)

	if sr.score <= minScore {
		t.Fatal("expected a match but got minScore")
	}
	if sr.len < 4 {
		t.Fatalf("expected match length >= 4, got %d", sr.len)
	}
	// Verify the distance maps back to the pattern position in the dictionary.
	offset := distanceOffset - sr.distance
	if offset != 64 {
		t.Fatalf("expected offset 64, got %d", offset)
	}
}

func TestFindCompoundDictionaryMatch_CacheHit(t *testing.T) {
	// Place a pattern in the dict and set up a distance cache entry that points to it.
	dictData := make([]byte, 128)
	pattern := []byte("XYZWXYZW12345678")
	copy(dictData[32:], pattern)

	pd := newPreparedDictionary(dictData)

	ringSize := 1024
	ring := make([]byte, ringSize)
	copy(ring[256:], pattern)
	mask := uint(ringSize - 1)

	cur := uint(256)
	maxLength := uint(len(pattern))
	distanceOffset := cur + uint(len(dictData))

	// Set distance cache[0] to point exactly at dictData[32].
	cacheDistance := distanceOffset - 32
	distCache := [4]uint{cacheDistance, 1, 2, 3}
	var sr hasherSearchResult
	sr.score = minScore

	pd.findCompoundMatch(ring, mask,
		&distCache, cur, maxLength, distanceOffset, &sr)

	if sr.score <= minScore {
		t.Fatal("expected a cache hit match")
	}
	if sr.len < 2 {
		t.Fatalf("expected match length >= 2 for cache hit, got %d", sr.len)
	}
	if sr.distance != cacheDistance {
		t.Fatalf("expected distance %d, got %d", cacheDistance, sr.distance)
	}
}

func TestFindCompoundDictionaryMatch_NoMatch(t *testing.T) {
	dictData := make([]byte, 128)
	for i := range dictData {
		dictData[i] = byte(i)
	}
	pd := newPreparedDictionary(dictData)

	ringSize := 1024
	ring := make([]byte, ringSize)
	// Fill ring with 0xFF — nothing matches dictionary.
	for i := range ring {
		ring[i] = 0xFF
	}
	// Need at least 8 bytes at cur for hash computation.
	binary.LittleEndian.PutUint64(ring[256:], 0xDEADBEEFCAFEBABE)
	mask := uint(ringSize - 1)

	cur := uint(256)
	maxLength := uint(64)
	distanceOffset := cur + uint(len(dictData))

	distCache := [4]uint{1, 2, 3, 4}
	var sr hasherSearchResult
	sr.score = minScore

	pd.findCompoundMatch(ring, mask,
		&distCache, cur, maxLength, distanceOffset, &sr)

	if sr.score != minScore {
		t.Fatal("expected no match")
	}
}

func TestLookupCompoundDictionaryMatch_SingleChunk(t *testing.T) {
	pattern := []byte("REPEATEDPATTERN!")
	dictData := make([]byte, 128)
	copy(dictData[48:], pattern)

	var cd compoundDictionary
	_ = cd.attach(newPreparedDictionary(dictData))

	ringSize := 1024
	ring := make([]byte, ringSize)
	copy(ring[256:], pattern)
	mask := uint(ringSize - 1)

	cur := uint(256)
	maxLength := uint(len(pattern))
	maxRingBufferDistance := cur

	distCache := [4]uint{1, 2, 3, 4}
	var sr hasherSearchResult
	sr.score = minScore

	cd.lookupMatch(ring, mask,
		&distCache, cur, maxLength, maxRingBufferDistance, &sr)

	if sr.score <= minScore {
		t.Fatal("expected a match from lookupMatch")
	}
	if sr.len < 4 {
		t.Fatalf("expected match length >= 4, got %d", sr.len)
	}
}

func TestLookupCompoundDictionaryMatch_MultiChunk(t *testing.T) {
	pattern := []byte("MULTICHUNKMATCH!")
	dict1 := make([]byte, 128)
	// First dict does NOT contain the pattern.
	for i := range dict1 {
		dict1[i] = byte(i)
	}
	dict2 := make([]byte, 128)
	// Second dict contains the pattern.
	copy(dict2[64:], pattern)

	var cd compoundDictionary
	_ = cd.attach(newPreparedDictionary(dict1))
	_ = cd.attach(newPreparedDictionary(dict2))

	ringSize := 1024
	ring := make([]byte, ringSize)
	copy(ring[256:], pattern)
	mask := uint(ringSize - 1)

	cur := uint(256)
	maxLength := uint(len(pattern))
	maxRingBufferDistance := cur

	distCache := [4]uint{1, 2, 3, 4}
	var sr hasherSearchResult
	sr.score = minScore

	cd.lookupMatch(ring, mask,
		&distCache, cur, maxLength, maxRingBufferDistance, &sr)

	if sr.score <= minScore {
		t.Fatal("expected a match from second chunk")
	}
	if sr.len < 4 {
		t.Fatalf("expected match length >= 4, got %d", sr.len)
	}
}
