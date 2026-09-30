package encoder

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkCreateHqZopfliBackwardReferencesParallel(b *testing.B) {
	for _, name := range []string{"plrabn12.txt", "lcet10.txt", "mapsdatazrh"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "brotli-ref", "tests", "testdata", name))
		if err != nil {
			b.Fatal(err)
		}
		ring := make([]byte, 2<<20)
		copy(ring, data)
		for _, quality := range []int{10, 11} {
			b.Run(fmt.Sprintf("%s/q%d", name, quality), func(b *testing.B) {
				bufs := &q10Bufs{parallel: true}
				b.Cleanup(bufs.hqCollector.stop)
				h := &h10{lgwin: 22, quality: quality, bufs: bufs}
				var commands []command
				var distCache [4]int
				var lastInsertLen, numLiterals uint
				run := func() {
					h.reset(true, uint(len(data)), nil)
					commands = commands[:0]
					distCache = [4]int{4, 11, 15, 16}
					lastInsertLen, numLiterals = 0, 0
					createHqZopfliBackwardReferences(uint(len(data)), 0, ring, uint(len(ring)-1), quality, 22, 0, nil,
						distCache[:], h, &lastInsertLen, &commands, &numLiterals, bufs)
				}
				run()
				b.ReportAllocs()
				b.SetBytes(int64(len(data)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					run()
				}
			})
		}
	}
}
