// SIMD match length for amd64 (SSE2).

//go:build amd64 && !purego

package brrr

//go:noescape
func matchLenSIMD(data []byte, a, b uint, limit int) int
