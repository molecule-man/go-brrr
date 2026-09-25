package encoder

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/molecule-man/go-brrr/internal/core"
)

var fmaGuardSink float64

func (s *onePassArena) shouldMergeBlockFMABefore(data []byte, length int, depths []byte) bool {
	const sampleRate = 43
	histogram := &s.histogram
	*histogram = [256]uint32{}
	for i := 0; i < length; i += sampleRate {
		histogram[data[i]]++
	}

	total := (length + sampleRate - 1) / sampleRate
	r := (fastLog2(total)+0.5)*float64(total) + 200
	for i := range 256 {
		r -= float64(histogram[i]) * (float64(depths[i]) + fastLog2(int(histogram[i])))
	}
	return r >= 0.0
}

func (bs *blockSplitter) combinedBitsEntropyFMABefore(aStart, bStart int) float64 {
	a := bs.histograms[aStart : aStart+bs.alphabetSize]
	b := bs.histograms[bStart : bStart+bs.alphabetSize]

	var sum int
	var retval float64
	for i, va := range a {
		p := int(va) + int(b[i])
		sum += p
		retval -= float64(p) * fastLog2(p)
	}
	if sum != 0 {
		retval += float64(sum) * fastLog2(sum)
	}
	if retval < float64(sum) {
		retval = float64(sum)
	}
	return retval
}

func findBlocksFMABefore(
	data []uint16,
	histograms []uint32,
	insertCost []float64,
	cost []float64,
	switchSignal []byte,
	blockID []byte,
	length, numHistograms, alphabetSize int,
	blockSwitchBitcost float64,
) int {
	bitmapLen := (numHistograms + 7) >> 3
	numBlocks := 1

	if numHistograms <= 1 {
		clear(blockID[:length])
		return 1
	}

	for j := range numHistograms {
		hist := histograms[j*alphabetSize:]
		var totalCount uint32
		for i := range alphabetSize {
			totalCount += hist[i]
		}
		insertCost[j] = fastLog2(int(totalCount))
	}
	for i := alphabetSize - 1; i >= 0; i-- {
		for j := range numHistograms {
			insertCost[i*numHistograms+j] =
				insertCost[j] - symbolBitCost(int(histograms[j*alphabetSize+i]))
		}
	}

	clear(cost[:numHistograms])
	clear(switchSignal[:length*bitmapLen])

	const prologueLength = 2000
	const prologueMultiplier = 0.07 / 2000

	for byteIx := range length {
		ix := byteIx * bitmapLen
		symbol := int(data[byteIx])
		insertCostIx := symbol * numHistograms
		switchCost := blockSwitchBitcost

		if numHistograms < minHistogramsForKernel {
			minCost := noMinCost
			for k := range numHistograms {
				cost[k] += insertCost[insertCostIx+k]
				if cost[k] < minCost {
					minCost = cost[k]
					blockID[byteIx] = byte(k)
				}
			}

			if byteIx < prologueLength {
				switchCost *= 0.77 + prologueMultiplier*float64(byteIx)
			}

			for k := range numHistograms {
				cost[k] -= minCost
				if cost[k] >= switchCost {
					cost[k] = switchCost
					switchSignal[ix+(k>>3)] |= 1 << (k & 7)
				}
			}

			continue
		}

		if byteIx < prologueLength {
			switchCost *= 0.77 + prologueMultiplier*float64(byteIx)
		}

		minCost, best := findBlocksStep(
			cost[:numHistograms],
			insertCost[insertCostIx:insertCostIx+numHistograms],
			switchSignal[ix:], switchCost)
		if minCost < noMinCost {
			blockID[byteIx] = byte(best)
		}
	}

	byteIx := length - 1
	ix := byteIx * bitmapLen
	curID := blockID[byteIx]
	for byteIx > 0 {
		mask := byte(1 << (curID & 7))
		byteIx--
		ix -= bitmapLen
		if switchSignal[ix+int(curID>>3)]&mask != 0 {
			if curID != blockID[byteIx] {
				curID = blockID[byteIx]
				numBlocks++
			}
		}
		blockID[byteIx] = curID
	}

	return numBlocks
}

