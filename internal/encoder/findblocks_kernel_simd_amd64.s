// Assembly for findblocks_kernel_simd_amd64.go. Go assembly cannot live inside
// a .go file.

//go:build amd64 && !purego

#include "textflag.h"

// func findBlocksDPStep(cost, insertCost []float64) (minCost float64, best int)
//
// Two passes over the same L1-resident array. Pass 1 does the adds and the
// minimum, pass 2 finds the first index holding that minimum. Fusing them would
// need a per-lane blend to carry indices, which SSE2 lacks, and would still need
// a horizontal first-occurrence resolution at the end.
//
// Four independent MINPD accumulators: MINPD has 3-cycle latency, so a single
// accumulator would serialise 100 histograms into a dependency chain longer than
// the load/store floor.
TEXT ·findBlocksDPStep(SB), NOSPLIT|NOFRAME, $0-64
	MOVQ cost_base+0(FP), SI
	MOVQ cost_len+8(FP), CX
	MOVQ insertCost_base+24(FP), DI

	// X12 = broadcast noMinCost. There is no MOVSD $imm form, so the sentinel
	// arrives as its bit pattern: float64(1e99) == 0x547d42aea2879f2e.
	MOVQ     $0x547d42aea2879f2e, BX
	MOVQ     BX, X12
	UNPCKLPD X12, X12
	MOVAPD   X12, X8
	MOVAPD   X12, X9
	MOVAPD   X12, X10
	MOVAPD   X12, X11

	XORQ AX, AX
	MOVQ CX, DX
	ANDQ $-8, DX

dp_add8:
	CMPQ AX, DX
	JGE  dp_add2setup

	// MOVUPD, never a memory operand on ADDPD: insertCost is sliced at
	// symbol*numHistograms, so its base is only 8-byte aligned when
	// numHistograms is odd, and non-VEX ADDPD would fault on that.
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

	// acc = (v < acc) ? v : acc, with the accumulator as the SOURCE operand:
	// MINPD returns its source when the operands are unordered, so a NaN lane
	// falls out of the minimum exactly as cost[k] < minCost does.
	MINPD  X8, X0
	MINPD  X9, X1
	MINPD  X10, X2
	MINPD  X11, X3
	MOVAPD X0, X8
	MOVAPD X1, X9
	MOVAPD X2, X10
	MOVAPD X3, X11
	ADDQ   $8, AX
	JMP    dp_add8

dp_add2setup:
	MOVQ CX, DX
	ANDQ $-2, DX

dp_add2:
	CMPQ AX, DX
	JGE  dp_fold
	MOVUPD (SI)(AX*8), X0
	MOVUPD (DI)(AX*8), X4
	ADDPD  X4, X0
	MOVUPD X0, (SI)(AX*8)
	MINPD  X8, X0
	MOVAPD X0, X8
	ADDQ   $2, AX
	JMP    dp_add2

dp_fold:
	MINPD   X9, X8
	MINPD   X11, X10
	MINPD   X10, X8
	MOVHLPS X8, X0
	MINSD   X0, X8

	// Odd final lane, scalar. A 2-lane MOVUPD here would read 8 bytes past
	// cost[n-1].
	CMPQ   AX, CX
	JGE    dp_guard
	MOVSD  (SI)(AX*8), X0
	ADDSD  (DI)(AX*8), X0
	MOVSD  X0, (SI)(AX*8)
	MINSD  X8, X0
	MOVAPD X0, X8

dp_guard:
	// Nothing beat the sentinel: the scalar loop never wrote an index and its
	// minCost is still noMinCost, so returning index 0 keeps the caller's
	// guard doing the same thing.
	UCOMISD X12, X8
	JCS     dp_scan
	MOVSD   X12, minCost+48(FP)
	MOVQ    $0, best+56(FP)
	RET

dp_scan:
	MOVAPD   X8, X5
	UNPCKLPD X5, X5
	XORQ     AX, AX
	MOVQ     CX, DX
	ANDQ     $-2, DX

