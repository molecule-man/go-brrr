//go:build !amd64 || purego

package encoder

func histogramCombineRedirect(s []uint32, old, replacement uint32) {
	for i, v := range s {
		if v == old {
			s[i] = replacement
		}
	}
}