func clusterCostDiffFMABefore(sizeA, sizeB uint32) float64 {
	sizeC := sizeA + sizeB
	return float64(sizeA)*fastLog2(int(sizeA)) +
		float64(sizeB)*fastLog2(int(sizeB)) -
		float64(sizeC)*fastLog2(int(sizeC))
}

func estimateEntropyFMABefore(population []uint32) float64 {
	var total uint32
	var result float64
	for _, p := range population {
		total += p
		result += float64(p) * fastLog2(int(p))
	}
	return float64(total)*fastLog2(int(total)) - result
}

func bitsEntropyFMABefore(population []uint32) float64 {
	var sum int
	var retval float64
	for _, p := range population {
		sum += int(p)
		retval -= float64(p) * fastLog2(int(p))
	}
	if sum != 0 {
		retval += float64(sum) * fastLog2(sum)
	}
	if retval < float64(sum) {
		retval = float64(sum)
	}
	return retval
}

func estimateBitCostsForLiteralsUTF8FMABefore(data []byte, pos, length, mask uint, histogram []uint, cost []float32) {
	maxUTF8 := decideMultiByteStatsLevel(data, pos, length, mask)
	windowHalf := uint(495)
	inWindow := min(windowHalf, length)
	var inWindowUTF8 [3]uint

	clear(histogram[:3*256])

	lastC := uint(0)
	utf8Pos := uint(0)
	for i := range inWindow {
		c := uint(data[(pos+i)&mask])
		histogram[256*utf8Pos+c]++
		inWindowUTF8[utf8Pos]++
		utf8Pos = utf8Position(lastC, c, maxUTF8)
		lastC = c
	}

	var cachedUTF8Count [3]uint
	var cachedUTF8Log [3]float64
	for k := range cachedUTF8Count {
		cachedUTF8Count[k] = inWindowUTF8[k]
		cachedUTF8Log[k] = fastLog2(int(inWindowUTF8[k]))
	}

	var addPrev1, addPrev2 uint
	if windowHalf < length {
		addPrev1 = uint(data[(pos+windowHalf-1)&mask])
		addPrev2 = uint(data[(pos+windowHalf-2)&mask])
	}
	var remPrev1, remPrev2 uint
	var curPrev1, curPrev2 uint

	for i := range length {
		if i >= windowHalf {
			utf8Pos2 := utf8Position(remPrev2, remPrev1, maxUTF8)
			gone := uint(data[(pos+i-windowHalf)&mask])
			histogram[256*utf8Pos2+gone]--
			inWindowUTF8[utf8Pos2]--
			remPrev2, remPrev1 = remPrev1, gone
		}
		if i+windowHalf < length {
			utf8Pos2 := utf8Position(addPrev2, addPrev1, maxUTF8)
			added := uint(data[(pos+i+windowHalf)&mask])
			histogram[256*utf8Pos2+added]++
			inWindowUTF8[utf8Pos2]++
			addPrev2, addPrev1 = addPrev1, added
		}

		curUTF8Pos := utf8Position(curPrev2, curPrev1, maxUTF8)
		maskedPos := (pos + i) & mask
		cur := uint(data[maskedPos])
		curPrev2, curPrev1 = curPrev1, cur
		histo := histogram[256*curUTF8Pos+cur]
		if cachedUTF8Count[curUTF8Pos] != inWindowUTF8[curUTF8Pos] {
			cachedUTF8Count[curUTF8Pos] = inWindowUTF8[curUTF8Pos]
			cachedUTF8Log[curUTF8Pos] = fastLog2(int(inWindowUTF8[curUTF8Pos]))
		}
		litCost := cachedUTF8Log[curUTF8Pos] - fastLog2(int(histo))
		litCost += 0.02905
		if litCost < 1.0 {
			litCost = litCost*0.5 + 0.5
		}
		const prologueLength = 2000
		const multiplier = 0.35 / prologueLength
		if i < prologueLength {
			litCost += 0.35 + multiplier*float64(i)
		}
		cost[i] = float32(litCost)
	}
}

