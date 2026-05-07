// Fallback matchLenSIMD for platforms without amd64 SSE2.

//go:build !amd64 || purego

package encoder

func matchLenSIMD(data []byte, a, b uint, limit int) int {
	return matchLenAtNoInline(data, a, b, limit)
}
