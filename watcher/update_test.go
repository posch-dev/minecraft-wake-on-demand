package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsNewerVersionComparesNumbersNotText(t *testing.T) {
	newer := map[string]string{
		"v2.1.0":  "v2.0.0",
		"v2.10.0": "v2.9.0",
		"v3.0.0":  "v2.99.99",
		"v2.0.1":  "v2.0.0",
		"2.1.0":   "v2.0.0",
	}
	for candidate, current := range newer {
		if !isNewerVersion(candidate, current) {
			t.Errorf("isNewerVersion(%q, %q) = false, want true", candidate, current)
		}
	}

	notNewer := [][2]string{
		{"v2.0.0", "v2.0.0"},
		{"v2.0.0", "v2.1.0"},
		{"v2.9.0", "v2.10.0"},
		{"v2.1.0", "dev"},
		{"garbage", "v2.0.0"},
	}
	for _, c := range notNewer {
		if isNewerVersion(c[0], c[1]) {
			t.Errorf("isNewerVersion(%q, %q) = true, want false", c[0], c[1])
		}
	}
}

// A release candidate is not a release, the suffix must not read as newer.
func TestPrereleaseSuffixIsIgnored(t *testing.T) {
	if isNewerVersion("v2.0.0-rc1", "v2.0.0") {
		t.Error("v2.0.0-rc1 should not count as newer than v2.0.0")
	}
	if !isNewerVersion("v2.1.0-rc1", "v2.0.0") {
		t.Error("v2.1.0-rc1 is still a newer minor than v2.0.0")
	}
}

func TestChecksumForHandlesBothSumFormats(t *testing.T) {
	checksums := "abc123  mcwod_linux_amd64\ndef456 *mcwod_linux_arm64\n"

	if sum, ok := checksumFor(checksums, "mcwod_linux_amd64"); !ok || sum != "abc123" {
		t.Errorf("text mode entry = %q, %v", sum, ok)
	}
	if sum, ok := checksumFor(checksums, "mcwod_linux_arm64"); !ok || sum != "def456" {
		t.Errorf("binary mode entry = %q, %v", sum, ok)
	}
	if _, ok := checksumFor(checksums, "mcwod_windows_amd64.exe"); ok {
		t.Error("an asset that is not listed must not report a checksum")
	}
}

// Serves a release plus its asset and checksums, so the download path can be
// exercised without reaching GitHub.
func fakeReleaseServer(t *testing.T, tag string, asset string, body []byte, corruptSum bool) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	if corruptSum {
		digest = strings.Repeat("0", 64)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(releaseInfo{Tag: tag, Body: "- something changed"})
	})
	mux.HandleFunc("/posch-dev/minecraft-wake-on-demand/releases/download/"+tag+"/"+asset,
		func(w http.ResponseWriter, r *http.Request) { w.Write(body) })
	mux.HandleFunc("/posch-dev/minecraft-wake-on-demand/releases/download/"+tag+"/checksums.txt",
		func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(digest + "  " + asset + "\n"))
		})
	return httptest.NewServer(mux)
}

func pointUpdateAt(t *testing.T, server *httptest.Server) {
	t.Helper()
	oldAPI, oldDownload := updateAPIBase, updateDownloadURL
	updateAPIBase, updateDownloadURL = server.URL, server.URL
	t.Cleanup(func() { updateAPIBase, updateDownloadURL = oldAPI, oldDownload })
}

