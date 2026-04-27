// Example tests demonstrating the brrr API.

package brrr_test

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"

	"github.com/molecule-man/go-brrr"
)

func Example_roundtrip() {
	// Compress
	original := []byte("Hello, brotli! This is a round-trip compression example.")
	var compressed bytes.Buffer
	w, err := brrr.NewWriter(&compressed, 6)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := w.Write(original); err != nil {
		log.Fatal(err)
	}
	if err := w.Close(); err != nil {
		log.Fatal(err)
	}

	// Decompress
	r := brrr.NewReader(&compressed)
	decompressed, err := io.ReadAll(r)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(decompressed))
	// Output: Hello, brotli! This is a round-trip compression example.
}

func Example_decompress() {
	// Compress some data first.
	original := []byte("Decompress restores the original bytes from a brotli-compressed slice.")
	var compressed bytes.Buffer
	w, err := brrr.NewWriter(&compressed, 4)
	if err != nil {
		log.Fatal(err)
	}
	if _, err = w.Write(original); err != nil {
		log.Fatal(err)
	}
	if err = w.Close(); err != nil {
		log.Fatal(err)
	}

	// One-shot decompression from a byte slice.
	result, err := brrr.Decompress(compressed.Bytes())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(result))
	// Output: Decompress restores the original bytes from a brotli-compressed slice.
}

func Example_reuse() {
	// Reset lets you reuse a Writer and Reader across multiple payloads,
	// avoiding repeated allocations.
	payloads := []string{
		"First payload: the quick brown fox jumps over the lazy dog.",
		"Second payload: pack my box with five dozen liquor jugs.",
	}

	var compressed bytes.Buffer
	w, err := brrr.NewWriter(&compressed, 4)
	if err != nil {
		log.Fatal(err)
	}
	r := brrr.NewReader(nil)

	for _, payload := range payloads {
		// Compress
		compressed.Reset()
		w.Reset(&compressed)
		if _, err := w.Write([]byte(payload)); err != nil {
			log.Fatal(err)
		}
		if err := w.Close(); err != nil {
			log.Fatal(err)
		}

		// Decompress
		r.Reset(&compressed)
		result, err := io.ReadAll(r)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(string(result))
	}
	// Output:
	// First payload: the quick brown fox jumps over the lazy dog.
	// Second payload: pack my box with five dozen liquor jugs.
}

func Example_pool() {
	// For repeated compression and decompression (e.g. per-request in an
	// HTTP server, per-message in a stream processor), keep *brrr.Writer
	// and *brrr.Reader instances in sync.Pools. Get, Reset, use, Put back.
	// This avoids allocating encoder hash tables and decoder ring buffers
	// each time.
	writerPool := sync.Pool{
		New: func() any {
			w, err := brrr.NewWriter(io.Discard, 5)
			if err != nil {
				// NewWriter only fails for an invalid level, which is
				// static here.
				panic(err)
			}
			return w
		},
	}
	readerPool := sync.Pool{
		New: func() any { return brrr.NewReader(nil) },
	}

	compress := func(dst io.Writer, payload []byte) error {
		w := writerPool.Get().(*brrr.Writer)
		defer writerPool.Put(w)

		w.Reset(dst)
		if _, err := w.Write(payload); err != nil {
			return err
		}
		return w.Close()
	}

	decompress := func(src io.Reader) ([]byte, error) {
		r := readerPool.Get().(*brrr.Reader)
		defer readerPool.Put(r)

		r.Reset(src)
		return io.ReadAll(r)
	}

	payloads := []string{
		"First response body.",
		"Second response body.",
	}
	for _, p := range payloads {
		var buf bytes.Buffer
		if err := compress(&buf, []byte(p)); err != nil {
			log.Fatal(err)
		}
		out, err := decompress(&buf)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(string(out))
	}
	// Output:
	// First response body.
	// Second response body.
}

func Example_compoundDictionary() {
	// A compound dictionary supplies extra reference data that both the
	// encoder and decoder can use for backward references. This is useful
	// when compressing data that shares content with a known corpus.
	dict := []byte(strings.Repeat("common dictionary content that appears in many documents. ", 50))
	input := []byte("This document references common dictionary content that appears in many documents. " +
		"It benefits from the shared dictionary because repeated phrases compress better.")

	// Compress with compound dictionary.
	var compressed bytes.Buffer
	w, err := brrr.NewWriter(&compressed, 4)
	if err != nil {
		log.Fatal(err)
	}
	if err := w.AttachDictionary(dict); err != nil {
		log.Fatal(err)
	}
	if _, err := w.Write(input); err != nil {
		log.Fatal(err)
	}
	if err := w.Close(); err != nil {
		log.Fatal(err)
	}

	// Decompress with the same compound dictionary.
	r := brrr.NewReader(&compressed)
	if err := r.AttachDictionary(dict); err != nil {
		log.Fatal(err)
	}
	result, err := io.ReadAll(r)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(result))
	// Output: This document references common dictionary content that appears in many documents. It benefits from the shared dictionary because repeated phrases compress better.
}
