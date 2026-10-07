//go:build amd64 && !purego

#include "textflag.h"

// func tagMask32(tags *[32]uint8, tag uint8, next0, next1 unsafe.Pointer) uint32
TEXT ·tagMask32(SB), NOSPLIT|NOFRAME, $0-36
	MOVQ    tags+0(FP), SI
	MOVBLZX tag+8(FP), AX
	MOVQ    next0+16(FP), BX
	MOVQ    next1+24(FP), CX
	PREFETCHT0 (BX)
	PREFETCHT0 (CX)

	MOVD      AX, X0
	PUNPCKLBW X0, X0
	PSHUFLW   $0, X0, X0
	PSHUFD    $0, X0, X0

	MOVOU    0(SI), X1
	MOVOU    16(SI), X2
	PCMPEQB  X0, X1
	PCMPEQB  X0, X2
	PMOVMSKB X1, AX
	PMOVMSKB X2, DX
	SHLL     $16, DX
	ORL      DX, AX
	MOVL     AX, ret+32(FP)
	RET
