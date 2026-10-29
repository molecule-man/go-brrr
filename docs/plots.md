# Benchmark plots

- [Methodology](#methodology)
- [AMD](#amd): [0-64k](#0-64k-1) · [64k-1m](#64k-1m-1) · [1m-8m](#1m-8m-1) · [8m-inf](#8m-inf-1) · [silesia](#silesia-1)
- [Intel](#intel): [0-64k](#0-64k) · [64k-1m](#64k-1m) · [1m-8m](#1m-8m) · [8m-inf](#8m-inf) · [silesia](#silesia)

  ## Methodology

Each plot is one set of files. The benchmark compresses the files one by one with one encoder per library and level. The encoder is reused, with `Reset` between files. Each file goes to the encoder in one `Write` call. The encoder thus sees the full input size which basically means `SizeHint` = file size.

- Speed: total input bytes / time. Ratio: total input bytes / total compressed bytes.
- One CPU core, single thread (`-cpu 1`, `GODEBUG=asyncpreemptoff=1`).
- Each value is the benchstat median of 6 runs of at least 1 s.
- The machines are "denoised" by a script which turns off turbo boost, the SMT sibling of the bench core, CPU idle states deeper than C1, the NMI watchdog, transparent huge pages and ASLR. It sets the performance governor and power profile. On Intel, it also locks the uncore frequency at its maximum.
- zstd: [klauspost/compress](https://github.com/klauspost/compress), concurrency 1. gzip: Go standard library.
- Decompression: go-brrr and gzip use a streaming reader with `Reset`. zstd uses `DecodeAll` on one decoder.
- Intel: Core i5-12500, Go 1.27.1. AMD: Ryzen 5 7535HS, Go 1.26.0.

The benchmarks are run on groups of files split by size because library chooses different hashers for different size hints:
- 0-64k: ≤ 64 KiB. The encoder uses its small-input hashers.
- 64k-1m: > 64 KiB and < 1 MiB. Default hashers.
- 1m-8m: 1 MiB to 8 MiB. Hashers and context modeling for large inputs.
- 8m-inf: > 8 MiB. The 8 MiB ring buffer (default window lgwin=22) wraps.

## AMD

```
cpu: AMD Ryzen 5 7535HS with Radeon Graphics
```

### 0-64k

Small files below

| Compression | Decompression |
|---|---|
| ![css 0-64k](../assets/ryzen-7535hs/0-64k/css.svg) | ![css 0-64k](../assets/ryzen-7535hs/0-64k/css_decompress.svg) |
| ![html 0-64k](../assets/ryzen-7535hs/0-64k/html.svg) | ![html 0-64k](../assets/ryzen-7535hs/0-64k/html_decompress.svg) |
| ![js 0-64k](../assets/ryzen-7535hs/0-64k/js.svg) | ![js 0-64k](../assets/ryzen-7535hs/0-64k/js_decompress.svg) |
| ![json 0-64k](../assets/ryzen-7535hs/0-64k/json.svg) | ![json 0-64k](../assets/ryzen-7535hs/0-64k/json_decompress.svg) |
| ![prose 0-64k](../assets/ryzen-7535hs/0-64k/prose.svg) | ![prose 0-64k](../assets/ryzen-7535hs/0-64k/prose_decompress.svg) |

### 64k-1m

| Compression | Decompression |
|---|---|
| ![css 64k-1m](../assets/ryzen-7535hs/64k-1m/css.svg) | ![css 64k-1m](../assets/ryzen-7535hs/64k-1m/css_decompress.svg) |
| ![html 64k-1m](../assets/ryzen-7535hs/64k-1m/html.svg) | ![html 64k-1m](../assets/ryzen-7535hs/64k-1m/html_decompress.svg) |
| ![js 64k-1m](../assets/ryzen-7535hs/64k-1m/js.svg) | ![js 64k-1m](../assets/ryzen-7535hs/64k-1m/js_decompress.svg) |
| ![json 64k-1m](../assets/ryzen-7535hs/64k-1m/json.svg) | ![json 64k-1m](../assets/ryzen-7535hs/64k-1m/json_decompress.svg) |
| ![prose 64k-1m](../assets/ryzen-7535hs/64k-1m/prose.svg) | ![prose 64k-1m](../assets/ryzen-7535hs/64k-1m/prose_decompress.svg) |

### 1m-8m

| Compression | Decompression |
|---|---|
| ![css 1m-8m](../assets/ryzen-7535hs/1m-8m/css.svg) | ![css 1m-8m](../assets/ryzen-7535hs/1m-8m/css_decompress.svg) |
| ![html 1m-8m](../assets/ryzen-7535hs/1m-8m/html.svg) | ![html 1m-8m](../assets/ryzen-7535hs/1m-8m/html_decompress.svg) |
| ![js 1m-8m](../assets/ryzen-7535hs/1m-8m/js.svg) | ![js 1m-8m](../assets/ryzen-7535hs/1m-8m/js_decompress.svg) |
| ![json 1m-8m](../assets/ryzen-7535hs/1m-8m/json.svg) | ![json 1m-8m](../assets/ryzen-7535hs/1m-8m/json_decompress.svg) |
| ![prose 1m-8m](../assets/ryzen-7535hs/1m-8m/prose.svg) | ![prose 1m-8m](../assets/ryzen-7535hs/1m-8m/prose_decompress.svg) |

### 8m-inf

| Compression | Decompression |
|---|---|
| ![html 8m-inf](../assets/ryzen-7535hs/8m-inf/html.svg) | ![html 8m-inf](../assets/ryzen-7535hs/8m-inf/html_decompress.svg) |
| ![js 8m-inf](../assets/ryzen-7535hs/8m-inf/js.svg) | ![js 8m-inf](../assets/ryzen-7535hs/8m-inf/js_decompress.svg) |
| ![json 8m-inf](../assets/ryzen-7535hs/8m-inf/json.svg) | ![json 8m-inf](../assets/ryzen-7535hs/8m-inf/json_decompress.svg) |

### silesia

| Compression | Decompression |
|---|---|
| ![silesia](../assets/ryzen-7535hs/silesia/silesia.svg) | ![silesia](../assets/ryzen-7535hs/silesia/silesia_decompress.svg) |


## Intel

```
cpu: 12th Gen Intel(R) Core(TM) i5-12500
```

### 0-64k

| Compression | Decompression |
|---|---|
| ![css 0-64k](../assets/i5-12500/0-64k/css.svg) | ![css 0-64k](../assets/i5-12500/0-64k/css_decompress.svg) |
| ![html 0-64k](../assets/i5-12500/0-64k/html.svg) | ![html 0-64k](../assets/i5-12500/0-64k/html_decompress.svg) |
| ![js 0-64k](../assets/i5-12500/0-64k/js.svg) | ![js 0-64k](../assets/i5-12500/0-64k/js_decompress.svg) |
| ![json 0-64k](../assets/i5-12500/0-64k/json.svg) | ![json 0-64k](../assets/i5-12500/0-64k/json_decompress.svg) |
| ![prose 0-64k](../assets/i5-12500/0-64k/prose.svg) | ![prose 0-64k](../assets/i5-12500/0-64k/prose_decompress.svg) |

### 64k-1m

| Compression | Decompression |
|---|---|
| ![css 64k-1m](../assets/i5-12500/64k-1m/css.svg) | ![css 64k-1m](../assets/i5-12500/64k-1m/css_decompress.svg) |
| ![html 64k-1m](../assets/i5-12500/64k-1m/html.svg) | ![html 64k-1m](../assets/i5-12500/64k-1m/html_decompress.svg) |
| ![js 64k-1m](../assets/i5-12500/64k-1m/js.svg) | ![js 64k-1m](../assets/i5-12500/64k-1m/js_decompress.svg) |
| ![json 64k-1m](../assets/i5-12500/64k-1m/json.svg) | ![json 64k-1m](../assets/i5-12500/64k-1m/json_decompress.svg) |
| ![prose 64k-1m](../assets/i5-12500/64k-1m/prose.svg) | ![prose 64k-1m](../assets/i5-12500/64k-1m/prose_decompress.svg) |

### 1m-8m

| Compression | Decompression |
|---|---|
| ![css 1m-8m](../assets/i5-12500/1m-8m/css.svg) | ![css 1m-8m](../assets/i5-12500/1m-8m/css_decompress.svg) |
| ![html 1m-8m](../assets/i5-12500/1m-8m/html.svg) | ![html 1m-8m](../assets/i5-12500/1m-8m/html_decompress.svg) |
| ![js 1m-8m](../assets/i5-12500/1m-8m/js.svg) | ![js 1m-8m](../assets/i5-12500/1m-8m/js_decompress.svg) |
| ![json 1m-8m](../assets/i5-12500/1m-8m/json.svg) | ![json 1m-8m](../assets/i5-12500/1m-8m/json_decompress.svg) |
| ![prose 1m-8m](../assets/i5-12500/1m-8m/prose.svg) | ![prose 1m-8m](../assets/i5-12500/1m-8m/prose_decompress.svg) |

### 8m-inf

| Compression | Decompression |
|---|---|
| ![html 8m-inf](../assets/i5-12500/8m-inf/html.svg) | ![html 8m-inf](../assets/i5-12500/8m-inf/html_decompress.svg) |
| ![js 8m-inf](../assets/i5-12500/8m-inf/js.svg) | ![js 8m-inf](../assets/i5-12500/8m-inf/js_decompress.svg) |
| ![json 8m-inf](../assets/i5-12500/8m-inf/json.svg) | ![json 8m-inf](../assets/i5-12500/8m-inf/json_decompress.svg) |

### silesia

| Compression | Decompression |
|---|---|
| ![silesia](../assets/i5-12500/silesia/silesia.svg) | ![silesia](../assets/i5-12500/silesia/silesia_decompress.svg) |
