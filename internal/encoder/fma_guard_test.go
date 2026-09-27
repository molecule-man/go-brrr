package encoder

import "testing"

func TestGuardedProductsRoundTwiceLikeTheCReferenceEvenWhereTheCompilerFusesMultiplyAdd(t *testing.T) {
	split := &blockSplitter{histograms: []uint32{38, 20, 37, 25, 11, 38, 20, 37, 26, 11}, alphabetSize: 5}
	cases := []struct {
		fn        string
		got, want float64
	}{
		{"bitsEntropy{76,40,74,51,22}", bitsEntropy([]uint32{76, 40, 74, 51, 22}), 0x1.21cec2d21dcdp+09},
		{"combinedBitsEntropy{38,20,37,25,11}+{38,20,37,26,11}", split.combinedBitsEntropy(0, 5), 0x1.21cec2d21dcdp+09},
		{"estimateEntropy{260,382,382}", estimateEntropy([]uint32{260, 382, 382}), 0x1.9041d6f10bc9p+10},
		{"populationCost{37,128,14,89,43,127}", populationCost([]uint32{37, 128, 14, 89, 43, 127}, 6), 0x1.04b4e19f65708p+10},
		{"clusterCostDiff(257,255)", clusterCostDiff(257, 255), -0x1.fffe910ce17ep+08},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %x, C rounds the product before adding and gets %x; at GOAMD64=v3 and above the Go "+
				"compiler fuses x*y+z into one VFMADD rounding unless the product is wrapped in float64(), and "+
				"one ulp here is enough to flip shouldCompress, a block split or a cluster merge against C",
				c.fn, c.got, c.want)
		}
	}
}
