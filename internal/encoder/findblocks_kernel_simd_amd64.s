// Assembly for findblocks_kernel_simd_amd64.go. Go assembly cannot live inside
// a .go file.

//go:build amd64 && !purego

#include "textflag.h"
#include "tier_amd64.h"

// func findBlocksDP(data []uint16, insertCost, cost []float64, switchSignal, blockID []byte, blockSwitchBitcost float64)
//
// The whole forward pass of findBlocks in one call. Per byte: add the symbol's
// insertCost row into cost, take the minimum and the first index holding it,
// store that index as the byte's block type unless nothing beat noMinCost,
// rebase cost against the minimum, clamp it at the switch cost and set one
// switchSignal bit per clamped histogram. Every lane does the scalar loop's
// IEEE-754 operations in the scalar loop's order.
//
// Below byte 2000 the switch cost is blockSwitchBitcost*(0.77 + m*byteIx),
// rounded after every operation like the C reference, never fused into an FMA.
// m is float64(0.07/2000) as Go evaluates the untyped constant,
// 0x3f02599ed7c6fbd2, the value the Go loop always used; C rounds 0.07 first
// and lands one ulp higher.
//
// findBlocks never calls this with fewer than two histograms. Cost stays in
// registers for the whole pass while it fits: up to 16 histograms in XMM at v1
// and v2; up to 6 in XMM and 7 to 32 in YMM at v3; up to 6 in XMM, 7 to 16 in
// YMM and 17 to 128 in ZMM at v4. Each register count has its own loop. The
// last XMM or YMM register overlaps the one before it and the last ZMM
// register is masked, so no lane is padding. More histograms stream cost
// through memory per byte, with the first-index scan folded into the clamp
// pass. Up to eight histograms match the scalar loop bit for bit, NaN
// included. Above eight a NaN lane is not handled like the scalar loop;
// insertCost is finite, so no NaN reaches it.
DATA fbDPConst<>+0(SB)/8, $0x3f02599ed7c6fbd2
DATA fbDPConst<>+8(SB)/8, $0x3fe8a3d70a3d70a4
GLOBL fbDPConst<>(SB), RODATA|NOPTR, $16

TEXT ·findBlocksDP(SB), NOSPLIT, $0-128
	MOVQ cost_len+56(FP), CX
#ifdef hasAVX512
	CMPQ CX, $128
	JGT  fb_zmm
	CMPQ CX, $16
	JGT  fb_z_dispatch
	CMPQ CX, $6
	JGT  fb_y_dispatch
#else
#ifdef hasAVX2
	CMPQ CX, $32
	JGT  fb_wide
	CMPQ CX, $6
	JGT  fb_y_dispatch
#else
	CMPQ CX, $16
	JGT  fb_wide
#endif
#endif
	CMPQ CX, $2
	JLE  fb_s1
	CMPQ CX, $4
	JLE  fb_s2
#ifdef hasAVX2
	JMP  fb_s3
#else
	CMPQ CX, $6
	JLE  fb_s3
	CMPQ CX, $8
	JLE  fb_s4
	CMPQ CX, $10
	JLE  fb_s5
	CMPQ CX, $12
	JLE  fb_s6
	CMPQ CX, $14
	JLE  fb_s7
	JMP  fb_s8
#endif

fb_s1:
	MOVQ     cost_base+48(FP), SI
	MOVQ     data_base+0(FP), R8
	MOVQ     insertCost_base+24(FP), DI
	MOVQ     switchSignal_base+72(FP), R13
	MOVQ     blockID_base+96(FP), R14
	MOVQ     CX, R11
	LEAQ     -2(CX), R15
	LEAQ     (DI)(R15*8), R10
	MOVUPD (SI)(R15*8), X0
	MOVQ     $0x547d42aea2879f2e, AX
	MOVQ     AX, X15
	UNPCKLPD X15, X15
	XORPS    X12, X12
	MOVQ     data_len+8(FP), CX
	XORQ     R9, R9
	TESTQ    CX, CX
	JEQ      fb_s1_done

fb_s1_byte:
	CMPQ     R9, $2000
	JA       fb_s1_row
	JEQ      fb_s1_steady
	XORPS    X13, X13
	CVTSQ2SD R9, X13
	MULSD    fbDPConst<>+0(SB), X13
	ADDSD    fbDPConst<>+8(SB), X13
	MULSD    blockSwitchBitcost+120(FP), X13
	MOVAPD   X13, X14
	UNPCKLPD X14, X14

fb_s1_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	MOVUPD (R10)(BX*8), X9
	ADDPD  X9, X0
	MOVAPD X0, X8
	MINPD  X15, X8
	MOVAPD  X8, X9
	SHUFPD  $1, X9, X9
	MINPD   X9, X8
	UCOMISD X15, X8
	JCC     fb_s1_nomin
	MOVAPD   X0, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, AX
	XORL    BX, BX
	BSFL    AX, BX
	MOVB    BX, (R14)(R9*1)
	UCOMISD X12, X8
	JNE     fb_s1_clamp
	MOVMSKPD X0, AX
	BTL      BX, AX
	SBBQ     AX, AX
	SHLQ     $63, AX
	MOVQ     AX, X8
	UNPCKLPD X8, X8
	JMP      fb_s1_clamp

fb_s1_nomin:
	MOVAPD X15, X8

fb_s1_clamp:
	SUBPD  X8, X0
	MOVAPD   X14, X9
	CMPPD    X0, X9, $2
	MOVAPD   X14, X10
	MINPD    X0, X10
	MOVAPD   X10, X0
	MOVMSKPD X9, AX
	ORB AX, 0(R13)
	ADDQ $1, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_s1_byte

fb_s1_done:
	MOVUPD X0, (SI)(R15*8)
	RET

fb_s1_steady:
	MOVSD    blockSwitchBitcost+120(FP), X14
	UNPCKLPD X14, X14
	JMP      fb_s1_row

fb_s2:
	MOVQ     cost_base+48(FP), SI
	MOVQ     data_base+0(FP), R8
	MOVQ     insertCost_base+24(FP), DI
	MOVQ     switchSignal_base+72(FP), R13
	MOVQ     blockID_base+96(FP), R14
	MOVQ     CX, R11
	LEAQ     -2(CX), R15
	LEAQ     (DI)(R15*8), R10
	MOVL    $1, R12
	MOVQ    R15, CX
	SHLL    CX, R12
	MOVUPD 0(SI), X0
	MOVUPD (SI)(R15*8), X1
	MOVQ     $0x547d42aea2879f2e, AX
	MOVQ     AX, X15
	UNPCKLPD X15, X15
	XORPS    X12, X12
	MOVQ     data_len+8(FP), CX
	XORQ     R9, R9
	TESTQ    CX, CX
	JEQ      fb_s2_done

fb_s2_byte:
	CMPQ     R9, $2000
	JA       fb_s2_row
	JEQ      fb_s2_steady
	XORPS    X13, X13
	CVTSQ2SD R9, X13
	MULSD    fbDPConst<>+0(SB), X13
	ADDSD    fbDPConst<>+8(SB), X13
	MULSD    blockSwitchBitcost+120(FP), X13
	MOVAPD   X13, X14
	UNPCKLPD X14, X14

fb_s2_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	MOVUPD 0(DI)(BX*8), X9
	ADDPD  X9, X0
	MOVUPD (R10)(BX*8), X9
	ADDPD  X9, X1
	MOVAPD X1, X8
	MINPD  X15, X8
	MOVAPD X0, X9
	MINPD  X8, X9
	MOVAPD  X9, X8
	SHUFPD  $1, X8, X8
	MINPD   X9, X8
	UCOMISD X15, X8
	JCC     fb_s2_nomin
	MOVAPD   X0, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, AX
	MOVAPD   X1, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	IMULL    R12, DX
	ORL      DX, AX
	XORL    BX, BX
	BSFL    AX, BX
	MOVB    BX, (R14)(R9*1)
	UCOMISD X12, X8
	JNE     fb_s2_clamp
	MOVMSKPD X0, AX
	MOVMSKPD X1, DX
	IMULL    R12, DX
	ORL      DX, AX
	BTL      BX, AX
	SBBQ     AX, AX
	SHLQ     $63, AX
	MOVQ     AX, X8
	UNPCKLPD X8, X8
	JMP      fb_s2_clamp

fb_s2_nomin:
	MOVAPD X15, X8

fb_s2_clamp:
	SUBPD  X8, X0
	SUBPD  X8, X1
	MOVAPD   X14, X9
	CMPPD    X0, X9, $2
	MOVAPD   X14, X10
	MINPD    X0, X10
	MOVAPD   X10, X0
	MOVMSKPD X9, AX
	MOVAPD   X14, X9
	CMPPD    X1, X9, $2
	MOVAPD   X14, X10
	MINPD    X1, X10
	MOVAPD   X10, X1
	MOVMSKPD X9, DX
	IMULL    R12, DX
	ORL      DX, AX
	ORB AX, 0(R13)
	ADDQ $1, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_s2_byte

fb_s2_done:
	MOVUPD X0, 0(SI)
	MOVUPD X1, (SI)(R15*8)
	RET

fb_s2_steady:
	MOVSD    blockSwitchBitcost+120(FP), X14
	UNPCKLPD X14, X14
	JMP      fb_s2_row

fb_s3:
	MOVQ     cost_base+48(FP), SI
	MOVQ     data_base+0(FP), R8
	MOVQ     insertCost_base+24(FP), DI
	MOVQ     switchSignal_base+72(FP), R13
	MOVQ     blockID_base+96(FP), R14
	MOVQ     CX, R11
	LEAQ     -2(CX), R15
	LEAQ     (DI)(R15*8), R10
	MOVL    $1, R12
	MOVQ    R15, CX
	SHLL    CX, R12
	MOVUPD 0(SI), X0
	MOVUPD 16(SI), X1
	MOVUPD (SI)(R15*8), X2
	MOVQ     $0x547d42aea2879f2e, AX
	MOVQ     AX, X15
	UNPCKLPD X15, X15
	XORPS    X12, X12
	MOVQ     data_len+8(FP), CX
	XORQ     R9, R9
	TESTQ    CX, CX
	JEQ      fb_s3_done

fb_s3_byte:
	CMPQ     R9, $2000
	JA       fb_s3_row
	JEQ      fb_s3_steady
	XORPS    X13, X13
	CVTSQ2SD R9, X13
	MULSD    fbDPConst<>+0(SB), X13
	ADDSD    fbDPConst<>+8(SB), X13
	MULSD    blockSwitchBitcost+120(FP), X13
	MOVAPD   X13, X14
	UNPCKLPD X14, X14

fb_s3_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	MOVUPD 0(DI)(BX*8), X9
	ADDPD  X9, X0
	MOVUPD 16(DI)(BX*8), X9
	ADDPD  X9, X1
	MOVUPD (R10)(BX*8), X9
	ADDPD  X9, X2
	MOVAPD X1, X8
	MINPD  X15, X8
	MOVAPD X0, X9
	MINPD  X8, X9
	MOVAPD X2, X8
	MINPD  X15, X8
	MOVAPD X9, X10
	MINPD  X8, X10
	MOVAPD  X10, X8
	SHUFPD  $1, X8, X8
	MINPD   X10, X8
	UCOMISD X15, X8
	JCC     fb_s3_nomin
	MOVAPD   X0, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, AX
	MOVAPD   X1, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVAPD   X2, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	IMULL    R12, DX
	ORL      DX, AX
	XORL    BX, BX
	BSFL    AX, BX
	MOVB    BX, (R14)(R9*1)
	UCOMISD X12, X8
	JNE     fb_s3_clamp
	MOVMSKPD X0, AX
	MOVMSKPD X1, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVMSKPD X2, DX
	IMULL    R12, DX
	ORL      DX, AX
	BTL      BX, AX
	SBBQ     AX, AX
	SHLQ     $63, AX
	MOVQ     AX, X8
	UNPCKLPD X8, X8
	JMP      fb_s3_clamp

fb_s3_nomin:
	MOVAPD X15, X8

fb_s3_clamp:
	SUBPD  X8, X0
	SUBPD  X8, X1
	SUBPD  X8, X2
	MOVAPD   X14, X9
	CMPPD    X0, X9, $2
	MOVAPD   X14, X10
	MINPD    X0, X10
	MOVAPD   X10, X0
	MOVMSKPD X9, AX
	MOVAPD   X14, X9
	CMPPD    X1, X9, $2
	MOVAPD   X14, X10
	MINPD    X1, X10
	MOVAPD   X10, X1
	MOVMSKPD X9, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVAPD   X14, X9
	CMPPD    X2, X9, $2
	MOVAPD   X14, X10
	MINPD    X2, X10
	MOVAPD   X10, X2
	MOVMSKPD X9, DX
	IMULL    R12, DX
	ORL      DX, AX
	ORB AX, 0(R13)
	ADDQ $1, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_s3_byte

fb_s3_done:
	MOVUPD X0, 0(SI)
	MOVUPD X1, 16(SI)
	MOVUPD X2, (SI)(R15*8)
	RET

fb_s3_steady:
	MOVSD    blockSwitchBitcost+120(FP), X14
	UNPCKLPD X14, X14
	JMP      fb_s3_row

#ifndef hasAVX2
fb_s4:
	MOVQ     cost_base+48(FP), SI
	MOVQ     data_base+0(FP), R8
	MOVQ     insertCost_base+24(FP), DI
	MOVQ     switchSignal_base+72(FP), R13
	MOVQ     blockID_base+96(FP), R14
	MOVQ     CX, R11
	LEAQ     -2(CX), R15
	LEAQ     (DI)(R15*8), R10
	MOVL    $1, R12
	MOVQ    R15, CX
	SHLL    CX, R12
	MOVUPD 0(SI), X0
	MOVUPD 16(SI), X1
	MOVUPD 32(SI), X2
	MOVUPD (SI)(R15*8), X3
	MOVQ     $0x547d42aea2879f2e, AX
	MOVQ     AX, X15
	UNPCKLPD X15, X15
	XORPS    X12, X12
	MOVQ     data_len+8(FP), CX
	XORQ     R9, R9
	TESTQ    CX, CX
	JEQ      fb_s4_done

fb_s4_byte:
	CMPQ     R9, $2000
	JA       fb_s4_row
	JEQ      fb_s4_steady
	XORPS    X13, X13
	CVTSQ2SD R9, X13
	MULSD    fbDPConst<>+0(SB), X13
	ADDSD    fbDPConst<>+8(SB), X13
	MULSD    blockSwitchBitcost+120(FP), X13
	MOVAPD   X13, X14
	UNPCKLPD X14, X14

fb_s4_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	MOVUPD 0(DI)(BX*8), X9
	ADDPD  X9, X0
	MOVUPD 16(DI)(BX*8), X9
	ADDPD  X9, X1
	MOVUPD 32(DI)(BX*8), X9
	ADDPD  X9, X2
	MOVUPD (R10)(BX*8), X9
	ADDPD  X9, X3
	MOVAPD X1, X8
	MINPD  X15, X8
	MOVAPD X0, X9
	MINPD  X8, X9
	MOVAPD X3, X8
	MINPD  X15, X8
	MOVAPD X2, X10
	MINPD  X8, X10
	MOVAPD X9, X8
	MINPD  X10, X8
	MOVAPD  X8, X9
	SHUFPD  $1, X9, X9
	MINPD   X9, X8
	UCOMISD X15, X8
	JCC     fb_s4_nomin
	MOVAPD   X0, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, AX
	MOVAPD   X1, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVAPD   X2, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $4, DX
	ORL      DX, AX
	MOVAPD   X3, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	IMULL    R12, DX
	ORL      DX, AX
	XORL    BX, BX
	BSFL    AX, BX
	MOVB    BX, (R14)(R9*1)
	UCOMISD X12, X8
	JNE     fb_s4_clamp
	MOVMSKPD X0, AX
	MOVMSKPD X1, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVMSKPD X2, DX
	SHLL     $4, DX
	ORL      DX, AX
	MOVMSKPD X3, DX
	IMULL    R12, DX
	ORL      DX, AX
	BTL      BX, AX
	SBBQ     AX, AX
	SHLQ     $63, AX
	MOVQ     AX, X8
	UNPCKLPD X8, X8
	JMP      fb_s4_clamp

fb_s4_nomin:
	MOVAPD X15, X8

fb_s4_clamp:
	SUBPD  X8, X0
	SUBPD  X8, X1
	SUBPD  X8, X2
	SUBPD  X8, X3
	MOVAPD   X14, X9
	CMPPD    X0, X9, $2
	MOVAPD   X14, X10
	MINPD    X0, X10
	MOVAPD   X10, X0
	MOVMSKPD X9, AX
	MOVAPD   X14, X9
	CMPPD    X1, X9, $2
	MOVAPD   X14, X10
	MINPD    X1, X10
	MOVAPD   X10, X1
	MOVMSKPD X9, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVAPD   X14, X9
	CMPPD    X2, X9, $2
	MOVAPD   X14, X10
	MINPD    X2, X10
	MOVAPD   X10, X2
	MOVMSKPD X9, DX
	SHLL     $4, DX
	ORL      DX, AX
	MOVAPD   X14, X9
	CMPPD    X3, X9, $2
	MOVAPD   X14, X10
	MINPD    X3, X10
	MOVAPD   X10, X3
	MOVMSKPD X9, DX
	IMULL    R12, DX
	ORL      DX, AX
	ORB AX, 0(R13)
	ADDQ $1, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_s4_byte

fb_s4_done:
	MOVUPD X0, 0(SI)
	MOVUPD X1, 16(SI)
	MOVUPD X2, 32(SI)
	MOVUPD X3, (SI)(R15*8)
	RET

fb_s4_steady:
	MOVSD    blockSwitchBitcost+120(FP), X14
	UNPCKLPD X14, X14
	JMP      fb_s4_row

fb_s5:
	MOVQ     cost_base+48(FP), SI
	MOVQ     data_base+0(FP), R8
	MOVQ     insertCost_base+24(FP), DI
	MOVQ     switchSignal_base+72(FP), R13
	MOVQ     blockID_base+96(FP), R14
	MOVQ     CX, R11
	LEAQ     -2(CX), R15
	LEAQ     (DI)(R15*8), R10
	MOVL    $1, R12
	MOVQ    R15, CX
	SHLL    CX, R12
	MOVUPD 0(SI), X0
	MOVUPD 16(SI), X1
	MOVUPD 32(SI), X2
	MOVUPD 48(SI), X3
	MOVUPD (SI)(R15*8), X4
	MOVQ     $0x547d42aea2879f2e, AX
	MOVQ     AX, X15
	UNPCKLPD X15, X15
	XORPS    X12, X12
	MOVQ     data_len+8(FP), CX
	XORQ     R9, R9
	TESTQ    CX, CX
	JEQ      fb_s5_done

fb_s5_byte:
	CMPQ     R9, $2000
	JA       fb_s5_row
	JEQ      fb_s5_steady
	XORPS    X13, X13
	CVTSQ2SD R9, X13
	MULSD    fbDPConst<>+0(SB), X13
	ADDSD    fbDPConst<>+8(SB), X13
	MULSD    blockSwitchBitcost+120(FP), X13
	MOVAPD   X13, X14
	UNPCKLPD X14, X14

fb_s5_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	MOVUPD 0(DI)(BX*8), X9
	ADDPD  X9, X0
	MOVUPD 16(DI)(BX*8), X9
	ADDPD  X9, X1
	MOVUPD 32(DI)(BX*8), X9
	ADDPD  X9, X2
	MOVUPD 48(DI)(BX*8), X9
	ADDPD  X9, X3
	MOVUPD (R10)(BX*8), X9
	ADDPD  X9, X4
	MOVAPD X0, X8
	MINPD  X1, X8
	MOVAPD X2, X9
	MINPD  X3, X9
	MINPD  X9, X8
	MINPD  X4, X8
	MOVAPD  X8, X9
	SHUFPD  $1, X9, X9
	MINPD   X9, X8
	UCOMISD X15, X8
	JCC     fb_s5_nomin
	MOVAPD   X0, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, AX
	MOVAPD   X1, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVAPD   X2, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $4, DX
	ORL      DX, AX
	MOVAPD   X3, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $6, DX
	ORL      DX, AX
	MOVAPD   X4, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	IMULL    R12, DX
	ORL      DX, AX
	XORL    BX, BX
	BSFL    AX, BX
	MOVB    BX, (R14)(R9*1)
	UCOMISD X12, X8
	JNE     fb_s5_clamp
	MOVMSKPD X0, AX
	MOVMSKPD X1, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVMSKPD X2, DX
	SHLL     $4, DX
	ORL      DX, AX
	MOVMSKPD X3, DX
	SHLL     $6, DX
	ORL      DX, AX
	MOVMSKPD X4, DX
	IMULL    R12, DX
	ORL      DX, AX
	BTL      BX, AX
	SBBQ     AX, AX
	SHLQ     $63, AX
	MOVQ     AX, X8
	UNPCKLPD X8, X8
	JMP      fb_s5_clamp

fb_s5_nomin:
	MOVAPD X15, X8

fb_s5_clamp:
	SUBPD  X8, X0
	SUBPD  X8, X1
	SUBPD  X8, X2
	SUBPD  X8, X3
	SUBPD  X8, X4
	MOVAPD   X0, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X0
	MOVMSKPD X9, AX
	MOVAPD   X1, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X1
	MOVMSKPD X9, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVAPD   X2, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X2
	MOVMSKPD X9, DX
	SHLL     $4, DX
	ORL      DX, AX
	MOVAPD   X3, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X3
	MOVMSKPD X9, DX
	SHLL     $6, DX
	ORL      DX, AX
	MOVAPD   X4, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X4
	MOVMSKPD X9, DX
	IMULL    R12, DX
	ORL      DX, AX
	ORW AX, 0(R13)
	ADDQ $2, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_s5_byte

fb_s5_done:
	MOVUPD X0, 0(SI)
	MOVUPD X1, 16(SI)
	MOVUPD X2, 32(SI)
	MOVUPD X3, 48(SI)
	MOVUPD X4, (SI)(R15*8)
	RET

fb_s5_steady:
	MOVSD    blockSwitchBitcost+120(FP), X14
	UNPCKLPD X14, X14
	JMP      fb_s5_row

fb_s6:
	MOVQ     cost_base+48(FP), SI
	MOVQ     data_base+0(FP), R8
	MOVQ     insertCost_base+24(FP), DI
	MOVQ     switchSignal_base+72(FP), R13
	MOVQ     blockID_base+96(FP), R14
	MOVQ     CX, R11
	LEAQ     -2(CX), R15
	LEAQ     (DI)(R15*8), R10
	MOVL    $1, R12
	MOVQ    R15, CX
	SHLL    CX, R12
	MOVUPD 0(SI), X0
	MOVUPD 16(SI), X1
	MOVUPD 32(SI), X2
	MOVUPD 48(SI), X3
	MOVUPD 64(SI), X4
	MOVUPD (SI)(R15*8), X5
	MOVQ     $0x547d42aea2879f2e, AX
	MOVQ     AX, X15
	UNPCKLPD X15, X15
	XORPS    X12, X12
	MOVQ     data_len+8(FP), CX
	XORQ     R9, R9
	TESTQ    CX, CX
	JEQ      fb_s6_done