dp_eq2:
	CMPQ AX, DX
	JGE  dp_eq1
	MOVUPD   (SI)(AX*8), X0
	CMPPD    X5, X0, $0
	MOVMSKPD X0, BX
	ADDQ     $2, AX
	TESTL    BX, BX
	JEQ      dp_eq2

	// Lowest set bit is the lowest lane, which is also the answer when several
	// lanes hold the minimum.
	BSFL BX, BX
	LEAQ -2(AX)(BX*1), AX
	JMP  dp_found

dp_eq1:
	CMPQ  AX, CX
	JLT   dp_found
	MOVSD X8, minCost+48(FP)
	MOVQ  $0, best+56(FP)
	RET

dp_found:
	// Return cost[best], not the folded accumulator: they differ when the
	// minimum is a signed zero, and this is the form the scalar loop produces.
	MOVSD (SI)(AX*8), X0
	MOVSD X0, minCost+48(FP)
	MOVQ  AX, best+56(FP)
	RET

// func findBlocksClamp(cost []float64, sig []byte, minCost, switchCost float64)
//
// Eight lanes per iteration so each iteration fills exactly one bitmap byte with
// a single read-modify-write.
TEXT ·findBlocksClamp(SB), NOSPLIT|NOFRAME, $0-64
	MOVQ     cost_base+0(FP), SI
	MOVQ     cost_len+8(FP), R9
	MOVQ     sig_base+24(FP), R8
	MOVSD    minCost+48(FP), X6
	MOVSD    switchCost+56(FP), X7
	UNPCKLPD X6, X6
	UNPCKLPD X7, X7

	XORQ AX, AX
	MOVQ R9, DX
	ANDQ $-8, DX

clamp8:
	CMPQ AX, DX
	JGE  clamp_tail
	MOVUPD (SI)(AX*8), X0
	MOVUPD 16(SI)(AX*8), X1
	MOVUPD 32(SI)(AX*8), X2
	MOVUPD 48(SI)(AX*8), X3
	SUBPD  X6, X0
	SUBPD  X6, X1
	SUBPD  X6, X2
	SUBPD  X6, X3

	// CMPPD destroys its destination, so the value needs a copy.
	MOVAPD X0, X13
	MOVAPD X1, X14
	MOVAPD X2, X15
	MOVAPD X3, X12

	// Predicate 5 is NLT: !(cost < switchCost), which for ordered operands is
	// exactly cost >= switchCost, and at equality the mask is set just as the
	// scalar branch fires on ==.
	CMPPD X7, X13, $5
	CMPPD X7, X14, $5
	CMPPD X7, X15, $5
	CMPPD X7, X12, $5

	// switchCost must be the SOURCE: MINPD returns its source when the operands
	// compare equal, which is what makes cost == switchCost store switchCost's
	// bits. Reversing the operands would also clobber the broadcast.
	MINPD  X7, X0
	MINPD  X7, X1
	MINPD  X7, X2
	MINPD  X7, X3
	MOVUPD X0, (SI)(AX*8)
	MOVUPD X1, 16(SI)(AX*8)
	MOVUPD X2, 32(SI)(AX*8)
	MOVUPD X3, 48(SI)(AX*8)

	MOVMSKPD X13, BX
	MOVMSKPD X14, R10
	MOVMSKPD X15, R11
	MOVMSKPD X12, R12
	SHLL     $2, R10
	SHLL     $4, R11
	SHLL     $6, R12
	ORL      R10, BX
	ORL      R12, R11
	ORL      R11, BX

	// OR, not MOV: the kernel has to be a drop-in for the scalar |=.
	ORB  BX, (R8)
	INCQ R8
	ADDQ $8, AX
	JMP  clamp8

clamp_tail:
	// AX == n&^7 and R8 == &sig[n>>3]. When n is a multiple of eight that byte
	// belongs to the NEXT bitmap row, and on the last row it is one past the end
	// of an exactly sized switchSignal, so it must not be touched at all.
	MOVQ  R9, R11
	ANDQ  $7, R11
	TESTQ R11, R11
	JEQ   clamp_done
	XORL  BX, BX
	XORL  CX, CX
	MOVQ  R9, DX
	ANDQ  $-2, DX

