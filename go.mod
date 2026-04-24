module github.com/molecule-man/go-brrr

go 1.25.0

require (
	github.com/google/brotli/go/cbrotli v0.0.0
	golang.org/x/tools v0.42.0
)

require (
	github.com/andybalholm/brotli v1.2.0 // bench build tag only
	github.com/google/brotli/go/brotli v0.0.0 // bench build tag only
	github.com/klauspost/compress v1.18.5 // bench build tag only
)

replace github.com/google/brotli/go/cbrotli => ./brotli-ref/go/cbrotli

replace github.com/google/brotli/go/brotli => ./brotli-ref/go/brotli
