// Runtime heap padding for benchmark alignment perturbation.
package brrr

import (
	"os"
	"runtime"
	"strconv"
)

var benchHeapPadSink []byte

func init() {
	n, _ := strconv.Atoi(os.Getenv("BENCH_HEAP_PAD"))
	if n <= 0 {
		return
	}

	benchHeapPadSink = make([]byte, n)

	for i := 0; i < len(benchHeapPadSink); i += 64 {
		benchHeapPadSink[i] = byte(i)
	}

	runtime.KeepAlive(benchHeapPadSink)
}