clamp2:
	CMPQ AX, DX
	JGE  clamp1
	MOVUPD   (SI)(AX*8), X0
	SUBPD    X6, X0
	MOVAPD   X0, X13
	CMPPD    X7, X13, $5
	MINPD    X7, X0
	MOVUPD   X0, (SI)(AX*8)
	MOVMSKPD X13, R10
	SHLL     CX, R10
	ORL      R10, BX
	ADDQ     $2, AX
	ADDL     $2, CX
	JMP      clamp2

clamp1:
	CMPQ     AX, R9
	JGE      clamp_store
	MOVSD    (SI)(AX*8), X0
	SUBSD    X6, X0
	MOVAPD   X0, X13
	CMPSD    X7, X13, $5
	MINSD    X7, X0
	MOVSD    X0, (SI)(AX*8)
	MOVMSKPD X13, R10
	ANDL     $1, R10
	SHLL     CX, R10
	ORL      R10, BX

clamp_store:
	ORB BX, (R8)

clamp_done:
	RET

// func findBlocksStep(cost, insertCost []float64, sig []byte, switchCost float64) (minCost float64, best int)
//
// findBlocksDPStep and findBlocksClamp back to back in one call. The caller ran
// them as two, paying a Go->asm transition per histogram row per byte; the
// bodies are unchanged, minCost simply stays in a register between them.
TEXT ·findBlocksStep(SB), NOSPLIT|NOFRAME, $0-96
	MOVQ cost_base+0(FP), SI
	MOVQ cost_len+8(FP), CX
	MOVQ insertCost_base+24(FP), DI

	MOVQ     $0x547d42aea2879f2e, BX
	MOVQ     BX, X12
	UNPCKLPD X12, X12
	MOVAPD   X12, X8
	MOVAPD   X12, X9
	MOVAPD   X12, X10
	MOVAPD   X12, X11

	XORQ AX, AX
	MOVQ CX, DX
	ANDQ $-8, DX

fs_add8:
	CMPQ AX, DX
	JGE  fs_add2setup
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
	JMP    fs_add8

fs_add2setup:
	MOVQ CX, DX
	ANDQ $-2, DX

fs_add2:
	CMPQ AX, DX
	JGE  fs_fold
	MOVUPD (SI)(AX*8), X0
	MOVUPD (DI)(AX*8), X4
	ADDPD  X4, X0
	MOVUPD X0, (SI)(AX*8)
	MINPD  X8, X0
	MOVAPD X0, X8
	ADDQ   $2, AX
	JMP    fs_add2

fs_fold:
	MINPD   X9, X8
	MINPD   X11, X10
	MINPD   X10, X8
	MOVHLPS X8, X0
	MINSD   X0, X8

	CMPQ   AX, CX
	JGE    fs_guard
	MOVSD  (SI)(AX*8), X0
	ADDSD  (DI)(AX*8), X0
	MOVSD  X0, (SI)(AX*8)
	MINSD  X8, X0
	MOVAPD X0, X8

fs_guard:
	UCOMISD X12, X8
	JCS     fs_scan
	MOVSD   X12, minCost+80(FP)
	MOVQ    $0, best+88(FP)
	MOVAPD  X12, X6
	JMP     fs_clamp_setup

fs_scan:
	MOVAPD   X8, X5
	UNPCKLPD X5, X5
	XORQ     AX, AX
	MOVQ     CX, DX
	ANDQ     $-2, DX

fs_eq2:
	CMPQ AX, DX
	JGE  fs_eq1
	MOVUPD   (SI)(AX*8), X0
	CMPPD    X5, X0, $0
	MOVMSKPD X0, BX
	ADDQ     $2, AX
	TESTL    BX, BX
	JEQ      fs_eq2
	BSFL BX, BX
	LEAQ -2(AX)(BX*1), AX
	JMP  fs_found