func populationCostFMABefore(histogram []uint32, dataSize int) float64 {
	const (
		oneSymbolHistogramCost   = 12
		twoSymbolHistogramCost   = 20
		threeSymbolHistogramCost = 28
		fourSymbolHistogramCost  = 37
	)

	var totalCount uint32
	for i := range dataSize {
		totalCount += histogram[i]
	}
	if totalCount == 0 {
		return oneSymbolHistogramCost
	}

	var count int
	var s [5]int
	for i := range dataSize {
		if histogram[i] > 0 {
			s[count] = i
			count++
			if count > 4 {
				break
			}
		}
	}

	if count == 1 {
		return oneSymbolHistogramCost
	}
	if count == 2 {
		return twoSymbolHistogramCost + float64(totalCount)
	}
	if count == 3 {
		histo0 := histogram[s[0]]
		histo1 := histogram[s[1]]
		histo2 := histogram[s[2]]
		histoMax := max(histo0, max(histo1, histo2))
		return threeSymbolHistogramCost +
			float64(2*(histo0+histo1+histo2)-histoMax)
	}
	if count == 4 {
		histo := [4]uint32{histogram[s[0]], histogram[s[1]], histogram[s[2]], histogram[s[3]]}
		for i := range 4 {
			for j := i + 1; j < 4; j++ {
				if histo[j] > histo[i] {
					histo[j], histo[i] = histo[i], histo[j]
				}
			}
		}
		h23 := histo[2] + histo[3]
		histoMax := max(h23, histo[0])
		return fourSymbolHistogramCost +
			float64(3*h23+2*(histo[0]+histo[1])-histoMax)
	}

	var bits float64
	maxDepth := 1
	var depthHisto [core.AlphabetSizeCodeLengths]uint32
	log2total := fastLog2(int(totalCount))

	for i := 0; i < dataSize; {
		if histogram[i] > 0 {
			log2p := log2total - fastLog2(int(histogram[i]))
			depth := int(log2p + 0.5)
			bits += float64(histogram[i]) * log2p
			if depth > 15 {
				depth = 15
			}
			if depth > maxDepth {
				maxDepth = depth
			}
			depthHisto[depth]++
			i++
		} else {
			reps := uint32(1)
			for k := i + 1; k < dataSize && histogram[k] == 0; k++ {
				reps++
			}
			i += int(reps)
			if i == dataSize {
				break
			}
			if reps < 3 {
				depthHisto[0] += reps
			} else {
				reps -= 2
				for reps > 0 {
					depthHisto[alphabetSizeRepeatZeroCodeLength]++
					bits += 3
					reps >>= 3
				}
			}
		}
	}

	bits += float64(18 + 2*maxDepth)
	bits += bitsEntropyFMABefore(depthHisto[:])
	return bits
}

//go:noinline
func fusedMulAddProbe(x, y, z float64) float64 {
	return x*y + z
}

func compilerFusesMulAdd() bool {
	x := 1 + 0x1p-30
	return fusedMulAddProbe(x, x, -1) != float64(x*x)-1
}

type fmaGuardInput struct {
	name string
	data []byte
}

func fmaGuardInputs(tb testing.TB) []fmaGuardInput {
	tb.Helper()
	var paths []string
	for _, pattern := range []string{"../../testdata/*.*", "../../brotli-ref/tests/testdata/*.txt", "../../brotli-ref/tests/testdata/mapsdatazrh"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			tb.Fatal(err)
		}
		paths = append(paths, matches...)
	}
	var inputs []fmaGuardInput
	for _, path := range paths {
		if filepath.Ext(path) == ".sh" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			tb.Fatal(err)
		}
		inputs = append(inputs, fmaGuardInput{filepath.Base(path), data})
	}
	if len(inputs) == 0 {
		tb.Fatal("no real inputs found under ../../testdata")
	}
	return inputs
}

