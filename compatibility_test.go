// Compatibility tests that decompress prebuilt *.compressed test vectors from
// the brotli reference corpus and compare against the original reference files.
package brrr

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var compressedSuffix = regexp.MustCompile(`\.compressed(\.\d+)?$`)

func TestCompatibility(t *testing.T) {
	t.Parallel()

	testdataDir := filepath.Join("brotli-ref", "tests", "testdata")

	entries, err := os.ReadDir(testdataDir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", testdataDir, err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if !compressedSuffix.MatchString(name) {
			continue
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			refName := compressedSuffix.ReplaceAllString(name, "")
			refPath := filepath.Join(testdataDir, refName)
			compPath := filepath.Join(testdataDir, name)

			assertDecompressMatch(t, compPath, refPath)
		})
	}
}

func TestCompatibilityCorpus(t *testing.T) {
	dir := os.Getenv("TEST_CORPUS_DIR")
	if dir == "" {
		t.Skip("TEST_CORPUS_DIR not set")
	}

	t.Parallel()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".br") {
			continue
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			compPath := filepath.Join(dir, name)
			refPath := filepath.Join(dir, strings.TrimSuffix(name, ".br"))

			assertDecompressMatch(t, compPath, refPath)
		})
	}
}

// assertDecompressMatch reads a compressed file and its reference, decompresses,
// and verifies the output matches.
func assertDecompressMatch(t *testing.T, compPath, refPath string) {
	t.Helper()

	compressed, err := os.ReadFile(compPath)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", compPath, err)
	}

	want, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", refPath, err)
	}

	got, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("output mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}
}
