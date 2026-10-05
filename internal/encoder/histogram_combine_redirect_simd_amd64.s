// Assembly for histogram_combine_redirect_simd_amd64.go.

//go:build amd64 && !purego

#include "textflag.h"
#include "tier_amd64.h"

#ifdef GOAMD64_v2
#define hasSSE41
#endif
#ifdef hasAVX2
#define hasSSE41
#endif

#ifdef hasSSE41
// X4 = old, X5 = replacement. PBLENDVB takes its mask from X0.
#define SSE_VEC(off) \
	MOVOU    off(SI), X2; \
	MOVO     X2, X0; \
	PCMPEQL  X4, X0; \
	PBLENDVB X0, X5, X2; \
	MOVOU    X2, off(SI)
#else
// X4 = old, X5 = old^replacement: a matching lane XORs to replacement.
#define SSE_VEC(off) \
	MOVOU   off(SI), X2; \
	MOVO    X2, X3; \
	PCMPEQL X4, X3; \
	PAND    X5, X3; \
	PXOR    X3, X2; \
	MOVOU   X2, off(SI)
#endif

#define AVX2_VEC(off) \
	VMOVDQU   off(SI), Y2; \
	VPCMPEQD  Y0, Y2, Y3; \
	VPBLENDVB Y3, Y1, Y2, Y2; \
	VMOVDQU   Y2, off(SI)

#define AVX512_VEC(off) \
	VPCMPEQD  off(SI), Z0, K1; \
	VMOVDQU32 Z1, K1, off(SI)

// func histogramCombineRedirect(s []uint32, old, replacement uint32)
//
// Rewrites every old in s to replacement, four vectors per iteration. The
// last vector ends at len(s) and overlaps the one before it, which is safe
// because a rewritten lane no longer equals old. Inputs shorter than one
// vector fall back to the next narrower body, then to a scalar loop.
TEXT ·histogramCombineRedirect(SB), NOSPLIT|NOFRAME, $0-32
	MOVQ s_base+0(FP), SI
	MOVQ s_len+8(FP), CX
	MOVL old+24(FP), AX
	MOVL replacement+28(FP), DX
#ifdef hasAVX512
	CMPQ CX, $16
	JGE  avx512
#endif
#ifdef hasAVX2
	CMPQ CX, $8
	JGE  avx2
#endif
	CMPQ CX, $4
	JGE  sse

	TESTQ CX, CX
	JEQ   done

scalar:
	CMPL (SI), AX
	JNE  scalar_next
	MOVL DX, (SI)

scalar_next:
	ADDQ $4, SI
	DECQ CX
	JNE  scalar

done:
	RET

sse:
	MOVL   AX, X4
	PSHUFD $0, X4, X4
#ifndef hasSSE41
	XORL   AX, DX
#endif
	MOVL   DX, X5
	PSHUFD $0, X5, X5
	LEAQ   -16(SI)(CX*4), DI
	MOVQ   CX, BX
	SHRQ   $4, BX
	JEQ    sse_one

sse_four:
	SSE_VEC(0)
	SSE_VEC(16)
	SSE_VEC(32)
	SSE_VEC(48)
	ADDQ $64, SI
	DECQ BX
	JNE  sse_four

sse_one:
	MOVQ CX, BX
	ANDQ $15, BX
	SHRQ $2, BX
	JEQ  sse_tail

sse_one_loop:
	SSE_VEC(0)
	ADDQ $16, SI
	DECQ BX
	JNE  sse_one_loop

sse_tail:
	TESTQ $3, CX
	JEQ   done
	MOVQ  DI, SI
	SSE_VEC(0)
	RET

#ifdef hasAVX2
avx2:
	MOVL         AX, X0
	VPBROADCASTD X0, Y0
	MOVL         DX, X1
	VPBROADCASTD X1, Y1
	LEAQ         -32(SI)(CX*4), DI
	MOVQ         CX, BX
	SHRQ         $5, BX
	JEQ          avx2_one

avx2_four:
	AVX2_VEC(0)
	AVX2_VEC(32)
	AVX2_VEC(64)
	AVX2_VEC(96)
	ADDQ $128, SI
	DECQ BX
	JNE  avx2_four

avx2_one:
	MOVQ CX, BX
	ANDQ $31, BX
	SHRQ $3, BX
	JEQ  avx2_tail

avx2_one_loop:
	AVX2_VEC(0)
	ADDQ $32, SI
	DECQ BX
	JNE  avx2_one_loop

avx2_tail:
	TESTQ $7, CX
	JEQ   avx2_done
	MOVQ  DI, SI
	AVX2_VEC(0)

avx2_done:
	VZEROUPPER
	RET
#endif

#ifdef hasAVX512
avx512:
	VPBROADCASTD AX, Z0
	VPBROADCASTD DX, Z1
	LEAQ         -64(SI)(CX*4), DI
	MOVQ         CX, BX
	SHRQ         $6, BX
	JEQ          avx512_one

avx512_four:
	AVX512_VEC(0)
	AVX512_VEC(64)
	AVX512_VEC(128)
	AVX512_VEC(192)
	ADDQ $256, SI
	DECQ BX
	JNE  avx512_four

avx512_one:
	MOVQ CX, BX
	ANDQ $63, BX
	SHRQ $4, BX
	JEQ  avx512_tail

avx512_one_loop:
	AVX512_VEC(0)
	ADDQ $64, SI
	DECQ BX
	JNE  avx512_one_loop

avx512_tail:
	TESTQ $15, CX
	JEQ   avx512_done
	MOVQ  DI, SI
	AVX512_VEC(0)

avx512_done:
	VZEROUPPER
	RET
#endif
