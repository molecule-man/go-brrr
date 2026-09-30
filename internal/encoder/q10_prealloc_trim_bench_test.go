package encoder

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkColdQ10AndQ11EncoderStreamAtLGWin22(b *testing.B) {
	for _, name := range []string{"plrabn12.txt", "lcet10.txt", "mapsdatazrh"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "brotli-ref", "tests", "testdata", name))
		if err != nil {
			b.Fatal(err)
		}
		for _, prefix := range []struct {
			name string
			n    int
		}{{"10KiB", 10 << 10}, {"256KiB", 256 << 10}} {
			in := data[:prefix.n]
			for _, quality := range []int{10, 11} {
				b.Run(fmt.Sprintf("%s/prefix=%s/q=%d", name, prefix.name, quality), func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(in)))
					for i := 0; i < b.N; i++ {
						e := new(encoderSplit)
						e.reset(quality, 22, uint(len(in)))
						if _, err := e.Write(io.Discard, in); err != nil {
							b.Fatal(err)
						}
						if err := e.Close(io.Discard); err != nil {
							b.Fatal(err)
						}
						e.releaseBuffers()
					}
				})
			}
		}
	}
}
