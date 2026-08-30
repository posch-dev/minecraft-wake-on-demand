package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Overridable so the update path can be tested without publishing a release,
// the same knobs install.sh uses.
var (
	updateRepo        = envOr("MC_WOL_REPO", "posch-dev/minecraft-wake-on-demand")
	updateAPIBase     = envOr("MC_WOL_API_BASE", "https://api.github.com")
	updateDownloadURL = envOr("MC_WOL_DOWNLOAD_BASE", "https://github.com")
)

const (
	// Short enough that an offline machine barely notices the check.
	updateCheckTimeout = 2 * time.Second
	updateCacheMaxAge  = 24 * time.Hour
	maxDownloadBytes   = 64 << 20
)

type releaseInfo struct {
	Tag     string    `json:"tag_name"`
	Body    string    `json:"body"`
	Checked time.Time `json:"checked"`
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// One line after init, config and check when something newer exists. Never acts
// on it, an unattended service must not replace its own binary.
func printUpdateHint(cfg *Config) {
	if cfg != nil && !cfg.Update.Check {
		return
	}
	release, err := cachedLatestRelease(cfg)
	if err != nil || release == nil {
		return
	}
	if !isNewerVersion(release.Tag, version) {
		return
	}
	fmt.Printf("\nVersion %s is available, you have %s. Update with: sudo mc-wol-proxy update\n",
		release.Tag, version)
}

// Skips the cache, because someone asking for it wants the answer now.
func fetchLatestReleaseNow() (*releaseInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return fetchLatestRelease(ctx)
}

func cachedLatestRelease(cfg *Config) (*releaseInfo, error) {
	path := updateCachePath(cfg)
	if cached := readUpdateCache(path); cached != nil {
		return cached, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()

	release, err := fetchLatestRelease(ctx)
	if err != nil {
		return nil, err
	}
	writeUpdateCache(path, release)
	return release, nil
}

func readUpdateCache(path string) *releaseInfo {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cached releaseInfo
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil
	}
	if time.Since(cached.Checked) > updateCacheMaxAge {
		return nil
	}
	return &cached
}

func writeUpdateCache(path string, release *releaseInfo) {
	release.Checked = time.Now()
	if data, err := json.Marshal(release); err == nil {
		os.WriteFile(path, data, 0o600)
	}
}

func updateCachePath(cfg *Config) string {
	const name = ".update-check.json"
	if cfg != nil && cfg.Path != "" {
		if abs, err := filepath.Abs(cfg.Path); err == nil {
			return filepath.Join(filepath.Dir(abs), name)
		}
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), name)
	}
	return name
}

func fetchLatestRelease(ctx context.Context) (*releaseInfo, error) {
	endpoint := updateAPIBase + "/repos/" + updateRepo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "mc-wol-proxy/"+version)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the release API answered %s", resp.Status)
	}

	var release releaseInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return nil, err
	}
	if release.Tag == "" {
		return nil, fmt.Errorf("the release API returned no tag")
	}
	return &release, nil
}

// Compares v2.10.0 against v2.9.0 by number, so the newer one wins rather than
// the one that sorts later as text.
func isNewerVersion(candidate, current string) bool {
	if current == "dev" || current == "" {
		return false
	}
	left, right := versionParts(candidate), versionParts(current)
	for i := range 3 {
		if left[i] != right[i] {
			return left[i] > right[i]
		}
	}
	return false
}

func versionParts(tag string) [3]int {
	var parts [3]int
	trimmed := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	// A suffix like -rc1 is not a release, so it is cut before parsing.
	if cut := strings.IndexAny(trimmed, "-+"); cut >= 0 {
		trimmed = trimmed[:cut]
	}
	for i, field := range strings.SplitN(trimmed, ".", 3) {
		if i >= 3 {
			break
		}
		parts[i], _ = strconv.Atoi(field)
	}
	return parts
}

// The published asset names, matching what the release workflow uploads.
func releaseAssetName() (string, error) {
	switch runtime.GOOS {
	case "linux":
		return "mc-wol-proxy_linux_" + runtime.GOARCH, nil
	case "windows":
		return "mc-wol-proxy_windows_" + runtime.GOARCH + ".exe", nil
	}
	return "", fmt.Errorf("no published build for %s", runtime.GOOS)
}

// The checksum is the only thing between a release URL and running whatever
// came back, so a mismatch or a missing entry refuses the install.
func downloadRelease(ctx context.Context, tag, asset string) ([]byte, error) {
	base := updateDownloadURL + "/" + updateRepo + "/releases/download/" + url.PathEscape(tag)

	binary, err := fetchBytes(ctx, base+"/"+asset, maxDownloadBytes)
	if err != nil {
		return nil, fmt.Errorf("cannot download %s: %w", asset, err)
	}
	checksums, err := fetchBytes(ctx, base+"/checksums.txt", 1<<20)
	if err != nil {
		return nil, fmt.Errorf("cannot download checksums.txt, refusing to install unverified: %w", err)
	}

	expected, found := checksumFor(string(checksums), asset)
	if !found {
		return nil, fmt.Errorf("%s is not listed in checksums.txt, refusing to install", asset)
	}
	sum := sha256.Sum256(binary)
	if actual := hex.EncodeToString(sum[:]); actual != expected {
		return nil, fmt.Errorf("checksum mismatch for %s, refusing to install\n  expected %s\n  actual   %s",
			asset, expected, actual)
	}
	return binary, nil
}

// sha256sum writes "hash  name" in text mode and "hash *name" in binary mode.
func checksumFor(checksums, asset string) (string, bool) {
	for _, line := range strings.Split(checksums, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == asset {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

// A redirect off the download host would hand the binary to someone else, so
// only the hosts we started at are followed.
func fetchBytes(ctx context.Context, endpoint string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mc-wol-proxy/"+version)

	client := &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !isAllowedDownloadHost(req.URL.Host) {
				return fmt.Errorf("refusing a redirect to %s", req.URL.Host)
			}
			if len(via) > 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server answered %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func isAllowedDownloadHost(host string) bool {
	for _, base := range []string{updateDownloadURL, updateAPIBase} {
		if parsed, err := url.Parse(base); err == nil && parsed.Host == host {
			return true
		}
	}
	return strings.HasSuffix(host, ".githubusercontent.com")
}