fs_eq1:
	CMPQ  AX, CX
	JLT   fs_found
	MOVSD X8, minCost+80(FP)
	MOVQ  $0, best+88(FP)
	MOVAPD X8, X6
	JMP   fs_clamp_setup

fs_found:
	MOVSD  (SI)(AX*8), X6
	MOVSD  X6, minCost+80(FP)
	MOVQ   AX, best+88(FP)

fs_clamp_setup:
	MOVQ     sig_base+48(FP), R8
	MOVQ     CX, R9
	MOVSD    switchCost+72(FP), X7
	UNPCKLPD X6, X6
	UNPCKLPD X7, X7

	XORQ AX, AX
	MOVQ R9, DX
	ANDQ $-8, DX

fs_clamp8:
	CMPQ AX, DX
	JGE  fs_clamp_tail
	MOVUPD (SI)(AX*8), X0
	MOVUPD 16(SI)(AX*8), X1
	MOVUPD 32(SI)(AX*8), X2
	MOVUPD 48(SI)(AX*8), X3
	SUBPD  X6, X0
	SUBPD  X6, X1
	SUBPD  X6, X2
	SUBPD  X6, X3
	MOVAPD X0, X13
	MOVAPD X1, X14
	MOVAPD X2, X15
	MOVAPD X3, X12
	CMPPD X7, X13, $5
	CMPPD X7, X14, $5
	CMPPD X7, X15, $5
	CMPPD X7, X12, $5
	MINPD  X7, X0
	MINPD  X7, X1
	MINPD  X7, X2
	MINPD  X7, X3
	MOVUPD X0, (SI)(AX*8)
	MOVUPD X1, 16(SI)(AX*8)
	MOVUPD X2, 32(SI)(AX*8)
	MOVUPD X3, 48(SI)(AX*8)
	MOVMSKPD X13, BX
	MOVMSKPD X14, R10
	MOVMSKPD X15, R11
	MOVMSKPD X12, R12
	SHLL     $2, R10
	SHLL     $4, R11
	SHLL     $6, R12
	ORL      R10, BX
	ORL      R12, R11
	ORL      R11, BX
	ORB  BX, (R8)
	INCQ R8
	ADDQ $8, AX
	JMP  fs_clamp8

fs_clamp_tail:
	MOVQ  R9, R11
	ANDQ  $7, R11
	TESTQ R11, R11
	JEQ   fs_clamp_done
	XORL  BX, BX
	XORL  CX, CX
	MOVQ  R9, DX
	ANDQ  $-2, DX

fs_clamp2:
	CMPQ AX, DX
	JGE  fs_clamp1
	MOVUPD   (SI)(AX*8), X0
	SUBPD    X6, X0
	MOVAPD   X0, X13
	CMPPD    X7, X13, $5
	MINPD    X7, X0
	MOVUPD   X0, (SI)(AX*8)
	MOVMSKPD X13, R10
	SHLL     CX, R10
	ORL      R10, BX
	ADDQ     $2, AX
	ADDL     $2, CX
	JMP      fs_clamp2

fs_clamp1:
	CMPQ     AX, R9
	JGE      fs_clamp_store
	MOVSD    (SI)(AX*8), X0
	SUBSD    X6, X0
	MOVAPD   X0, X13
	CMPSD    X7, X13, $5
	MINSD    X7, X0
	MOVSD    X0, (SI)(AX*8)
	MOVMSKPD X13, R10
	ANDL     $1, R10
	SHLL     CX, R10
	ORL      R10, BX

fs_clamp_store:
	ORB BX, (R8)