fb_s6_byte:
	CMPQ     R9, $2000
	JA       fb_s6_row
	JEQ      fb_s6_steady
	XORPS    X13, X13
	CVTSQ2SD R9, X13
	MULSD    fbDPConst<>+0(SB), X13
	ADDSD    fbDPConst<>+8(SB), X13
	MULSD    blockSwitchBitcost+120(FP), X13
	MOVAPD   X13, X14
	UNPCKLPD X14, X14

fb_s6_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	MOVUPD 0(DI)(BX*8), X9
	ADDPD  X9, X0
	MOVUPD 16(DI)(BX*8), X9
	ADDPD  X9, X1
	MOVUPD 32(DI)(BX*8), X9
	ADDPD  X9, X2
	MOVUPD 48(DI)(BX*8), X9
	ADDPD  X9, X3
	MOVUPD 64(DI)(BX*8), X9
	ADDPD  X9, X4
	MOVUPD (R10)(BX*8), X9
	ADDPD  X9, X5
	MOVAPD X0, X8
	MINPD  X1, X8
	MOVAPD X2, X9
	MINPD  X3, X9
	MOVAPD X4, X10
	MINPD  X5, X10
	MINPD  X9, X8
	MINPD  X10, X8
	MOVAPD  X8, X9
	SHUFPD  $1, X9, X9
	MINPD   X9, X8
	UCOMISD X15, X8
	JCC     fb_s6_nomin
	MOVAPD   X0, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, AX
	MOVAPD   X1, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVAPD   X2, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $4, DX
	ORL      DX, AX
	MOVAPD   X3, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $6, DX
	ORL      DX, AX
	MOVAPD   X4, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $8, DX
	ORL      DX, AX
	MOVAPD   X5, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	IMULL    R12, DX
	ORL      DX, AX
	XORL    BX, BX
	BSFL    AX, BX
	MOVB    BX, (R14)(R9*1)
	UCOMISD X12, X8
	JNE     fb_s6_clamp
	MOVMSKPD X0, AX
	MOVMSKPD X1, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVMSKPD X2, DX
	SHLL     $4, DX
	ORL      DX, AX
	MOVMSKPD X3, DX
	SHLL     $6, DX
	ORL      DX, AX
	MOVMSKPD X4, DX
	SHLL     $8, DX
	ORL      DX, AX
	MOVMSKPD X5, DX
	IMULL    R12, DX
	ORL      DX, AX
	BTL      BX, AX
	SBBQ     AX, AX
	SHLQ     $63, AX
	MOVQ     AX, X8
	UNPCKLPD X8, X8
	JMP      fb_s6_clamp

fb_s6_nomin:
	MOVAPD X15, X8

fb_s6_clamp:
	SUBPD  X8, X0
	SUBPD  X8, X1
	SUBPD  X8, X2
	SUBPD  X8, X3
	SUBPD  X8, X4
	SUBPD  X8, X5
	MOVAPD   X0, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X0
	MOVMSKPD X9, AX
	MOVAPD   X1, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X1
	MOVMSKPD X9, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVAPD   X2, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X2
	MOVMSKPD X9, DX
	SHLL     $4, DX
	ORL      DX, AX
	MOVAPD   X3, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X3
	MOVMSKPD X9, DX
	SHLL     $6, DX
	ORL      DX, AX
	MOVAPD   X4, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X4
	MOVMSKPD X9, DX
	SHLL     $8, DX
	ORL      DX, AX
	MOVAPD   X5, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X5
	MOVMSKPD X9, DX
	IMULL    R12, DX
	ORL      DX, AX
	ORW AX, 0(R13)
	ADDQ $2, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_s6_byte

fb_s6_done:
	MOVUPD X0, 0(SI)
	MOVUPD X1, 16(SI)
	MOVUPD X2, 32(SI)
	MOVUPD X3, 48(SI)
	MOVUPD X4, 64(SI)
	MOVUPD X5, (SI)(R15*8)
	RET

fb_s6_steady:
	MOVSD    blockSwitchBitcost+120(FP), X14
	UNPCKLPD X14, X14
	JMP      fb_s6_row

fb_s7:
	MOVQ     cost_base+48(FP), SI
	MOVQ     data_base+0(FP), R8
	MOVQ     insertCost_base+24(FP), DI
	MOVQ     switchSignal_base+72(FP), R13
	MOVQ     blockID_base+96(FP), R14
	MOVQ     CX, R11
	LEAQ     -2(CX), R15
	LEAQ     (DI)(R15*8), R10
	MOVL    $1, R12
	MOVQ    R15, CX
	SHLL    CX, R12
	MOVUPD 0(SI), X0
	MOVUPD 16(SI), X1
	MOVUPD 32(SI), X2
	MOVUPD 48(SI), X3
	MOVUPD 64(SI), X4
	MOVUPD 80(SI), X5
	MOVUPD (SI)(R15*8), X6
	MOVQ     $0x547d42aea2879f2e, AX
	MOVQ     AX, X15
	UNPCKLPD X15, X15
	XORPS    X12, X12
	MOVQ     data_len+8(FP), CX
	XORQ     R9, R9
	TESTQ    CX, CX
	JEQ      fb_s7_done

fb_s7_byte:
	CMPQ     R9, $2000
	JA       fb_s7_row
	JEQ      fb_s7_steady
	XORPS    X13, X13
	CVTSQ2SD R9, X13
	MULSD    fbDPConst<>+0(SB), X13
	ADDSD    fbDPConst<>+8(SB), X13
	MULSD    blockSwitchBitcost+120(FP), X13
	MOVAPD   X13, X14
	UNPCKLPD X14, X14

fb_s7_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	MOVUPD 0(DI)(BX*8), X9
	ADDPD  X9, X0
	MOVUPD 16(DI)(BX*8), X9
	ADDPD  X9, X1
	MOVUPD 32(DI)(BX*8), X9
	ADDPD  X9, X2
	MOVUPD 48(DI)(BX*8), X9
	ADDPD  X9, X3
	MOVUPD 64(DI)(BX*8), X9
	ADDPD  X9, X4
	MOVUPD 80(DI)(BX*8), X9
	ADDPD  X9, X5
	MOVUPD (R10)(BX*8), X9
	ADDPD  X9, X6
	MOVAPD X0, X8
	MINPD  X1, X8
	MOVAPD X2, X9
	MINPD  X3, X9
	MOVAPD X4, X10
	MINPD  X5, X10
	MINPD  X9, X8
	MINPD  X6, X10
	MINPD  X10, X8
	MOVAPD  X8, X9
	SHUFPD  $1, X9, X9
	MINPD   X9, X8
	UCOMISD X15, X8
	JCC     fb_s7_nomin
	MOVAPD   X0, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, AX
	MOVAPD   X1, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVAPD   X2, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $4, DX
	ORL      DX, AX
	MOVAPD   X3, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $6, DX
	ORL      DX, AX
	MOVAPD   X4, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $8, DX
	ORL      DX, AX
	MOVAPD   X5, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $10, DX
	ORL      DX, AX
	MOVAPD   X6, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	IMULL    R12, DX
	ORL      DX, AX
	XORL    BX, BX
	BSFL    AX, BX
	MOVB    BX, (R14)(R9*1)
	UCOMISD X12, X8
	JNE     fb_s7_clamp
	MOVMSKPD X0, AX
	MOVMSKPD X1, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVMSKPD X2, DX
	SHLL     $4, DX
	ORL      DX, AX
	MOVMSKPD X3, DX
	SHLL     $6, DX
	ORL      DX, AX
	MOVMSKPD X4, DX
	SHLL     $8, DX
	ORL      DX, AX
	MOVMSKPD X5, DX
	SHLL     $10, DX
	ORL      DX, AX
	MOVMSKPD X6, DX
	IMULL    R12, DX
	ORL      DX, AX
	BTL      BX, AX
	SBBQ     AX, AX
	SHLQ     $63, AX
	MOVQ     AX, X8
	UNPCKLPD X8, X8
	JMP      fb_s7_clamp

fb_s7_nomin:
	MOVAPD X15, X8

fb_s7_clamp:
	SUBPD  X8, X0
	SUBPD  X8, X1
	SUBPD  X8, X2
	SUBPD  X8, X3
	SUBPD  X8, X4
	SUBPD  X8, X5
	SUBPD  X8, X6
	MOVAPD   X0, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X0
	MOVMSKPD X9, AX
	MOVAPD   X1, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X1
	MOVMSKPD X9, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVAPD   X2, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X2
	MOVMSKPD X9, DX
	SHLL     $4, DX
	ORL      DX, AX
	MOVAPD   X3, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X3
	MOVMSKPD X9, DX
	SHLL     $6, DX
	ORL      DX, AX
	MOVAPD   X4, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X4
	MOVMSKPD X9, DX
	SHLL     $8, DX
	ORL      DX, AX
	MOVAPD   X5, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X5
	MOVMSKPD X9, DX
	SHLL     $10, DX
	ORL      DX, AX
	MOVAPD   X6, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X6
	MOVMSKPD X9, DX
	IMULL    R12, DX
	ORL      DX, AX
	ORW AX, 0(R13)
	ADDQ $2, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_s7_byte

fb_s7_done:
	MOVUPD X0, 0(SI)
	MOVUPD X1, 16(SI)
	MOVUPD X2, 32(SI)
	MOVUPD X3, 48(SI)
	MOVUPD X4, 64(SI)
	MOVUPD X5, 80(SI)
	MOVUPD X6, (SI)(R15*8)
	RET

fb_s7_steady:
	MOVSD    blockSwitchBitcost+120(FP), X14
	UNPCKLPD X14, X14
	JMP      fb_s7_row

fb_s8:
	MOVQ     cost_base+48(FP), SI
	MOVQ     data_base+0(FP), R8
	MOVQ     insertCost_base+24(FP), DI
	MOVQ     switchSignal_base+72(FP), R13
	MOVQ     blockID_base+96(FP), R14
	MOVQ     CX, R11
	LEAQ     -2(CX), R15
	LEAQ     (DI)(R15*8), R10
	MOVL    $1, R12
	MOVQ    R15, CX
	SHLL    CX, R12
	MOVUPD 0(SI), X0
	MOVUPD 16(SI), X1
	MOVUPD 32(SI), X2
	MOVUPD 48(SI), X3
	MOVUPD 64(SI), X4
	MOVUPD 80(SI), X5
	MOVUPD 96(SI), X6
	MOVUPD (SI)(R15*8), X7
	MOVQ     $0x547d42aea2879f2e, AX
	MOVQ     AX, X15
	UNPCKLPD X15, X15
	XORPS    X12, X12
	MOVQ     data_len+8(FP), CX
	XORQ     R9, R9
	TESTQ    CX, CX
	JEQ      fb_s8_done

fb_s8_byte:
	CMPQ     R9, $2000
	JA       fb_s8_row
	JEQ      fb_s8_steady
	XORPS    X13, X13
	CVTSQ2SD R9, X13
	MULSD    fbDPConst<>+0(SB), X13
	ADDSD    fbDPConst<>+8(SB), X13
	MULSD    blockSwitchBitcost+120(FP), X13
	MOVAPD   X13, X14
	UNPCKLPD X14, X14

fb_s8_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	MOVUPD 0(DI)(BX*8), X9
	ADDPD  X9, X0
	MOVUPD 16(DI)(BX*8), X9
	ADDPD  X9, X1
	MOVUPD 32(DI)(BX*8), X9
	ADDPD  X9, X2
	MOVUPD 48(DI)(BX*8), X9
	ADDPD  X9, X3
	MOVUPD 64(DI)(BX*8), X9
	ADDPD  X9, X4
	MOVUPD 80(DI)(BX*8), X9
	ADDPD  X9, X5
	MOVUPD 96(DI)(BX*8), X9
	ADDPD  X9, X6
	MOVUPD (R10)(BX*8), X9
	ADDPD  X9, X7
	MOVAPD X0, X8
	MINPD  X1, X8
	MOVAPD X2, X9
	MINPD  X3, X9
	MOVAPD X4, X10
	MINPD  X5, X10
	MOVAPD X6, X11
	MINPD  X7, X11
	MINPD  X9, X8
	MINPD  X11, X10
	MINPD  X10, X8
	MOVAPD  X8, X9
	SHUFPD  $1, X9, X9
	MINPD   X9, X8
	UCOMISD X15, X8
	JCC     fb_s8_nomin
	MOVAPD   X0, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, AX
	MOVAPD   X1, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVAPD   X2, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $4, DX
	ORL      DX, AX
	MOVAPD   X3, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $6, DX
	ORL      DX, AX
	MOVAPD   X4, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $8, DX
	ORL      DX, AX
	MOVAPD   X5, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $10, DX
	ORL      DX, AX
	MOVAPD   X6, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	SHLL     $12, DX
	ORL      DX, AX
	MOVAPD   X7, X9
	CMPPD    X8, X9, $0
	MOVMSKPD X9, DX
	IMULL    R12, DX
	ORL      DX, AX
	XORL    BX, BX
	BSFL    AX, BX
	MOVB    BX, (R14)(R9*1)
	UCOMISD X12, X8
	JNE     fb_s8_clamp
	MOVMSKPD X0, AX
	MOVMSKPD X1, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVMSKPD X2, DX
	SHLL     $4, DX
	ORL      DX, AX
	MOVMSKPD X3, DX
	SHLL     $6, DX
	ORL      DX, AX
	MOVMSKPD X4, DX
	SHLL     $8, DX
	ORL      DX, AX
	MOVMSKPD X5, DX
	SHLL     $10, DX
	ORL      DX, AX
	MOVMSKPD X6, DX
	SHLL     $12, DX
	ORL      DX, AX
	MOVMSKPD X7, DX
	IMULL    R12, DX
	ORL      DX, AX
	BTL      BX, AX
	SBBQ     AX, AX
	SHLQ     $63, AX
	MOVQ     AX, X8
	UNPCKLPD X8, X8
	JMP      fb_s8_clamp

fb_s8_nomin:
	MOVAPD X15, X8

fb_s8_clamp:
	SUBPD  X8, X0
	SUBPD  X8, X1
	SUBPD  X8, X2
	SUBPD  X8, X3
	SUBPD  X8, X4
	SUBPD  X8, X5
	SUBPD  X8, X6
	SUBPD  X8, X7
	MOVAPD   X0, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X0
	MOVMSKPD X9, AX
	MOVAPD   X1, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X1
	MOVMSKPD X9, DX
	SHLL     $2, DX
	ORL      DX, AX
	MOVAPD   X2, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X2
	MOVMSKPD X9, DX
	SHLL     $4, DX
	ORL      DX, AX
	MOVAPD   X3, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X3
	MOVMSKPD X9, DX
	SHLL     $6, DX
	ORL      DX, AX
	MOVAPD   X4, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X4
	MOVMSKPD X9, DX
	SHLL     $8, DX
	ORL      DX, AX
	MOVAPD   X5, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X5
	MOVMSKPD X9, DX
	SHLL     $10, DX
	ORL      DX, AX
	MOVAPD   X6, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X6
	MOVMSKPD X9, DX
	SHLL     $12, DX
	ORL      DX, AX
	MOVAPD   X7, X9
	CMPPD    X14, X9, $5
	MINPD    X14, X7
	MOVMSKPD X9, DX
	IMULL    R12, DX
	ORL      DX, AX
	ORW AX, 0(R13)
	ADDQ $2, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_s8_byte

fb_s8_done:
	MOVUPD X0, 0(SI)
	MOVUPD X1, 16(SI)
	MOVUPD X2, 32(SI)
	MOVUPD X3, 48(SI)
	MOVUPD X4, 64(SI)
	MOVUPD X5, 80(SI)
	MOVUPD X6, 96(SI)
	MOVUPD X7, (SI)(R15*8)
	RET

fb_s8_steady:
	MOVSD    blockSwitchBitcost+120(FP), X14
	UNPCKLPD X14, X14
	JMP      fb_s8_row

#endif

#ifdef hasAVX2
fb_y_dispatch:
	CMPQ CX, $8
	JLE  fb_y2
	CMPQ CX, $12
	JLE  fb_y3
#ifdef hasAVX512
	JMP  fb_y4
#else
	CMPQ CX, $16
	JLE  fb_y4
	CMPQ CX, $20
	JLE  fb_y5
	CMPQ CX, $24
	JLE  fb_y6
	CMPQ CX, $28
	JLE  fb_y7
	JMP  fb_y8
#endif

fb_y2:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -4(CX), R15
	LEAQ         (DI)(R15*8), R10
	VMOVUPD 0(SI), Y0
	VMOVUPD (SI)(R15*8), Y1
	MOVQ         $0x547d42aea2879f2e, AX
	VMOVQ        AX, X15
	VBROADCASTSD X15, Y15
	VXORPD       X12, X12, X12
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_y2_done

fb_y2_byte:
	CMPQ         R9, $2000
	JA           fb_y2_row
	JEQ          fb_y2_steady
	VCVTSI2SDQ   R9, X12, X13
	VMULSD       fbDPConst<>+0(SB), X13, X13
	VADDSD       fbDPConst<>+8(SB), X13, X13
	VMULSD       blockSwitchBitcost+120(FP), X13, X13
	VBROADCASTSD X13, Y14

fb_y2_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Y0, Y0
	VADDPD (R10)(BX*8), Y1, Y1
	VMINPD Y15, Y1, Y9
	VMINPD Y9, Y0, Y8
	VPERM2F128 $1, Y8, Y8, Y9
	VMINPD     Y9, Y8, Y8
	VPERMILPD  $5, Y8, Y9
	VMINPD     Y9, Y8, Y8
	VUCOMISD   X15, X8
	JCC        fb_y2_nomin
	VCMPPD    $0, Y8, Y0, Y9
	VMOVMSKPD Y9, AX
	VCMPPD    $0, Y8, Y1, Y9
	VMOVMSKPD Y9, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	XORL     DX, DX
	TZCNTL   AX, BX
	CMOVLCS  DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X12, X8
	JNE      fb_y2_clamp
	VMOVMSKPD Y0, AX
	VMOVMSKPD Y1, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	BTL          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X8
	VBROADCASTSD X8, Y8
	JMP          fb_y2_clamp

fb_y2_nomin:
	VMOVAPD Y15, Y8

fb_y2_clamp:
	VSUBPD Y8, Y0, Y0
	VSUBPD Y8, Y1, Y1
	VCMPPD    $2, Y0, Y14, Y9
	VMINPD    Y0, Y14, Y0
	VMOVMSKPD Y9, AX
	VCMPPD    $2, Y1, Y14, Y9
	VMINPD    Y1, Y14, Y1
	VMOVMSKPD Y9, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	ORB AX, 0(R13)
	ADDQ $1, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_y2_byte

fb_y2_done:
	VMOVUPD Y0, 0(SI)
	VMOVUPD Y1, (SI)(R15*8)
	VZEROUPPER
	RET

fb_y2_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Y14
	JMP          fb_y2_row

fb_y3:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -4(CX), R15
	LEAQ         (DI)(R15*8), R10
	VMOVUPD 0(SI), Y0
	VMOVUPD 32(SI), Y1
	VMOVUPD (SI)(R15*8), Y2
	MOVQ         $0x547d42aea2879f2e, AX
	VMOVQ        AX, X15
	VBROADCASTSD X15, Y15
	VXORPD       X12, X12, X12
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_y3_done

fb_y3_byte:
	CMPQ         R9, $2000
	JA           fb_y3_row
	JEQ          fb_y3_steady
	VCVTSI2SDQ   R9, X12, X13
	VMULSD       fbDPConst<>+0(SB), X13, X13
	VADDSD       fbDPConst<>+8(SB), X13, X13
	VMULSD       blockSwitchBitcost+120(FP), X13, X13
	VBROADCASTSD X13, Y14

fb_y3_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Y0, Y0
	VADDPD 32(DI)(BX*8), Y1, Y1
	VADDPD (R10)(BX*8), Y2, Y2
	VMINPD Y1, Y0, Y8
	VMINPD Y2, Y8, Y8
	VPERM2F128 $1, Y8, Y8, Y9
	VMINPD     Y9, Y8, Y8
	VPERMILPD  $5, Y8, Y9
	VMINPD     Y9, Y8, Y8
	VUCOMISD   X15, X8
	JCC        fb_y3_nomin
	VCMPPD    $0, Y8, Y0, Y9
	VMOVMSKPD Y9, AX
	VCMPPD    $0, Y8, Y1, Y9
	VMOVMSKPD Y9, DX
	SHLL      $4, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y2, Y9
	VMOVMSKPD Y9, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	XORL     DX, DX
	TZCNTL   AX, BX
	CMOVLCS  DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X12, X8
	JNE      fb_y3_clamp
	VMOVMSKPD Y0, AX
	VMOVMSKPD Y1, DX
	SHLL      $4, DX
	ORL       DX, AX
	VMOVMSKPD Y2, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	BTL          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X8
	VBROADCASTSD X8, Y8
	JMP          fb_y3_clamp

fb_y3_nomin:
	VMOVAPD Y15, Y8

fb_y3_clamp:
	VSUBPD Y8, Y0, Y0
	VSUBPD Y8, Y1, Y1
	VSUBPD Y8, Y2, Y2
	VCMPPD    $5, Y14, Y0, Y9
	VMINPD    Y14, Y0, Y0
	VMOVMSKPD Y9, AX
	VCMPPD    $5, Y14, Y1, Y9
	VMINPD    Y14, Y1, Y1
	VMOVMSKPD Y9, DX
	SHLL      $4, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y2, Y9
	VMINPD    Y14, Y2, Y2
	VMOVMSKPD Y9, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	ORW AX, 0(R13)
	ADDQ $2, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_y3_byte

fb_y3_done:
	VMOVUPD Y0, 0(SI)
	VMOVUPD Y1, 32(SI)
	VMOVUPD Y2, (SI)(R15*8)
	VZEROUPPER
	RET

fb_y3_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Y14
	JMP          fb_y3_row

fb_y4:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -4(CX), R15
	LEAQ         (DI)(R15*8), R10
	VMOVUPD 0(SI), Y0
	VMOVUPD 32(SI), Y1
	VMOVUPD 64(SI), Y2
	VMOVUPD (SI)(R15*8), Y3
	MOVQ         $0x547d42aea2879f2e, AX
	VMOVQ        AX, X15
	VBROADCASTSD X15, Y15
	VXORPD       X12, X12, X12
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_y4_done

