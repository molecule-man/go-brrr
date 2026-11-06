//go:build linux && amd64 && !purego

package encoder

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"syscall"
	"testing"
	"unsafe"
)

func guardedFloat64s(tb testing.TB, n int) ([]float64, func() error) {
	tb.Helper()
	page := syscall.Getpagesize()
	size := (n*8 + page - 1) / page * page
	mem, err := syscall.Mmap(-1, 0, size+page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		tb.Fatal(err)
	}
	if err := syscall.Mprotect(mem[size:], syscall.PROT_NONE); err != nil {
		tb.Fatal(errors.Join(err, syscall.Munmap(mem)))
	}
	s := unsafe.Slice((*float64)(unsafe.Pointer(&mem[size-n*8])), n)
	return s, func() error { return syscall.Munmap(mem) }
}

func TestFindBlocksDPReadsNothingPastTheLastInsertCostRowOrCostForEveryHistogramCount(t *testing.T) {
	var errs []error
	for _, c := range findBlocksDPCases() {
		if len(c.data) != 2 || c.name[:7] != "random/" {
			continue
		}
		insertCost, freeInsert := guardedFloat64s(t, len(c.insertCost))
		cost, freeCost := guardedFloat64s(t, c.numHistograms)
		copy(insertCost, c.insertCost)
		copy(cost, c.cost)
		alphabet := len(c.insertCost) / c.numHistograms
		data := []uint16{uint16(alphabet - 1), c.data[0], uint16(alphabet - 1)}
		switchSignal := make([]byte, len(data)*((c.numHistograms+7)>>3))
		blockID := make([]byte, len(data))
		findBlocksDP(data, insertCost, cost, switchSignal, blockID, findBlocksDPSwitchBitcost)

		wantCost := slices.Clone(c.cost)
		wantSig := make([]byte, len(switchSignal))
		wantID := make([]byte, len(blockID))
		findBlocksDPScalarReference(data, c.insertCost, wantCost, wantSig, wantID, findBlocksDPSwitchBitcost)
		for i := range wantCost {
			if math.Float64bits(cost[i]) != math.Float64bits(wantCost[i]) {
				errs = append(errs, fmt.Errorf("%s: cost[%d] differs once cost and the last insertCost row end at a guard page", c.name, i))
				break
			}
		}
		if !slices.Equal(switchSignal, wantSig) || !slices.Equal(blockID, wantID) {
			errs = append(errs, fmt.Errorf("%s: switchSignal or blockID differ once cost and the last insertCost row end at a guard page", c.name))
		}
		errs = append(errs, freeInsert(), freeCost())
	}
	if err := errors.Join(errs...); err != nil {
		t.Error(err)
	}
}
