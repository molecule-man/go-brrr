//go:build amd64 && !purego

package encoder

//go:noescape
func histogramCombineRedirect(s []uint32, old, replacement uint32)