fb_y4_byte:
	CMPQ         R9, $2000
	JA           fb_y4_row
	JEQ          fb_y4_steady
	VCVTSI2SDQ   R9, X12, X13
	VMULSD       fbDPConst<>+0(SB), X13, X13
	VADDSD       fbDPConst<>+8(SB), X13, X13
	VMULSD       blockSwitchBitcost+120(FP), X13, X13
	VBROADCASTSD X13, Y14

fb_y4_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Y0, Y0
	VADDPD 32(DI)(BX*8), Y1, Y1
	VADDPD 64(DI)(BX*8), Y2, Y2
	VADDPD (R10)(BX*8), Y3, Y3
	VMINPD Y1, Y0, Y8
	VMINPD Y3, Y2, Y9
	VMINPD Y9, Y8, Y8
	VPERM2F128 $1, Y8, Y8, Y9
	VMINPD     Y9, Y8, Y8
	VPERMILPD  $5, Y8, Y9
	VMINPD     Y9, Y8, Y8
	VUCOMISD   X15, X8
	JCC        fb_y4_nomin
	VCMPPD    $0, Y8, Y0, Y9
	VMOVMSKPD Y9, AX
	VCMPPD    $0, Y8, Y1, Y9
	VMOVMSKPD Y9, DX
	SHLL      $4, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y2, Y9
	VMOVMSKPD Y9, DX
	SHLL      $8, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y3, Y9
	VMOVMSKPD Y9, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	XORL     DX, DX
	TZCNTL   AX, BX
	CMOVLCS  DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X12, X8
	JNE      fb_y4_clamp
	VMOVMSKPD Y0, AX
	VMOVMSKPD Y1, DX
	SHLL      $4, DX
	ORL       DX, AX
	VMOVMSKPD Y2, DX
	SHLL      $8, DX
	ORL       DX, AX
	VMOVMSKPD Y3, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	BTL          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X8
	VBROADCASTSD X8, Y8
	JMP          fb_y4_clamp

fb_y4_nomin:
	VMOVAPD Y15, Y8

fb_y4_clamp:
	VSUBPD Y8, Y0, Y0
	VSUBPD Y8, Y1, Y1
	VSUBPD Y8, Y2, Y2
	VSUBPD Y8, Y3, Y3
	VCMPPD    $5, Y14, Y0, Y9
	VMINPD    Y14, Y0, Y0
	VMOVMSKPD Y9, AX
	VCMPPD    $5, Y14, Y1, Y9
	VMINPD    Y14, Y1, Y1
	VMOVMSKPD Y9, DX
	SHLL      $4, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y2, Y9
	VMINPD    Y14, Y2, Y2
	VMOVMSKPD Y9, DX
	SHLL      $8, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y3, Y9
	VMINPD    Y14, Y3, Y3
	VMOVMSKPD Y9, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	ORW AX, 0(R13)
	ADDQ $2, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_y4_byte

fb_y4_done:
	VMOVUPD Y0, 0(SI)
	VMOVUPD Y1, 32(SI)
	VMOVUPD Y2, 64(SI)
	VMOVUPD Y3, (SI)(R15*8)
	VZEROUPPER
	RET

fb_y4_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Y14
	JMP          fb_y4_row

#ifndef hasAVX512
fb_y5:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -4(CX), R15
	LEAQ         (DI)(R15*8), R10
	VMOVUPD 0(SI), Y0
	VMOVUPD 32(SI), Y1
	VMOVUPD 64(SI), Y2
	VMOVUPD 96(SI), Y3
	VMOVUPD (SI)(R15*8), Y4
	MOVQ         $0x547d42aea2879f2e, AX
	VMOVQ        AX, X15
	VBROADCASTSD X15, Y15
	VXORPD       X12, X12, X12
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_y5_done

fb_y5_byte:
	CMPQ         R9, $2000
	JA           fb_y5_row
	JEQ          fb_y5_steady
	VCVTSI2SDQ   R9, X12, X13
	VMULSD       fbDPConst<>+0(SB), X13, X13
	VADDSD       fbDPConst<>+8(SB), X13, X13
	VMULSD       blockSwitchBitcost+120(FP), X13, X13
	VBROADCASTSD X13, Y14

fb_y5_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Y0, Y0
	VADDPD 32(DI)(BX*8), Y1, Y1
	VADDPD 64(DI)(BX*8), Y2, Y2
	VADDPD 96(DI)(BX*8), Y3, Y3
	VADDPD (R10)(BX*8), Y4, Y4
	VMINPD Y1, Y0, Y8
	VMINPD Y3, Y2, Y9
	VMINPD Y9, Y8, Y8
	VMINPD Y4, Y8, Y8
	VPERM2F128 $1, Y8, Y8, Y9
	VMINPD     Y9, Y8, Y8
	VPERMILPD  $5, Y8, Y9
	VMINPD     Y9, Y8, Y8
	VUCOMISD   X15, X8
	JCC        fb_y5_nomin
	VCMPPD    $0, Y8, Y0, Y9
	VMOVMSKPD Y9, AX
	VCMPPD    $0, Y8, Y1, Y9
	VMOVMSKPD Y9, DX
	SHLL      $4, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y2, Y9
	VMOVMSKPD Y9, DX
	SHLL      $8, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y3, Y9
	VMOVMSKPD Y9, DX
	SHLL      $12, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y4, Y9
	VMOVMSKPD Y9, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	XORL     DX, DX
	TZCNTL   AX, BX
	CMOVLCS  DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X12, X8
	JNE      fb_y5_clamp
	VMOVMSKPD Y0, AX
	VMOVMSKPD Y1, DX
	SHLL      $4, DX
	ORL       DX, AX
	VMOVMSKPD Y2, DX
	SHLL      $8, DX
	ORL       DX, AX
	VMOVMSKPD Y3, DX
	SHLL      $12, DX
	ORL       DX, AX
	VMOVMSKPD Y4, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	BTL          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X8
	VBROADCASTSD X8, Y8
	JMP          fb_y5_clamp

fb_y5_nomin:
	VMOVAPD Y15, Y8

fb_y5_clamp:
	VSUBPD Y8, Y0, Y0
	VSUBPD Y8, Y1, Y1
	VSUBPD Y8, Y2, Y2
	VSUBPD Y8, Y3, Y3
	VSUBPD Y8, Y4, Y4
	VCMPPD    $5, Y14, Y0, Y9
	VMINPD    Y14, Y0, Y0
	VMOVMSKPD Y9, AX
	VCMPPD    $5, Y14, Y1, Y9
	VMINPD    Y14, Y1, Y1
	VMOVMSKPD Y9, DX
	SHLL      $4, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y2, Y9
	VMINPD    Y14, Y2, Y2
	VMOVMSKPD Y9, DX
	SHLL      $8, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y3, Y9
	VMINPD    Y14, Y3, Y3
	VMOVMSKPD Y9, DX
	SHLL      $12, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y4, Y9
	VMINPD    Y14, Y4, Y4
	VMOVMSKPD Y9, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	ORW AX, 0(R13)
	SHRQ $16, AX
	ORB AX, 2(R13)
	ADDQ $3, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_y5_byte

fb_y5_done:
	VMOVUPD Y0, 0(SI)
	VMOVUPD Y1, 32(SI)
	VMOVUPD Y2, 64(SI)
	VMOVUPD Y3, 96(SI)
	VMOVUPD Y4, (SI)(R15*8)
	VZEROUPPER
	RET

fb_y5_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Y14
	JMP          fb_y5_row

fb_y6:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -4(CX), R15
	LEAQ         (DI)(R15*8), R10
	VMOVUPD 0(SI), Y0
	VMOVUPD 32(SI), Y1
	VMOVUPD 64(SI), Y2
	VMOVUPD 96(SI), Y3
	VMOVUPD 128(SI), Y4
	VMOVUPD (SI)(R15*8), Y5
	MOVQ         $0x547d42aea2879f2e, AX
	VMOVQ        AX, X15
	VBROADCASTSD X15, Y15
	VXORPD       X12, X12, X12
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_y6_done

fb_y6_byte:
	CMPQ         R9, $2000
	JA           fb_y6_row
	JEQ          fb_y6_steady
	VCVTSI2SDQ   R9, X12, X13
	VMULSD       fbDPConst<>+0(SB), X13, X13
	VADDSD       fbDPConst<>+8(SB), X13, X13
	VMULSD       blockSwitchBitcost+120(FP), X13, X13
	VBROADCASTSD X13, Y14

fb_y6_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Y0, Y0
	VADDPD 32(DI)(BX*8), Y1, Y1
	VADDPD 64(DI)(BX*8), Y2, Y2
	VADDPD 96(DI)(BX*8), Y3, Y3
	VADDPD 128(DI)(BX*8), Y4, Y4
	VADDPD (R10)(BX*8), Y5, Y5
	VMINPD Y1, Y0, Y8
	VMINPD Y3, Y2, Y9
	VMINPD Y5, Y4, Y10
	VMINPD Y9, Y8, Y8
	VMINPD Y10, Y8, Y8
	VPERM2F128 $1, Y8, Y8, Y9
	VMINPD     Y9, Y8, Y8
	VPERMILPD  $5, Y8, Y9
	VMINPD     Y9, Y8, Y8
	VUCOMISD   X15, X8
	JCC        fb_y6_nomin
	VCMPPD    $0, Y8, Y0, Y9
	VMOVMSKPD Y9, AX
	VCMPPD    $0, Y8, Y1, Y9
	VMOVMSKPD Y9, DX
	SHLL      $4, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y2, Y9
	VMOVMSKPD Y9, DX
	SHLL      $8, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y3, Y9
	VMOVMSKPD Y9, DX
	SHLL      $12, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y4, Y9
	VMOVMSKPD Y9, DX
	SHLL      $16, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y5, Y9
	VMOVMSKPD Y9, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	XORL     DX, DX
	TZCNTL   AX, BX
	CMOVLCS  DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X12, X8
	JNE      fb_y6_clamp
	VMOVMSKPD Y0, AX
	VMOVMSKPD Y1, DX
	SHLL      $4, DX
	ORL       DX, AX
	VMOVMSKPD Y2, DX
	SHLL      $8, DX
	ORL       DX, AX
	VMOVMSKPD Y3, DX
	SHLL      $12, DX
	ORL       DX, AX
	VMOVMSKPD Y4, DX
	SHLL      $16, DX
	ORL       DX, AX
	VMOVMSKPD Y5, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	BTL          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X8
	VBROADCASTSD X8, Y8
	JMP          fb_y6_clamp

fb_y6_nomin:
	VMOVAPD Y15, Y8

fb_y6_clamp:
	VSUBPD Y8, Y0, Y0
	VSUBPD Y8, Y1, Y1
	VSUBPD Y8, Y2, Y2
	VSUBPD Y8, Y3, Y3
	VSUBPD Y8, Y4, Y4
	VSUBPD Y8, Y5, Y5
	VCMPPD    $5, Y14, Y0, Y9
	VMINPD    Y14, Y0, Y0
	VMOVMSKPD Y9, AX
	VCMPPD    $5, Y14, Y1, Y9
	VMINPD    Y14, Y1, Y1
	VMOVMSKPD Y9, DX
	SHLL      $4, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y2, Y9
	VMINPD    Y14, Y2, Y2
	VMOVMSKPD Y9, DX
	SHLL      $8, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y3, Y9
	VMINPD    Y14, Y3, Y3
	VMOVMSKPD Y9, DX
	SHLL      $12, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y4, Y9
	VMINPD    Y14, Y4, Y4
	VMOVMSKPD Y9, DX
	SHLL      $16, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y5, Y9
	VMINPD    Y14, Y5, Y5
	VMOVMSKPD Y9, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	ORW AX, 0(R13)
	SHRQ $16, AX
	ORB AX, 2(R13)
	ADDQ $3, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_y6_byte

fb_y6_done:
	VMOVUPD Y0, 0(SI)
	VMOVUPD Y1, 32(SI)
	VMOVUPD Y2, 64(SI)
	VMOVUPD Y3, 96(SI)
	VMOVUPD Y4, 128(SI)
	VMOVUPD Y5, (SI)(R15*8)
	VZEROUPPER
	RET

fb_y6_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Y14
	JMP          fb_y6_row

fb_y7:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -4(CX), R15
	LEAQ         (DI)(R15*8), R10
	VMOVUPD 0(SI), Y0
	VMOVUPD 32(SI), Y1
	VMOVUPD 64(SI), Y2
	VMOVUPD 96(SI), Y3
	VMOVUPD 128(SI), Y4
	VMOVUPD 160(SI), Y5
	VMOVUPD (SI)(R15*8), Y6
	MOVQ         $0x547d42aea2879f2e, AX
	VMOVQ        AX, X15
	VBROADCASTSD X15, Y15
	VXORPD       X12, X12, X12
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_y7_done

fb_y7_byte:
	CMPQ         R9, $2000
	JA           fb_y7_row
	JEQ          fb_y7_steady
	VCVTSI2SDQ   R9, X12, X13
	VMULSD       fbDPConst<>+0(SB), X13, X13
	VADDSD       fbDPConst<>+8(SB), X13, X13
	VMULSD       blockSwitchBitcost+120(FP), X13, X13
	VBROADCASTSD X13, Y14

fb_y7_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Y0, Y0
	VADDPD 32(DI)(BX*8), Y1, Y1
	VADDPD 64(DI)(BX*8), Y2, Y2
	VADDPD 96(DI)(BX*8), Y3, Y3
	VADDPD 128(DI)(BX*8), Y4, Y4
	VADDPD 160(DI)(BX*8), Y5, Y5
	VADDPD (R10)(BX*8), Y6, Y6
	VMINPD Y1, Y0, Y8
	VMINPD Y3, Y2, Y9
	VMINPD Y5, Y4, Y10
	VMINPD Y9, Y8, Y8
	VMINPD Y6, Y10, Y10
	VMINPD Y10, Y8, Y8
	VPERM2F128 $1, Y8, Y8, Y9
	VMINPD     Y9, Y8, Y8
	VPERMILPD  $5, Y8, Y9
	VMINPD     Y9, Y8, Y8
	VUCOMISD   X15, X8
	JCC        fb_y7_nomin
	VCMPPD    $0, Y8, Y0, Y9
	VMOVMSKPD Y9, AX
	VCMPPD    $0, Y8, Y1, Y9
	VMOVMSKPD Y9, DX
	SHLL      $4, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y2, Y9
	VMOVMSKPD Y9, DX
	SHLL      $8, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y3, Y9
	VMOVMSKPD Y9, DX
	SHLL      $12, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y4, Y9
	VMOVMSKPD Y9, DX
	SHLL      $16, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y5, Y9
	VMOVMSKPD Y9, DX
	SHLL      $20, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y6, Y9
	VMOVMSKPD Y9, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	XORL     DX, DX
	TZCNTL   AX, BX
	CMOVLCS  DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X12, X8
	JNE      fb_y7_clamp
	VMOVMSKPD Y0, AX
	VMOVMSKPD Y1, DX
	SHLL      $4, DX
	ORL       DX, AX
	VMOVMSKPD Y2, DX
	SHLL      $8, DX
	ORL       DX, AX
	VMOVMSKPD Y3, DX
	SHLL      $12, DX
	ORL       DX, AX
	VMOVMSKPD Y4, DX
	SHLL      $16, DX
	ORL       DX, AX
	VMOVMSKPD Y5, DX
	SHLL      $20, DX
	ORL       DX, AX
	VMOVMSKPD Y6, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	BTL          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X8
	VBROADCASTSD X8, Y8
	JMP          fb_y7_clamp

fb_y7_nomin:
	VMOVAPD Y15, Y8

fb_y7_clamp:
	VSUBPD Y8, Y0, Y0
	VSUBPD Y8, Y1, Y1
	VSUBPD Y8, Y2, Y2
	VSUBPD Y8, Y3, Y3
	VSUBPD Y8, Y4, Y4
	VSUBPD Y8, Y5, Y5
	VSUBPD Y8, Y6, Y6
	VCMPPD    $5, Y14, Y0, Y9
	VMINPD    Y14, Y0, Y0
	VMOVMSKPD Y9, AX
	VCMPPD    $5, Y14, Y1, Y9
	VMINPD    Y14, Y1, Y1
	VMOVMSKPD Y9, DX
	SHLL      $4, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y2, Y9
	VMINPD    Y14, Y2, Y2
	VMOVMSKPD Y9, DX
	SHLL      $8, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y3, Y9
	VMINPD    Y14, Y3, Y3
	VMOVMSKPD Y9, DX
	SHLL      $12, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y4, Y9
	VMINPD    Y14, Y4, Y4
	VMOVMSKPD Y9, DX
	SHLL      $16, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y5, Y9
	VMINPD    Y14, Y5, Y5
	VMOVMSKPD Y9, DX
	SHLL      $20, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y6, Y9
	VMINPD    Y14, Y6, Y6
	VMOVMSKPD Y9, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	ORL AX, 0(R13)
	ADDQ $4, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_y7_byte

fb_y7_done:
	VMOVUPD Y0, 0(SI)
	VMOVUPD Y1, 32(SI)
	VMOVUPD Y2, 64(SI)
	VMOVUPD Y3, 96(SI)
	VMOVUPD Y4, 128(SI)
	VMOVUPD Y5, 160(SI)
	VMOVUPD Y6, (SI)(R15*8)
	VZEROUPPER
	RET

fb_y7_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Y14
	JMP          fb_y7_row

fb_y8:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -4(CX), R15
	LEAQ         (DI)(R15*8), R10
	VMOVUPD 0(SI), Y0
	VMOVUPD 32(SI), Y1
	VMOVUPD 64(SI), Y2
	VMOVUPD 96(SI), Y3
	VMOVUPD 128(SI), Y4
	VMOVUPD 160(SI), Y5
	VMOVUPD 192(SI), Y6
	VMOVUPD (SI)(R15*8), Y7
	MOVQ         $0x547d42aea2879f2e, AX
	VMOVQ        AX, X15
	VBROADCASTSD X15, Y15
	VXORPD       X12, X12, X12
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_y8_done

fb_y8_byte:
	CMPQ         R9, $2000
	JA           fb_y8_row
	JEQ          fb_y8_steady
	VCVTSI2SDQ   R9, X12, X13
	VMULSD       fbDPConst<>+0(SB), X13, X13
	VADDSD       fbDPConst<>+8(SB), X13, X13
	VMULSD       blockSwitchBitcost+120(FP), X13, X13
	VBROADCASTSD X13, Y14

fb_y8_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Y0, Y0
	VADDPD 32(DI)(BX*8), Y1, Y1
	VADDPD 64(DI)(BX*8), Y2, Y2
	VADDPD 96(DI)(BX*8), Y3, Y3
	VADDPD 128(DI)(BX*8), Y4, Y4
	VADDPD 160(DI)(BX*8), Y5, Y5
	VADDPD 192(DI)(BX*8), Y6, Y6
	VADDPD (R10)(BX*8), Y7, Y7
	VMINPD Y1, Y0, Y8
	VMINPD Y3, Y2, Y9
	VMINPD Y5, Y4, Y10
	VMINPD Y7, Y6, Y11
	VMINPD Y9, Y8, Y8
	VMINPD Y11, Y10, Y10
	VMINPD Y10, Y8, Y8
	VPERM2F128 $1, Y8, Y8, Y9
	VMINPD     Y9, Y8, Y8
	VPERMILPD  $5, Y8, Y9
	VMINPD     Y9, Y8, Y8
	VUCOMISD   X15, X8
	JCC        fb_y8_nomin
	VCMPPD    $0, Y8, Y0, Y9
	VMOVMSKPD Y9, AX
	VCMPPD    $0, Y8, Y1, Y9
	VMOVMSKPD Y9, DX
	SHLL      $4, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y2, Y9
	VMOVMSKPD Y9, DX
	SHLL      $8, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y3, Y9
	VMOVMSKPD Y9, DX
	SHLL      $12, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y4, Y9
	VMOVMSKPD Y9, DX
	SHLL      $16, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y5, Y9
	VMOVMSKPD Y9, DX
	SHLL      $20, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y6, Y9
	VMOVMSKPD Y9, DX
	SHLL      $24, DX
	ORL       DX, AX
	VCMPPD    $0, Y8, Y7, Y9
	VMOVMSKPD Y9, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	XORL     DX, DX
	TZCNTL   AX, BX
	CMOVLCS  DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X12, X8
	JNE      fb_y8_clamp
	VMOVMSKPD Y0, AX
	VMOVMSKPD Y1, DX
	SHLL      $4, DX
	ORL       DX, AX
	VMOVMSKPD Y2, DX
	SHLL      $8, DX
	ORL       DX, AX
	VMOVMSKPD Y3, DX
	SHLL      $12, DX
	ORL       DX, AX
	VMOVMSKPD Y4, DX
	SHLL      $16, DX
	ORL       DX, AX
	VMOVMSKPD Y5, DX
	SHLL      $20, DX
	ORL       DX, AX
	VMOVMSKPD Y6, DX
	SHLL      $24, DX
	ORL       DX, AX
	VMOVMSKPD Y7, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	BTL          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X8
	VBROADCASTSD X8, Y8
	JMP          fb_y8_clamp

fb_y8_nomin:
	VMOVAPD Y15, Y8

fb_y8_clamp:
	VSUBPD Y8, Y0, Y0
	VSUBPD Y8, Y1, Y1
	VSUBPD Y8, Y2, Y2
	VSUBPD Y8, Y3, Y3
	VSUBPD Y8, Y4, Y4
	VSUBPD Y8, Y5, Y5
	VSUBPD Y8, Y6, Y6
	VSUBPD Y8, Y7, Y7
	VCMPPD    $5, Y14, Y0, Y9
	VMINPD    Y14, Y0, Y0
	VMOVMSKPD Y9, AX
	VCMPPD    $5, Y14, Y1, Y9
	VMINPD    Y14, Y1, Y1
	VMOVMSKPD Y9, DX
	SHLL      $4, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y2, Y9
	VMINPD    Y14, Y2, Y2
	VMOVMSKPD Y9, DX
	SHLL      $8, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y3, Y9
	VMINPD    Y14, Y3, Y3
	VMOVMSKPD Y9, DX
	SHLL      $12, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y4, Y9
	VMINPD    Y14, Y4, Y4
	VMOVMSKPD Y9, DX
	SHLL      $16, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y5, Y9
	VMINPD    Y14, Y5, Y5
	VMOVMSKPD Y9, DX
	SHLL      $20, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y6, Y9
	VMINPD    Y14, Y6, Y6
	VMOVMSKPD Y9, DX
	SHLL      $24, DX
	ORL       DX, AX
	VCMPPD    $5, Y14, Y7, Y9
	VMINPD    Y14, Y7, Y7
	VMOVMSKPD Y9, DX
	SHLXL     R15, DX, DX
	ORL       DX, AX
	ORL AX, 0(R13)
	ADDQ $4, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_y8_byte

