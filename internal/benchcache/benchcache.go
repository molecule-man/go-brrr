// Package benchcache stores precomputed compression outputs on disk so that
// benchmark and test setup does not have to re-run the (slow) C reference
// encoder or other expensive paths on every run.
package benchcache

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

const cacheDirName = ".cache"

var (
	repoRootOnce sync.Once
	repoRootDir  string
)

// Key returns a hex-encoded SHA-256 digest that uniquely identifies a
// compression call by its input bytes plus all parameters that influence the
// output. Different (quality, lgwin, sizeHint, dict, input) tuples never
// collide because the parameter header is fixed-size and the dictionary is
// length-prefixed.
func Key(input []byte, quality, lgwin int, sizeHint uint, dict []byte) string {
	h := sha256.New()
	var hdr [24]byte
	binary.LittleEndian.PutUint64(hdr[0:], uint64(quality))
	binary.LittleEndian.PutUint64(hdr[8:], uint64(lgwin))
	binary.LittleEndian.PutUint64(hdr[16:], uint64(sizeHint))
	h.Write(hdr[:])
	if dict != nil {
		var dlen [8]byte
		binary.LittleEndian.PutUint64(dlen[:], uint64(len(dict)))
		h.Write(dlen[:])
		h.Write(dict)
	}
	h.Write(input)
	return hex.EncodeToString(h.Sum(nil))
}

// Lookup returns cached output if available.
func Lookup(key string) ([]byte, bool) {
	data, err := os.ReadFile(filepath.Join(cacheDir(), key))
	if err != nil {
		return nil, false
	}
	return data, true
}

// Store writes output to the cache directory atomically.
func Store(key string, data []byte) {
	dir := cacheDir()
	_ = os.MkdirAll(dir, 0o755)
	tmp := filepath.Join(dir, key+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, filepath.Join(dir, key))
}

// RepoRoot returns the absolute path of the go-brrr repository root,
// determined by walking upward from this source file until a go.mod is found.
// It panics if no go.mod is located, since that means the package was used
// outside of the repository it was designed for.
func RepoRoot() string {
	repoRootOnce.Do(func() {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			panic("benchcache: runtime.Caller failed")
		}
		dir := filepath.Dir(file)
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				repoRootDir = dir
				return
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				panic("benchcache: could not locate go.mod walking up from " + file)
			}
			dir = parent
		}
	})
	return repoRootDir
}

func cacheDir() string {
	return filepath.Join(RepoRoot(), cacheDirName)
}