fs_clamp_done:
	RET

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
// findBlocks never calls this with fewer than two histograms. Up to eight keep
// cost in X0-X3 for the whole pass and match the scalar loop bit for bit, NaN
// included. More run findBlocksStep's body per byte, its NaN handling
// included, with the first-index scan folded into the clamp pass.
TEXT ·findBlocksDP(SB), NOSPLIT, $24-128
	MOVQ cost_len+56(FP), CX
	CMPQ CX, $8
	JGT  fb_wide

	// Register r holds lanes o_r and o_r+1, o_r = min(2r, n-2). Past n the
	// registers repeat the last pair, and for odd n the last pair overlaps the
	// one before it, so no lane is padding. A repeated lane goes through the
	// same operations as its original and keeps the same bits.
	LEAQ    -2(CX), AX
	MOVQ    $2, R10
	CMPQ    R10, AX
	CMOVQGT AX, R10
	MOVQ    $4, R11
	CMPQ    R11, AX
	CMOVQGT AX, R11
	MOVQ    $6, R12
	CMPQ    R12, AX
	CMOVQGT AX, R12

	// Multiplying register r's two-bit MOVMSKPD mask by 1<<o_r moves the bits
	// onto its lanes, and ORing the products merges repeated lanes, which
	// always agree.
	MOVQ R10, CX
	MOVL $1, AX
	SHLL CX, AX
	MOVL AX, m1-8(SP)
	MOVQ R11, CX
	MOVL $1, AX
	SHLL CX, AX
	MOVL AX, m2-16(SP)
	MOVQ R12, CX
	MOVL $1, AX
	SHLL CX, AX
	MOVL AX, m3-24(SP)
	SHLQ $3, R10
	SHLQ $3, R11
	SHLQ $3, R12

	MOVQ   data_base+0(FP), SI
	MOVQ   insertCost_base+24(FP), DI
	MOVQ   switchSignal_base+72(FP), R8
	MOVQ   blockID_base+96(FP), R13
	MOVQ   cost_base+48(FP), AX
	MOVUPD (AX), X0
	MOVUPD (AX)(R10*1), X1
	MOVUPD (AX)(R11*1), X2
	MOVUPD (AX)(R12*1), X3

	// X8 = noMinCost and X12 = blockSwitchBitcost in both lanes, X13 = m and
	// X14 = 0.77 in the low lane.
	MOVQ     $0x547d42aea2879f2e, AX
	MOVQ     AX, X8
	UNPCKLPD X8, X8
	MOVSD    blockSwitchBitcost+120(FP), X12
	UNPCKLPD X12, X12
	MOVQ     $0x3f02599ed7c6fbd2, AX
	MOVQ     AX, X13
	MOVQ     $0x3fe8a3d70a3d70a4, AX
	MOVQ     AX, X14
	XORQ     R9, R9

fb_small:
	CMPQ R9, data_len+8(FP)
	JGE  fb_small_done

	MOVAPD   X12, X9
	CMPQ     R9, $2000
	JGE      fb_small_row
	XORPS    X15, X15
	CVTSQ2SD R9, X15
	MULSD    X13, X15
	ADDSD    X14, X15
	MULSD    X12, X15
	UNPCKLPD X15, X15
	MOVAPD   X15, X9