fb_y8_done:
	VMOVUPD Y0, 0(SI)
	VMOVUPD Y1, 32(SI)
	VMOVUPD Y2, 64(SI)
	VMOVUPD Y3, 96(SI)
	VMOVUPD Y4, 128(SI)
	VMOVUPD Y5, 160(SI)
	VMOVUPD Y6, 192(SI)
	VMOVUPD Y7, (SI)(R15*8)
	VZEROUPPER
	RET

fb_y8_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Y14
	JMP          fb_y8_row

#endif
#endif

#ifdef hasAVX512
fb_z_dispatch:
	CMPQ CX, $64
	JGT  fb_z_hi
	CMPQ CX, $24
	JLE  fb_z3
	CMPQ CX, $32
	JLE  fb_z4
	CMPQ CX, $40
	JLE  fb_z5
	CMPQ CX, $48
	JLE  fb_z6
	CMPQ CX, $56
	JLE  fb_z7
	JMP  fb_z8

fb_z_hi:
	CMPQ CX, $72
	JLE  fb_z9
	CMPQ CX, $80
	JLE  fb_z10
	CMPQ CX, $88
	JLE  fb_z11
	CMPQ CX, $96
	JLE  fb_z12
	CMPQ CX, $104
	JLE  fb_z13
	CMPQ CX, $112
	JLE  fb_z14
	CMPQ CX, $120
	JLE  fb_z15
	JMP  fb_z16

fb_z3:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -16(CX), DX
	MOVL         $1, AX
	SHLXL        DX, AX, AX
	DECL         AX
	KMOVB        AX, K1
	MOVQ         $0x547d42aea2879f2e, AX
	VPBROADCASTQ AX, Z29
	VMOVUPD      0(SI), Z0
	VMOVUPD      64(SI), Z1
	VMOVAPD      Z29, Z2
	VMOVUPD      128(SI), K1, Z2
	VPXORQ       X26, X26, X26
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_z3_done

fb_z3_byte:
	CMPQ         R9, $2000
	JA           fb_z3_row
	JEQ          fb_z3_steady
	VCVTSI2SDQ   R9, X26, X27
	VMULSD       fbDPConst<>+0(SB), X27, X27
	VADDSD       fbDPConst<>+8(SB), X27, X27
	VMULSD       blockSwitchBitcost+120(FP), X27, X27
	VBROADCASTSD X27, Z28

fb_z3_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Z0, Z0
	VADDPD 64(DI)(BX*8), Z1, Z1
	VADDPD 128(DI)(BX*8), Z2, K1, Z2
	VMINPD Z1, Z0, Z16
	VMINPD Z2, Z16, Z24
	VSHUFF64X2 $0x4e, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VSHUFF64X2 $0xb1, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VPERMILPD  $0x55, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VUCOMISD   X29, X24
	JCC        fb_z3_nomin
	VCMPPD $0, Z24, Z0, K2
	VCMPPD $0, Z24, Z1, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z2, K3
	KUNPCKWD K2, K3, K2
	KMOVD K2, AX
	XORL    DX, DX
	TZCNTL  AX, BX
	CMOVLCS DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X26, X24
	JNE      fb_z3_clamp
	VPMOVQ2M Z0, K2
	VPMOVQ2M Z1, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z2, K3
	KUNPCKWD K2, K3, K2
	KMOVD K2, AX
	BTQ          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X24
	VBROADCASTSD X24, Z24
	JMP          fb_z3_clamp

fb_z3_nomin:
	VMOVAPD Z29, Z24

fb_z3_clamp:
	VSUBPD Z24, Z0, Z0
	VSUBPD Z24, Z1, Z1
	VSUBPD Z24, Z2, K1, Z2
	VCMPPD $5, Z28, Z0, K2
	VMINPD Z28, Z0, Z0
	VCMPPD $5, Z28, Z1, K3
	VMINPD Z28, Z1, Z1
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z2, K1, K3
	VMINPD Z28, Z2, K1, Z2
	KUNPCKWD K2, K3, K2
	KMOVD K2, AX
	ORW AX, 0(R13)
	SHRQ $16, AX
	ORB AX, 2(R13)
	ADDQ $3, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_z3_byte

fb_z3_done:
	VMOVUPD Z0, 0(SI)
	VMOVUPD Z1, 64(SI)
	VMOVUPD Z2, K1, 128(SI)
	VZEROUPPER
	RET

fb_z3_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Z28
	JMP          fb_z3_row

fb_z4:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -24(CX), DX
	MOVL         $1, AX
	SHLXL        DX, AX, AX
	DECL         AX
	KMOVB        AX, K1
	MOVQ         $0x547d42aea2879f2e, AX
	VPBROADCASTQ AX, Z29
	VMOVUPD      0(SI), Z0
	VMOVUPD      64(SI), Z1
	VMOVUPD      128(SI), Z2
	VMOVAPD      Z29, Z3
	VMOVUPD      192(SI), K1, Z3
	VPXORQ       X26, X26, X26
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_z4_done

fb_z4_byte:
	CMPQ         R9, $2000
	JA           fb_z4_row
	JEQ          fb_z4_steady
	VCVTSI2SDQ   R9, X26, X27
	VMULSD       fbDPConst<>+0(SB), X27, X27
	VADDSD       fbDPConst<>+8(SB), X27, X27
	VMULSD       blockSwitchBitcost+120(FP), X27, X27
	VBROADCASTSD X27, Z28

fb_z4_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Z0, Z0
	VADDPD 64(DI)(BX*8), Z1, Z1
	VADDPD 128(DI)(BX*8), Z2, Z2
	VADDPD 192(DI)(BX*8), Z3, K1, Z3
	VMINPD Z1, Z0, Z16
	VMINPD Z3, Z2, Z17
	VMINPD Z17, Z16, Z24
	VSHUFF64X2 $0x4e, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VSHUFF64X2 $0xb1, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VPERMILPD  $0x55, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VUCOMISD   X29, X24
	JCC        fb_z4_nomin
	VCMPPD $0, Z24, Z0, K2
	VCMPPD $0, Z24, Z1, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z2, K3
	VCMPPD $0, Z24, Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	KMOVD K2, AX
	XORL    DX, DX
	TZCNTL  AX, BX
	CMOVLCS DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X26, X24
	JNE      fb_z4_clamp
	VPMOVQ2M Z0, K2
	VPMOVQ2M Z1, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z2, K3
	VPMOVQ2M Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	KMOVD K2, AX
	BTQ          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X24
	VBROADCASTSD X24, Z24
	JMP          fb_z4_clamp

fb_z4_nomin:
	VMOVAPD Z29, Z24

fb_z4_clamp:
	VSUBPD Z24, Z0, Z0
	VSUBPD Z24, Z1, Z1
	VSUBPD Z24, Z2, Z2
	VSUBPD Z24, Z3, K1, Z3
	VCMPPD $5, Z28, Z0, K2
	VMINPD Z28, Z0, Z0
	VCMPPD $5, Z28, Z1, K3
	VMINPD Z28, Z1, Z1
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z2, K3
	VMINPD Z28, Z2, Z2
	VCMPPD $5, Z28, Z3, K1, K4
	VMINPD Z28, Z3, K1, Z3
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	KMOVD K2, AX
	ORL AX, 0(R13)
	ADDQ $4, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_z4_byte

fb_z4_done:
	VMOVUPD Z0, 0(SI)
	VMOVUPD Z1, 64(SI)
	VMOVUPD Z2, 128(SI)
	VMOVUPD Z3, K1, 192(SI)
	VZEROUPPER
	RET

fb_z4_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Z28
	JMP          fb_z4_row

fb_z5:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -32(CX), DX
	MOVL         $1, AX
	SHLXL        DX, AX, AX
	DECL         AX
	KMOVB        AX, K1
	MOVQ         $0x547d42aea2879f2e, AX
	VPBROADCASTQ AX, Z29
	VMOVUPD      0(SI), Z0
	VMOVUPD      64(SI), Z1
	VMOVUPD      128(SI), Z2
	VMOVUPD      192(SI), Z3
	VMOVAPD      Z29, Z4
	VMOVUPD      256(SI), K1, Z4
	VPXORQ       X26, X26, X26
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_z5_done

fb_z5_byte:
	CMPQ         R9, $2000
	JA           fb_z5_row
	JEQ          fb_z5_steady
	VCVTSI2SDQ   R9, X26, X27
	VMULSD       fbDPConst<>+0(SB), X27, X27
	VADDSD       fbDPConst<>+8(SB), X27, X27
	VMULSD       blockSwitchBitcost+120(FP), X27, X27
	VBROADCASTSD X27, Z28

fb_z5_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Z0, Z0
	VADDPD 64(DI)(BX*8), Z1, Z1
	VADDPD 128(DI)(BX*8), Z2, Z2
	VADDPD 192(DI)(BX*8), Z3, Z3
	VADDPD 256(DI)(BX*8), Z4, K1, Z4
	VMINPD Z1, Z0, Z16
	VMINPD Z3, Z2, Z17
	VMINPD Z17, Z16, Z16
	VMINPD Z4, Z16, Z24
	VSHUFF64X2 $0x4e, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VSHUFF64X2 $0xb1, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VPERMILPD  $0x55, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VUCOMISD   X29, X24
	JCC        fb_z5_nomin
	VCMPPD $0, Z24, Z0, K2
	VCMPPD $0, Z24, Z1, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z2, K3
	VCMPPD $0, Z24, Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $0, Z24, Z4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	XORL    DX, DX
	TZCNTQ  AX, BX
	CMOVQCS DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X26, X24
	JNE      fb_z5_clamp
	VPMOVQ2M Z0, K2
	VPMOVQ2M Z1, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z2, K3
	VPMOVQ2M Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VPMOVQ2M Z4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	BTQ          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X24
	VBROADCASTSD X24, Z24
	JMP          fb_z5_clamp

fb_z5_nomin:
	VMOVAPD Z29, Z24

fb_z5_clamp:
	VSUBPD Z24, Z0, Z0
	VSUBPD Z24, Z1, Z1
	VSUBPD Z24, Z2, Z2
	VSUBPD Z24, Z3, Z3
	VSUBPD Z24, Z4, K1, Z4
	VCMPPD $5, Z28, Z0, K2
	VMINPD Z28, Z0, Z0
	VCMPPD $5, Z28, Z1, K3
	VMINPD Z28, Z1, Z1
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z2, K3
	VMINPD Z28, Z2, Z2
	VCMPPD $5, Z28, Z3, K4
	VMINPD Z28, Z3, Z3
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $5, Z28, Z4, K1, K3
	VMINPD Z28, Z4, K1, Z4
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	ORL AX, 0(R13)
	SHRQ $32, AX
	ORB AX, 4(R13)
	ADDQ $5, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_z5_byte

fb_z5_done:
	VMOVUPD Z0, 0(SI)
	VMOVUPD Z1, 64(SI)
	VMOVUPD Z2, 128(SI)
	VMOVUPD Z3, 192(SI)
	VMOVUPD Z4, K1, 256(SI)
	VZEROUPPER
	RET

fb_z5_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Z28
	JMP          fb_z5_row

fb_z6:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -40(CX), DX
	MOVL         $1, AX
	SHLXL        DX, AX, AX
	DECL         AX
	KMOVB        AX, K1
	MOVQ         $0x547d42aea2879f2e, AX
	VPBROADCASTQ AX, Z29
	VMOVUPD      0(SI), Z0
	VMOVUPD      64(SI), Z1
	VMOVUPD      128(SI), Z2
	VMOVUPD      192(SI), Z3
	VMOVUPD      256(SI), Z4
	VMOVAPD      Z29, Z5
	VMOVUPD      320(SI), K1, Z5
	VPXORQ       X26, X26, X26
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_z6_done

fb_z6_byte:
	CMPQ         R9, $2000
	JA           fb_z6_row
	JEQ          fb_z6_steady
	VCVTSI2SDQ   R9, X26, X27
	VMULSD       fbDPConst<>+0(SB), X27, X27
	VADDSD       fbDPConst<>+8(SB), X27, X27
	VMULSD       blockSwitchBitcost+120(FP), X27, X27
	VBROADCASTSD X27, Z28

fb_z6_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Z0, Z0
	VADDPD 64(DI)(BX*8), Z1, Z1
	VADDPD 128(DI)(BX*8), Z2, Z2
	VADDPD 192(DI)(BX*8), Z3, Z3
	VADDPD 256(DI)(BX*8), Z4, Z4
	VADDPD 320(DI)(BX*8), Z5, K1, Z5
	VMINPD Z1, Z0, Z16
	VMINPD Z3, Z2, Z17
	VMINPD Z5, Z4, Z18
	VMINPD Z17, Z16, Z16
	VMINPD Z18, Z16, Z24
	VSHUFF64X2 $0x4e, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VSHUFF64X2 $0xb1, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VPERMILPD  $0x55, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VUCOMISD   X29, X24
	JCC        fb_z6_nomin
	VCMPPD $0, Z24, Z0, K2
	VCMPPD $0, Z24, Z1, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z2, K3
	VCMPPD $0, Z24, Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $0, Z24, Z4, K3
	VCMPPD $0, Z24, Z5, K4
	KUNPCKBW K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	XORL    DX, DX
	TZCNTQ  AX, BX
	CMOVQCS DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X26, X24
	JNE      fb_z6_clamp
	VPMOVQ2M Z0, K2
	VPMOVQ2M Z1, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z2, K3
	VPMOVQ2M Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VPMOVQ2M Z4, K3
	VPMOVQ2M Z5, K4
	KUNPCKBW K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	BTQ          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X24
	VBROADCASTSD X24, Z24
	JMP          fb_z6_clamp

fb_z6_nomin:
	VMOVAPD Z29, Z24

fb_z6_clamp:
	VSUBPD Z24, Z0, Z0
	VSUBPD Z24, Z1, Z1
	VSUBPD Z24, Z2, Z2
	VSUBPD Z24, Z3, Z3
	VSUBPD Z24, Z4, Z4
	VSUBPD Z24, Z5, K1, Z5
	VCMPPD $5, Z28, Z0, K2
	VMINPD Z28, Z0, Z0
	VCMPPD $5, Z28, Z1, K3
	VMINPD Z28, Z1, Z1
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z2, K3
	VMINPD Z28, Z2, Z2
	VCMPPD $5, Z28, Z3, K4
	VMINPD Z28, Z3, Z3
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $5, Z28, Z4, K3
	VMINPD Z28, Z4, Z4
	VCMPPD $5, Z28, Z5, K1, K4
	VMINPD Z28, Z5, K1, Z5
	KUNPCKBW K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	ORL AX, 0(R13)
	SHRQ $32, AX
	ORW AX, 4(R13)
	ADDQ $6, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_z6_byte

fb_z6_done:
	VMOVUPD Z0, 0(SI)
	VMOVUPD Z1, 64(SI)
	VMOVUPD Z2, 128(SI)
	VMOVUPD Z3, 192(SI)
	VMOVUPD Z4, 256(SI)
	VMOVUPD Z5, K1, 320(SI)
	VZEROUPPER
	RET

fb_z6_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Z28
	JMP          fb_z6_row

fb_z7:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -48(CX), DX
	MOVL         $1, AX
	SHLXL        DX, AX, AX
	DECL         AX
	KMOVB        AX, K1
	MOVQ         $0x547d42aea2879f2e, AX
	VPBROADCASTQ AX, Z29
	VMOVUPD      0(SI), Z0
	VMOVUPD      64(SI), Z1
	VMOVUPD      128(SI), Z2
	VMOVUPD      192(SI), Z3
	VMOVUPD      256(SI), Z4
	VMOVUPD      320(SI), Z5
	VMOVAPD      Z29, Z6
	VMOVUPD      384(SI), K1, Z6
	VPXORQ       X26, X26, X26
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_z7_done

fb_z7_byte:
	CMPQ         R9, $2000
	JA           fb_z7_row
	JEQ          fb_z7_steady
	VCVTSI2SDQ   R9, X26, X27
	VMULSD       fbDPConst<>+0(SB), X27, X27
	VADDSD       fbDPConst<>+8(SB), X27, X27
	VMULSD       blockSwitchBitcost+120(FP), X27, X27
	VBROADCASTSD X27, Z28

fb_z7_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Z0, Z0
	VADDPD 64(DI)(BX*8), Z1, Z1
	VADDPD 128(DI)(BX*8), Z2, Z2
	VADDPD 192(DI)(BX*8), Z3, Z3
	VADDPD 256(DI)(BX*8), Z4, Z4
	VADDPD 320(DI)(BX*8), Z5, Z5
	VADDPD 384(DI)(BX*8), Z6, K1, Z6
	VMINPD Z1, Z0, Z16
	VMINPD Z3, Z2, Z17
	VMINPD Z5, Z4, Z18
	VMINPD Z17, Z16, Z16
	VMINPD Z6, Z18, Z18
	VMINPD Z18, Z16, Z24
	VSHUFF64X2 $0x4e, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VSHUFF64X2 $0xb1, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VPERMILPD  $0x55, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VUCOMISD   X29, X24
	JCC        fb_z7_nomin
	VCMPPD $0, Z24, Z0, K2
	VCMPPD $0, Z24, Z1, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z2, K3
	VCMPPD $0, Z24, Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $0, Z24, Z4, K3
	VCMPPD $0, Z24, Z5, K4
	KUNPCKBW K3, K4, K3
	VCMPPD $0, Z24, Z6, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	XORL    DX, DX
	TZCNTQ  AX, BX
	CMOVQCS DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X26, X24
	JNE      fb_z7_clamp
	VPMOVQ2M Z0, K2
	VPMOVQ2M Z1, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z2, K3
	VPMOVQ2M Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VPMOVQ2M Z4, K3
	VPMOVQ2M Z5, K4
	KUNPCKBW K3, K4, K3
	VPMOVQ2M Z6, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	BTQ          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X24
	VBROADCASTSD X24, Z24
	JMP          fb_z7_clamp

fb_z7_nomin:
	VMOVAPD Z29, Z24

fb_z7_clamp:
	VSUBPD Z24, Z0, Z0
	VSUBPD Z24, Z1, Z1
	VSUBPD Z24, Z2, Z2
	VSUBPD Z24, Z3, Z3
	VSUBPD Z24, Z4, Z4
	VSUBPD Z24, Z5, Z5
	VSUBPD Z24, Z6, K1, Z6
	VCMPPD $5, Z28, Z0, K2
	VMINPD Z28, Z0, Z0
	VCMPPD $5, Z28, Z1, K3
	VMINPD Z28, Z1, Z1
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z2, K3
	VMINPD Z28, Z2, Z2
	VCMPPD $5, Z28, Z3, K4
	VMINPD Z28, Z3, Z3
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $5, Z28, Z4, K3
	VMINPD Z28, Z4, Z4
	VCMPPD $5, Z28, Z5, K4
	VMINPD Z28, Z5, Z5
	KUNPCKBW K3, K4, K3
	VCMPPD $5, Z28, Z6, K1, K4
	VMINPD Z28, Z6, K1, Z6
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	ORL AX, 0(R13)
	SHRQ $32, AX
	ORW AX, 4(R13)
	SHRQ $16, AX
	ORB AX, 6(R13)
	ADDQ $7, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_z7_byte

fb_z7_done:
	VMOVUPD Z0, 0(SI)
	VMOVUPD Z1, 64(SI)
	VMOVUPD Z2, 128(SI)
	VMOVUPD Z3, 192(SI)
	VMOVUPD Z4, 256(SI)
	VMOVUPD Z5, 320(SI)
	VMOVUPD Z6, K1, 384(SI)
	VZEROUPPER
	RET

fb_z7_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Z28
	JMP          fb_z7_row

fb_z8:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -56(CX), DX
	MOVL         $1, AX
	SHLXL        DX, AX, AX
	DECL         AX
	KMOVB        AX, K1
	MOVQ         $0x547d42aea2879f2e, AX
	VPBROADCASTQ AX, Z29
	VMOVUPD      0(SI), Z0
	VMOVUPD      64(SI), Z1
	VMOVUPD      128(SI), Z2
	VMOVUPD      192(SI), Z3
	VMOVUPD      256(SI), Z4
	VMOVUPD      320(SI), Z5
	VMOVUPD      384(SI), Z6
	VMOVAPD      Z29, Z7
	VMOVUPD      448(SI), K1, Z7
	VPXORQ       X26, X26, X26
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_z8_done

fb_z8_byte:
	CMPQ         R9, $2000
	JA           fb_z8_row
	JEQ          fb_z8_steady
	VCVTSI2SDQ   R9, X26, X27
	VMULSD       fbDPConst<>+0(SB), X27, X27
	VADDSD       fbDPConst<>+8(SB), X27, X27
	VMULSD       blockSwitchBitcost+120(FP), X27, X27
	VBROADCASTSD X27, Z28

fb_z8_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Z0, Z0
	VADDPD 64(DI)(BX*8), Z1, Z1
	VADDPD 128(DI)(BX*8), Z2, Z2
	VADDPD 192(DI)(BX*8), Z3, Z3
	VADDPD 256(DI)(BX*8), Z4, Z4
	VADDPD 320(DI)(BX*8), Z5, Z5
	VADDPD 384(DI)(BX*8), Z6, Z6
	VADDPD 448(DI)(BX*8), Z7, K1, Z7
	VMINPD Z1, Z0, Z16
	VMINPD Z3, Z2, Z17
	VMINPD Z5, Z4, Z18
	VMINPD Z7, Z6, Z19
	VMINPD Z17, Z16, Z16
	VMINPD Z19, Z18, Z18
	VMINPD Z18, Z16, Z24
	VSHUFF64X2 $0x4e, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VSHUFF64X2 $0xb1, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VPERMILPD  $0x55, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VUCOMISD   X29, X24
	JCC        fb_z8_nomin
	VCMPPD $0, Z24, Z0, K2
	VCMPPD $0, Z24, Z1, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z2, K3
	VCMPPD $0, Z24, Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $0, Z24, Z4, K3
	VCMPPD $0, Z24, Z5, K4
	KUNPCKBW K3, K4, K3
	VCMPPD $0, Z24, Z6, K4
	VCMPPD $0, Z24, Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	XORL    DX, DX
	TZCNTQ  AX, BX
	CMOVQCS DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X26, X24
	JNE      fb_z8_clamp
	VPMOVQ2M Z0, K2
	VPMOVQ2M Z1, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z2, K3
	VPMOVQ2M Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VPMOVQ2M Z4, K3
	VPMOVQ2M Z5, K4
	KUNPCKBW K3, K4, K3
	VPMOVQ2M Z6, K4
	VPMOVQ2M Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	BTQ          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X24
	VBROADCASTSD X24, Z24
	JMP          fb_z8_clamp

