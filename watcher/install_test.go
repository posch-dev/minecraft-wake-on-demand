package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallDirFollowsTheEnvironment(t *testing.T) {
	t.Setenv("MCWOD_INSTALL_DIR", "")
	t.Setenv("MC_WOL_INSTALL_DIR", "")
	if got := installDir(); got != defaultInstallDir() {
		t.Fatalf("want the default %q, got %q", defaultInstallDir(), got)
	}

	t.Setenv("MCWOD_INSTALL_DIR", filepath.Join(t.TempDir(), "elsewhere"))
	if got := installDir(); got == defaultInstallDir() {
		t.Fatal("MCWOD_INSTALL_DIR was ignored")
	}
}

func TestExampleAssetsAreWrittenWithoutACheckout(t *testing.T) {
	dir := t.TempDir()
	if err := writeExampleAssets(dir); err != nil {
		t.Fatalf("writeExampleAssets: %v", err)
	}

	for _, name := range []string{"motd-sleeping.json", "motd-login-wait.json", "server-icon.png"} {
		if !fileExists(filepath.Join(dir, "assets", "examples", name)) {
			t.Errorf("%s was not written", name)
		}
	}
	if !fileExists(filepath.Join(dir, "assets")) {
		t.Error("assets/ was not created")
	}
}

// Someone who already put a MOTD there would otherwise lose it on an update.
func TestWritingExamplesKeepsWhatIsAlreadyThere(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "assets", "examples", "motd-sleeping.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"text":"mine"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeExampleAssets(dir); err != nil {
		t.Fatalf("writeExampleAssets: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"text":"mine"}` {
		t.Errorf("the existing file was overwritten: %s", data)
	}
}

func TestCopyFileLeavesNoHalfBinaryBehind(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "target")
	if err := copyFile(source, target, 0o755); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "payload" {
		t.Errorf("copied %q", data)
	}
	if fileExists(target + ".new") {
		t.Error("the temporary neighbour was left behind")
	}
}

func TestQuoteForShellOnlyQuotesWhatNeedsIt(t *testing.T) {
	if got := quoteForShell("/opt/mcwod/mcwod"); got != "/opt/mcwod/mcwod" {
		t.Errorf("quoted a path that needs no quoting: %s", got)
	}
	if got := quoteForShell(`C:\Program Files\mcwod.exe`); !strings.HasPrefix(got, `"`) {
		t.Errorf("a path with a space was not quoted: %s", got)
	}
}
