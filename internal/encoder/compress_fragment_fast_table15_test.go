package encoder

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWriteCommandsTable15Generated(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the go tool")
	}
	out := filepath.Join(t.TempDir(), "table15.go")
	cmd := exec.Command("go", "run", "../../cmd/gencommands15", "-out", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generator: %v\n%s", err, b)
	}
	want, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("compress_fragment_fast_table15.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("compress_fragment_fast_table15.go is stale: run go generate")
	}
}