fb_z8_nomin:
	VMOVAPD Z29, Z24

fb_z8_clamp:
	VSUBPD Z24, Z0, Z0
	VSUBPD Z24, Z1, Z1
	VSUBPD Z24, Z2, Z2
	VSUBPD Z24, Z3, Z3
	VSUBPD Z24, Z4, Z4
	VSUBPD Z24, Z5, Z5
	VSUBPD Z24, Z6, Z6
	VSUBPD Z24, Z7, K1, Z7
	VCMPPD $5, Z28, Z0, K2
	VMINPD Z28, Z0, Z0
	VCMPPD $5, Z28, Z1, K3
	VMINPD Z28, Z1, Z1
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z2, K3
	VMINPD Z28, Z2, Z2
	VCMPPD $5, Z28, Z3, K4
	VMINPD Z28, Z3, Z3
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $5, Z28, Z4, K3
	VMINPD Z28, Z4, Z4
	VCMPPD $5, Z28, Z5, K4
	VMINPD Z28, Z5, Z5
	KUNPCKBW K3, K4, K3
	VCMPPD $5, Z28, Z6, K4
	VMINPD Z28, Z6, Z6
	VCMPPD $5, Z28, Z7, K1, K5
	VMINPD Z28, Z7, K1, Z7
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	ORQ AX, 0(R13)
	ADDQ $8, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_z8_byte

fb_z8_done:
	VMOVUPD Z0, 0(SI)
	VMOVUPD Z1, 64(SI)
	VMOVUPD Z2, 128(SI)
	VMOVUPD Z3, 192(SI)
	VMOVUPD Z4, 256(SI)
	VMOVUPD Z5, 320(SI)
	VMOVUPD Z6, 384(SI)
	VMOVUPD Z7, K1, 448(SI)
	VZEROUPPER
	RET

fb_z8_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Z28
	JMP          fb_z8_row

fb_z9:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -64(CX), DX
	MOVL         $1, AX
	SHLXL        DX, AX, AX
	DECL         AX
	KMOVB        AX, K1
	MOVQ         $0x547d42aea2879f2e, AX
	VPBROADCASTQ AX, Z29
	VMOVUPD      0(SI), Z0
	VMOVUPD      64(SI), Z1
	VMOVUPD      128(SI), Z2
	VMOVUPD      192(SI), Z3
	VMOVUPD      256(SI), Z4
	VMOVUPD      320(SI), Z5
	VMOVUPD      384(SI), Z6
	VMOVUPD      448(SI), Z7
	VMOVAPD      Z29, Z8
	VMOVUPD      512(SI), K1, Z8
	VPXORQ       X26, X26, X26
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_z9_done

fb_z9_byte:
	CMPQ         R9, $2000
	JA           fb_z9_row
	JEQ          fb_z9_steady
	VCVTSI2SDQ   R9, X26, X27
	VMULSD       fbDPConst<>+0(SB), X27, X27
	VADDSD       fbDPConst<>+8(SB), X27, X27
	VMULSD       blockSwitchBitcost+120(FP), X27, X27
	VBROADCASTSD X27, Z28

fb_z9_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Z0, Z0
	VADDPD 64(DI)(BX*8), Z1, Z1
	VADDPD 128(DI)(BX*8), Z2, Z2
	VADDPD 192(DI)(BX*8), Z3, Z3
	VADDPD 256(DI)(BX*8), Z4, Z4
	VADDPD 320(DI)(BX*8), Z5, Z5
	VADDPD 384(DI)(BX*8), Z6, Z6
	VADDPD 448(DI)(BX*8), Z7, Z7
	VADDPD 512(DI)(BX*8), Z8, K1, Z8
	VMINPD Z1, Z0, Z16
	VMINPD Z3, Z2, Z17
	VMINPD Z5, Z4, Z18
	VMINPD Z7, Z6, Z19
	VMINPD Z17, Z16, Z16
	VMINPD Z19, Z18, Z18
	VMINPD Z18, Z16, Z16
	VMINPD Z8, Z16, Z24
	VSHUFF64X2 $0x4e, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VSHUFF64X2 $0xb1, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VPERMILPD  $0x55, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VUCOMISD   X29, X24
	JCC        fb_z9_nomin
	VCMPPD $0, Z24, Z0, K2
	VCMPPD $0, Z24, Z1, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z2, K3
	VCMPPD $0, Z24, Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $0, Z24, Z4, K3
	VCMPPD $0, Z24, Z5, K4
	KUNPCKBW K3, K4, K3
	VCMPPD $0, Z24, Z6, K4
	VCMPPD $0, Z24, Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VCMPPD $0, Z24, Z8, K2
	KMOVD K2, DX
	TZCNTQ  DX, DX
	ADDQ    $64, DX
	TZCNTQ  AX, BX
	CMOVQCS DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X26, X24
	JNE      fb_z9_clamp
	VPMOVQ2M Z0, K2
	VPMOVQ2M Z1, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z2, K3
	VPMOVQ2M Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VPMOVQ2M Z4, K3
	VPMOVQ2M Z5, K4
	KUNPCKBW K3, K4, K3
	VPMOVQ2M Z6, K4
	VPMOVQ2M Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VPMOVQ2M Z8, K2
	KMOVD K2, DX
	CMPQ    BX, $64
	CMOVQCC DX, AX
	BTQ          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X24
	VBROADCASTSD X24, Z24
	JMP          fb_z9_clamp

fb_z9_nomin:
	VMOVAPD Z29, Z24

fb_z9_clamp:
	VSUBPD Z24, Z0, Z0
	VSUBPD Z24, Z1, Z1
	VSUBPD Z24, Z2, Z2
	VSUBPD Z24, Z3, Z3
	VSUBPD Z24, Z4, Z4
	VSUBPD Z24, Z5, Z5
	VSUBPD Z24, Z6, Z6
	VSUBPD Z24, Z7, Z7
	VSUBPD Z24, Z8, K1, Z8
	VCMPPD $5, Z28, Z0, K2
	VMINPD Z28, Z0, Z0
	VCMPPD $5, Z28, Z1, K3
	VMINPD Z28, Z1, Z1
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z2, K3
	VMINPD Z28, Z2, Z2
	VCMPPD $5, Z28, Z3, K4
	VMINPD Z28, Z3, Z3
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $5, Z28, Z4, K3
	VMINPD Z28, Z4, Z4
	VCMPPD $5, Z28, Z5, K4
	VMINPD Z28, Z5, Z5
	KUNPCKBW K3, K4, K3
	VCMPPD $5, Z28, Z6, K4
	VMINPD Z28, Z6, Z6
	VCMPPD $5, Z28, Z7, K5
	VMINPD Z28, Z7, Z7
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VCMPPD $5, Z28, Z8, K1, K2
	VMINPD Z28, Z8, K1, Z8
	KMOVD K2, DX
	ORQ AX, 0(R13)
	ORB DX, 8(R13)
	ADDQ $9, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_z9_byte

fb_z9_done:
	VMOVUPD Z0, 0(SI)
	VMOVUPD Z1, 64(SI)
	VMOVUPD Z2, 128(SI)
	VMOVUPD Z3, 192(SI)
	VMOVUPD Z4, 256(SI)
	VMOVUPD Z5, 320(SI)
	VMOVUPD Z6, 384(SI)
	VMOVUPD Z7, 448(SI)
	VMOVUPD Z8, K1, 512(SI)
	VZEROUPPER
	RET

fb_z9_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Z28
	JMP          fb_z9_row

fb_z10:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -72(CX), DX
	MOVL         $1, AX
	SHLXL        DX, AX, AX
	DECL         AX
	KMOVB        AX, K1
	MOVQ         $0x547d42aea2879f2e, AX
	VPBROADCASTQ AX, Z29
	VMOVUPD      0(SI), Z0
	VMOVUPD      64(SI), Z1
	VMOVUPD      128(SI), Z2
	VMOVUPD      192(SI), Z3
	VMOVUPD      256(SI), Z4
	VMOVUPD      320(SI), Z5
	VMOVUPD      384(SI), Z6
	VMOVUPD      448(SI), Z7
	VMOVUPD      512(SI), Z8
	VMOVAPD      Z29, Z9
	VMOVUPD      576(SI), K1, Z9
	VPXORQ       X26, X26, X26
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_z10_done

fb_z10_byte:
	CMPQ         R9, $2000
	JA           fb_z10_row
	JEQ          fb_z10_steady
	VCVTSI2SDQ   R9, X26, X27
	VMULSD       fbDPConst<>+0(SB), X27, X27
	VADDSD       fbDPConst<>+8(SB), X27, X27
	VMULSD       blockSwitchBitcost+120(FP), X27, X27
	VBROADCASTSD X27, Z28

fb_z10_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Z0, Z0
	VADDPD 64(DI)(BX*8), Z1, Z1
	VADDPD 128(DI)(BX*8), Z2, Z2
	VADDPD 192(DI)(BX*8), Z3, Z3
	VADDPD 256(DI)(BX*8), Z4, Z4
	VADDPD 320(DI)(BX*8), Z5, Z5
	VADDPD 384(DI)(BX*8), Z6, Z6
	VADDPD 448(DI)(BX*8), Z7, Z7
	VADDPD 512(DI)(BX*8), Z8, Z8
	VADDPD 576(DI)(BX*8), Z9, K1, Z9
	VMINPD Z1, Z0, Z16
	VMINPD Z3, Z2, Z17
	VMINPD Z5, Z4, Z18
	VMINPD Z7, Z6, Z19
	VMINPD Z9, Z8, Z20
	VMINPD Z17, Z16, Z16
	VMINPD Z19, Z18, Z18
	VMINPD Z18, Z16, Z16
	VMINPD Z20, Z16, Z24
	VSHUFF64X2 $0x4e, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VSHUFF64X2 $0xb1, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VPERMILPD  $0x55, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VUCOMISD   X29, X24
	JCC        fb_z10_nomin
	VCMPPD $0, Z24, Z0, K2
	VCMPPD $0, Z24, Z1, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z2, K3
	VCMPPD $0, Z24, Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $0, Z24, Z4, K3
	VCMPPD $0, Z24, Z5, K4
	KUNPCKBW K3, K4, K3
	VCMPPD $0, Z24, Z6, K4
	VCMPPD $0, Z24, Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VCMPPD $0, Z24, Z8, K2
	VCMPPD $0, Z24, Z9, K3
	KUNPCKBW K2, K3, K2
	KMOVD K2, DX
	TZCNTQ  DX, DX
	ADDQ    $64, DX
	TZCNTQ  AX, BX
	CMOVQCS DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X26, X24
	JNE      fb_z10_clamp
	VPMOVQ2M Z0, K2
	VPMOVQ2M Z1, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z2, K3
	VPMOVQ2M Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VPMOVQ2M Z4, K3
	VPMOVQ2M Z5, K4
	KUNPCKBW K3, K4, K3
	VPMOVQ2M Z6, K4
	VPMOVQ2M Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VPMOVQ2M Z8, K2
	VPMOVQ2M Z9, K3
	KUNPCKBW K2, K3, K2
	KMOVD K2, DX
	CMPQ    BX, $64
	CMOVQCC DX, AX
	BTQ          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X24
	VBROADCASTSD X24, Z24
	JMP          fb_z10_clamp

fb_z10_nomin:
	VMOVAPD Z29, Z24

fb_z10_clamp:
	VSUBPD Z24, Z0, Z0
	VSUBPD Z24, Z1, Z1
	VSUBPD Z24, Z2, Z2
	VSUBPD Z24, Z3, Z3
	VSUBPD Z24, Z4, Z4
	VSUBPD Z24, Z5, Z5
	VSUBPD Z24, Z6, Z6
	VSUBPD Z24, Z7, Z7
	VSUBPD Z24, Z8, Z8
	VSUBPD Z24, Z9, K1, Z9
	VCMPPD $5, Z28, Z0, K2
	VMINPD Z28, Z0, Z0
	VCMPPD $5, Z28, Z1, K3
	VMINPD Z28, Z1, Z1
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z2, K3
	VMINPD Z28, Z2, Z2
	VCMPPD $5, Z28, Z3, K4
	VMINPD Z28, Z3, Z3
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $5, Z28, Z4, K3
	VMINPD Z28, Z4, Z4
	VCMPPD $5, Z28, Z5, K4
	VMINPD Z28, Z5, Z5
	KUNPCKBW K3, K4, K3
	VCMPPD $5, Z28, Z6, K4
	VMINPD Z28, Z6, Z6
	VCMPPD $5, Z28, Z7, K5
	VMINPD Z28, Z7, Z7
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VCMPPD $5, Z28, Z8, K2
	VMINPD Z28, Z8, Z8
	VCMPPD $5, Z28, Z9, K1, K3
	VMINPD Z28, Z9, K1, Z9
	KUNPCKBW K2, K3, K2
	KMOVD K2, DX
	ORQ AX, 0(R13)
	ORW DX, 8(R13)
	ADDQ $10, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_z10_byte

fb_z10_done:
	VMOVUPD Z0, 0(SI)
	VMOVUPD Z1, 64(SI)
	VMOVUPD Z2, 128(SI)
	VMOVUPD Z3, 192(SI)
	VMOVUPD Z4, 256(SI)
	VMOVUPD Z5, 320(SI)
	VMOVUPD Z6, 384(SI)
	VMOVUPD Z7, 448(SI)
	VMOVUPD Z8, 512(SI)
	VMOVUPD Z9, K1, 576(SI)
	VZEROUPPER
	RET

fb_z10_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Z28
	JMP          fb_z10_row

fb_z11:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -80(CX), DX
	MOVL         $1, AX
	SHLXL        DX, AX, AX
	DECL         AX
	KMOVB        AX, K1
	MOVQ         $0x547d42aea2879f2e, AX
	VPBROADCASTQ AX, Z29
	VMOVUPD      0(SI), Z0
	VMOVUPD      64(SI), Z1
	VMOVUPD      128(SI), Z2
	VMOVUPD      192(SI), Z3
	VMOVUPD      256(SI), Z4
	VMOVUPD      320(SI), Z5
	VMOVUPD      384(SI), Z6
	VMOVUPD      448(SI), Z7
	VMOVUPD      512(SI), Z8
	VMOVUPD      576(SI), Z9
	VMOVAPD      Z29, Z10
	VMOVUPD      640(SI), K1, Z10
	VPXORQ       X26, X26, X26
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_z11_done

fb_z11_byte:
	CMPQ         R9, $2000
	JA           fb_z11_row
	JEQ          fb_z11_steady
	VCVTSI2SDQ   R9, X26, X27
	VMULSD       fbDPConst<>+0(SB), X27, X27
	VADDSD       fbDPConst<>+8(SB), X27, X27
	VMULSD       blockSwitchBitcost+120(FP), X27, X27
	VBROADCASTSD X27, Z28

fb_z11_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Z0, Z0
	VADDPD 64(DI)(BX*8), Z1, Z1
	VADDPD 128(DI)(BX*8), Z2, Z2
	VADDPD 192(DI)(BX*8), Z3, Z3
	VADDPD 256(DI)(BX*8), Z4, Z4
	VADDPD 320(DI)(BX*8), Z5, Z5
	VADDPD 384(DI)(BX*8), Z6, Z6
	VADDPD 448(DI)(BX*8), Z7, Z7
	VADDPD 512(DI)(BX*8), Z8, Z8
	VADDPD 576(DI)(BX*8), Z9, Z9
	VADDPD 640(DI)(BX*8), Z10, K1, Z10
	VMINPD Z1, Z0, Z16
	VMINPD Z3, Z2, Z17
	VMINPD Z5, Z4, Z18
	VMINPD Z7, Z6, Z19
	VMINPD Z9, Z8, Z20
	VMINPD Z17, Z16, Z16
	VMINPD Z19, Z18, Z18
	VMINPD Z10, Z20, Z20
	VMINPD Z18, Z16, Z16
	VMINPD Z20, Z16, Z24
	VSHUFF64X2 $0x4e, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VSHUFF64X2 $0xb1, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VPERMILPD  $0x55, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VUCOMISD   X29, X24
	JCC        fb_z11_nomin
	VCMPPD $0, Z24, Z0, K2
	VCMPPD $0, Z24, Z1, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z2, K3
	VCMPPD $0, Z24, Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $0, Z24, Z4, K3
	VCMPPD $0, Z24, Z5, K4
	KUNPCKBW K3, K4, K3
	VCMPPD $0, Z24, Z6, K4
	VCMPPD $0, Z24, Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VCMPPD $0, Z24, Z8, K2
	VCMPPD $0, Z24, Z9, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z10, K3
	KUNPCKWD K2, K3, K2
	KMOVD K2, DX
	TZCNTQ  DX, DX
	ADDQ    $64, DX
	TZCNTQ  AX, BX
	CMOVQCS DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X26, X24
	JNE      fb_z11_clamp
	VPMOVQ2M Z0, K2
	VPMOVQ2M Z1, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z2, K3
	VPMOVQ2M Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VPMOVQ2M Z4, K3
	VPMOVQ2M Z5, K4
	KUNPCKBW K3, K4, K3
	VPMOVQ2M Z6, K4
	VPMOVQ2M Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VPMOVQ2M Z8, K2
	VPMOVQ2M Z9, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z10, K3
	KUNPCKWD K2, K3, K2
	KMOVD K2, DX
	CMPQ    BX, $64
	CMOVQCC DX, AX
	BTQ          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X24
	VBROADCASTSD X24, Z24
	JMP          fb_z11_clamp

fb_z11_nomin:
	VMOVAPD Z29, Z24

fb_z11_clamp:
	VSUBPD Z24, Z0, Z0
	VSUBPD Z24, Z1, Z1
	VSUBPD Z24, Z2, Z2
	VSUBPD Z24, Z3, Z3
	VSUBPD Z24, Z4, Z4
	VSUBPD Z24, Z5, Z5
	VSUBPD Z24, Z6, Z6
	VSUBPD Z24, Z7, Z7
	VSUBPD Z24, Z8, Z8
	VSUBPD Z24, Z9, Z9
	VSUBPD Z24, Z10, K1, Z10
	VCMPPD $5, Z28, Z0, K2
	VMINPD Z28, Z0, Z0
	VCMPPD $5, Z28, Z1, K3
	VMINPD Z28, Z1, Z1
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z2, K3
	VMINPD Z28, Z2, Z2
	VCMPPD $5, Z28, Z3, K4
	VMINPD Z28, Z3, Z3
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $5, Z28, Z4, K3
	VMINPD Z28, Z4, Z4
	VCMPPD $5, Z28, Z5, K4
	VMINPD Z28, Z5, Z5
	KUNPCKBW K3, K4, K3
	VCMPPD $5, Z28, Z6, K4
	VMINPD Z28, Z6, Z6
	VCMPPD $5, Z28, Z7, K5
	VMINPD Z28, Z7, Z7
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VCMPPD $5, Z28, Z8, K2
	VMINPD Z28, Z8, Z8
	VCMPPD $5, Z28, Z9, K3
	VMINPD Z28, Z9, Z9
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z10, K1, K3
	VMINPD Z28, Z10, K1, Z10
	KUNPCKWD K2, K3, K2
	KMOVD K2, DX
	ORQ AX, 0(R13)
	ORW DX, 8(R13)
	SHRQ $16, DX
	ORB DX, 10(R13)
	ADDQ $11, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_z11_byte

fb_z11_done:
	VMOVUPD Z0, 0(SI)
	VMOVUPD Z1, 64(SI)
	VMOVUPD Z2, 128(SI)
	VMOVUPD Z3, 192(SI)
	VMOVUPD Z4, 256(SI)
	VMOVUPD Z5, 320(SI)
	VMOVUPD Z6, 384(SI)
	VMOVUPD Z7, 448(SI)
	VMOVUPD Z8, 512(SI)
	VMOVUPD Z9, 576(SI)
	VMOVUPD Z10, K1, 640(SI)
	VZEROUPPER
	RET

fb_z11_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Z28
	JMP          fb_z11_row

fb_z12:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -88(CX), DX
	MOVL         $1, AX
	SHLXL        DX, AX, AX
	DECL         AX
	KMOVB        AX, K1
	MOVQ         $0x547d42aea2879f2e, AX
	VPBROADCASTQ AX, Z29
	VMOVUPD      0(SI), Z0
	VMOVUPD      64(SI), Z1
	VMOVUPD      128(SI), Z2
	VMOVUPD      192(SI), Z3
	VMOVUPD      256(SI), Z4
	VMOVUPD      320(SI), Z5
	VMOVUPD      384(SI), Z6
	VMOVUPD      448(SI), Z7
	VMOVUPD      512(SI), Z8
	VMOVUPD      576(SI), Z9
	VMOVUPD      640(SI), Z10
	VMOVAPD      Z29, Z11
	VMOVUPD      704(SI), K1, Z11
	VPXORQ       X26, X26, X26
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_z12_done

fb_z12_byte:
	CMPQ         R9, $2000
	JA           fb_z12_row
	JEQ          fb_z12_steady
	VCVTSI2SDQ   R9, X26, X27
	VMULSD       fbDPConst<>+0(SB), X27, X27
	VADDSD       fbDPConst<>+8(SB), X27, X27
	VMULSD       blockSwitchBitcost+120(FP), X27, X27
	VBROADCASTSD X27, Z28

