package brrr

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func BenchmarkColdQ10Q11WriterStreams(b *testing.B) {
	for _, name := range []string{"plrabn12.txt", "lcet10.txt", "mapsdatazrh"} {
		data, err := os.ReadFile(filepath.Join("brotli-ref", "tests", "testdata", name))
		if err != nil {
			b.Fatal(err)
		}
		for _, quality := range []int{10, 11} {
			for _, lgwin := range []int{22, 24} {
				b.Run(fmt.Sprintf("%s/q=%d/lgwin=%d", name, quality, lgwin), func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(data)))
					for i := 0; i < b.N; i++ {
						b.StopTimer()
						runtime.GC()
						runtime.GC()
						b.StartTimer()
						w, err := NewWriterOptions(io.Discard, quality, WriterOptions{LGWin: lgwin})
						if err != nil {
							b.Fatal(err)
						}
						if _, err := w.Write(data); err != nil {
							b.Fatal(err)
						}
						if err := w.Close(); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