func fmaGuardHistograms(data []byte, size, off int) []uint32 {
	h := make([]uint32, 512)
	for _, c := range data[off : off+size] {
		h[c]++
	}
	for _, c := range data[len(data)-size-off : len(data)-off] {
		h[256+int(c)]++
	}
	return h
}

func fmaGuardFindBlocksFixture(data []byte, length, numHistograms int) ([]uint16, []uint32) {
	symbols := make([]uint16, length)
	histograms := make([]uint32, numHistograms*256)
	for i, c := range data[:length] {
		symbols[i] = uint16(c)
		histograms[(i*numHistograms/length)*256+int(c)]++
	}
	return symbols, histograms
}

func runFindBlocks(
	impl func([]uint16, []uint32, []float64, []float64, []byte, []byte, int, int, int, float64) int,
	symbols []uint16, histograms []uint32, length, numHistograms int, bitcost float64,
) (int, []byte) {
	insertCost := make([]float64, 256*numHistograms)
	cost := make([]float64, numHistograms)
	switchSignal := make([]byte, length*((numHistograms+7)>>3))
	blockID := make([]byte, length)
	n := impl(symbols, histograms, insertCost, cost, switchSignal, blockID, length, numHistograms, 256, bitcost)
	return n, blockID
}

func fmaGuardLiteralDepths(data []byte) []byte {
	var a onePassArena
	w := bitWriter{buf: make([]byte, 1<<12)}
	a.buildAndWriteLiteralPrefixCode(data[:min(len(data), 98304)], &w)
	return a.litDepth[:]
}

func TestProductRoundingGuardsLeaveEveryResultBitIdenticalWhereTheCompilerDoesNotFuseMultiplyAdd(t *testing.T) {
	if compilerFusesMulAdd() {
		t.Skip("this build fuses x*y+z into one rounding, so the FMABefore copies no longer round like C; " +
			"TestGuardedProductsRoundTwiceLikeTheCReferenceEvenWhereTheCompilerFusesMultiplyAdd covers such builds")
	}
	inputs := fmaGuardInputs(t)
	for _, in := range inputs {
		checkHistogramGuards(t, in)
		checkLiteralCostGuard(t, in)
		checkFindBlocksGuard(t, in)
		for _, depthsFrom := range inputs {
			checkShouldMergeBlockGuard(t, in, depthsFrom)
		}
	}
	for a := uint32(0); a < 3000; a += 13 {
		for b := uint32(0); b < 3000; b += 17 {
			if got, want := clusterCostDiff(a, b), clusterCostDiffFMABefore(a, b); got != want {
				t.Errorf("clusterCostDiff(%d, %d) = %x, the unguarded expression gives %x; "+
					"histogram clustering would merge different clusters than C", a, b, got, want)
			}
		}
	}
}

func checkHistogramGuards(t *testing.T, in fmaGuardInput) {
	t.Helper()
	for _, size := range []int{1, 100, 255, 256, 257, 1000, 4096, 65536} {
		if 2*size > len(in.data) {
			continue
		}
		for _, off := range []int{0, (len(in.data) - 2*size) / 2, len(in.data) - 2*size} {
			h := fmaGuardHistograms(in.data, size, off)
			bs := &blockSplitter{histograms: h, alphabetSize: 256}
			pairs := []struct {
				fn        string
				got, want float64
			}{
				{"bitsEntropy", bitsEntropy(h[:256]), bitsEntropyFMABefore(h[:256])},
				{"combinedBitsEntropy", bs.combinedBitsEntropy(0, 256), bs.combinedBitsEntropyFMABefore(0, 256)},
				{"estimateEntropy/3", estimateEntropy(h[:3]), estimateEntropyFMABefore(h[:3])},
				{"estimateEntropy/32", estimateEntropy(h[32:64]), estimateEntropyFMABefore(h[32:64])},
				{"estimateEntropy/256", estimateEntropy(h[:256]), estimateEntropyFMABefore(h[:256])},
				{"populationCost", populationCost(h[:256], 256), populationCostFMABefore(h[:256], 256)},
				{"populationCost/joined", populationCost(h, 512), populationCostFMABefore(h, 512)},
			}
			for _, p := range pairs {
				if p.got != p.want {
					t.Errorf("%s %s size=%d off=%d: %x, the unguarded expression gives %x; the explicit float64 "+
						"conversion must be a no-op when nothing fuses", in.name, p.fn, size, off, p.got, p.want)
				}
			}
		}
	}
}

