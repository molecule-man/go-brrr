// Assembly for histogramTotalCount.go. Go assembly cannot live inside a .go file.

//go:build amd64 && !purego

#include "textflag.h"
#include "tier_amd64.h"

#ifdef GOAMD64_v2
#define hasSSSE3
#endif
#ifdef hasAVX2
#define hasSSSE3
#endif

// func histogramTotalCountAsm(h []uint32, n int) uint32
// Four independent accumulators so the sum is not serialised on one add. The
// AVX2 and AVX-512 loops hand their partial sums to the four-lane loop, which
// finishes the remainder.
TEXT ·histogramTotalCountAsm(SB), NOSPLIT|NOFRAME, $0-36
	MOVQ h_base+0(FP), SI
	MOVQ n+24(FP), CX
	XORQ AX, AX
#ifdef hasAVX512
	CMPQ CX, $64
	JGE  sum64
#endif
#ifdef hasAVX2
	CMPQ CX, $32
	JGE  sum32
#endif
	PXOR X0, X0
	PXOR X2, X2
	PXOR X4, X4
	PXOR X6, X6
	MOVQ CX, DX
	SUBQ $16, DX

sum16:
	CMPQ  AX, DX
	JG    sum16fold
	MOVOU (SI)(AX*4), X1
	MOVOU 16(SI)(AX*4), X3
	MOVOU 32(SI)(AX*4), X5
	MOVOU 48(SI)(AX*4), X7
	PADDL X1, X0
	PADDL X3, X2
	PADDL X5, X4
	PADDL X7, X6
	ADDQ  $16, AX
	JMP   sum16

sum16fold:
	PADDL X2, X0
	PADDL X6, X4
	PADDL X4, X0

sum4setup:
	MOVQ CX, DX
	SUBQ $4, DX

sum16_4:
	CMPQ  AX, DX
	JG    sum16_done
	MOVOU (SI)(AX*4), X1
	PADDL X1, X0
	ADDQ  $4, AX
	JMP   sum16_4

sum16_done:
#ifdef hasSSSE3
	PHADDD X0, X0
	PHADDD X0, X0
#else
	PSHUFD $0x0E, X0, X1
	PADDL  X1, X0
	PSHUFD $0x01, X0, X1
	PADDL  X1, X0
#endif
	MOVL X0, R8

sum16_scalar:
	CMPQ AX, CX
	JGE  sum16_ret
	MOVL (SI)(AX*4), R9
	ADDL R9, R8
	INCQ AX
	JMP  sum16_scalar

sum16_ret:
	MOVL R8, ret+32(FP)
	RET

#ifdef hasAVX2
sum32:
	VPXOR Y0, Y0, Y0
	VPXOR Y1, Y1, Y1
	VPXOR Y2, Y2, Y2
	VPXOR Y3, Y3, Y3
	MOVQ  CX, DX
	SUBQ  $32, DX

sum32_loop:
	VPADDD (SI)(AX*4), Y0, Y0
	VPADDD 32(SI)(AX*4), Y1, Y1
	VPADDD 64(SI)(AX*4), Y2, Y2
	VPADDD 96(SI)(AX*4), Y3, Y3
	ADDQ   $32, AX
	CMPQ   AX, DX
	JLE    sum32_loop
	VPADDD Y1, Y0, Y0
	VPADDD Y3, Y2, Y2
	VPADDD Y2, Y0, Y0
	VEXTRACTI128 $1, Y0, X1
	VPADDD X1, X0, X0
	VZEROUPPER
	JMP    sum4setup
#endif

#ifdef hasAVX512
sum64:
	VPXORD Z0, Z0, Z0
	VPXORD Z1, Z1, Z1
	VPXORD Z2, Z2, Z2
	VPXORD Z3, Z3, Z3
	MOVQ   CX, DX
	SUBQ   $64, DX

sum64_loop:
	VPADDD (SI)(AX*4), Z0, Z0
	VPADDD 64(SI)(AX*4), Z1, Z1
	VPADDD 128(SI)(AX*4), Z2, Z2
	VPADDD 192(SI)(AX*4), Z3, Z3
	ADDQ   $64, AX
	CMPQ   AX, DX
	JLE    sum64_loop
	VPADDD Z1, Z0, Z0
	VPADDD Z3, Z2, Z2
	VPADDD Z2, Z0, Z0
	VEXTRACTI64X4 $1, Z0, Y1
	VPADDD Y1, Y0, Y0
	VEXTRACTI128  $1, Y0, X1
	VPADDD X1, X0, X0
	VZEROUPPER
	JMP    sum4setup
#endif
