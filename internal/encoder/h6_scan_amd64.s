//go:build amd64 && !purego

#include "go_asm.h"
#include "textflag.h"

// func h6FindInBucket(h *h6, data unsafe.Pointer, keyTag, curMasked, mask,
//	minPrev, maxLength, bestLen, bestScore uint,
//	next *h6Block) (length, distance, score uint)
//
// Registers: SI data, DI positions of block, AX matches, R9 curMasked, R10 mask,
// R11 first 4 bytes at cur, R12 bestScore, R13 bestLen, R14 probe at
// cur+bestLen-3, R15 head. No frame and no stack locals: each stack store
// costs store-buffer entries.
// TZCNT runs as BSF on CPUs without BMI1. Both give the same result for a
// non-zero input.
TEXT ·h6FindInBucket(SB), NOSPLIT|NOFRAME, $0-104
	MOVQ       next+72(FP), BX
	PREFETCHT0 (BX)
	PREFETCHT0 76(BX)

	// Block and n of the key.
	MOVQ    h+0(FP), DI
	MOVQ    keyTag+16(FP), CX
	MOVBLZX CL, AX
	SHRQ    $8, CX
	MOVWLZX h6_num(DI)(CX*2), DX
	LEAQ    (CX)(CX*4), CX
	SHLQ    $4, CX
	LEAQ    h6_blocks(DI)(CX*1), DI

	// Tag mask of the block, rotated so bit 0 is the newest slot.
	MOVD      AX, X0
	PUNPCKLBW X0, X0
	PSHUFLW   $0, X0, X0
	PSHUFD    $0, X0, X0
	MOVOU     (DI), X1
	PCMPEQB   X0, X1
	PMOVMSKB  X1, AX
	LEAL      1(DX), R15
	ANDL      $15, R15
	MOVL      R15, CX
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
	ADDQ  $16, DI
	MOVQ  data+8(FP), SI
	MOVQ  curMasked+24(FP), R9
	MOVQ  mask+32(FP), R10
	MOVQ  bestLen+56(FP), R13
	MOVQ  bestScore+64(FP), R12
	MOVQ  R12, score+96(FP)
	MOVL  (SI)(R9*1), R11
	LEAQ  (R9)(R13*1), CX
	CMPQ  CX, R10
	JHI   done
	MOVL  -3(SI)(CX*1), R14
	TESTL AX, AX
	JZ    done

loop:
	XORL   CX, CX
	TZCNTL AX, CX
	ADDL   R15, CX
	ANDL   $15, CX
	MOVL   (DI)(CX*4), BX
	CMPQ   BX, minPrev+40(FP)
	JCS    done
	ANDQ   R10, BX
	LEAQ   (BX)(R13*1), CX
	CMPL   -3(SI)(CX*1), R14
	JNE    next
	CMPQ   CX, R10
	JHI    next
	CMPL   (SI)(BX*1), R11
	JNE    next

	// Match length from byte 4. BX counts the bytes left to maxLength-4.
	// CX and R8 move together, so R8-CX stays curMasked-prevMasked.
	LEAQ 4(SI)(BX*1), CX
	LEAQ 4(SI)(R9*1), R8
	MOVQ maxLength+48(FP), BX
	SUBQ $4, BX

len8:
	CMPQ BX, $8
	JLT  lentail
	MOVQ (CX), DX
	XORQ (R8), DX
	JNZ  lendiff
	ADDQ $8, CX
	ADDQ $8, R8
	SUBQ $8, BX
	JMP  len8

lendiff:
	TZCNTQ DX, DX
	SHRQ   $3, DX
	SUBQ   DX, BX
	JMP    lendone

lentail:
	CMPQ    BX, $0
	JLE     lendone
	MOVBLZX (CX), DX
	CMPB    DL, (R8)
	JNE     lendone
	INCQ    CX
	INCQ    R8
	DECQ    BX
	JMP     lentail

lendone:
	// DX length, R8 backward. backward < window, so the mask gives cur-prevRaw.
	MOVQ maxLength+48(FP), DX
	SUBQ BX, DX
	SUBQ CX, R8
	ANDQ R10, R8

	// score = 1920 + 135*len - 30*floor(log2(backward)); backward > 0.
	BSRQ   R8, CX
	IMUL3Q $30, CX, CX
	IMUL3Q $135, DX, BX
	ADDQ   $1920, BX
	SUBQ   CX, BX
	CMPQ   BX, R12
	JLS    next
	MOVQ   BX, R12
	MOVQ   DX, R13
	MOVQ   DX, length+80(FP)
	MOVQ   R8, distance+88(FP)
	MOVQ   BX, score+96(FP)
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