func TestDownloadReleaseVerifiesTheChecksum(t *testing.T) {
	payload := []byte("this stands in for a binary")
	server := fakeReleaseServer(t, "v9.9.9", "asset", payload, false)
	defer server.Close()
	pointUpdateAt(t, server)

	got, err := downloadRelease(context.Background(), "v9.9.9", "asset")
	if err != nil {
		t.Fatalf("downloadRelease: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("downloaded %q, want %q", got, payload)
	}
}

// The only thing between a release URL and running whatever came back.
func TestDownloadReleaseRefusesAMismatchedChecksum(t *testing.T) {
	server := fakeReleaseServer(t, "v9.9.9", "asset", []byte("tampered"), true)
	defer server.Close()
	pointUpdateAt(t, server)

	_, err := downloadRelease(context.Background(), "v9.9.9", "asset")
	if err == nil {
		t.Fatal("a mismatched checksum must not be installed")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("err = %v, want it to name the mismatch", err)
	}
}

func TestDownloadReleaseRefusesAnUnlistedAsset(t *testing.T) {
	server := fakeReleaseServer(t, "v9.9.9", "asset", []byte("payload"), false)
	defer server.Close()
	pointUpdateAt(t, server)

	_, err := downloadRelease(context.Background(), "v9.9.9", "other-asset")
	if err == nil {
		t.Fatal("an asset missing from checksums.txt must not be installed")
	}
}

func TestFetchLatestReleaseReadsTheTag(t *testing.T) {
	server := fakeReleaseServer(t, "v9.9.9", "asset", []byte("payload"), false)
	defer server.Close()
	pointUpdateAt(t, server)

	release, err := fetchLatestRelease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release.Tag != "v9.9.9" {
		t.Errorf("tag = %q", release.Tag)
	}
}

func TestUpdateCacheIsReusedThenExpires(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.Path = filepath.Join(dir, "config.yml")
	path := updateCachePath(&cfg)

	writeUpdateCache(path, &releaseInfo{Tag: "v9.9.9"})
	if cached := readUpdateCache(path); cached == nil || cached.Tag != "v9.9.9" {
		t.Fatalf("a fresh cache should be reused, got %+v", cached)
	}

	stale := releaseInfo{Tag: "v9.9.9", Checked: time.Now().Add(-updateCacheMaxAge - time.Minute)}
	data, _ := json.Marshal(stale)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if cached := readUpdateCache(path); cached != nil {
		t.Error("a cache older than the max age should be ignored")
	}
}

// The check has to be silent when the machine is offline, config and check must
// still work without a network.
func TestUpdateHintStaysQuietWhenTheAPIIsUnreachable(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.Path = filepath.Join(dir, "config.yml")

	oldAPI := updateAPIBase
	updateAPIBase = "http://127.0.0.1:1"
	defer func() { updateAPIBase = oldAPI }()

	if _, err := cachedLatestRelease(&cfg); err == nil {
		t.Error("an unreachable API should report an error rather than a release")
	}
	if _, err := os.Stat(updateCachePath(&cfg)); err == nil {
		t.Error("a failed check must not write a cache file")
	}
}

func TestUpdateCheckCanBeTurnedOff(t *testing.T) {
	cfg := defaultConfig()
	if !cfg.Update.Check {
		t.Error("the check should be on by default")
	}

	// printUpdateHint returns before touching the network when it is off.
	cfg.Update.Check = false
	oldAPI := updateAPIBase
	updateAPIBase = "http://127.0.0.1:1"
	defer func() { updateAPIBase = oldAPI }()
	printUpdateHint(&cfg)
}

func TestReleaseAssetNameMatchesThePublishedFiles(t *testing.T) {
	name, err := releaseAssetName()
	if err != nil {
		t.Skipf("no published build for this platform: %v", err)
	}
	if !strings.HasPrefix(name, "mcwod_") {
		t.Errorf("asset name = %q", name)
	}
}

// A redirect off the release host would hand the download to someone else.
func TestOnlyTheDownloadHostsAreFollowed(t *testing.T) {
	server := fakeReleaseServer(t, "v9.9.9", "asset", []byte("payload"), false)
	defer server.Close()
	pointUpdateAt(t, server)

	if !isAllowedDownloadHost(strings.TrimPrefix(server.URL, "http://")) {
		t.Error("the configured download host should be allowed")
	}
	if !isAllowedDownloadHost("objects.githubusercontent.com") {
		t.Error("the GitHub asset host should be allowed")
	}
	if isAllowedDownloadHost("evil.example.org") {
		t.Error("an unrelated host must not be followed")
	}
}
