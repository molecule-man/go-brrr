module github.com/molecule-man/go-brrr/benchmarks

go 1.25.0

require (
	github.com/andybalholm/brotli v1.2.0
	github.com/google/brotli/go/brotli v0.0.0
	github.com/google/brotli/go/cbrotli v0.0.0
	github.com/klauspost/compress v1.18.5
	github.com/molecule-man/go-brrr v0.0.0
)

replace github.com/molecule-man/go-brrr => ../

replace github.com/google/brotli/go/brotli => ../brotli-ref/go/brotli

replace github.com/google/brotli/go/cbrotli => ../brotli-ref/go/cbrotli
