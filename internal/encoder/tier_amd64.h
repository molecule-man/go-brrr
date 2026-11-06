// cmd/go defines only the exact level for the assembler: GOAMD64_v4 does not
// imply GOAMD64_v3. Kernels select on these cumulative feature names instead.

#ifdef GOAMD64_v3
#define hasAVX2
#endif

#ifdef GOAMD64_v4
#define hasAVX2
#define hasAVX512
#endif
