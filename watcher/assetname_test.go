package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(append([]string{".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// Three places name the release assets. If they drift, update stops finding
// what the workflow published and the failure is silent.
func TestAssetNamesAgreeAcrossCodeWorkflowAndInstaller(t *testing.T) {
	workflow := readRepoFile(t, ".github", "workflows", "release.yml")
	installer := readRepoFile(t, "watcher", "install.sh")

	for _, platform := range []struct{ goos, goarch, want string }{
		{"linux", "amd64", "mcwod_linux_amd64"},
		{"linux", "arm64", "mcwod_linux_arm64"},
		{"windows", "amd64", "mcwod_windows_amd64.exe"},
	} {
		got, err := assetNameFor(platform.goos, platform.goarch)
		if err != nil {
			t.Errorf("%s/%s: %v", platform.goos, platform.goarch, err)
			continue
		}
		if got != platform.want {
			t.Errorf("assetNameFor(%s, %s) = %q, want %q", platform.goos, platform.goarch, got, platform.want)
		}
		if !strings.Contains(workflow, got) {
			t.Errorf("release.yml does not publish %q", got)
		}
	}

	// The installer builds the name from GOARCH, so only the shape is checked.
	if !strings.Contains(installer, "mcwod_linux_") {
		t.Error("install.sh does not download an mcwod asset")
	}
}

func TestAssetNameRefusesAPlatformNothingIsPublishedFor(t *testing.T) {
	if _, err := assetNameFor("darwin", "arm64"); err == nil {
		t.Error("there is no macOS build, so there is no asset name for it")
	}
}

// checksums.txt is what the download is verified against, so the workflow has
// to actually produce it.
func TestWorkflowPublishesChecksums(t *testing.T) {
	workflow := readRepoFile(t, ".github", "workflows", "release.yml")

	if !strings.Contains(workflow, "checksums.txt") {
		t.Error("release.yml publishes no checksums.txt, update would refuse every install")
	}
}
