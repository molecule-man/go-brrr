//go:build linux

package encoder

import (
	"errors"
	"fmt"
	"syscall"
	"testing"
	"unsafe"
)

func TestHistogramCombineRedirectReadsAndWritesNothingPastASliceThatEndsAtAGuardPage(t *testing.T) {
	page := syscall.Getpagesize()
	mem, err := syscall.Mmap(-1, 0, 2*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mprotect(mem[page:], syscall.PROT_NONE); err != nil {
		t.Fatal(errors.Join(err, syscall.Munmap(mem)))
	}
	var errs []error
	for n := 1; n <= 80; n++ {
		s := unsafe.Slice((*uint32)(unsafe.Pointer(&mem[page-4*n])), n)
		for i := range s {
			s[i] = uint32(i % 3)
		}
		histogramCombineRedirect(s, 1, 5)
		for i, v := range s {
			want := uint32(i % 3)
			if want == 1 {
				want = 5
			}
			if v != want {
				errs = append(errs, fmt.Errorf("len %d: element %d is %d, want %d when the slice ends at a guard page", n, i, v, want))
				break
			}
		}
	}
	errs = append(errs, syscall.Munmap(mem))
	if err := errors.Join(errs...); err != nil {
		t.Error(err)
	}
}
