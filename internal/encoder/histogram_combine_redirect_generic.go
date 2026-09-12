//go:build !(go1.27 && amd64) || purego

package encoder

// histogramCombineRedirect repoints symbols from one cluster to another.
func histogramCombineRedirect(s []uint32, old, replacement uint32) {
	for i := range s {
		if s[i] == old {
			s[i] = replacement
		}
	}
}
