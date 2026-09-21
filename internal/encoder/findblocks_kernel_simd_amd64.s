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
