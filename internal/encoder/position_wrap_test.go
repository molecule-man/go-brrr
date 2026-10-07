package encoder

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/molecule-man/go-brrr/internal/creftest"
)

func TestParallelQ10AndQ11CompressAcrossThe32BitPositionWrap(t *testing.T) {
	data := pseudoRandomBytesRT(1<<20, 7)
	for _, quality := range []int{10, 11} {
		t.Run(fmt.Sprintf("q%d", quality), func(t *testing.T) {
			c := NewCompressor(quality, 18, 0, true)
			defer c.Release()
			if !SeedStreamPosForTest(c, (1<<32)-(512<<10)) {
				t.Fatalf("unexpected compressor type %T", c)
			}
			var out bytes.Buffer
			if _, err := c.Write(&out, data); err != nil {
				t.Fatal(err)
			}
			if err := c.Close(&out); err != nil {
				t.Fatal(err)
			}
			if got := creftest.BrotliDecompress(t, out.Bytes()); !bytes.Equal(got, data) {
				t.Fatalf("roundtrip mismatch: decoded %d bytes, want %d", len(got), len(data))
			}
		})
	}
}
