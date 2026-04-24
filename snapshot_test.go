// Test infrastructure for snapshot-based golden file testing.
//
// Snapshots are stored as txtar archives: one .txtar file per top-level test
// function, with one named section per subtest. Set UPDATE_SNAPS=1 to
// regenerate. Stale sections (from renamed/removed subtests) are automatically
// cleaned up during regeneration.

package brrr

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/tools/txtar"
)

// cleanedArchives tracks which txtar files have already been cleaned during
// this UPDATE_SNAPS run, so we only delete the old archive once per top-level
// test function.
var cleanedArchives sync.Map // map[string]bool

// topLevelTestName returns the first path component of t.Name()
// (e.g. "TestFoo" from "TestFoo/sub/case").
func topLevelTestName(t *testing.T) string {
	before, _, _ := strings.Cut(t.Name(), "/")
	return before
}

// sectionName returns everything after the first '/' in t.Name(),
// or the full name if there is no '/'.
func sectionName(t *testing.T) string {
	name := t.Name()
	_, after, found := strings.Cut(name, "/")
	if found {
		return after
	}
	return name
}

// snapshot compares got against a golden section inside a txtar archive.
// The archive path is testdata/snapshots/<TopLevelTest>.txtar and the section
// name is the subtest path. On first run (no archive exists), creates the
// file. Set UPDATE_SNAPS=1 to overwrite existing snapshots; stale sections
// are removed because the archive is deleted before the first write.
func snapshot(t *testing.T, got string) {
	t.Helper()

	dir := filepath.Join("testdata", "snapshots")
	archivePath := filepath.Join(dir, topLevelTestName(t)+".txtar")
	section := sectionName(t)
	updating := os.Getenv("UPDATE_SNAPS") != ""

	if updating {
		// First call for this archive in an UPDATE_SNAPS run: delete the
		// old file so stale sections are cleaned up (Problem 2).
		if _, loaded := cleanedArchives.LoadOrStore(archivePath, true); !loaded {
			_ = os.Remove(archivePath)
		}
	}

	// Read existing archive (may not exist yet).
	ar := &txtar.Archive{}
	if raw, err := os.ReadFile(archivePath); err == nil {
		ar = txtar.Parse(raw)
	} else if !os.IsNotExist(err) {
		t.Fatalf("reading snapshot archive: %v", err)
	}

	if !updating {
		// Look up this section.
		for _, f := range ar.Files {
			if f.Name == section {
				want := string(f.Data)
				if got != want {
					t.Errorf("snapshot mismatch (%s → %s):\n%s", archivePath, section, hexDiff(want, got))
				}
				return
			}
		}
		// Section not found — fall through to create it.
	}

	// Append or replace section, then write.
	replaced := false
	for i := range ar.Files {
		if ar.Files[i].Name == section {
			ar.Files[i].Data = []byte(got)
			replaced = true
			break
		}
	}
	if !replaced {
		ar.Files = append(ar.Files, txtar.File{
			Name: section,
			Data: []byte(got),
		})
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating snapshot dir: %v", err)
	}
	if err := os.WriteFile(archivePath, txtar.Format(ar), 0o644); err != nil {
		t.Fatalf("writing snapshot archive: %v", err)
	}
}

// snapshotBitstream compares bitstream output against a golden hex dump
// section in a txtar archive.
func snapshotBitstream(t *testing.T, data []byte, bits uint) {
	t.Helper()
	snapshot(t, formatHexSnap(data, bits))
}

// assertBitstream compares a bitstream result against inline expected values.
// Use this instead of snapshotBitstream for small, easy-to-inline outputs.
func assertBitstream(t *testing.T, gotBytes []byte, gotBits uint, wantBytes []byte, wantBits uint) {
	t.Helper()
	if gotBits != wantBits {
		t.Errorf("bits: got %d, want %d", gotBits, wantBits)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Errorf("bytes: got %x, want %x", gotBytes, wantBytes)
	}
}

func formatHexSnap(data []byte, bits uint) string {
	if len(data) == 0 {
		return fmt.Sprintf("%d bits\n", bits)
	}
	return fmt.Sprintf("%d bits\n%s", bits, hex.Dump(data))
}

func hexDiff(want, got string) string {
	wLines := strings.Split(strings.TrimRight(want, "\n"), "\n")
	gLines := strings.Split(strings.TrimRight(got, "\n"), "\n")

	var b strings.Builder
	n := max(len(wLines), len(gLines))
	for i := range n {
		var w, g string
		if i < len(wLines) {
			w = wLines[i]
		}
		if i < len(gLines) {
			g = gLines[i]
		}
		if w != g {
			if w != "" {
				fmt.Fprintf(&b, "  - %s\n", w)
			}
			if g != "" {
				fmt.Fprintf(&b, "  + %s\n", g)
			}
		}
	}
	return b.String()
}
