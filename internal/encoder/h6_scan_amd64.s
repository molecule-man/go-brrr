//go:build amd64 && !purego

#include "textflag.h"

// func h6FindInBucket(data unsafe.Pointer, block *h6Block, tag uint8,
//	n, cur, curMasked, mask, minPrev, maxLength, bestLen, bestScore uint,
//	next *h6Block) (length, distance, score uint)
//
// Registers: SI data, DI positions of block, AX matches, R9 curMasked, R10 mask,
// R11 first 4 bytes at cur, R12 bestScore, R13 bestLen, R14 probe at
// cur+bestLen-3. 0(SP) holds prevRaw and 8(SP) matches during a match,
// 16(SP) holds head.
// TZCNT runs as BSF on CPUs without BMI1. Both give the same result for a
// non-zero input.
TEXT ·h6FindInBucket(SB), NOSPLIT, $24-120
	MOVQ       next+88(FP), BX
	PREFETCHT0 (BX)
	PREFETCHT0 76(BX)

	// Tag mask of the block, rotated so bit 0 is the newest slot.
	MOVQ      block+8(FP), DI
	MOVBLZX   tag+16(FP), AX
	MOVD      AX, X0
	PUNPCKLBW X0, X0
	PSHUFLW   $0, X0, X0
	PSHUFD    $0, X0, X0
	MOVOU     (DI), X1
	PCMPEQB   X0, X1
	PMOVMSKB  X1, AX
	MOVQ      n+24(FP), DX
	LEAL      1(DX), CX
	ANDL      $15, CX
	MOVQ      CX, 16(SP)
	RORW      CX, AX
	MOVWLZX   AX, AX

	// Drop the slots that hold no entry yet: 0xFFFF-n entries are stored.
	MOVL  $0xFFFF, CX
	SUBL  DX, CX
	CMPL  CX, $16
	JCC   allstored
	MOVL  $1, DX
	SHLL  CX, DX
	DECL  DX
	ANDL  DX, AX

allstored:
	ADDQ $16, DI
	MOVQ data+0(FP), SI
	MOVQ curMasked+40(FP), R9
	MOVQ mask+48(FP), R10
	MOVQ bestLen+72(FP), R13
	MOVQ bestScore+80(FP), R12
	MOVQ R12, score+112(FP)
	MOVL (SI)(R9*1), R11
	LEAQ (R9)(R13*1), CX
	CMPQ CX, R10
	JHI  done
	MOVL -3(SI)(CX*1), R14
	TESTL AX, AX
	JZ   done

loop:
	XORL   CX, CX
	TZCNTL AX, CX
	ADDQ   16(SP), CX
	ANDL   $15, CX
	MOVL   (DI)(CX*4), BX
	CMPQ   BX, minPrev+56(FP)
	JCS    done
	MOVQ   BX, DX
	ANDQ   R10, DX
	LEAQ   (DX)(R13*1), CX
	CMPL   -3(SI)(CX*1), R14
	JNE    next
	CMPQ   CX, R10
	JHI    next
	CMPL   (SI)(DX*1), R11
	JNE    next

	// Match length from byte 4, limit maxLength-4.
	MOVQ BX, 0(SP)
	MOVL AX, 8(SP)
	LEAQ 4(SI)(DX*1), CX
	LEAQ 4(SI)(R9*1), R8
	MOVQ maxLength+64(FP), DX
	SUBQ $12, DX
	XORL BX, BX

len8:
	CMPQ BX, DX
	JGT  lentail
	MOVQ (CX)(BX*1), AX
	XORQ (R8)(BX*1), AX
	JNZ  lendiff
	ADDQ $8, BX
	JMP  len8

lendiff:
	TZCNTQ AX, AX
	SHRQ   $3, AX
	ADDQ   AX, BX
	JMP    lendone

lentail:
	ADDQ $8, DX

lentailloop:
	CMPQ    BX, DX
	JGE     lendone
	MOVBLZX (CX)(BX*1), AX
	CMPB    AL, (R8)(BX*1)
	JNE     lendone
	INCQ    BX
	JMP     lentailloop

lendone:
	ADDQ $4, BX

	// score = 1920 + 135*len - 30*floor(log2(backward)); backward > 0.
	MOVQ   cur+32(FP), CX
	SUBQ   0(SP), CX
	BSRQ   CX, DX
	IMUL3Q $30, DX, DX
	IMUL3Q $135, BX, R8
	ADDQ   $1920, R8
	SUBQ   DX, R8
	MOVL   8(SP), AX
	CMPQ   R8, R12
	JLS    next
	MOVQ   R8, R12
	MOVQ   BX, R13
	MOVQ   BX, length+96(FP)
	MOVQ   CX, distance+104(FP)
	MOVQ   R8, score+112(FP)
	LEAQ   (R9)(R13*1), CX
	CMPQ   CX, R10
	JHI    done
	MOVL   -3(SI)(CX*1), R14

next:
	LEAL -1(AX), CX
	ANDL CX, AX
	JNZ  loop

done:
	RET
