![go brrr](assets/go-brrr-logo.jpg)

# go-brrr

[![CI](https://github.com/molecule-man/go-brrr/actions/workflows/ci.yml/badge.svg)](https://github.com/molecule-man/go-brrr/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/molecule-man/go-brrr.svg)](https://pkg.go.dev/github.com/molecule-man/go-brrr)
[![Go Report Card](https://goreportcard.com/badge/github.com/molecule-man/go-brrr)](https://goreportcard.com/report/github.com/molecule-man/go-brrr)
[![Version](https://img.shields.io/github/v/tag/molecule-man/go-brrr?sort=semver)](https://github.com/molecule-man/go-brrr/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Brotli compression library for Go (RFC 7932), with encoder and decoder support.

## Highlights

- **No C toolchain.** Builds with standard Go tooling.
- **Faster than other pure-Go brotli libraries** at every quality level we measure (see [Benchmarks](#benchmarks)).
- **Compound dictionaries.**
- **Encoder tuning.** `LGWin` (window size) and `SizeHint` (expected total input size) are exposed via `WriterOptions`. `SizeHint` lets the encoder pick context modeling and hasher parameters tuned for the actual payload size.

## Performance summary

Against [andybalholm/brotli](https://github.com/andybalholm/brotli) on the mixed benchmark corpus:

- Compression: **58.8% faster geomean**
- Streaming decompression: **70.3% faster geomean**
- One-shot decompression: **74.9% faster geomean**

Encoder + decoder.

## Status

v0.1.0. The encoder and decoder are covered by compatibility tests and fuzzing, but the public API may still evolve before v1.0.0.

## Compatibility

go-brrr implements Brotli RFC 7932 and is tested against the Brotli reference corpus. Encoded output is byte-compatible with the C reference implementation.

## Compared to other Go brotli libraries

| | go-brrr | [andybalholm](https://github.com/andybalholm/brotli) | [google/brotli/go/brotli](https://github.com/google/brotli/tree/master/go/brotli) | [cbrotli](https://github.com/google/brotli/tree/master/go/cbrotli) |
|---|---|---|---|---|
| Pure Go (no cgo) | ✓ | ✓ | ✓ | ✗ |
| Encoder | ✓ | ✓ | ✗ | ✓ |
| Decoder | ✓ | ✓ | ✓ | ✓ |
| Compound dictionaries (encode) | ✓ | ✗ | n/a | ✓ |
| Compound dictionaries (decode) | ✓ | ✗ | ✓ | ✓ |
| `LGWin` tuning | ✓ | ✓ | n/a | ✓ |
| `SizeHint` | ✓ | ✗ | n/a | ✗ |
| Writer `Reset` | ✓ | ✓ | n/a | ✗ |
| Reader `Reset` | ✓ | ✓ | ✗ | ✗ |

If you're using `andybalholm/brotli`, go-brrr is a near drop-in upgrade with higher throughput on both compression and decompression, plus compound-dictionary and `SizeHint` support. If you're using `cbrotli`, go-brrr trades roughly 7% on one-shot decompression (3.99 ms vs 3.71 ms geomean - see [Benchmarks](#benchmarks)) for: no cgo, multi-chunk compound dictionaries, and poolable `Writer`/`Reader` (cbrotli has no `Reset`, so each stream allocates a fresh encoder/decoder state - noticeable on many-small-file workloads).

## Install

```sh
go get github.com/molecule-man/go-brrr
```

```go
import "github.com/molecule-man/go-brrr"
```

The import path is `github.com/molecule-man/go-brrr`; the package name is `brrr`.

## Examples

### Compression

[embedmd]:# (example_test.go go /func Example_compress/ /^}/)
```go
func Example_compress() {
	input := []byte("Hello, brotli!")

	var compressed bytes.Buffer
	w, err := brrr.NewWriter(&compressed, 6)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := w.Write(input); err != nil {
		log.Fatal(err)
	}
	if err := w.Close(); err != nil {
		log.Fatal(err)
	}
}
```

More examples are available in [example_test.go](example_test.go) and the [Go package docs](https://pkg.go.dev/github.com/molecule-man/go-brrr#pkg-examples): round-trip compression and decompression, one-shot decompression, reusing writers and readers, pooling, and compound dictionaries.

## When to use go-brrr

The best use case for brotli is **static asset compression** - CSS, JS, HTML, fonts, WASM - where you compress once at build time and serve the result millions of times. Use **quality 11** for this: speed doesn't matter because you pay the cost once, and brotli q11 delivers ratios that neither gzip nor zstd can match. Every browser shipped since 2016 supports `Content-Encoding: br`.

For on-the-fly compression, brotli q5–6 is a strong choice if you're already using zstd at its highest level: q5 is often **faster** with a **better ratio**, and q6 is only slightly slower with an even better ratio. At lower compression levels, zstd is significantly faster - if throughput is your priority and you don't need the best ratio, zstd is the better tool for the job.

If you compress or decompress repeatedly (e.g. per request in a webserver), keep `*brrr.Writer` and `*brrr.Reader` instances in `sync.Pool`s and `Reset` each one into the next stream rather than allocating new instances each time. See the [compiled examples](example_test.go).

## Implementation notes

go-brrr is optimized for throughput. Some hot paths intentionally use larger functions, duplicated loops, and specialized code where benchmarks showed measurable wins. These choices stay local to performance-sensitive encoder and decoder internals; public APIs stay small and conventional.

## Acknowledgments

This library is a port of the [Brotli reference implementation](https://github.com/google/brotli) by the Brotli Authors, licensed under the MIT License.

## Compression Speed vs Ratio

All benchmarks were taken on the following setup with turboboost, etc, being
disabled via [denoise-amd.sh](scripts/denoise-amd.sh):

```
goos: linux
goarch: amd64
cpu: AMD Ryzen 5 7535HS with Radeon Graphics
```

Compared against [klauspost/compress](https://github.com/klauspost/compress) zstd (pure Go) and stdlib gzip. Single CPU, no parallelism. Compression speed plots measure reused streaming encoders: the timed loop resets a warmed writer and discards compressed output, while ratio is measured from a warmup buffer.

| Compression | Decompression |
|---|---|
| ![HTML 522KB](assets/gh_522KB_html.png) | ![HTML 522KB](assets/gh_522KB_html_decompress.png) |
| ![JS 187KB](assets/reactcore_187KB_js.png) | ![JS 187KB](assets/reactcore_187KB_js_decompress.png) |
| ![JSON 58KB](assets/github_events_58KB_json.png) | ![JSON 58KB](assets/github_events_58KB_json_decompress.png) |

## Benchmarks

Compared against other Go brotli libraries. **go-brrr** is the base in all comparisons. The smaller the number the better.

- **andybalholm** - [github.com/andybalholm/brotli](https://github.com/andybalholm/brotli), pure Go encoder and decoder.
- **google-brotli** - [github.com/google/brotli/go/brotli](https://github.com/google/brotli/tree/master/go/brotli), Google's official pure Go decoder, transpiled from the Java reference. Decompression only, no encoder.
- **cbrotli** - [github.com/google/brotli/go/cbrotli](https://github.com/google/brotli/tree/master/go/cbrotli), Google's official cgo bindings to the C reference implementation. Including a cgo library in a pure Go comparison isn't apples-to-apples, but it provides a useful ceiling for how fast brotli can go with C under the hood.

### Compression

<!-- bench:compress -->
| | go-brrr (sec/op) | andybalholm (sec/op) |
| --- | --- | --- |
| Compress/q=0/payload=VariedPayloads | 7.603m ± 0% | 11.530m ± 1%  +51.65% (p=0.002 n=6) |
| Compress/q=1/payload=VariedPayloads | 10.49m ± 0% | 16.19m ± 0%  +54.42% (p=0.002 n=6) |
| Compress/q=2/payload=VariedPayloads | 16.46m ± 0% | 29.26m ± 0%  +77.73% (p=0.002 n=6) |
| Compress/q=3/payload=VariedPayloads | 18.17m ± 0% | 34.56m ± 0%  +90.21% (p=0.002 n=6) |
| Compress/q=4/payload=VariedPayloads | 26.97m ± 0% | 47.53m ± 0%  +76.23% (p=0.002 n=6) |
| Compress/q=5/payload=VariedPayloads | 39.57m ± 0% | 65.49m ± 3%  +65.50% (p=0.002 n=6) |
| Compress/q=6/payload=VariedPayloads | 44.50m ± 0% | 74.82m ± 0%  +68.13% (p=0.002 n=6) |
| Compress/q=7/payload=VariedPayloads | 54.50m ± 0% | 95.70m ± 0%  +75.60% (p=0.002 n=6) |
| Compress/q=8/payload=VariedPayloads | 62.87m ± 0% | 113.77m ± 4%  +80.95% (p=0.002 n=6) |
| Compress/q=9/payload=VariedPayloads | 83.32m ± 1% | 143.06m ± 0%  +71.71% (p=0.002 n=6) |
| Compress/q=10/payload=VariedPayloads | 1.191 ± 0% | 1.310 ± 0%   +9.99% (p=0.002 n=6) |
| Compress/q=11/payload=VariedPayloads | 3.036 ± 0% | 3.357 ± 0%  +10.56% (p=0.002 n=6) |
| **geomean** | 56.97m | 90.49m       +58.82% |
<!-- /bench:compress -->

*Streaming* uses `brrr.NewReader` + `io.ReadAll`; *one-shot* uses `brrr.Decompress` on a complete in-memory blob.

### Streaming Decompression

<!-- bench:decompress -->
| | go-brrr (sec/op) | andybalholm (sec/op) |
| --- | --- | --- |
| Decompress/q=4/payload=VariedPayloads | 5.378m ± 0% | 9.539m ± 0%  +77.36% (p=0.000 n=12) |
| Decompress/q=5/payload=VariedPayloads | 5.302m ± 0% | 9.143m ± 0%  +72.43% (p=0.000 n=12) |
| Decompress/q=6/payload=VariedPayloads | 5.146m ± 0% | 8.881m ± 0%  +72.56% (p=0.000 n=12) |
| Decompress/q=11/payload=VariedPayloads | 5.621m ± 0% | 8.959m ± 0%  +59.37% (p=0.000 n=12) |
| **geomean** | 5.359m | 9.127m       +70.30% |
<!-- /bench:decompress -->

### One-shot Decompression

<!-- bench:decompresso -->
| | go-brrr (sec/op) | andybalholm (sec/op) | cbrotli (sec/op) | google-brotli (sec/op) |
| --- | --- | --- | --- | --- |
| DecompressOneshot/q=4/payload=VariedPayloads | 5.458m ± 0% | 10.042m ± 0%  +84.01% (p=0.000 n=12) | 5.191m ±  2%   -4.89% (p=0.000 n=12) | 10.595m ± 0%  +94.13% (p=0.000 n=12) |
| DecompressOneshot/q=5/payload=VariedPayloads | 5.458m ± 1% | 9.609m ± 0%  +76.07% (p=0.000 n=12) | 5.022m ± 11%        ~ (p=0.514 n=12) | 10.541m ± 0%  +93.14% (p=0.000 n=12) |
| DecompressOneshot/q=6/payload=VariedPayloads | 5.329m ± 1% | 9.384m ± 0%  +76.11% (p=0.000 n=12) | 4.916m ±  4%   -7.74% (p=0.001 n=12) | 10.240m ± 1%  +92.17% (p=0.000 n=12) |
| DecompressOneshot/q=11/payload=VariedPayloads | 5.816m ± 1% | 9.540m ± 0%  +64.03% (p=0.000 n=12) | 6.981m ±  1%  +20.03% (p=0.000 n=12) | **crashed** |
| **geomean** | 5.512m | 9.641m       +74.91% | 5.469m         -0.78% | 10.46m       +93.14%                 |
<!-- /bench:decompresso -->


The `VariedPayloads` benchmark rotates through a heterogeneous mix of files, guarding against benchmark-shaped optimizations - wins that only show up when the same input is fed back-to-back should not move these rows. Payloads span small JSON API responses, mid-size HTML and JS bundles, and larger English prose, drawn from the [Brotli reference test corpus](https://github.com/google/brotli/tree/master/tests/testdata) and the local [testdata/](testdata/) directory.

| File                  | Size   | Source     |
|-----------------------|-------:|------------|
| github_events_2k.json | 2.2 KB | testdata   |
| github_events_5k.json | 5.2 KB | testdata   |
| github_events_8k.json | 8.3 KB | testdata   |
| asyoulik.txt          | 122 KB | brotli-ref |
| alice29.txt           | 149 KB | brotli-ref |
| gh_172KB.html         | 167 KB | testdata   |
| reactcore_187KB.js    | 182 KB | testdata   |
| lcet10.txt            | 417 KB | brotli-ref |
| plrabn12.txt          | 471 KB | brotli-ref |
