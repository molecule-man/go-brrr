package encoder

import "testing"

func refineEntropyCodesScratchReference(
	data []uint16, histograms []uint32,
	length, stride, numHistograms, alphabetSize int,
) {
	iters := iterMulForRefining*length/stride + minItersForRefining
	seed := uint32(7)
	iters = ((iters + numHistograms - 1) / numHistograms) * numHistograms

	tmp := histograms[numHistograms*alphabetSize : (numHistograms+1)*alphabetSize]
	for iter := range iters {
		clear(tmp)
		randomSample(data, tmp, &seed, length, stride, alphabetSize)
		hist := histograms[(iter%numHistograms)*alphabetSize:]
		for j := range alphabetSize {
			hist[j] += tmp[j]
		}
	}
}

func refineFixture(tb testing.TB, length, numHistograms, alphabetSize int) ([]uint16, []uint32) {
	tb.Helper()
	data := make([]uint16, length)
	for i := range data {
		data[i] = uint16((i * 2654435761) % alphabetSize)
	}
	return data, make([]uint32, (numHistograms+1)*alphabetSize)
}

func TestRefineEntropyCodesMatchesScratchReference(t *testing.T) {
	for _, alphabetSize := range []int{256, 704} {
		for _, numHistograms := range []int{1, 4, 16} {
			const length, stride = 40000, 70
			data, got := refineFixture(t, length, numHistograms, alphabetSize)
			_, want := refineFixture(t, length, numHistograms, alphabetSize)

			refineEntropyCodes(data, got, length, stride, numHistograms, alphabetSize)
			refineEntropyCodesScratchReference(data, want, length, stride, numHistograms, alphabetSize)

			for i := range numHistograms * alphabetSize {
				if got[i] != want[i] {
					t.Fatalf("alphabetSize=%d numHistograms=%d index %d: got %d, scratch "+
						"reference gives %d; refined histograms drive block splitting, so any "+
						"difference changes the compressed output",
						alphabetSize, numHistograms, i, got[i], want[i])
				}
			}
		}
	}
}

func BenchmarkRefineEntropyCodes(b *testing.B) {
	const length, stride, numHistograms = 40000, 70, 16
	for _, alphabetSize := range []int{256, 704} {
		name := "alphabet=256"
		if alphabetSize == 704 {
			name = "alphabet=704"
		}
		b.Run(name+"/impl=scratch_histogram", func(b *testing.B) {
			data, h := refineFixture(b, length, numHistograms, alphabetSize)
			b.ReportAllocs()
			for range b.N {
				refineEntropyCodesScratchReference(data, h, length, stride, numHistograms, alphabetSize)
			}
		})
		b.Run(name+"/impl=direct_sample", func(b *testing.B) {
			data, h := refineFixture(b, length, numHistograms, alphabetSize)
			b.ReportAllocs()
			for range b.N {
				refineEntropyCodes(data, h, length, stride, numHistograms, alphabetSize)
			}
		})
	}
}
