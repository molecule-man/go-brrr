package encoder

import (
	"bytes"
	"os"
	"testing"
	"time"
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

func waitInBackground(f *matchFeed, i uint) <-chan bool {
	res := make(chan bool, 1)
	go func() { res <- f.wait(i) }()
	return res
}

func waitUntilParked(t *testing.T, f *matchFeed) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !f.parked.Load() {
		if time.Now().After(deadline) {
			t.Fatal("consumer did not park")
		}
		time.Sleep(time.Millisecond)
	}
}

func receiveWait(t *testing.T, res <-chan bool) bool {
	t.Helper()
	select {
	case ok := <-res:
		return ok
	case <-time.After(5 * time.Second):
		t.Fatal("consumer did not wake")
		return false
	}
}

func TestMatchFeedParkedConsumerWakesOnPublish(t *testing.T) {
	var f matchFeed
	f.reset()
	res := waitInBackground(&f, 5)
	waitUntilParked(t, &f)
	f.publish(6)
	if !receiveWait(t, res) {
		t.Fatal("wait(5) returned false after publish(6)")
	}
}

func TestMatchFeedParkedConsumerWakesOnAbort(t *testing.T) {
	var f matchFeed
	f.reset()
	res := waitInBackground(&f, 5)
	waitUntilParked(t, &f)
	f.abort()
	if receiveWait(t, res) {
		t.Fatal("wait(5) returned true after abort")
	}
}

func TestMatchFeedConsumerSeesEveryPublishWithoutLostWakeups(t *testing.T) {
	const n = 20000
	var f matchFeed
	for round := range 3 {
		f.reset()
		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := uint(1); i <= n; i++ {
				f.publish(i)
				if i%64 == 0 {
					time.Sleep(time.Microsecond)
				}
			}
		}()
		res := make(chan bool, 1)
		go func() {
			for i := range uint(n) {
				if !f.wait(i) {
					res <- false
					return
				}
			}
			res <- true
		}()
		if !receiveWait(t, res) {
			t.Fatalf("round %d: wait returned false without an abort", round)
		}
		<-done
	}
}
