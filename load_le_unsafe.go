// Little-endian 32-bit and 64-bit loads (unsafe, little-endian platforms only).

//go:build !purego && (amd64 || 386 || arm || arm64 || loong64 || mips64le || mipsle || ppc64le || riscv64 || wasm)

package brrr

import "unsafe"

//go:nosplit
func loadByte(b []byte, i uint) byte {
	return *(*byte)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(b)), i))
}

//go:nosplit
func loadU32LE(b []byte, i uint) uint32 {
	return *(*uint32)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(b)), i))
}

//go:nosplit
func loadU64LE(b []byte, i uint) uint64 {
	return *(*uint64)(unsafe.Add(unsafe.Pointer(unsafe.SliceData(b)), i))
}
