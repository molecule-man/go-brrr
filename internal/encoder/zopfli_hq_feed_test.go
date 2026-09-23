package encoder

import (
	"bytes"
	"os"
	"testing"
)

func encodeQ11ForFeedTest(t *testing.T, in []byte, perByte uint, shrink bool) []byte {
	t.Helper()
	saved := hqMatchesPerByte
	hqMatchesPerByte = perByte
	t.Cleanup(func() { hqMatchesPerByte = saved })

	c := NewCompressor(11, 22, uint(len(in)), true).(*encoderSplit)
	if shrink {
		c.q10.hqMatches = make([]backwardMatch, 0, 16)
	}
	var out bytes.Buffer
	if _, err := c.Write(&out, in); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(&out); err != nil {
		t.Fatal(err)
	}
	c.Release()
	return out.Bytes()
}

func TestQ11FeedAbortAndRestartProducesTheSameBytesAsAnUninterruptedPass(t *testing.T) {
	in, err := os.ReadFile("../../testdata/github_events_8k.json")
	if err != nil {
		t.Fatal(err)
	}
	want := encodeQ11ForFeedTest(t, in, 8, false)
	got := encodeQ11ForFeedTest(t, in, 0, true)
	if !bytes.Equal(got, want) {
		t.Fatalf("a mid-collection reallocation must abort the feed and restart the first DP pass on the "+
			"reallocated buffer; the restarted pass produced %d bytes that differ from the %d-byte "+
			"uninterrupted result, so the consumer read a stale or partial match buffer",
			len(got), len(want))
	}
}
