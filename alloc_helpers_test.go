package brrr

import (
	"bytes"
	"os"
	"runtime/debug"
	"testing"
)

func allocTestPayload(tb testing.TB) []byte {
	tb.Helper()
	page, err := os.ReadFile("testdata/gh_172KB.html")
	if err != nil {
		tb.Fatal(err)
	}
	return bytes.Repeat(page, 3)
}

func allocsBetweenCollections(runs int, f func()) float64 {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	return testing.AllocsPerRun(runs, f)
}
