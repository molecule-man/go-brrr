package encoder

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestFixedShiftGenerated checks each genfixedshift output against its committed file.
func TestFixedShiftGenerated(t *testing.T) {
	if testing.Short() {
		t.Skip("requires the go tool")
	}
	const prefix = "//go:generate go run ../../cmd/genfixedshift "
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, src := range sources {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if args, ok := strings.CutPrefix(line, prefix); ok {
				n++
				checkFixedShiftGenerated(t, strings.Fields(args))
			}
		}
	}
	if n == 0 {
		t.Fatal("no genfixedshift go:generate lines found")
	}
}

func checkFixedShiftGenerated(t *testing.T, args []string) {
	t.Helper()
	out := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-out" {
			out = args[i+1]
			args[i+1] = filepath.Join(t.TempDir(), out)
		}
	}
	if out == "" {
		t.Fatalf("no -out flag in %v", args)
	}
	cmd := exec.Command("go", append([]string{"run", "../../cmd/genfixedshift"}, args...)...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generator: %v\n%s", err, b)
	}
	want, err := os.ReadFile(args[indexOf(args, "-out")+1])
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is stale: run go generate", out)
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
