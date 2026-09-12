//go:build go1.27 && amd64 && !purego

#include "textflag.h"

// func histogramCombineRedirectAVX512Masked(s []uint32, old, replacement uint32)
// Sixteen uint32 per iteration. VPCMPEQD produces an opmask and VMOVDQU32
// writes only the matching lanes, so no blend and no read-modify-write.
TEXT ·histogramCombineRedirectAVX512Masked(SB), NOSPLIT|NOFRAME, $0-32
	MOVQ         s_base+0(FP), SI
	MOVQ         s_len+8(FP), CX
	MOVL         old+24(FP), R10
	MOVL         replacement+28(FP), R11
	VPBROADCASTD R10, Z0
	VPBROADCASTD R11, Z1
	XORQ         AX, AX
	MOVQ         CX, DX
	SUBQ         $16, DX
	JL           tail

loop:
	CMPQ      AX, DX
	JG        tail
	VMOVDQU32 (SI)(AX*4), Z2
	VPCMPEQD  Z0, Z2, K1
	VMOVDQU32 Z1, K1, (SI)(AX*4)
	ADDQ      $16, AX
	JMP       loop

tail:
	CMPQ AX, CX
	JGE  done
	MOVL (SI)(AX*4), R8
	CMPL R8, R10
	JNE  next
	MOVL R11, (SI)(AX*4)

next:
	INCQ AX
	JMP  tail

done:
	VZEROUPPER
	RET