fb_z12_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Z0, Z0
	VADDPD 64(DI)(BX*8), Z1, Z1
	VADDPD 128(DI)(BX*8), Z2, Z2
	VADDPD 192(DI)(BX*8), Z3, Z3
	VADDPD 256(DI)(BX*8), Z4, Z4
	VADDPD 320(DI)(BX*8), Z5, Z5
	VADDPD 384(DI)(BX*8), Z6, Z6
	VADDPD 448(DI)(BX*8), Z7, Z7
	VADDPD 512(DI)(BX*8), Z8, Z8
	VADDPD 576(DI)(BX*8), Z9, Z9
	VADDPD 640(DI)(BX*8), Z10, Z10
	VADDPD 704(DI)(BX*8), Z11, K1, Z11
	VMINPD Z1, Z0, Z16
	VMINPD Z3, Z2, Z17
	VMINPD Z5, Z4, Z18
	VMINPD Z7, Z6, Z19
	VMINPD Z9, Z8, Z20
	VMINPD Z11, Z10, Z21
	VMINPD Z17, Z16, Z16
	VMINPD Z19, Z18, Z18
	VMINPD Z21, Z20, Z20
	VMINPD Z18, Z16, Z16
	VMINPD Z20, Z16, Z24
	VSHUFF64X2 $0x4e, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VSHUFF64X2 $0xb1, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VPERMILPD  $0x55, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VUCOMISD   X29, X24
	JCC        fb_z12_nomin
	VCMPPD $0, Z24, Z0, K2
	VCMPPD $0, Z24, Z1, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z2, K3
	VCMPPD $0, Z24, Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $0, Z24, Z4, K3
	VCMPPD $0, Z24, Z5, K4
	KUNPCKBW K3, K4, K3
	VCMPPD $0, Z24, Z6, K4
	VCMPPD $0, Z24, Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VCMPPD $0, Z24, Z8, K2
	VCMPPD $0, Z24, Z9, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z10, K3
	VCMPPD $0, Z24, Z11, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	KMOVD K2, DX
	TZCNTQ  DX, DX
	ADDQ    $64, DX
	TZCNTQ  AX, BX
	CMOVQCS DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X26, X24
	JNE      fb_z12_clamp
	VPMOVQ2M Z0, K2
	VPMOVQ2M Z1, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z2, K3
	VPMOVQ2M Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VPMOVQ2M Z4, K3
	VPMOVQ2M Z5, K4
	KUNPCKBW K3, K4, K3
	VPMOVQ2M Z6, K4
	VPMOVQ2M Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VPMOVQ2M Z8, K2
	VPMOVQ2M Z9, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z10, K3
	VPMOVQ2M Z11, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	KMOVD K2, DX
	CMPQ    BX, $64
	CMOVQCC DX, AX
	BTQ          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X24
	VBROADCASTSD X24, Z24
	JMP          fb_z12_clamp

fb_z12_nomin:
	VMOVAPD Z29, Z24

fb_z12_clamp:
	VSUBPD Z24, Z0, Z0
	VSUBPD Z24, Z1, Z1
	VSUBPD Z24, Z2, Z2
	VSUBPD Z24, Z3, Z3
	VSUBPD Z24, Z4, Z4
	VSUBPD Z24, Z5, Z5
	VSUBPD Z24, Z6, Z6
	VSUBPD Z24, Z7, Z7
	VSUBPD Z24, Z8, Z8
	VSUBPD Z24, Z9, Z9
	VSUBPD Z24, Z10, Z10
	VSUBPD Z24, Z11, K1, Z11
	VCMPPD $5, Z28, Z0, K2
	VMINPD Z28, Z0, Z0
	VCMPPD $5, Z28, Z1, K3
	VMINPD Z28, Z1, Z1
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z2, K3
	VMINPD Z28, Z2, Z2
	VCMPPD $5, Z28, Z3, K4
	VMINPD Z28, Z3, Z3
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $5, Z28, Z4, K3
	VMINPD Z28, Z4, Z4
	VCMPPD $5, Z28, Z5, K4
	VMINPD Z28, Z5, Z5
	KUNPCKBW K3, K4, K3
	VCMPPD $5, Z28, Z6, K4
	VMINPD Z28, Z6, Z6
	VCMPPD $5, Z28, Z7, K5
	VMINPD Z28, Z7, Z7
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VCMPPD $5, Z28, Z8, K2
	VMINPD Z28, Z8, Z8
	VCMPPD $5, Z28, Z9, K3
	VMINPD Z28, Z9, Z9
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z10, K3
	VMINPD Z28, Z10, Z10
	VCMPPD $5, Z28, Z11, K1, K4
	VMINPD Z28, Z11, K1, Z11
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	KMOVD K2, DX
	ORQ AX, 0(R13)
	ORL DX, 8(R13)
	ADDQ $12, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_z12_byte

fb_z12_done:
	VMOVUPD Z0, 0(SI)
	VMOVUPD Z1, 64(SI)
	VMOVUPD Z2, 128(SI)
	VMOVUPD Z3, 192(SI)
	VMOVUPD Z4, 256(SI)
	VMOVUPD Z5, 320(SI)
	VMOVUPD Z6, 384(SI)
	VMOVUPD Z7, 448(SI)
	VMOVUPD Z8, 512(SI)
	VMOVUPD Z9, 576(SI)
	VMOVUPD Z10, 640(SI)
	VMOVUPD Z11, K1, 704(SI)
	VZEROUPPER
	RET

fb_z12_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Z28
	JMP          fb_z12_row

fb_z13:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -96(CX), DX
	MOVL         $1, AX
	SHLXL        DX, AX, AX
	DECL         AX
	KMOVB        AX, K1
	MOVQ         $0x547d42aea2879f2e, AX
	VPBROADCASTQ AX, Z29
	VMOVUPD      0(SI), Z0
	VMOVUPD      64(SI), Z1
	VMOVUPD      128(SI), Z2
	VMOVUPD      192(SI), Z3
	VMOVUPD      256(SI), Z4
	VMOVUPD      320(SI), Z5
	VMOVUPD      384(SI), Z6
	VMOVUPD      448(SI), Z7
	VMOVUPD      512(SI), Z8
	VMOVUPD      576(SI), Z9
	VMOVUPD      640(SI), Z10
	VMOVUPD      704(SI), Z11
	VMOVAPD      Z29, Z12
	VMOVUPD      768(SI), K1, Z12
	VPXORQ       X26, X26, X26
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_z13_done

fb_z13_byte:
	CMPQ         R9, $2000
	JA           fb_z13_row
	JEQ          fb_z13_steady
	VCVTSI2SDQ   R9, X26, X27
	VMULSD       fbDPConst<>+0(SB), X27, X27
	VADDSD       fbDPConst<>+8(SB), X27, X27
	VMULSD       blockSwitchBitcost+120(FP), X27, X27
	VBROADCASTSD X27, Z28

fb_z13_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Z0, Z0
	VADDPD 64(DI)(BX*8), Z1, Z1
	VADDPD 128(DI)(BX*8), Z2, Z2
	VADDPD 192(DI)(BX*8), Z3, Z3
	VADDPD 256(DI)(BX*8), Z4, Z4
	VADDPD 320(DI)(BX*8), Z5, Z5
	VADDPD 384(DI)(BX*8), Z6, Z6
	VADDPD 448(DI)(BX*8), Z7, Z7
	VADDPD 512(DI)(BX*8), Z8, Z8
	VADDPD 576(DI)(BX*8), Z9, Z9
	VADDPD 640(DI)(BX*8), Z10, Z10
	VADDPD 704(DI)(BX*8), Z11, Z11
	VADDPD 768(DI)(BX*8), Z12, K1, Z12
	VMINPD Z1, Z0, Z16
	VMINPD Z3, Z2, Z17
	VMINPD Z5, Z4, Z18
	VMINPD Z7, Z6, Z19
	VMINPD Z9, Z8, Z20
	VMINPD Z11, Z10, Z21
	VMINPD Z17, Z16, Z16
	VMINPD Z19, Z18, Z18
	VMINPD Z21, Z20, Z20
	VMINPD Z18, Z16, Z16
	VMINPD Z12, Z20, Z20
	VMINPD Z20, Z16, Z24
	VSHUFF64X2 $0x4e, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VSHUFF64X2 $0xb1, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VPERMILPD  $0x55, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VUCOMISD   X29, X24
	JCC        fb_z13_nomin
	VCMPPD $0, Z24, Z0, K2
	VCMPPD $0, Z24, Z1, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z2, K3
	VCMPPD $0, Z24, Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $0, Z24, Z4, K3
	VCMPPD $0, Z24, Z5, K4
	KUNPCKBW K3, K4, K3
	VCMPPD $0, Z24, Z6, K4
	VCMPPD $0, Z24, Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VCMPPD $0, Z24, Z8, K2
	VCMPPD $0, Z24, Z9, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z10, K3
	VCMPPD $0, Z24, Z11, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $0, Z24, Z12, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, DX
	TZCNTQ  DX, DX
	ADDQ    $64, DX
	TZCNTQ  AX, BX
	CMOVQCS DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X26, X24
	JNE      fb_z13_clamp
	VPMOVQ2M Z0, K2
	VPMOVQ2M Z1, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z2, K3
	VPMOVQ2M Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VPMOVQ2M Z4, K3
	VPMOVQ2M Z5, K4
	KUNPCKBW K3, K4, K3
	VPMOVQ2M Z6, K4
	VPMOVQ2M Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VPMOVQ2M Z8, K2
	VPMOVQ2M Z9, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z10, K3
	VPMOVQ2M Z11, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VPMOVQ2M Z12, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, DX
	CMPQ    BX, $64
	CMOVQCC DX, AX
	BTQ          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X24
	VBROADCASTSD X24, Z24
	JMP          fb_z13_clamp

fb_z13_nomin:
	VMOVAPD Z29, Z24

fb_z13_clamp:
	VSUBPD Z24, Z0, Z0
	VSUBPD Z24, Z1, Z1
	VSUBPD Z24, Z2, Z2
	VSUBPD Z24, Z3, Z3
	VSUBPD Z24, Z4, Z4
	VSUBPD Z24, Z5, Z5
	VSUBPD Z24, Z6, Z6
	VSUBPD Z24, Z7, Z7
	VSUBPD Z24, Z8, Z8
	VSUBPD Z24, Z9, Z9
	VSUBPD Z24, Z10, Z10
	VSUBPD Z24, Z11, Z11
	VSUBPD Z24, Z12, K1, Z12
	VCMPPD $5, Z28, Z0, K2
	VMINPD Z28, Z0, Z0
	VCMPPD $5, Z28, Z1, K3
	VMINPD Z28, Z1, Z1
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z2, K3
	VMINPD Z28, Z2, Z2
	VCMPPD $5, Z28, Z3, K4
	VMINPD Z28, Z3, Z3
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $5, Z28, Z4, K3
	VMINPD Z28, Z4, Z4
	VCMPPD $5, Z28, Z5, K4
	VMINPD Z28, Z5, Z5
	KUNPCKBW K3, K4, K3
	VCMPPD $5, Z28, Z6, K4
	VMINPD Z28, Z6, Z6
	VCMPPD $5, Z28, Z7, K5
	VMINPD Z28, Z7, Z7
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VCMPPD $5, Z28, Z8, K2
	VMINPD Z28, Z8, Z8
	VCMPPD $5, Z28, Z9, K3
	VMINPD Z28, Z9, Z9
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z10, K3
	VMINPD Z28, Z10, Z10
	VCMPPD $5, Z28, Z11, K4
	VMINPD Z28, Z11, Z11
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $5, Z28, Z12, K1, K3
	VMINPD Z28, Z12, K1, Z12
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, DX
	ORQ AX, 0(R13)
	ORL DX, 8(R13)
	SHRQ $32, DX
	ORB DX, 12(R13)
	ADDQ $13, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_z13_byte

fb_z13_done:
	VMOVUPD Z0, 0(SI)
	VMOVUPD Z1, 64(SI)
	VMOVUPD Z2, 128(SI)
	VMOVUPD Z3, 192(SI)
	VMOVUPD Z4, 256(SI)
	VMOVUPD Z5, 320(SI)
	VMOVUPD Z6, 384(SI)
	VMOVUPD Z7, 448(SI)
	VMOVUPD Z8, 512(SI)
	VMOVUPD Z9, 576(SI)
	VMOVUPD Z10, 640(SI)
	VMOVUPD Z11, 704(SI)
	VMOVUPD Z12, K1, 768(SI)
	VZEROUPPER
	RET

fb_z13_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Z28
	JMP          fb_z13_row

fb_z14:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -104(CX), DX
	MOVL         $1, AX
	SHLXL        DX, AX, AX
	DECL         AX
	KMOVB        AX, K1
	MOVQ         $0x547d42aea2879f2e, AX
	VPBROADCASTQ AX, Z29
	VMOVUPD      0(SI), Z0
	VMOVUPD      64(SI), Z1
	VMOVUPD      128(SI), Z2
	VMOVUPD      192(SI), Z3
	VMOVUPD      256(SI), Z4
	VMOVUPD      320(SI), Z5
	VMOVUPD      384(SI), Z6
	VMOVUPD      448(SI), Z7
	VMOVUPD      512(SI), Z8
	VMOVUPD      576(SI), Z9
	VMOVUPD      640(SI), Z10
	VMOVUPD      704(SI), Z11
	VMOVUPD      768(SI), Z12
	VMOVAPD      Z29, Z13
	VMOVUPD      832(SI), K1, Z13
	VPXORQ       X26, X26, X26
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_z14_done

fb_z14_byte:
	CMPQ         R9, $2000
	JA           fb_z14_row
	JEQ          fb_z14_steady
	VCVTSI2SDQ   R9, X26, X27
	VMULSD       fbDPConst<>+0(SB), X27, X27
	VADDSD       fbDPConst<>+8(SB), X27, X27
	VMULSD       blockSwitchBitcost+120(FP), X27, X27
	VBROADCASTSD X27, Z28

fb_z14_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Z0, Z0
	VADDPD 64(DI)(BX*8), Z1, Z1
	VADDPD 128(DI)(BX*8), Z2, Z2
	VADDPD 192(DI)(BX*8), Z3, Z3
	VADDPD 256(DI)(BX*8), Z4, Z4
	VADDPD 320(DI)(BX*8), Z5, Z5
	VADDPD 384(DI)(BX*8), Z6, Z6
	VADDPD 448(DI)(BX*8), Z7, Z7
	VADDPD 512(DI)(BX*8), Z8, Z8
	VADDPD 576(DI)(BX*8), Z9, Z9
	VADDPD 640(DI)(BX*8), Z10, Z10
	VADDPD 704(DI)(BX*8), Z11, Z11
	VADDPD 768(DI)(BX*8), Z12, Z12
	VADDPD 832(DI)(BX*8), Z13, K1, Z13
	VMINPD Z1, Z0, Z16
	VMINPD Z3, Z2, Z17
	VMINPD Z5, Z4, Z18
	VMINPD Z7, Z6, Z19
	VMINPD Z9, Z8, Z20
	VMINPD Z11, Z10, Z21
	VMINPD Z13, Z12, Z22
	VMINPD Z17, Z16, Z16
	VMINPD Z19, Z18, Z18
	VMINPD Z21, Z20, Z20
	VMINPD Z18, Z16, Z16
	VMINPD Z22, Z20, Z20
	VMINPD Z20, Z16, Z24
	VSHUFF64X2 $0x4e, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VSHUFF64X2 $0xb1, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VPERMILPD  $0x55, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VUCOMISD   X29, X24
	JCC        fb_z14_nomin
	VCMPPD $0, Z24, Z0, K2
	VCMPPD $0, Z24, Z1, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z2, K3
	VCMPPD $0, Z24, Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $0, Z24, Z4, K3
	VCMPPD $0, Z24, Z5, K4
	KUNPCKBW K3, K4, K3
	VCMPPD $0, Z24, Z6, K4
	VCMPPD $0, Z24, Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VCMPPD $0, Z24, Z8, K2
	VCMPPD $0, Z24, Z9, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z10, K3
	VCMPPD $0, Z24, Z11, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $0, Z24, Z12, K3
	VCMPPD $0, Z24, Z13, K4
	KUNPCKBW K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, DX
	TZCNTQ  DX, DX
	ADDQ    $64, DX
	TZCNTQ  AX, BX
	CMOVQCS DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X26, X24
	JNE      fb_z14_clamp
	VPMOVQ2M Z0, K2
	VPMOVQ2M Z1, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z2, K3
	VPMOVQ2M Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VPMOVQ2M Z4, K3
	VPMOVQ2M Z5, K4
	KUNPCKBW K3, K4, K3
	VPMOVQ2M Z6, K4
	VPMOVQ2M Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VPMOVQ2M Z8, K2
	VPMOVQ2M Z9, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z10, K3
	VPMOVQ2M Z11, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VPMOVQ2M Z12, K3
	VPMOVQ2M Z13, K4
	KUNPCKBW K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, DX
	CMPQ    BX, $64
	CMOVQCC DX, AX
	BTQ          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X24
	VBROADCASTSD X24, Z24
	JMP          fb_z14_clamp

fb_z14_nomin:
	VMOVAPD Z29, Z24

fb_z14_clamp:
	VSUBPD Z24, Z0, Z0
	VSUBPD Z24, Z1, Z1
	VSUBPD Z24, Z2, Z2
	VSUBPD Z24, Z3, Z3
	VSUBPD Z24, Z4, Z4
	VSUBPD Z24, Z5, Z5
	VSUBPD Z24, Z6, Z6
	VSUBPD Z24, Z7, Z7
	VSUBPD Z24, Z8, Z8
	VSUBPD Z24, Z9, Z9
	VSUBPD Z24, Z10, Z10
	VSUBPD Z24, Z11, Z11
	VSUBPD Z24, Z12, Z12
	VSUBPD Z24, Z13, K1, Z13
	VCMPPD $5, Z28, Z0, K2
	VMINPD Z28, Z0, Z0
	VCMPPD $5, Z28, Z1, K3
	VMINPD Z28, Z1, Z1
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z2, K3
	VMINPD Z28, Z2, Z2
	VCMPPD $5, Z28, Z3, K4
	VMINPD Z28, Z3, Z3
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $5, Z28, Z4, K3
	VMINPD Z28, Z4, Z4
	VCMPPD $5, Z28, Z5, K4
	VMINPD Z28, Z5, Z5
	KUNPCKBW K3, K4, K3
	VCMPPD $5, Z28, Z6, K4
	VMINPD Z28, Z6, Z6
	VCMPPD $5, Z28, Z7, K5
	VMINPD Z28, Z7, Z7
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VCMPPD $5, Z28, Z8, K2
	VMINPD Z28, Z8, Z8
	VCMPPD $5, Z28, Z9, K3
	VMINPD Z28, Z9, Z9
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z10, K3
	VMINPD Z28, Z10, Z10
	VCMPPD $5, Z28, Z11, K4
	VMINPD Z28, Z11, Z11
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $5, Z28, Z12, K3
	VMINPD Z28, Z12, Z12
	VCMPPD $5, Z28, Z13, K1, K4
	VMINPD Z28, Z13, K1, Z13
	KUNPCKBW K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, DX
	ORQ AX, 0(R13)
	ORL DX, 8(R13)
	SHRQ $32, DX
	ORW DX, 12(R13)
	ADDQ $14, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_z14_byte

fb_z14_done:
	VMOVUPD Z0, 0(SI)
	VMOVUPD Z1, 64(SI)
	VMOVUPD Z2, 128(SI)
	VMOVUPD Z3, 192(SI)
	VMOVUPD Z4, 256(SI)
	VMOVUPD Z5, 320(SI)
	VMOVUPD Z6, 384(SI)
	VMOVUPD Z7, 448(SI)
	VMOVUPD Z8, 512(SI)
	VMOVUPD Z9, 576(SI)
	VMOVUPD Z10, 640(SI)
	VMOVUPD Z11, 704(SI)
	VMOVUPD Z12, 768(SI)
	VMOVUPD Z13, K1, 832(SI)
	VZEROUPPER
	RET

fb_z14_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Z28
	JMP          fb_z14_row

fb_z15:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -112(CX), DX
	MOVL         $1, AX
	SHLXL        DX, AX, AX
	DECL         AX
	KMOVB        AX, K1
	MOVQ         $0x547d42aea2879f2e, AX
	VPBROADCASTQ AX, Z29
	VMOVUPD      0(SI), Z0
	VMOVUPD      64(SI), Z1
	VMOVUPD      128(SI), Z2
	VMOVUPD      192(SI), Z3
	VMOVUPD      256(SI), Z4
	VMOVUPD      320(SI), Z5
	VMOVUPD      384(SI), Z6
	VMOVUPD      448(SI), Z7
	VMOVUPD      512(SI), Z8
	VMOVUPD      576(SI), Z9
	VMOVUPD      640(SI), Z10
	VMOVUPD      704(SI), Z11
	VMOVUPD      768(SI), Z12
	VMOVUPD      832(SI), Z13
	VMOVAPD      Z29, Z14
	VMOVUPD      896(SI), K1, Z14
	VPXORQ       X26, X26, X26
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_z15_done

fb_z15_byte:
	CMPQ         R9, $2000
	JA           fb_z15_row
	JEQ          fb_z15_steady
	VCVTSI2SDQ   R9, X26, X27
	VMULSD       fbDPConst<>+0(SB), X27, X27
	VADDSD       fbDPConst<>+8(SB), X27, X27
	VMULSD       blockSwitchBitcost+120(FP), X27, X27
	VBROADCASTSD X27, Z28

fb_z15_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Z0, Z0
	VADDPD 64(DI)(BX*8), Z1, Z1
	VADDPD 128(DI)(BX*8), Z2, Z2
	VADDPD 192(DI)(BX*8), Z3, Z3
	VADDPD 256(DI)(BX*8), Z4, Z4
	VADDPD 320(DI)(BX*8), Z5, Z5
	VADDPD 384(DI)(BX*8), Z6, Z6
	VADDPD 448(DI)(BX*8), Z7, Z7
	VADDPD 512(DI)(BX*8), Z8, Z8
	VADDPD 576(DI)(BX*8), Z9, Z9
	VADDPD 640(DI)(BX*8), Z10, Z10
	VADDPD 704(DI)(BX*8), Z11, Z11
	VADDPD 768(DI)(BX*8), Z12, Z12
	VADDPD 832(DI)(BX*8), Z13, Z13
	VADDPD 896(DI)(BX*8), Z14, K1, Z14
	VMINPD Z1, Z0, Z16
	VMINPD Z3, Z2, Z17
	VMINPD Z5, Z4, Z18
	VMINPD Z7, Z6, Z19
	VMINPD Z9, Z8, Z20
	VMINPD Z11, Z10, Z21
	VMINPD Z13, Z12, Z22
	VMINPD Z17, Z16, Z16
	VMINPD Z19, Z18, Z18
	VMINPD Z21, Z20, Z20
	VMINPD Z14, Z22, Z22
	VMINPD Z18, Z16, Z16
	VMINPD Z22, Z20, Z20
	VMINPD Z20, Z16, Z24
	VSHUFF64X2 $0x4e, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VSHUFF64X2 $0xb1, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VPERMILPD  $0x55, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VUCOMISD   X29, X24
	JCC        fb_z15_nomin
	VCMPPD $0, Z24, Z0, K2
	VCMPPD $0, Z24, Z1, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z2, K3
	VCMPPD $0, Z24, Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $0, Z24, Z4, K3
	VCMPPD $0, Z24, Z5, K4
	KUNPCKBW K3, K4, K3
	VCMPPD $0, Z24, Z6, K4
	VCMPPD $0, Z24, Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VCMPPD $0, Z24, Z8, K2
	VCMPPD $0, Z24, Z9, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z10, K3
	VCMPPD $0, Z24, Z11, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $0, Z24, Z12, K3
	VCMPPD $0, Z24, Z13, K4
	KUNPCKBW K3, K4, K3
	VCMPPD $0, Z24, Z14, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, DX
	TZCNTQ  DX, DX
	ADDQ    $64, DX
	TZCNTQ  AX, BX
	CMOVQCS DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X26, X24
	JNE      fb_z15_clamp
	VPMOVQ2M Z0, K2
	VPMOVQ2M Z1, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z2, K3
	VPMOVQ2M Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VPMOVQ2M Z4, K3
	VPMOVQ2M Z5, K4
	KUNPCKBW K3, K4, K3
	VPMOVQ2M Z6, K4
	VPMOVQ2M Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VPMOVQ2M Z8, K2
	VPMOVQ2M Z9, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z10, K3
	VPMOVQ2M Z11, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VPMOVQ2M Z12, K3
	VPMOVQ2M Z13, K4
	KUNPCKBW K3, K4, K3
	VPMOVQ2M Z14, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, DX
	CMPQ    BX, $64
	CMOVQCC DX, AX
	BTQ          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X24
	VBROADCASTSD X24, Z24
	JMP          fb_z15_clamp

