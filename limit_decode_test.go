package brrr

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"math/rand/v2"
	"testing"
)

// decodeLimitStream returns a stream with size decoded bytes.
func decodeLimitStream(t *testing.T, size int) (comp, raw []byte) {
	t.Helper()
	raw = make([]byte, size)
	for i := range raw {
		raw[i] = byte(i*7 + 1)
	}
	comp, err := Compress(raw, 5)
	if err != nil {
		t.Fatal(err)
	}
	return comp, raw
}

func TestReaderMaxDecodedBytesRejectsBomb(t *testing.T) {
	payload, err := hex.DecodeString("200010414141fcffff07002228f00b60f7f41f000000")
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewReaderOptions(bytes.NewReader(payload), ReaderOptions{MaxDecodedBytes: 1 << 10})
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, r)
	if !errors.Is(err, ErrDecodedSizeLimit) {
		t.Fatalf("err = %v; want ErrDecodedSizeLimit", err)
	}
	if n != 0 {
		t.Fatalf("delivered %d bytes; want 0", n)
	}
}

func TestReaderMaxDecodedBytesExactLimit(t *testing.T) {
	const size = 50000
	comp, raw := decodeLimitStream(t, size)
	r, err := NewReaderOptions(bytes.NewReader(comp), ReaderOptions{MaxDecodedBytes: size})
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatal("decoded bytes differ from the input")
	}
}

func TestReaderMaxDecodedBytesOneByteOver(t *testing.T) {
	const size = 50000
	comp, _ := decodeLimitStream(t, size)
	r, err := NewReaderOptions(bytes.NewReader(comp), ReaderOptions{MaxDecodedBytes: size - 1})
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, r)
	if !errors.Is(err, ErrDecodedSizeLimit) {
		t.Fatalf("err = %v; want ErrDecodedSizeLimit", err)
	}
	if n > size-1 {
		t.Fatalf("delivered %d bytes; want <= %d", n, size-1)
	}
}

// Flush ends the first meta-block. The limit covers both blocks.
func TestReaderMaxDecodedBytesMultipleBlocks(t *testing.T) {
	first := bytes.Repeat([]byte("a"), 4096)
	second := bytes.Repeat([]byte("b"), 4096)
	var comp bytes.Buffer
	w, err := NewWriter(&comp, 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(first); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(second); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	limit := uint64(len(first) + len(second) - 1)
	r, err := NewReaderOptions(bytes.NewReader(comp.Bytes()), ReaderOptions{MaxDecodedBytes: limit})
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, r)
	if !errors.Is(err, ErrDecodedSizeLimit) {
		t.Fatalf("err = %v; want ErrDecodedSizeLimit", err)
	}
	if n >= int64(limit) {
		t.Fatalf("delivered %d bytes; want less than %d", n, limit)
	}
}

// The header check can reject an overrun before serve sees it. Test both
// internal output sources here.
func TestReaderServeCapsOutBuffer(t *testing.T) {
	src := make([]byte, 100)
	for i := range src {
		src[i] = byte(i)
	}
	r := &Reader{out: src, maxDecodedBytes: 40}

	p := make([]byte, 100)
	n := r.serve(p)
	if n != 40 {
		t.Fatalf("first serve = %d; want 40", n)
	}
	if !bytes.Equal(p[:n], src[:40]) {
		t.Fatal("served bytes differ from r.out")
	}
	if n, err := r.Read(p); n != 0 || !errors.Is(err, ErrDecodedSizeLimit) {
		t.Fatalf("Read = %d, %v; want 0, ErrDecodedSizeLimit", n, err)
	}
}

func TestReaderServeCapsRingBuffer(t *testing.T) {
	ring := make([]byte, 100)
	for i := range ring {
		ring[i] = byte(i)
	}
	r := &Reader{maxDecodedBytes: 40, started: true}
	r.state.ringbuffer = ring
	r.state.ringbufferSize = len(ring)
	r.state.pos = len(ring)

	p := make([]byte, 100)
	n := r.serve(p)
	if n != 40 {
		t.Fatalf("first serve = %d; want 40", n)
	}
	if !bytes.Equal(p[:n], ring[:40]) {
		t.Fatal("served bytes differ from the ring buffer")
	}
	if n, err := r.Read(p); n != 0 || !errors.Is(err, ErrDecodedSizeLimit) {
		t.Fatalf("Read = %d, %v; want 0, ErrDecodedSizeLimit", n, err)
	}
}

