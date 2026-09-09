package encoder

import (
	"testing"
	"unsafe"
)

func TestH5bLayout(t *testing.T) {
	var h6 h5b6
	var h7 h5b7

	if got, want := unsafe.Sizeof(h6.buckets), uintptr(h5bBucketSize*h5b6BlockSize*4); got != want {
		t.Errorf("h5b6 buckets size = %d, want %d", got, want)
	}
	if got, want := unsafe.Sizeof(h7.buckets), uintptr(h5bBucketSize*h5b7BlockSize*4); got != want {
		t.Errorf("h5b7 buckets size = %d, want %d", got, want)
	}
	if got, want := unsafe.Offsetof(h6.buckets), uintptr(h5bBucketSize*2); got != want {
		t.Errorf("h5b6 buckets offset = %d, want %d", got, want)
	}
	if got, want := unsafe.Offsetof(h7.buckets), uintptr(h5bBucketSize*2); got != want {
		t.Errorf("h5b7 buckets offset = %d, want %d", got, want)
	}
}