fb_z15_nomin:
	VMOVAPD Z29, Z24

fb_z15_clamp:
	VSUBPD Z24, Z0, Z0
	VSUBPD Z24, Z1, Z1
	VSUBPD Z24, Z2, Z2
	VSUBPD Z24, Z3, Z3
	VSUBPD Z24, Z4, Z4
	VSUBPD Z24, Z5, Z5
	VSUBPD Z24, Z6, Z6
	VSUBPD Z24, Z7, Z7
	VSUBPD Z24, Z8, Z8
	VSUBPD Z24, Z9, Z9
	VSUBPD Z24, Z10, Z10
	VSUBPD Z24, Z11, Z11
	VSUBPD Z24, Z12, Z12
	VSUBPD Z24, Z13, Z13
	VSUBPD Z24, Z14, K1, Z14
	VCMPPD $5, Z28, Z0, K2
	VMINPD Z28, Z0, Z0
	VCMPPD $5, Z28, Z1, K3
	VMINPD Z28, Z1, Z1
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z2, K3
	VMINPD Z28, Z2, Z2
	VCMPPD $5, Z28, Z3, K4
	VMINPD Z28, Z3, Z3
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $5, Z28, Z4, K3
	VMINPD Z28, Z4, Z4
	VCMPPD $5, Z28, Z5, K4
	VMINPD Z28, Z5, Z5
	KUNPCKBW K3, K4, K3
	VCMPPD $5, Z28, Z6, K4
	VMINPD Z28, Z6, Z6
	VCMPPD $5, Z28, Z7, K5
	VMINPD Z28, Z7, Z7
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VCMPPD $5, Z28, Z8, K2
	VMINPD Z28, Z8, Z8
	VCMPPD $5, Z28, Z9, K3
	VMINPD Z28, Z9, Z9
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z10, K3
	VMINPD Z28, Z10, Z10
	VCMPPD $5, Z28, Z11, K4
	VMINPD Z28, Z11, Z11
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $5, Z28, Z12, K3
	VMINPD Z28, Z12, Z12
	VCMPPD $5, Z28, Z13, K4
	VMINPD Z28, Z13, Z13
	KUNPCKBW K3, K4, K3
	VCMPPD $5, Z28, Z14, K1, K4
	VMINPD Z28, Z14, K1, Z14
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, DX
	ORQ AX, 0(R13)
	ORL DX, 8(R13)
	SHRQ $32, DX
	ORW DX, 12(R13)
	SHRQ $16, DX
	ORB DX, 14(R13)
	ADDQ $15, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_z15_byte

fb_z15_done:
	VMOVUPD Z0, 0(SI)
	VMOVUPD Z1, 64(SI)
	VMOVUPD Z2, 128(SI)
	VMOVUPD Z3, 192(SI)
	VMOVUPD Z4, 256(SI)
	VMOVUPD Z5, 320(SI)
	VMOVUPD Z6, 384(SI)
	VMOVUPD Z7, 448(SI)
	VMOVUPD Z8, 512(SI)
	VMOVUPD Z9, 576(SI)
	VMOVUPD Z10, 640(SI)
	VMOVUPD Z11, 704(SI)
	VMOVUPD Z12, 768(SI)
	VMOVUPD Z13, 832(SI)
	VMOVUPD Z14, K1, 896(SI)
	VZEROUPPER
	RET

fb_z15_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Z28
	JMP          fb_z15_row

fb_z16:
	MOVQ         cost_base+48(FP), SI
	MOVQ         data_base+0(FP), R8
	MOVQ         insertCost_base+24(FP), DI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         blockID_base+96(FP), R14
	MOVQ         CX, R11
	LEAQ         -120(CX), DX
	MOVL         $1, AX
	SHLXL        DX, AX, AX
	DECL         AX
	KMOVB        AX, K1
	MOVQ         $0x547d42aea2879f2e, AX
	VPBROADCASTQ AX, Z29
	VMOVUPD      0(SI), Z0
	VMOVUPD      64(SI), Z1
	VMOVUPD      128(SI), Z2
	VMOVUPD      192(SI), Z3
	VMOVUPD      256(SI), Z4
	VMOVUPD      320(SI), Z5
	VMOVUPD      384(SI), Z6
	VMOVUPD      448(SI), Z7
	VMOVUPD      512(SI), Z8
	VMOVUPD      576(SI), Z9
	VMOVUPD      640(SI), Z10
	VMOVUPD      704(SI), Z11
	VMOVUPD      768(SI), Z12
	VMOVUPD      832(SI), Z13
	VMOVUPD      896(SI), Z14
	VMOVAPD      Z29, Z15
	VMOVUPD      960(SI), K1, Z15
	VPXORQ       X26, X26, X26
	MOVQ         data_len+8(FP), CX
	XORQ         R9, R9
	TESTQ        CX, CX
	JEQ          fb_z16_done

fb_z16_byte:
	CMPQ         R9, $2000
	JA           fb_z16_row
	JEQ          fb_z16_steady
	VCVTSI2SDQ   R9, X26, X27
	VMULSD       fbDPConst<>+0(SB), X27, X27
	VADDSD       fbDPConst<>+8(SB), X27, X27
	VMULSD       blockSwitchBitcost+120(FP), X27, X27
	VBROADCASTSD X27, Z28

fb_z16_row:
	MOVWLZX (R8)(R9*2), BX
	IMULQ   R11, BX
	VADDPD 0(DI)(BX*8), Z0, Z0
	VADDPD 64(DI)(BX*8), Z1, Z1
	VADDPD 128(DI)(BX*8), Z2, Z2
	VADDPD 192(DI)(BX*8), Z3, Z3
	VADDPD 256(DI)(BX*8), Z4, Z4
	VADDPD 320(DI)(BX*8), Z5, Z5
	VADDPD 384(DI)(BX*8), Z6, Z6
	VADDPD 448(DI)(BX*8), Z7, Z7
	VADDPD 512(DI)(BX*8), Z8, Z8
	VADDPD 576(DI)(BX*8), Z9, Z9
	VADDPD 640(DI)(BX*8), Z10, Z10
	VADDPD 704(DI)(BX*8), Z11, Z11
	VADDPD 768(DI)(BX*8), Z12, Z12
	VADDPD 832(DI)(BX*8), Z13, Z13
	VADDPD 896(DI)(BX*8), Z14, Z14
	VADDPD 960(DI)(BX*8), Z15, K1, Z15
	VMINPD Z1, Z0, Z16
	VMINPD Z3, Z2, Z17
	VMINPD Z5, Z4, Z18
	VMINPD Z7, Z6, Z19
	VMINPD Z9, Z8, Z20
	VMINPD Z11, Z10, Z21
	VMINPD Z13, Z12, Z22
	VMINPD Z15, Z14, Z23
	VMINPD Z17, Z16, Z16
	VMINPD Z19, Z18, Z18
	VMINPD Z21, Z20, Z20
	VMINPD Z23, Z22, Z22
	VMINPD Z18, Z16, Z16
	VMINPD Z22, Z20, Z20
	VMINPD Z20, Z16, Z24
	VSHUFF64X2 $0x4e, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VSHUFF64X2 $0xb1, Z24, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VPERMILPD  $0x55, Z24, Z25
	VMINPD     Z25, Z24, Z24
	VUCOMISD   X29, X24
	JCC        fb_z16_nomin
	VCMPPD $0, Z24, Z0, K2
	VCMPPD $0, Z24, Z1, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z2, K3
	VCMPPD $0, Z24, Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $0, Z24, Z4, K3
	VCMPPD $0, Z24, Z5, K4
	KUNPCKBW K3, K4, K3
	VCMPPD $0, Z24, Z6, K4
	VCMPPD $0, Z24, Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VCMPPD $0, Z24, Z8, K2
	VCMPPD $0, Z24, Z9, K3
	KUNPCKBW K2, K3, K2
	VCMPPD $0, Z24, Z10, K3
	VCMPPD $0, Z24, Z11, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $0, Z24, Z12, K3
	VCMPPD $0, Z24, Z13, K4
	KUNPCKBW K3, K4, K3
	VCMPPD $0, Z24, Z14, K4
	VCMPPD $0, Z24, Z15, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, DX
	TZCNTQ  DX, DX
	ADDQ    $64, DX
	TZCNTQ  AX, BX
	CMOVQCS DX, BX
	MOVB     BX, (R14)(R9*1)
	VUCOMISD X26, X24
	JNE      fb_z16_clamp
	VPMOVQ2M Z0, K2
	VPMOVQ2M Z1, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z2, K3
	VPMOVQ2M Z3, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VPMOVQ2M Z4, K3
	VPMOVQ2M Z5, K4
	KUNPCKBW K3, K4, K3
	VPMOVQ2M Z6, K4
	VPMOVQ2M Z7, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VPMOVQ2M Z8, K2
	VPMOVQ2M Z9, K3
	KUNPCKBW K2, K3, K2
	VPMOVQ2M Z10, K3
	VPMOVQ2M Z11, K4
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VPMOVQ2M Z12, K3
	VPMOVQ2M Z13, K4
	KUNPCKBW K3, K4, K3
	VPMOVQ2M Z14, K4
	VPMOVQ2M Z15, K5
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, DX
	CMPQ    BX, $64
	CMOVQCC DX, AX
	BTQ          BX, AX
	SBBQ         AX, AX
	SHLQ         $63, AX
	VMOVQ        AX, X24
	VBROADCASTSD X24, Z24
	JMP          fb_z16_clamp

fb_z16_nomin:
	VMOVAPD Z29, Z24

fb_z16_clamp:
	VSUBPD Z24, Z0, Z0
	VSUBPD Z24, Z1, Z1
	VSUBPD Z24, Z2, Z2
	VSUBPD Z24, Z3, Z3
	VSUBPD Z24, Z4, Z4
	VSUBPD Z24, Z5, Z5
	VSUBPD Z24, Z6, Z6
	VSUBPD Z24, Z7, Z7
	VSUBPD Z24, Z8, Z8
	VSUBPD Z24, Z9, Z9
	VSUBPD Z24, Z10, Z10
	VSUBPD Z24, Z11, Z11
	VSUBPD Z24, Z12, Z12
	VSUBPD Z24, Z13, Z13
	VSUBPD Z24, Z14, Z14
	VSUBPD Z24, Z15, K1, Z15
	VCMPPD $5, Z28, Z0, K2
	VMINPD Z28, Z0, Z0
	VCMPPD $5, Z28, Z1, K3
	VMINPD Z28, Z1, Z1
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z2, K3
	VMINPD Z28, Z2, Z2
	VCMPPD $5, Z28, Z3, K4
	VMINPD Z28, Z3, Z3
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $5, Z28, Z4, K3
	VMINPD Z28, Z4, Z4
	VCMPPD $5, Z28, Z5, K4
	VMINPD Z28, Z5, Z5
	KUNPCKBW K3, K4, K3
	VCMPPD $5, Z28, Z6, K4
	VMINPD Z28, Z6, Z6
	VCMPPD $5, Z28, Z7, K5
	VMINPD Z28, Z7, Z7
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, AX
	VCMPPD $5, Z28, Z8, K2
	VMINPD Z28, Z8, Z8
	VCMPPD $5, Z28, Z9, K3
	VMINPD Z28, Z9, Z9
	KUNPCKBW K2, K3, K2
	VCMPPD $5, Z28, Z10, K3
	VMINPD Z28, Z10, Z10
	VCMPPD $5, Z28, Z11, K4
	VMINPD Z28, Z11, Z11
	KUNPCKBW K3, K4, K3
	KUNPCKWD K2, K3, K2
	VCMPPD $5, Z28, Z12, K3
	VMINPD Z28, Z12, Z12
	VCMPPD $5, Z28, Z13, K4
	VMINPD Z28, Z13, Z13
	KUNPCKBW K3, K4, K3
	VCMPPD $5, Z28, Z14, K4
	VMINPD Z28, Z14, Z14
	VCMPPD $5, Z28, Z15, K1, K5
	VMINPD Z28, Z15, K1, Z15
	KUNPCKBW K4, K5, K4
	KUNPCKWD K3, K4, K3
	KUNPCKDQ K2, K3, K2
	KMOVQ K2, DX
	ORQ AX, 0(R13)
	ORQ DX, 8(R13)
	ADDQ $16, R13
	INCQ R9
	CMPQ R9, CX
	JLT  fb_z16_byte

fb_z16_done:
	VMOVUPD Z0, 0(SI)
	VMOVUPD Z1, 64(SI)
	VMOVUPD Z2, 128(SI)
	VMOVUPD Z3, 192(SI)
	VMOVUPD Z4, 256(SI)
	VMOVUPD Z5, 320(SI)
	VMOVUPD Z6, 384(SI)
	VMOVUPD Z7, 448(SI)
	VMOVUPD Z8, 512(SI)
	VMOVUPD Z9, 576(SI)
	VMOVUPD Z10, 640(SI)
	VMOVUPD Z11, 704(SI)
	VMOVUPD Z12, 768(SI)
	VMOVUPD Z13, 832(SI)
	VMOVUPD Z14, 896(SI)
	VMOVUPD Z15, K1, 960(SI)
	VZEROUPPER
	RET

fb_z16_steady:
	VBROADCASTSD blockSwitchBitcost+120(FP), Z28
	JMP          fb_z16_row

fb_zmm:
	// The AVX2 body with eight lanes per ZMM register for every whole group
	// of eight, and its unmasked four-, two- and one-lane tail. A masked store
	// cannot forward to the next load of the same lanes. It runs above 128
	// histograms, where cost no longer fits in ZMM registers.
	MOVQ         CX, R15
	MOVQ         cost_base+48(FP), SI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         $0x547d42aea2879f2e, AX
	VPBROADCASTQ AX, Z12
	MOVQ         R15, R14
	ANDQ         $-8, R14
	XORQ         R9, R9

fb_zmm_byte:
	CMPQ R9, data_len+8(FP)
	JGE  fb_zmm_done

	VMOVSD     blockSwitchBitcost+120(FP), X13
	CMPQ       R9, $2000
	JGE        fb_zmm_row
	VXORPS     X15, X15, X15
	VCVTSI2SDQ R9, X15, X15
	MOVQ       $0x3f02599ed7c6fbd2, AX
	VMOVQ      AX, X14
	VMULSD     X14, X15, X15
	MOVQ       $0x3fe8a3d70a3d70a4, AX
	VMOVQ      AX, X14
	VADDSD     X14, X15, X15
	VMULSD     X15, X13, X13

fb_zmm_row:
	VBROADCASTSD X13, Z13
	MOVQ         data_base+0(FP), DI
	MOVWLZX      (DI)(R9*2), DI
	IMULQ        R15, DI
	SHLQ         $3, DI
	ADDQ         insertCost_base+24(FP), DI

	VMOVAPD Z12, Z8
	VMOVAPD Z12, Z9
	VMOVAPD Z12, Z10
	VMOVAPD Z12, Z11
	XORQ    AX, AX
	MOVQ    R15, DX
	ANDQ    $-32, DX

fb_zmm_add32:
	CMPQ    AX, DX
	JGE     fb_zmm_add8
	VMOVUPD (SI)(AX*8), Z0
	VMOVUPD 64(SI)(AX*8), Z1
	VMOVUPD 128(SI)(AX*8), Z2
	VMOVUPD 192(SI)(AX*8), Z3
	VADDPD  (DI)(AX*8), Z0, Z0
	VADDPD  64(DI)(AX*8), Z1, Z1
	VADDPD  128(DI)(AX*8), Z2, Z2
	VADDPD  192(DI)(AX*8), Z3, Z3
	VMOVUPD Z0, (SI)(AX*8)
	VMOVUPD Z1, 64(SI)(AX*8)
	VMOVUPD Z2, 128(SI)(AX*8)
	VMOVUPD Z3, 192(SI)(AX*8)
	VMINPD  Z8, Z0, Z8
	VMINPD  Z9, Z1, Z9
	VMINPD  Z10, Z2, Z10
	VMINPD  Z11, Z3, Z11
	ADDQ    $32, AX
	JMP     fb_zmm_add32

fb_zmm_add8:
	CMPQ    AX, R14
	JGE     fb_zmm_fold
	VMOVUPD (SI)(AX*8), Z0
	VADDPD  (DI)(AX*8), Z0, Z0
	VMOVUPD Z0, (SI)(AX*8)
	VMINPD  Z8, Z0, Z8
	ADDQ    $8, AX
	JMP     fb_zmm_add8

fb_zmm_fold:
	VMINPD        Z9, Z8, Z8
	VMINPD        Z11, Z10, Z10
	VMINPD        Z10, Z8, Z8
	VEXTRACTF64X4 $1, Z8, Y0
	VMINPD        Y0, Y8, Y8

	TESTQ   $4, R15
	JEQ     fb_zmm_fold4
	VMOVUPD (SI)(AX*8), Y0
	VADDPD  (DI)(AX*8), Y0, Y0
	VMOVUPD Y0, (SI)(AX*8)
	VMINPD  Y8, Y0, Y8
	ADDQ    $4, AX

fb_zmm_fold4:
	VEXTRACTF128 $1, Y8, X0
	VMINPD       X0, X8, X8

	TESTQ   $2, R15
	JEQ     fb_zmm_fold2
	VMOVUPD (SI)(AX*8), X0
	VADDPD  (DI)(AX*8), X0, X0
	VMOVUPD X0, (SI)(AX*8)
	VMINPD  X8, X0, X8
	ADDQ    $2, AX

fb_zmm_fold2:
	VUNPCKHPD X8, X8, X0
	VMINSD    X0, X8, X8

	TESTQ  $1, R15
	JEQ    fb_zmm_guard
	VMOVSD (SI)(AX*8), X0
	VADDSD (DI)(AX*8), X0, X0
	VMOVSD X0, (SI)(AX*8)
	VMINSD X8, X0, X8

fb_zmm_guard:
	MOVQ     $1, DI
	VMOVAPD  Z12, Z6
	VUCOMISD X12, X8
	JCC      fb_zmm_clamp

	VBROADCASTSD X8, Z6
	XORQ         DI, DI
	VMOVQ        X8, AX
	SHLQ         $1, AX
	JNE          fb_zmm_clamp

	MOVQ $1, DI
	XORQ AX, AX

fb_zmm_eq8:
	CMPQ    AX, R14
	JGE     fb_zmm_eq2setup
	VMOVUPD (SI)(AX*8), Z0
	VCMPPD  $0, Z6, Z0, K2
	KMOVB   K2, BX
	TESTL   BX, BX
	JNE     fb_zmm_eqhit
	ADDQ    $8, AX
	JMP     fb_zmm_eq8

fb_zmm_eqhit:
	TZCNTL BX, BX
	ADDQ   BX, AX
	JMP    fb_zmm_found

fb_zmm_eq2setup:
	MOVQ R15, DX
	ANDQ $-2, DX

fb_zmm_eq2:
	CMPQ      AX, DX
	JGE       fb_zmm_eq1
	VMOVUPD   (SI)(AX*8), X0
	VCMPPD    $0, X6, X0, X0
	VMOVMSKPD X0, BX
	ADDQ      $2, AX
	TESTL     BX, BX
	JEQ       fb_zmm_eq2
	TZCNTL    BX, BX
	LEAQ      -2(AX)(BX*1), AX
	JMP       fb_zmm_found

fb_zmm_eq1:
	CMPQ AX, R15
	JLT  fb_zmm_found
	XORQ AX, AX
	MOVQ blockID_base+96(FP), BX
	MOVB AX, (BX)(R9*1)
	JMP  fb_zmm_clamp

fb_zmm_found:
	VMOVSD       (SI)(AX*8), X6
	VBROADCASTSD X6, Z6
	MOVQ         blockID_base+96(FP), BX
	MOVB         AX, (BX)(R9*1)

fb_zmm_clamp:
	XORQ AX, AX
	MOVQ R13, R8

fb_zmm_clamp8:
	CMPQ    AX, R14
	JGE     fb_zmm_tail
	VMOVUPD (SI)(AX*8), Z0
	TESTQ   DI, DI
	JNE     fb_zmm_sub8

	VCMPPD $0, Z6, Z0, K2
	KMOVB  K2, BX
	TESTL  BX, BX
	JEQ    fb_zmm_sub8
	TZCNTL BX, BX
	ADDQ   AX, BX
	MOVQ   blockID_base+96(FP), R10
	MOVB   BX, (R10)(R9*1)
	MOVQ   $1, DI

fb_zmm_sub8:
	VSUBPD  Z6, Z0, Z0
	VCMPPD  $5, Z13, Z0, K3
	VMINPD  Z13, Z0, Z0
	VMOVUPD Z0, (SI)(AX*8)
	KMOVB   K3, BX
	ORB     BX, (R8)
	INCQ    R8
	ADDQ    $8, AX
	JMP     fb_zmm_clamp8

fb_zmm_tail:
	TESTQ DI, DI
	JNE   fb_zmm_tail_clamp
	MOVQ  AX, R10
	MOVQ  R15, DX
	ANDQ  $-2, DX

fb_zmm_tail_eq2:
	CMPQ      R10, DX
	JGE       fb_zmm_tail_eq1
	VMOVUPD   (SI)(R10*8), X0
	VCMPPD    $0, X6, X0, X0
	VMOVMSKPD X0, BX
	ADDQ      $2, R10
	TESTL     BX, BX
	JEQ       fb_zmm_tail_eq2
	TZCNTL    BX, BX
	LEAQ      -2(R10)(BX*1), R10
	JMP       fb_zmm_tail_found

fb_zmm_tail_eq1:
	CMPQ R10, R15
	JLT  fb_zmm_tail_found
	XORQ R10, R10

fb_zmm_tail_found:
	MOVQ blockID_base+96(FP), BX
	MOVB R10, (BX)(R9*1)