fb_small_row:
	MOVWLZX (SI)(R9*2), BX
	IMULQ   cost_len+56(FP), BX
	LEAQ    (DI)(BX*8), BX
	MOVUPD  (BX), X4
	MOVUPD  (BX)(R10*1), X5
	MOVUPD  (BX)(R11*1), X6
	MOVUPD  (BX)(R12*1), X7
	ADDPD   X4, X0
	ADDPD   X5, X1
	ADDPD   X6, X2
	ADDPD   X7, X3

	// Every MINPD has lane values as its destination and a NaN-free source, so
	// a NaN lane drops out as it does from cost[k] < minCost, and noMinCost
	// survives only when no lane is below it. The swap leaves the minimum in
	// both lanes of X6.
	MOVAPD X1, X4
	MINPD  X8, X4
	MOVAPD X3, X5
	MINPD  X8, X5
	MOVAPD X0, X6
	MINPD  X4, X6
	MOVAPD X2, X7
	MINPD  X5, X7
	MINPD  X7, X6
	MOVAPD X6, X10
	SHUFPD $1, X10, X10
	MINPD  X10, X6

	// Nothing beat noMinCost: X6 holds exactly it, the scalar loop's minCost,
	// and the block type stays unwritten.
	UCOMISD X8, X6
	JCC     fb_small_clamp

	MOVAPD   X0, X4
	CMPPD    X6, X4, $0
	MOVAPD   X1, X5
	CMPPD    X6, X5, $0
	MOVAPD   X2, X7
	CMPPD    X6, X7, $0
	MOVAPD   X3, X10
	CMPPD    X6, X10, $0
	MOVMSKPD X4, AX
	MOVMSKPD X5, CX
	MOVMSKPD X7, DX
	MOVMSKPD X10, R15
	IMULL    m1-8(SP), CX
	IMULL    m2-16(SP), DX
	IMULL    m3-24(SP), R15
	ORL      CX, AX
	ORL      R15, DX
	ORL      DX, AX

	// The lowest set bit is the first index holding the minimum.
	BSFL AX, BX
	MOVB BX, (R13)(R9*1)

	// A nonzero minimum has the bits of every lane equal to it. A zero one
	// must carry the sign of cost[best], as the scalar loop's does; a MINPD
	// tie between +0 and -0 need not.
	MOVQ     X6, AX
	SHLQ     $1, AX
	JNE      fb_small_clamp
	MOVMSKPD X0, AX
	MOVMSKPD X1, CX
	MOVMSKPD X2, DX
	MOVMSKPD X3, R15
	IMULL    m1-8(SP), CX
	IMULL    m2-16(SP), DX
	IMULL    m3-24(SP), R15
	ORL      CX, AX
	ORL      R15, DX
	ORL      DX, AX
	BTL      BX, AX
	SBBQ     AX, AX
	SHLQ     $63, AX
	MOVQ     AX, X6
	UNPCKLPD X6, X6

fb_small_clamp:
	SUBPD X6, X0
	SUBPD X6, X1
	SUBPD X6, X2
	SUBPD X6, X3

	// Predicate 2 is LE: switchCost <= cost, which is cost >= switchCost for
	// ordered operands and false on NaN, like the scalar branch.
	MOVAPD   X9, X4
	CMPPD    X0, X4, $2
	MOVAPD   X9, X5
	CMPPD    X1, X5, $2
	MOVAPD   X9, X6
	CMPPD    X2, X6, $2
	MOVAPD   X9, X7
	CMPPD    X3, X7, $2
	MOVMSKPD X4, AX
	MOVMSKPD X5, CX
	MOVMSKPD X6, DX
	MOVMSKPD X7, R15
	IMULL    m1-8(SP), CX
	IMULL    m2-16(SP), DX
	IMULL    m3-24(SP), R15
	ORL      CX, AX
	ORL      R15, DX
	ORL      DX, AX
	ORB      AX, (R8)(R9*1)

	// switchCost is the destination: MINPD returns its source when the
	// operands are equal or unordered, so a NaN lane stays NaN as in the scalar
	// loop, and cost == switchCost keeps bits identical to switchCost's, which
	// is never zero.
	MOVAPD X9, X4
	MINPD  X0, X4
	MOVAPD X4, X0
	MOVAPD X9, X5
	MINPD  X1, X5
	MOVAPD X5, X1
	MOVAPD X9, X6
	MINPD  X2, X6
	MOVAPD X6, X2
	MOVAPD X9, X7
	MINPD  X3, X7
	MOVAPD X7, X3

	INCQ R9
	JMP  fb_small

fb_small_done:
	MOVQ   cost_base+48(FP), AX
	MOVUPD X0, (AX)
	MOVUPD X1, (AX)(R10*1)
	MOVUPD X2, (AX)(R11*1)
	MOVUPD X3, (AX)(R12*1)
	RET

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
	CMPPD    X13, X4, $5
	CMPPD    X13, X5, $5
	CMPPD    X13, X7, $5
	CMPPD    X13, X14, $5
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
