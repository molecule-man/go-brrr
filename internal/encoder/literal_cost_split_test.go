package encoder

import (
	"os"
	"testing"
)

var literalCostSink float32

const (
	splitN    = 128 << 10
	splitHTML = "../../testdata/gh_172KB.html"
	splitJS   = "../../testdata/reactcore_187KB.js"
)

func literalCostRealData(tb testing.TB, path string) []byte {
	tb.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	if len(data) < splitN {
		tb.Fatalf("%s holds %d bytes, the benchmark name promises %d", path, len(data), splitN)
	}
	return data[:splitN]
}

func benchmarkEstimateDispatch(b *testing.B, path string) {
	data := literalCostRealData(b, path)
	histogram, cost := make([]uint, 3*256), make([]float32, splitN)
	b.ReportAllocs()
	b.SetBytes(int64(splitN))
	for range b.N {
		estimateBitCostsForLiterals(data, 0, uint(splitN), uint(splitN-1), histogram, cost)
	}
	literalCostSink = cost[0]
}

func BenchmarkEstimateBitCostsForLiteralsDispatchHtml128KiB(b *testing.B) {
	benchmarkEstimateDispatch(b, splitHTML)
}
func BenchmarkEstimateBitCostsForLiteralsDispatchJs128KiB(b *testing.B) {
	benchmarkEstimateDispatch(b, splitJS)
}