func checkLiteralCostGuard(t *testing.T, in fmaGuardInput) {
	t.Helper()
	size := 1 << 16
	for size > len(in.data) {
		size >>= 1
	}
	ring, mask := in.data[:size], uint(size-1)
	for _, length := range []uint{0, 1, 2, 494, 495, 496, 990, 1999, 2000, 2001, 4096, uint(size)} {
		if length > uint(size) {
			continue
		}
		for _, pos := range []uint{0, uint(size) / 3} {
			wantHisto, wantCost := make([]uint, 3*256), make([]float32, length)
			gotHisto, gotCost := make([]uint, 3*256), make([]float32, length)
			estimateBitCostsForLiteralsUTF8FMABefore(ring, pos, length, mask, wantHisto, wantCost)
			estimateBitCostsForLiteralsUTF8(ring, pos, length, mask, gotHisto, gotCost)
			for i := range wantCost {
				if gotCost[i] != wantCost[i] {
					t.Errorf("%s length=%d pos=%d: cost[%d] = %x, the unguarded prologue gives %x; "+
						"the Zopfli DP would price this literal differently from C", in.name, length, pos, i, gotCost[i], wantCost[i])
					break
				}
			}
			for i := range wantHisto {
				if gotHisto[i] != wantHisto[i] {
					t.Errorf("%s length=%d pos=%d: histogram[%d] = %d, want %d", in.name, length, pos, i, gotHisto[i], wantHisto[i])
					break
				}
			}
		}
	}
}

func checkFindBlocksGuard(t *testing.T, in fmaGuardInput) {
	t.Helper()
	for _, numHistograms := range []int{2, 5, 9, 16, 40} {
		for _, length := range []int{1, 1999, 2000, 2001, 5000} {
			if length > len(in.data) {
				continue
			}
			symbols, histograms := fmaGuardFindBlocksFixture(in.data, length, numHistograms)
			for _, bitcost := range []float64{28.1, 13.5, 14.6} {
				gotN, gotIDs := runFindBlocks(findBlocks, symbols, histograms, length, numHistograms, bitcost)
				wantN, wantIDs := runFindBlocks(findBlocksFMABefore, symbols, histograms, length, numHistograms, bitcost)
				if gotN != wantN || string(gotIDs) != string(wantIDs) {
					t.Errorf("%s histograms=%d length=%d bitcost=%v: %d blocks, the unguarded prologue switch cost gives %d "+
						"(or different block ids); the block split would differ from C", in.name, numHistograms, length, bitcost, gotN, wantN)
				}
			}
		}
	}
}

func checkShouldMergeBlockGuard(t *testing.T, in, depthsFrom fmaGuardInput) {
	t.Helper()
	depths := fmaGuardLiteralDepths(depthsFrom.data)
	var a onePassArena
	for _, length := range []int{1, 42, 43, 44, 10965, 10966, 32768, 65536} {
		if length > len(in.data) {
			continue
		}
		for _, off := range []int{0, (len(in.data) - length) / 2, len(in.data) - length} {
			chunk := in.data[off:]
			if got, want := a.shouldMergeBlock(chunk, length, depths), a.shouldMergeBlockFMABefore(chunk, length, depths); got != want {
				t.Errorf("%s with %s's literal depths, length=%d off=%d: shouldMergeBlock = %v, the unguarded expression gives %v; "+
					"quality 0 would cut meta-blocks where C does not", in.name, depthsFrom.name, length, off, got, want)
			}
		}
	}
}

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

type fmaGuardBenchFixture struct {
	histograms   []uint32
	split        *blockSplitter
	literals     []byte
	depths       []byte
	symbols      []uint16
	fbHistograms map[int][]uint32
}

