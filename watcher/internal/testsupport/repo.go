package testsupport

import (
	"os"
	"path/filepath"
	"testing"
)

// Paths are given from the repository root, so a test does not have to know how
// deep its own package sits.
func ReadRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 6 {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			break
		}
		if _, err := os.Stat(filepath.Join(dir, "LICENSE")); err == nil {
			break
		}
		dir = filepath.Dir(dir)
	}
	body, err := os.ReadFile(filepath.Join(append([]string{dir}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
