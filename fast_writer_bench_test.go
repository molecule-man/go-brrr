package brrr

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkNewFastWriterPerStream(b *testing.B) {
	for _, path := range []string{
		filepath.Join("brotli-ref", "tests", "testdata", "plrabn12.txt"),
		filepath.Join("brotli-ref", "tests", "testdata", "mapsdatazrh"),
		filepath.Join("testdata", "gh_172KB.html"),
	} {
		in, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		for _, level := range []int{0, 1} {
			b.Run(fmt.Sprintf("%s/q=%d", filepath.Base(path), level), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(in)))
				for i := 0; i < b.N; i++ {
					w, err := NewWriterOptions(io.Discard, level, WriterOptions{})
					if err != nil {
						b.Fatal(err)
					}
					if _, err := w.Write(in); err != nil {
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