func newFMAGuardBenchFixture(tb testing.TB) *fmaGuardBenchFixture {
	tb.Helper()
	data, err := os.ReadFile("../../testdata/gh_172KB.html")
	if err != nil {
		tb.Fatal(err)
	}
	f := &fmaGuardBenchFixture{
		histograms:   fmaGuardHistograms(data, 4096, 0),
		literals:     data[:1<<16],
		depths:       fmaGuardLiteralDepths(data),
		fbHistograms: map[int][]uint32{},
	}
	f.split = &blockSplitter{histograms: f.histograms, alphabetSize: 256}
	for _, n := range []int{4, 16} {
		f.symbols, f.fbHistograms[n] = fmaGuardFindBlocksFixture(data, 3000, n)
	}
	return f
}

func BenchmarkFMAGuardedSites(b *testing.B) {
	f := newFMAGuardBenchFixture(b)
	var arena onePassArena
	litHisto, litCost := make([]uint, 3*256), make([]float32, len(f.literals))
	findBlocksCase := func(n int, impl func([]uint16, []uint32, []float64, []float64, []byte, []byte, int, int, int, float64) int) func() {
		insertCost, cost := make([]float64, 256*n), make([]float64, n)
		switchSignal, blockID := make([]byte, len(f.symbols)*((n+7)>>3)), make([]byte, len(f.symbols))
		return func() {
			fmaGuardSink += float64(impl(f.symbols, f.fbHistograms[n], insertCost, cost, switchSignal, blockID, len(f.symbols), n, 256, 28.1))
		}
	}
	cases := []struct {
		name          string
		before, after func()
	}{
		{
			"bitsEntropy",
			func() { fmaGuardSink += bitsEntropyFMABefore(f.histograms[:256]) },
			func() { fmaGuardSink += bitsEntropy(f.histograms[:256]) },
		},
		{
			"combinedBitsEntropy",
			func() { fmaGuardSink += f.split.combinedBitsEntropyFMABefore(0, 256) },
			func() { fmaGuardSink += f.split.combinedBitsEntropy(0, 256) },
		},
		{
			"estimateEntropy",
			func() { fmaGuardSink += estimateEntropyFMABefore(f.histograms[:256]) },
			func() { fmaGuardSink += estimateEntropy(f.histograms[:256]) },
		},
		{
			"populationCost",
			func() { fmaGuardSink += populationCostFMABefore(f.histograms[:256], 256) },
			func() { fmaGuardSink += populationCost(f.histograms[:256], 256) },
		},
		{
			"clusterCostDiff",
			func() {
				for s := uint32(1); s < 300; s += 7 {
					fmaGuardSink += clusterCostDiffFMABefore(s, 300-s)
				}
			},
			func() {
				for s := uint32(1); s < 300; s += 7 {
					fmaGuardSink += clusterCostDiff(s, 300-s)
				}
			},
		},
		{"findBlocks4Histograms", findBlocksCase(4, findBlocksFMABefore), findBlocksCase(4, findBlocks)},
		{"findBlocks16Histograms", findBlocksCase(16, findBlocksFMABefore), findBlocksCase(16, findBlocks)},
		{
			"estimateBitCostsForLiteralsUTF8",
			func() {
				estimateBitCostsForLiteralsUTF8FMABefore(f.literals, 0, uint(len(f.literals)), uint(len(f.literals)-1), litHisto, litCost)
			},
			func() {
				estimateBitCostsForLiteralsUTF8(f.literals, 0, uint(len(f.literals)), uint(len(f.literals)-1), litHisto, litCost)
			},
		},
		{
			"shouldMergeBlock",
			func() {
				if arena.shouldMergeBlockFMABefore(f.literals, len(f.literals), f.depths) {
					fmaGuardSink++
				}
			},
			func() {
				if arena.shouldMergeBlock(f.literals, len(f.literals), f.depths) {
					fmaGuardSink++
				}
			},
		},
	}
	for _, c := range cases {
		b.Run(c.name+"/impl=before", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				c.before()
			}
		})
		b.Run(c.name+"/impl=after", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				c.after()
			}
		})
	}
}