// A source error must not replace a limit error from serve.
func TestReaderReadKeepsLimitErrorFromDecodeLoop(t *testing.T) {
	raw := make([]byte, 200000)
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range raw {
		raw[i] = byte(rng.Uint32())
	}
	comp, err := Compress(raw, 5)
	if err != nil {
		t.Fatal(err)
	}
	truncated := comp[:len(comp)/2]
	r, err := NewReaderOptions(bytes.NewReader(truncated), ReaderOptions{MaxDecodedBytes: 1 << 40})
	if err != nil {
		t.Fatal(err)
	}
	p := make([]byte, 1<<20)
	if n, err := r.Read(p); n == 0 || err != nil {
		t.Fatalf("first Read = %d, %v; want bytes and no error", n, err)
	}
	// Empty both output buffers before the next Read.
	for r.outPos < len(r.out) || len(r.state.pendingOutput()) > 0 {
		if _, err := r.Read(p); err != nil {
			t.Fatal(err)
		}
	}
	// Force the serve cap after the header check.
	r.state.maxDecodedBytes = 0
	r.maxDecodedBytes = r.delivered

	n, err := r.Read(p)
	if n != 0 || !errors.Is(err, ErrDecodedSizeLimit) {
		t.Fatalf("Read = %d, %v; want 0, ErrDecodedSizeLimit", n, err)
	}
	if _, err := r.Read(p); !errors.Is(err, ErrDecodedSizeLimit) {
		t.Fatalf("second Read err = %v; want ErrDecodedSizeLimit", err)
	}
}

func TestReaderMaxDecodedBytesStickyError(t *testing.T) {
	const size = 50000
	comp, _ := decodeLimitStream(t, size)
	r, err := NewReaderOptions(bytes.NewReader(comp), ReaderOptions{MaxDecodedBytes: size - 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, r); !errors.Is(err, ErrDecodedSizeLimit) {
		t.Fatalf("first err = %v; want ErrDecodedSizeLimit", err)
	}
	if _, err := r.Read(make([]byte, 16)); !errors.Is(err, ErrDecodedSizeLimit) {
		t.Fatalf("second err = %v; want ErrDecodedSizeLimit", err)
	}
}

func TestReaderMaxDecodedBytesPreservedAcrossReset(t *testing.T) {
	const size = 50000
	compSmall, rawSmall := decodeLimitStream(t, size)
	compExact, rawExact := decodeLimitStream(t, 2*size)
	compBig, _ := decodeLimitStream(t, 3*size)

	r, err := NewReaderOptions(bytes.NewReader(compSmall), ReaderOptions{MaxDecodedBytes: 2 * size})
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("first stream err = %v; want nil", err)
	}
	if !bytes.Equal(out, rawSmall) {
		t.Fatal("first stream decoded bytes differ")
	}

	r.Reset(bytes.NewReader(compExact))
	out, err = io.ReadAll(r)
	if err != nil {
		t.Fatalf("second stream err = %v; want nil", err)
	}
	if !bytes.Equal(out, rawExact) {
		t.Fatal("second stream decoded bytes differ")
	}

	r.Reset(bytes.NewReader(compBig))
	n, err := io.Copy(io.Discard, r)
	if !errors.Is(err, ErrDecodedSizeLimit) {
		t.Fatalf("third stream err = %v; want ErrDecodedSizeLimit", err)
	}
	if n > 2*size {
		t.Fatalf("third stream delivered %d bytes; want <= %d", n, 2*size)
	}
}

func TestReaderUnlimitedByDefault(t *testing.T) {
	const size = 200000
	comp, raw := decodeLimitStream(t, size)
	r, err := NewReaderOptions(bytes.NewReader(comp), ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatal("decoded bytes differ from the input")
	}
}