fb_zmm_tail_clamp:
	CMPQ AX, R15
	JGE  fb_zmm_next
	XORL BX, BX
	XORL CX, CX
	TESTQ $4, R15
	JEQ   fb_zmm_clamp2

	VMOVUPD   (SI)(AX*8), Y0
	VSUBPD    Y6, Y0, Y0
	VCMPPD    $5, Y13, Y0, Y2
	VMINPD    Y13, Y0, Y0
	VMOVUPD   Y0, (SI)(AX*8)
	VMOVMSKPD Y2, BX
	ADDQ      $4, AX
	MOVL      $4, CX

fb_zmm_clamp2:
	TESTQ     $2, R15
	JEQ       fb_zmm_clamp1
	VMOVUPD   (SI)(AX*8), X0
	VSUBPD    X6, X0, X0
	VCMPPD    $5, X13, X0, X4
	VMINPD    X13, X0, X0
	VMOVUPD   X0, (SI)(AX*8)
	VMOVMSKPD X4, R10
	SHLL      CX, R10
	ORL       R10, BX
	ADDQ      $2, AX
	ADDL      $2, CX

fb_zmm_clamp1:
	TESTQ     $1, R15
	JEQ       fb_zmm_clamp_store
	VMOVSD    (SI)(AX*8), X0
	VSUBSD    X6, X0, X0
	VCMPSD    $5, X13, X0, X4
	VMINSD    X13, X0, X0
	VMOVSD    X0, (SI)(AX*8)
	VMOVMSKPD X4, R10
	ANDL      $1, R10
	SHLL      CX, R10
	ORL       R10, BX

fb_zmm_clamp_store:
	ORB BX, (R8)

fb_zmm_next:
	LEAQ 7(R15), BX
	SHRQ $3, BX
	ADDQ BX, R13
	INCQ R9
	JMP  fb_zmm_byte

fb_zmm_done:
	VZEROUPPER
	RET
#endif

#ifndef hasAVX512
#ifdef hasAVX2
fb_wide:
	// The SSE2 body below, four lanes per YMM register. Every lane runs the
	// same operations with the same operand roles. The n%8 tail is split into
	// four, two and one lanes the same way in the add and the clamp pass, so
	// each load of cost reads exactly what one earlier store wrote.
	MOVQ         CX, R15
	MOVQ         cost_base+48(FP), SI
	MOVQ         switchSignal_base+72(FP), R13
	MOVQ         $0x547d42aea2879f2e, AX
	VMOVQ        AX, X12
	VBROADCASTSD X12, Y12
	XORQ         R9, R9

fb_wide_byte:
	CMPQ R9, data_len+8(FP)
	JGE  fb_wide_done

	VMOVSD     blockSwitchBitcost+120(FP), X13
	CMPQ       R9, $2000
	JGE        fb_wide_row
	VXORPS     X15, X15, X15
	VCVTSI2SDQ R9, X15, X15
	MOVQ       $0x3f02599ed7c6fbd2, AX
	VMOVQ      AX, X14
	VMULSD     X14, X15, X15
	MOVQ       $0x3fe8a3d70a3d70a4, AX
	VMOVQ      AX, X14
	VADDSD     X14, X15, X15
	VMULSD     X15, X13, X13

fb_wide_row:
	VBROADCASTSD X13, Y13
	MOVQ         data_base+0(FP), DI
	MOVWLZX      (DI)(R9*2), DI
	IMULQ        R15, DI
	SHLQ         $3, DI
	ADDQ         insertCost_base+24(FP), DI

	VMOVAPD Y12, Y8
	VMOVAPD Y12, Y9
	VMOVAPD Y12, Y10
	VMOVAPD Y12, Y11
	XORQ    AX, AX
	MOVQ    R15, DX
	ANDQ    $-16, DX

fb_wide_add16:
	CMPQ    AX, DX
	JGE     fb_wide_add4setup
	VMOVUPD (SI)(AX*8), Y0
	VMOVUPD 32(SI)(AX*8), Y1
	VMOVUPD 64(SI)(AX*8), Y2
	VMOVUPD 96(SI)(AX*8), Y3
	VADDPD  (DI)(AX*8), Y0, Y0
	VADDPD  32(DI)(AX*8), Y1, Y1
	VADDPD  64(DI)(AX*8), Y2, Y2
	VADDPD  96(DI)(AX*8), Y3, Y3
	VMOVUPD Y0, (SI)(AX*8)
	VMOVUPD Y1, 32(SI)(AX*8)
	VMOVUPD Y2, 64(SI)(AX*8)
	VMOVUPD Y3, 96(SI)(AX*8)
	VMINPD  Y8, Y0, Y8
	VMINPD  Y9, Y1, Y9
	VMINPD  Y10, Y2, Y10
	VMINPD  Y11, Y3, Y11
	ADDQ    $16, AX
	JMP     fb_wide_add16

fb_wide_add4setup:
	MOVQ R15, DX
	ANDQ $-4, DX

fb_wide_add4:
	CMPQ    AX, DX
	JGE     fb_wide_fold
	VMOVUPD (SI)(AX*8), Y0
	VADDPD  (DI)(AX*8), Y0, Y0
	VMOVUPD Y0, (SI)(AX*8)
	VMINPD  Y8, Y0, Y8
	ADDQ    $4, AX
	JMP     fb_wide_add4

fb_wide_fold:
	VMINPD       Y9, Y8, Y8
	VMINPD       Y11, Y10, Y10
	VMINPD       Y10, Y8, Y8
	VEXTRACTF128 $1, Y8, X0
	VMINPD       X0, X8, X8

	MOVQ    R15, DX
	ANDQ    $-2, DX
	CMPQ    AX, DX
	JGE     fb_wide_fold1
	VMOVUPD (SI)(AX*8), X0
	VADDPD  (DI)(AX*8), X0, X0
	VMOVUPD X0, (SI)(AX*8)
	VMINPD  X8, X0, X8
	ADDQ    $2, AX

fb_wide_fold1:
	VUNPCKHPD X8, X8, X0
	VMINSD    X0, X8, X8

	CMPQ   AX, R15
	JGE    fb_wide_guard
	VMOVSD (SI)(AX*8), X0
	VADDSD (DI)(AX*8), X0, X0
	VMOVSD X0, (SI)(AX*8)
	VMINSD X8, X0, X8

fb_wide_guard:
	MOVQ     $1, DI
	VMOVAPD  Y12, Y6
	VUCOMISD X12, X8
	JCC      fb_wide_clamp

	VBROADCASTSD X8, Y6
	XORQ         DI, DI
	VMOVQ        X8, AX
	SHLQ         $1, AX
	JNE          fb_wide_clamp

	MOVQ $1, DI
	XORQ AX, AX
	MOVQ R15, DX
	ANDQ $-2, DX

fb_wide_eq2:
	CMPQ      AX, DX
	JGE       fb_wide_eq1
	VMOVUPD   (SI)(AX*8), X0
	VCMPPD    $0, X6, X0, X0
	VMOVMSKPD X0, BX
	ADDQ      $2, AX
	TESTL     BX, BX
	JEQ       fb_wide_eq2
	BSFL      BX, BX
	LEAQ      -2(AX)(BX*1), AX
	JMP       fb_wide_found

fb_wide_eq1:
	CMPQ AX, R15
	JLT  fb_wide_found
	XORQ AX, AX
	MOVQ blockID_base+96(FP), BX
	MOVB AX, (BX)(R9*1)
	JMP  fb_wide_clamp

fb_wide_found:
	VMOVSD       (SI)(AX*8), X6
	VBROADCASTSD X6, Y6
	MOVQ         blockID_base+96(FP), BX
	MOVB         AX, (BX)(R9*1)

fb_wide_clamp:
	XORQ AX, AX
	MOVQ R13, R8
	MOVQ R15, DX
	ANDQ $-8, DX

fb_wide_clamp8:
	CMPQ    AX, DX
	JGE     fb_wide_tail
	VMOVUPD (SI)(AX*8), Y0
	VMOVUPD 32(SI)(AX*8), Y1
	TESTQ   DI, DI
	JNE     fb_wide_sub8

	VCMPPD    $0, Y6, Y0, Y2
	VCMPPD    $0, Y6, Y1, Y3
	VMOVMSKPD Y2, BX
	VMOVMSKPD Y3, R10
	SHLL      $4, R10
	ORL       R10, BX
	TESTL     BX, BX
	JEQ       fb_wide_sub8
	BSFL      BX, BX
	ADDQ      AX, BX
	MOVQ      blockID_base+96(FP), R10
	MOVB      BX, (R10)(R9*1)
	MOVQ      $1, DI

fb_wide_sub8:
	VSUBPD    Y6, Y0, Y0
	VSUBPD    Y6, Y1, Y1
	VCMPPD    $5, Y13, Y0, Y2
	VCMPPD    $5, Y13, Y1, Y3
	VMINPD    Y13, Y0, Y0
	VMINPD    Y13, Y1, Y1
	VMOVUPD   Y0, (SI)(AX*8)
	VMOVUPD   Y1, 32(SI)(AX*8)
	VMOVMSKPD Y2, BX
	VMOVMSKPD Y3, R10
	SHLL      $4, R10
	ORL       R10, BX
	ORB       BX, (R8)
	INCQ      R8
	ADDQ      $8, AX
	JMP       fb_wide_clamp8

fb_wide_tail:
	TESTQ DI, DI
	JNE   fb_wide_tail_clamp
	MOVQ  AX, R10
	MOVQ  R15, DX
	ANDQ  $-2, DX

fb_wide_tail_eq2:
	CMPQ      R10, DX
	JGE       fb_wide_tail_eq1
	VMOVUPD   (SI)(R10*8), X0
	VCMPPD    $0, X6, X0, X0
	VMOVMSKPD X0, BX
	ADDQ      $2, R10
	TESTL     BX, BX
	JEQ       fb_wide_tail_eq2
	BSFL      BX, BX
	LEAQ      -2(R10)(BX*1), R10
	JMP       fb_wide_tail_found

fb_wide_tail_eq1:
	CMPQ R10, R15
	JLT  fb_wide_tail_found
	XORQ R10, R10

fb_wide_tail_found:
	MOVQ blockID_base+96(FP), BX
	MOVB R10, (BX)(R9*1)

fb_wide_tail_clamp:
	CMPQ AX, R15
	JGE  fb_wide_next
	XORL BX, BX
	XORL CX, CX
	TESTQ $4, R15
	JEQ   fb_wide_clamp2

	VMOVUPD   (SI)(AX*8), Y0
	VSUBPD    Y6, Y0, Y0
	VCMPPD    $5, Y13, Y0, Y2
	VMINPD    Y13, Y0, Y0
	VMOVUPD   Y0, (SI)(AX*8)
	VMOVMSKPD Y2, BX
	ADDQ      $4, AX
	MOVL      $4, CX

fb_wide_clamp2:
	TESTQ     $2, R15
	JEQ       fb_wide_clamp1
	VMOVUPD   (SI)(AX*8), X0
	VSUBPD    X6, X0, X0
	VCMPPD    $5, X13, X0, X4
	VMINPD    X13, X0, X0
	VMOVUPD   X0, (SI)(AX*8)
	VMOVMSKPD X4, R10
	SHLL      CX, R10
	ORL       R10, BX
	ADDQ      $2, AX
	ADDL      $2, CX

fb_wide_clamp1:
	TESTQ     $1, R15
	JEQ       fb_wide_clamp_store
	VMOVSD    (SI)(AX*8), X0
	VSUBSD    X6, X0, X0
	VCMPSD    $5, X13, X0, X4
	VMINSD    X13, X0, X0
	VMOVSD    X0, (SI)(AX*8)
	VMOVMSKPD X4, R10
	ANDL      $1, R10
	SHLL      CX, R10
	ORL       R10, BX

fb_wide_clamp_store:
	ORB BX, (R8)

fb_wide_next:
	LEAQ 7(R15), BX
	SHRQ $3, BX
	ADDQ BX, R13
	INCQ R9
	JMP  fb_wide_byte

fb_wide_done:
	VZEROUPPER
	RET
#else
fb_wide:
	// R15 = n, SI = cost, X12 = noMinCost in both lanes, R9 = byteIx and R13 =
	// this byte's switchSignal row. X13 is the switch cost.
	MOVQ     CX, R15
	MOVQ     cost_base+48(FP), SI
	MOVQ     switchSignal_base+72(FP), R13
	MOVQ     $0x547d42aea2879f2e, AX
	MOVQ     AX, X12
	UNPCKLPD X12, X12
	XORQ     R9, R9

fb_wide_byte:
	CMPQ R9, data_len+8(FP)
	JGE  fb_wide_done

	MOVSD    blockSwitchBitcost+120(FP), X13
	CMPQ     R9, $2000
	JGE      fb_wide_row
	XORPS    X15, X15
	CVTSQ2SD R9, X15
	MOVQ     $0x3f02599ed7c6fbd2, AX
	MOVQ     AX, X14
	MULSD    X14, X15
	MOVQ     $0x3fe8a3d70a3d70a4, AX
	MOVQ     AX, X14
	ADDSD    X14, X15
	MULSD    X15, X13

fb_wide_row:
	UNPCKLPD X13, X13
	MOVQ     data_base+0(FP), DI
	MOVWLZX  (DI)(R9*2), DI
	IMULQ    R15, DI
	SHLQ     $3, DI
	ADDQ     insertCost_base+24(FP), DI

	MOVAPD X12, X8
	MOVAPD X12, X9
	MOVAPD X12, X10
	MOVAPD X12, X11
	XORQ   AX, AX
	MOVQ   R15, DX
	ANDQ   $-8, DX

fb_wide_add8:
	CMPQ   AX, DX
	JGE    fb_wide_add2setup

	// An odd histogram count gives insertCost an unaligned base.
	// ADDPD with a memory operand would fault.
	MOVUPD (SI)(AX*8), X0
	MOVUPD 16(SI)(AX*8), X1
	MOVUPD 32(SI)(AX*8), X2
	MOVUPD 48(SI)(AX*8), X3
	MOVUPD (DI)(AX*8), X4
	MOVUPD 16(DI)(AX*8), X5
	MOVUPD 32(DI)(AX*8), X6
	MOVUPD 48(DI)(AX*8), X7
	ADDPD  X4, X0
	ADDPD  X5, X1
	ADDPD  X6, X2
	ADDPD  X7, X3
	MOVUPD X0, (SI)(AX*8)
	MOVUPD X1, 16(SI)(AX*8)
	MOVUPD X2, 32(SI)(AX*8)
	MOVUPD X3, 48(SI)(AX*8)
	MINPD  X8, X0
	MINPD  X9, X1
	MINPD  X10, X2
	MINPD  X11, X3
	MOVAPD X0, X8
	MOVAPD X1, X9
	MOVAPD X2, X10
	MOVAPD X3, X11
	ADDQ   $8, AX
	JMP    fb_wide_add8

fb_wide_add2setup:
	MOVQ R15, DX
	ANDQ $-2, DX

fb_wide_add2:
	CMPQ   AX, DX
	JGE    fb_wide_fold
	MOVUPD (SI)(AX*8), X0
	MOVUPD (DI)(AX*8), X4
	ADDPD  X4, X0
	MOVUPD X0, (SI)(AX*8)
	MINPD  X8, X0
	MOVAPD X0, X8
	ADDQ   $2, AX
	JMP    fb_wide_add2

fb_wide_fold:
	MINPD   X9, X8
	MINPD   X11, X10
	MINPD   X10, X8
	MOVHLPS X8, X0
	MINSD   X0, X8

	// An odd final lane needs a scalar load to stay within cost.
	CMPQ   AX, R15
	JGE    fb_wide_guard
	MOVSD  (SI)(AX*8), X0
	ADDSD  (DI)(AX*8), X0
	MOVSD  X0, (SI)(AX*8)
	MINSD  X8, X0
	MOVAPD X0, X8

fb_wide_guard:
	// DI, free now, is 1 once the block type needs no more searching. Nothing
	// beat noMinCost: rebase by it and leave the block type unwritten.
	MOVQ    $1, DI
	MOVAPD  X12, X6
	UCOMISD X12, X8
	JCC     fb_wide_clamp

	MOVAPD   X8, X6
	UNPCKLPD X6, X6
	XORQ     DI, DI
	MOVQ     X8, AX
	SHLQ     $1, AX
	JNE      fb_wide_clamp

	// A zero minimum needs cost[best]'s sign before anything is subtracted, so
	// find best first, exactly as findBlocksStep does.
	MOVQ $1, DI
	XORQ AX, AX
	MOVQ R15, DX
	ANDQ $-2, DX

fb_wide_eq2:
	CMPQ     AX, DX
	JGE      fb_wide_eq1
	MOVUPD   (SI)(AX*8), X0
	CMPPD    X6, X0, $0
	MOVMSKPD X0, BX
	ADDQ     $2, AX
	TESTL    BX, BX
	JEQ      fb_wide_eq2
	BSFL     BX, BX
	LEAQ     -2(AX)(BX*1), AX
	JMP      fb_wide_found

fb_wide_eq1:
	CMPQ AX, R15
	JLT  fb_wide_found
	XORQ AX, AX
	MOVQ blockID_base+96(FP), BX
	MOVB AX, (BX)(R9*1)
	JMP  fb_wide_clamp

fb_wide_found:
	MOVSD    (SI)(AX*8), X6
	UNPCKLPD X6, X6
	MOVQ     blockID_base+96(FP), BX
	MOVB     AX, (BX)(R9*1)

fb_wide_clamp:
	XORQ AX, AX
	MOVQ R13, R8
	MOVQ R15, DX
	ANDQ $-8, DX

fb_wide_clamp8:
	CMPQ   AX, DX
	JGE    fb_wide_tail
	MOVUPD (SI)(AX*8), X0
	MOVUPD 16(SI)(AX*8), X1
	MOVUPD 32(SI)(AX*8), X2
	MOVUPD 48(SI)(AX*8), X3
	TESTQ  DI, DI
	JNE    fb_wide_sub8

	// The first-index scan rides along until it hits. A nonzero minimum has
	// the bits of cost[best], so the subtraction need not wait for best.
	MOVAPD   X0, X8
	MOVAPD   X1, X9
	MOVAPD   X2, X10
	MOVAPD   X3, X11
	CMPPD    X6, X8, $0
	CMPPD    X6, X9, $0
	CMPPD    X6, X10, $0
	CMPPD    X6, X11, $0
	MOVMSKPD X8, BX
	MOVMSKPD X9, R10
	MOVMSKPD X10, R11
	MOVMSKPD X11, R12
	SHLL     $2, R10
	SHLL     $4, R11
	SHLL     $6, R12
	ORL      R10, BX
	ORL      R12, R11
	ORL      R11, BX
	TESTL    BX, BX
	JEQ      fb_wide_sub8
	BSFL     BX, BX
	ADDQ     AX, BX
	MOVQ     blockID_base+96(FP), R10
	MOVB     BX, (R10)(R9*1)
	MOVQ     $1, DI

fb_wide_sub8:
	SUBPD    X6, X0
	SUBPD    X6, X1
	SUBPD    X6, X2
	SUBPD    X6, X3
	MOVAPD   X0, X4
	MOVAPD   X1, X5
	MOVAPD   X2, X7
	MOVAPD   X3, X14

	// With finite cost, predicate 5 sets the bit when cost >= switchCost.
	CMPPD    X13, X4, $5
	CMPPD    X13, X5, $5
	CMPPD    X13, X7, $5
	CMPPD    X13, X14, $5

	// MINPD returns its source at equality, so switchCost sets the stored bits.
	MINPD    X13, X0
	MINPD    X13, X1
	MINPD    X13, X2
	MINPD    X13, X3
	MOVUPD   X0, (SI)(AX*8)
	MOVUPD   X1, 16(SI)(AX*8)
	MOVUPD   X2, 32(SI)(AX*8)
	MOVUPD   X3, 48(SI)(AX*8)
	MOVMSKPD X4, BX
	MOVMSKPD X5, R10
	MOVMSKPD X7, R11
	MOVMSKPD X14, R12
	SHLL     $2, R10
	SHLL     $4, R11
	SHLL     $6, R12
	ORL      R10, BX
	ORL      R12, R11
	ORL      R11, BX
	ORB      BX, (R8)
	INCQ     R8
	ADDQ     $8, AX
	JMP      fb_wide_clamp8

fb_wide_tail:
	TESTQ DI, DI
	JNE   fb_wide_tail_clamp
	MOVQ  AX, R10
	MOVQ  R15, DX
	ANDQ  $-2, DX

fb_wide_tail_eq2:
	CMPQ     R10, DX
	JGE      fb_wide_tail_eq1
	MOVUPD   (SI)(R10*8), X0
	CMPPD    X6, X0, $0
	MOVMSKPD X0, BX
	ADDQ     $2, R10
	TESTL    BX, BX
	JEQ      fb_wide_tail_eq2
	BSFL     BX, BX
	LEAQ     -2(R10)(BX*1), R10
	JMP      fb_wide_tail_found

fb_wide_tail_eq1:
	CMPQ R10, R15
	JLT  fb_wide_tail_found
	XORQ R10, R10

fb_wide_tail_found:
	MOVQ blockID_base+96(FP), BX
	MOVB R10, (BX)(R9*1)

fb_wide_tail_clamp:
	// At a multiple of eight, R8 points past the current bitmap row.
	MOVQ  R15, R11
	ANDQ  $7, R11
	TESTQ R11, R11
	JEQ   fb_wide_next
	XORL  BX, BX
	XORL  CX, CX
	MOVQ  R15, DX
	ANDQ  $-2, DX

fb_wide_clamp2:
	CMPQ     AX, DX
	JGE      fb_wide_clamp1
	MOVUPD   (SI)(AX*8), X0
	SUBPD    X6, X0
	MOVAPD   X0, X4
	CMPPD    X13, X4, $5
	MINPD    X13, X0
	MOVUPD   X0, (SI)(AX*8)
	MOVMSKPD X4, R10
	SHLL     CX, R10
	ORL      R10, BX
	ADDQ     $2, AX
	ADDL     $2, CX
	JMP      fb_wide_clamp2

fb_wide_clamp1:
	CMPQ     AX, R15
	JGE      fb_wide_clamp_store
	MOVSD    (SI)(AX*8), X0
	SUBSD    X6, X0
	MOVAPD   X0, X4
	CMPSD    X13, X4, $5
	MINSD    X13, X0
	MOVSD    X0, (SI)(AX*8)
	MOVMSKPD X4, R10
	ANDL     $1, R10
	SHLL     CX, R10
	ORL      R10, BX

fb_wide_clamp_store:
	ORB BX, (R8)

fb_wide_next:
	LEAQ 7(R15), BX
	SHRQ $3, BX
	ADDQ BX, R13
	INCQ R9
	JMP  fb_wide_byte

fb_wide_done:
	RET
#endif
#endif
