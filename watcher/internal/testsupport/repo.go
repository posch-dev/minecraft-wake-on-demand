package testsupport

import (
	"os"
	"path/filepath"
	"testing"
)

func ReadRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(append([]string{".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
