package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/assets"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/testsupport"
)

func iconDataURI(t *testing.T, width, height int) (string, []byte) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.NRGBA{9, 8, 7, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return iconDataURIPrefix + base64.StdEncoding.EncodeToString(buf.Bytes()), buf.Bytes()
}

func TestDecodeFaviconDataURI(t *testing.T) {
	uri, raw := iconDataURI(t, 64, 64)

	decoded, err := decodeFaviconDataURI(uri)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Error("the decoded bytes do not match what was encoded")
	}
}

// Some servers wrap the base64, which is not valid in a data URI but happens.
func TestDecodeFaviconDataURIToleratesWrapping(t *testing.T) {
	uri, raw := iconDataURI(t, 64, 64)
	wrapped := uri[:60] + "\n" + uri[60:120] + "\r\n" + uri[120:]

	decoded, err := decodeFaviconDataURI(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Error("wrapped base64 did not decode to the same bytes")
	}
}

func TestDecodeFaviconDataURIRejectsRubbish(t *testing.T) {
	for _, favicon := range []string{
		"",
		"not a data uri",
		"data:image/gif;base64,R0lGODlhAQ==",
		iconDataURIPrefix + "!!!not base64!!!",
	} {
		if _, err := decodeFaviconDataURI(favicon); err == nil {
			t.Errorf("decodeFaviconDataURI(%.30q) should have failed", favicon)
		}
	}
}

// The same cap the status path uses, an oversized icon is refused before it
// ever reaches the disk.
func TestDecodeFaviconDataURIRefusesAnOversizedIcon(t *testing.T) {
	oversized := iconDataURIPrefix + base64.StdEncoding.EncodeToString(
		bytes.Repeat([]byte{0x89}, assets.MaxIconBytes+1))

	if _, err := decodeFaviconDataURI(oversized); err == nil {
		t.Error("an icon over the size limit must be refused")
	}
}

func TestWriteServerIconKeepsWhatWasThere(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "server-icon.png")
	if err := os.WriteFile(target, []byte("the old picture"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeServerIcon(target, []byte("the new picture"), true); err != nil {
		t.Fatal(err)
	}

	kept, err := os.ReadFile(filepath.Join(dir, "server-icon-old.png"))
	if err != nil {
		t.Fatalf("the picture was replaced without a way back: %v", err)
	}
	if string(kept) != "the old picture" {
		t.Errorf("kept copy holds %q", kept)
	}
	written, _ := os.ReadFile(target)
	if string(written) != "the new picture" {
		t.Errorf("target holds %q", written)
	}
}

// Answering no to the backup question means no backup, not a silent one.
func TestWriteServerIconDropsTheOldOneWhenNotAsked(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "server-icon.png")
	if err := os.WriteFile(target, []byte("the old picture"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeServerIcon(target, []byte("the new picture"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "server-icon-old.png")); err == nil {
		t.Error("no copy was asked for, so none should have been made")
	}
}

// A second run must not overwrite the copy the first one made.
func TestKeptIconsAreNumbered(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "server-icon.png")

	for i, body := range []string{"first", "second", "third"} {
		if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := writeServerIcon(target, []byte("new"), true); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
	}

	for name, want := range map[string]string{
		"server-icon-old.png":   "first",
		"server-icon-old-2.png": "second",
		"server-icon-old-3.png": "third",
	} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("%s is missing: %v", name, err)
			continue
		}
		if string(body) != want {
			t.Errorf("%s holds %q, want %q", name, body, want)
		}
	}
}

func TestWriteServerIconCreatesTheAssetsDirectory(t *testing.T) {
	target := filepath.Join(t.TempDir(), "assets", "server-icon.png")

	if err := writeServerIcon(target, []byte("icon"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("the icon was not written: %v", err)
	}
}

// A server must not be able to push a 4000x4000 PNG into assets/.
func TestFetchedIconGoesThroughTheSameValidation(t *testing.T) {
	_, raw := iconDataURI(t, 128, 128)

	if _, err := assets.DecodeIconPNG("the server's icon", raw); err == nil {
		t.Error("a 128x128 icon from the server must be refused")
	}
}

func TestFetchServerStatusReadsTheFavicon(t *testing.T) {
	uri, _ := iconDataURI(t, 64, 64)
	server := testsupport.StartFakeMCServerWithIcon(t, uri)

	cfg := sleepingConfig()
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.MCPort = server.Port

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	payload, err := fetchServerStatus(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(payload.Favicon, iconDataURIPrefix) {
		t.Errorf("favicon = %.40q", payload.Favicon)
	}
}

func TestFetchServerStatusFailsWhenTheServerIsAsleep(t *testing.T) {
	cfg := sleepingConfig()
	cfg.Server.IP = "127.0.0.1"
	cfg.Server.MCPort = 1

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := fetchServerStatus(ctx, cfg); err == nil {
		t.Error("an unreachable server should report an error, not an empty icon")
	}
}
