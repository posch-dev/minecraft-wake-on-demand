package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePNG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{1, 2, 3, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

func assetsIn(t *testing.T) (*Assets, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.Path = filepath.Join(dir, "config.yml")
	assets := NewAssets(&cfg)
	assets.dir = dir
	return assets, dir
}

func TestIconIsEncodedWhenCorrectlySized(t *testing.T) {
	assets, dir := assetsIn(t)
	writePNG(t, filepath.Join(dir, "server-icon.png"), 64, 64)

	icon := assets.Icon()
	if !strings.HasPrefix(icon, "data:image/png;base64,") {
		t.Fatalf("icon = %.40q, want a PNG data URI", icon)
	}
}

func TestIconIsSkippedWhenWronglySized(t *testing.T) {
	assets, dir := assetsIn(t)
	writePNG(t, filepath.Join(dir, "server-icon.png"), 128, 128)

	if icon := assets.Icon(); icon != "" {
		t.Errorf("a 128x128 icon should be skipped, got %.40q", icon)
	}
}

func TestIconIsSkippedWhenOverTheSizeLimit(t *testing.T) {
	assets, dir := assetsIn(t)
	path := filepath.Join(dir, "server-icon.png")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x89}, maxIconBytes+1), 0644); err != nil {
		t.Fatal(err)
	}

	if icon := assets.Icon(); icon != "" {
		t.Errorf("an oversized icon should be skipped, got %.40q", icon)
	}
}

func TestIconIsSkippedWhenNotAPNG(t *testing.T) {
	assets, dir := assetsIn(t)
	path := filepath.Join(dir, "server-icon.png")
	if err := os.WriteFile(path, []byte("this is not an image at all"), 0644); err != nil {
		t.Fatal(err)
	}

	if icon := assets.Icon(); icon != "" {
		t.Errorf("a non-PNG should be skipped, got %.40q", icon)
	}
}

func TestIconIsCachedUntilTheFileChanges(t *testing.T) {
	assets, dir := assetsIn(t)
	path := filepath.Join(dir, "server-icon.png")
	writePNG(t, path, 64, 64)

	first := assets.Icon()
	if first == "" {
		t.Fatal("icon should have been encoded")
	}
	if len(assets.iconCache) != 1 {
		t.Fatalf("cache holds %d entries, want 1", len(assets.iconCache))
	}

	// Deleting the file behind the cache still serves the cached copy only when
	// the stat succeeds, so a removed icon must disappear from the response.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if icon := assets.Icon(); icon != "" {
		t.Error("a removed icon should not be served from the cache")
	}
}

func TestMOTDIsIgnoredWhenOverTheSizeLimit(t *testing.T) {
	assets, dir := assetsIn(t)
	oversized := `{"text":"` + strings.Repeat("x", maxMOTDBytes) + `"}`
	if err := os.WriteFile(filepath.Join(dir, "motd-sleeping.json"), []byte(oversized), 0644); err != nil {
		t.Fatal(err)
	}

	if got := assets.MOTDSleeping(); got != assets.cfg.MOTD.Sleeping {
		t.Errorf("an oversized MOTD should fall back to the configured one, got %.40q", got)
	}
}

func TestPNGDimensionsReadsTheIHDRChunk(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}

	width, height, err := pngDimensions(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if width != 64 || height != 32 {
		t.Errorf("dimensions = %dx%d, want 64x32", width, height)
	}
}

func TestPNGDimensionsRejectsShortAndForeignData(t *testing.T) {
	truncated := append([]byte{}, pngSignature...)
	truncated = binary.BigEndian.AppendUint32(truncated, 13)
	for _, data := range [][]byte{nil, []byte("GIF89a"), truncated} {
		if _, _, err := pngDimensions(data); err == nil {
			t.Errorf("pngDimensions(%q) should have failed", data)
		}
	}
}
