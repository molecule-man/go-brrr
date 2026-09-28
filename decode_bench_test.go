package brrr

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkDecompressReferenceFiles(b *testing.B) {
	for _, path := range []string{
		filepath.Join("brotli-ref", "tests", "testdata", "plrabn12.txt"),
		filepath.Join("brotli-ref", "tests", "testdata", "mapsdatazrh"),
		filepath.Join("testdata", "gh_172KB.html"),
		filepath.Join("testdata", "reactcore_187KB.js"),
	} {
		in, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		for _, level := range []int{0, 1, 2, 5, 9, 11} {
			compressed, err := Compress(in, level)
			if err != nil {
				b.Fatal(err)
			}
			b.Run(fmt.Sprintf("%s/q=%d", filepath.Base(path), level), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(in)))
				for i := 0; i < b.N; i++ {
					out, err := Decompress(compressed)
					if err != nil {
						b.Fatal(err)
					}
					if len(out) != len(in) {
						b.Fatalf("decoded %d bytes, want %d", len(out), len(in))
					}
				}
			})
		}
	}
}
