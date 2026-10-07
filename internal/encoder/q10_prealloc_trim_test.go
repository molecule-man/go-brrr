package encoder

import (
	"bytes"
	"errors"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"testing"
)

func coldQ10StreamBytes(tb testing.TB, in []byte, quality int) uint64 {
	tb.Helper()
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	runtime.GC()
	runtime.GC()
	var start, end runtime.MemStats
	runtime.ReadMemStats(&start)
	e := new(encoderSplit)
	e.reset(quality, 22, uint(len(in)))
	_, writeErr := e.Write(io.Discard, in)
	closeErr := e.Close(io.Discard)
	runtime.ReadMemStats(&end)
	e.releaseBuffers()
	if err := errors.Join(writeErr, closeErr); err != nil {
		tb.Fatal(err)
	}
	return end.TotalAlloc - start.TotalAlloc
}

func TestColdQ10AndQ11EncodersDoNotAllocateTheWorstCaseMetablockScratchUpFront(t *testing.T) {
	in, err := os.ReadFile("../../testdata/github_events_8k.json")
	if err != nil {
		t.Fatal(err)
	}
	// Measured 36.0 MiB. Preallocation for 256 block types x 64 contexts takes 101.8 MiB.
	const maxBytes = 40 << 20
	for _, quality := range []int{10, 11} {
		if got := coldQ10StreamBytes(t, in, quality); got > maxBytes {
			t.Errorf("a cold q%d encoder compressing %d bytes at lgwin 22 allocated %d bytes, want at most %d; "+
				"the metablock histograms, switch signal, entropy-code tables and zopfli nodes must grow to "+
				"what the stream needs", quality, len(in), got, maxBytes)
		}
	}
}

func TestH10ForestAndQ10SnapshotFollowTheInputSeenSoFarInsteadOfReservingTheWholeWindow(t *testing.T) {
	html, err := os.ReadFile("../../testdata/gh_172KB.html")
	if err != nil {
		t.Fatal(err)
	}
	js, err := os.ReadFile("../../testdata/reactcore_187KB.js")
	if err != nil {
		t.Fatal(err)
	}
	in := bytes.Repeat(append(append([]byte(nil), html...), js...), 3)
	const lgwin = 22

	e := NewCompressor(10, lgwin, 0, true).(*encoderSplit)
	defer e.Release()
	// Fresh hasher: a pooled one can carry a forest from an earlier stream.
	releaseHasher(e.hasher)
	h := &h10{lgwin: lgwin, quality: 10, bufs: &e.q10}
	e.hasher = h
	e.resetHasher()
	e.q10.hqHasherSnap = nil
	if _, err := e.Write(io.Discard, in); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(io.Discard); err != nil {
		t.Fatal(err)
	}

	if forest := len(h.forest); forest < 2*len(in) || forest > 4*len(in) {
		t.Errorf("a %d-byte q10 stream at lgwin %d left a forest of %d entries; it needs 2 per byte seen and "+
			"may round up to twice that, but must not reserve the %d entries of the whole window",
			len(in), lgwin, forest, 2<<lgwin)
	}
	if snap := cap(e.q10.hqHasherSnap); snap >= 2<<lgwin {
		t.Errorf("the q10 hasher snapshot has capacity %d; it copies the forest in use plus the buckets, so it "+
			"must follow the grown forest instead of the %d-entry window", snap, 2<<lgwin)
	}
}
