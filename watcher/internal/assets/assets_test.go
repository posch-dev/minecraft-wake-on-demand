package assets

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/embedded"
)

func writePNG(t *testing.T, path string, width, height int, fill color.Color) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, fill)
		}
	}
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
	cfg := config.Default()
	cfg.Path = filepath.Join(dir, "config.yml")
	assets := NewAssets(&cfg)
	assets.dir = dir
	return assets, dir
}

func decodeDataURI(t *testing.T, dataURI string) image.Image {
	t.Helper()
	encoded, found := strings.CutPrefix(dataURI, "data:image/png;base64,")
	if !found {
		t.Fatalf("not a PNG data URI: %.40q", dataURI)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// Nothing configured at all still has to produce the built-in Z.
func TestSleepingIconFallsBackToTheBuiltInOverlay(t *testing.T) {
	assets, _ := assetsIn(t)

	icon := assets.IconSleeping()
	if icon == "" {
		t.Fatal("the built-in overlay should be served when no file exists")
	}
	img := decodeDataURI(t, icon)
	if got := img.Bounds().Dx(); got != IconEdge {
		t.Errorf("width = %d, want %d", got, IconEdge)
	}

	// The background has to be opaque white, not transparent.
	r, g, b, a := img.At(1, 1).RGBA()
	if r != 0xFFFF || g != 0xFFFF || b != 0xFFFF || a != 0xFFFF {
		t.Errorf("corner = %d,%d,%d,%d, want opaque white", r, g, b, a)
	}
}

func TestSleepingAndStartingOverlaysDiffer(t *testing.T) {
	assets, _ := assetsIn(t)

	if assets.IconSleeping() == assets.IconStarting() {
		t.Error("the waking icon should replace the largest Z with an exclamation mark")
	}
}

// The shared icon shows through at half opacity with the Z drawn over it.
func TestSleepingIconComposesOverTheSharedIcon(t *testing.T) {
	assets, dir := assetsIn(t)
	plain := assets.IconSleeping()

	writePNG(t, filepath.Join(dir, "server-icon.png"), 64, 64, color.NRGBA{0, 0, 0, 255})
	composed := assets.IconSleeping()

	if composed == plain {
		t.Fatal("a server-icon.png should change the composed sleeping icon")
	}
	img := decodeDataURI(t, composed)

	// Black at half opacity over white lands around mid grey.
	r, _, _, _ := img.At(1, 1).RGBA()
	if r < 0x6000 || r > 0xA000 {
		t.Errorf("corner red = %#x, want a mid grey from 50%% black over white", r)
	}
}

// A dedicated file is the user's own artwork and must not be drawn over.
func TestDedicatedStateIconIsUsedUntouched(t *testing.T) {
	assets, dir := assetsIn(t)
	writePNG(t, filepath.Join(dir, "server-icon-sleeping.png"), 64, 64, color.NRGBA{10, 20, 30, 255})

	img := decodeDataURI(t, assets.IconSleeping())
	r, g, b, _ := img.At(32, 32).RGBA()
	if r>>8 != 10 || g>>8 != 20 || b>>8 != 30 {
		t.Errorf("centre = %d,%d,%d, a dedicated icon should be passed through as it is", r>>8, g>>8, b>>8)
	}
}

// Without a file the running server keeps its own icon, which is the default.
func TestLiveIconIsEmptyUnlessOverridden(t *testing.T) {
	assets, dir := assetsIn(t)

	if got := assets.IconLive(); got != "" {
		t.Errorf("live icon = %.40q, want empty so the server's own icon survives", got)
	}

	writePNG(t, filepath.Join(dir, "server-icon-live.png"), 64, 64, color.NRGBA{1, 2, 3, 255})
	if assets.IconLive() == "" {
		t.Error("a server-icon-live.png should override the running server's icon")
	}
}

// One file drives all three states, so nobody has to place the same picture
// twice to have it shown while the server runs.
func TestLiveIconFallsBackToTheSharedIcon(t *testing.T) {
	assets, dir := assetsIn(t)
	writePNG(t, filepath.Join(dir, "server-icon.png"), 64, 64, color.NRGBA{1, 2, 3, 255})

	if assets.IconLive() == "" {
		t.Error("a shared server-icon.png should be shown while the server runs")
	}
	if assets.IconSleeping() == "" {
		t.Error("the same shared icon should still feed the sleeping state")
	}
	// Plain while running, dressed up while asleep, so the two differ.
	if assets.IconLive() == assets.IconSleeping() {
		t.Error("the live icon should not carry the sleeping overlay")
	}
}

// Nothing to show means the field is left out and the server answers for itself.
func TestLiveIconIsEmptyWithoutAnyFile(t *testing.T) {
	assets, _ := assetsIn(t)
	if got := assets.IconLive(); got != "" {
		t.Errorf("live icon = %.40q, want nothing", got)
	}
}

func TestLiveMOTDIsEmptyUnlessOverridden(t *testing.T) {
	assets, dir := assetsIn(t)

	if got := assets.MOTDLive(); got != "" {
		t.Errorf("live MOTD = %q, want empty so the server's own MOTD survives", got)
	}

	motd := `{"text":"custom"}`
	if err := os.WriteFile(filepath.Join(dir, "motd-live.json"), []byte(motd), 0644); err != nil {
		t.Fatal(err)
	}
	if got := assets.MOTDLive(); got != motd {
		t.Errorf("live MOTD = %q, want %q", got, motd)
	}
}

func TestStateMOTDPrefersTheFileOverConfig(t *testing.T) {
	assets, dir := assetsIn(t)

	if got := assets.MOTDSleeping(); got != assets.cfg.MOTD.Sleeping {
		t.Errorf("without a file the config should win, got %q", got)
	}

	motd := `{"text":"from the file"}`
	if err := os.WriteFile(filepath.Join(dir, "motd-sleeping.json"), []byte(motd), 0644); err != nil {
		t.Fatal(err)
	}
	if got := assets.MOTDSleeping(); got != motd {
		t.Errorf("sleeping MOTD = %q, want the file to win", got)
	}
}

func TestIconIsSkippedWhenWronglySized(t *testing.T) {
	assets, dir := assetsIn(t)
	writePNG(t, filepath.Join(dir, "server-icon-live.png"), 128, 128, color.NRGBA{1, 2, 3, 255})

	if icon := assets.IconLive(); icon != "" {
		t.Errorf("a 128x128 icon should be skipped, got %.40q", icon)
	}
}

func TestIconIsSkippedWhenOverTheSizeLimit(t *testing.T) {
	assets, dir := assetsIn(t)
	path := filepath.Join(dir, "server-icon-live.png")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x89}, MaxIconBytes+1), 0644); err != nil {
		t.Fatal(err)
	}

	if icon := assets.IconLive(); icon != "" {
		t.Errorf("an oversized icon should be skipped, got %.40q", icon)
	}
}

func TestIconIsSkippedWhenNotAPNG(t *testing.T) {
	assets, dir := assetsIn(t)
	path := filepath.Join(dir, "server-icon-live.png")
	if err := os.WriteFile(path, []byte("this is not an image at all"), 0644); err != nil {
		t.Fatal(err)
	}

	if icon := assets.IconLive(); icon != "" {
		t.Errorf("a non-PNG should be skipped, got %.40q", icon)
	}
}

func TestIconIsCachedUntilTheFileChanges(t *testing.T) {
	assets, dir := assetsIn(t)
	path := filepath.Join(dir, "server-icon-live.png")
	writePNG(t, path, 64, 64, color.NRGBA{1, 2, 3, 255})

	if assets.IconLive() == "" {
		t.Fatal("icon should have been encoded")
	}
	if _, cached := assets.iconCache[path]; !cached {
		t.Fatalf("the file was not cached, the cache holds %v", assets.iconCache)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if icon := assets.IconLive(); icon != "" {
		t.Error("a removed icon must not keep being served from the cache")
	}
}

// Dropping a new server-icon.png in has to reach the composed state icons too.
func TestComposedIconIsRecomposedWhenTheBaseAppears(t *testing.T) {
	assets, dir := assetsIn(t)
	before := assets.IconSleeping()

	writePNG(t, filepath.Join(dir, "server-icon.png"), 64, 64, color.NRGBA{200, 30, 30, 255})
	after := assets.IconSleeping()

	if before == after {
		t.Error("the composed icon should pick up a newly added server-icon.png")
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
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 64, 32))); err != nil {
		t.Fatal(err)
	}

	width, height, err := PngDimensions(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if width != 64 || height != 32 {
		t.Errorf("dimensions = %dx%d, want 64x32", width, height)
	}
}

func TestPNGDimensionsRejectsShortAndForeignData(t *testing.T) {
	truncated := binary.BigEndian.AppendUint32(append([]byte{}, pngSignature...), 13)
	for _, data := range [][]byte{nil, []byte("GIF89a"), truncated} {
		if _, _, err := PngDimensions(data); err == nil {
			t.Errorf("pngDimensions(%q) should have failed", data)
		}
	}
}

// The overlays ship inside the binary, a broken one would break every ping.
func TestEmbeddedOverlaysAreValid64x64PNGs(t *testing.T) {
	for name, data := range map[string][]byte{
		"sleeping": embedded.OverlaySleepingPNG,
		"starting": embedded.OverlayStartingPNG,
	} {
		width, height, err := PngDimensions(data)
		if err != nil {
			t.Errorf("the %s overlay is not a PNG: %v", name, err)
			continue
		}
		if width != IconEdge || height != IconEdge {
			t.Errorf("the %s overlay is %dx%d, want %dx%d", name, width, height, IconEdge, IconEdge)
		}
	}
}

// No client should ever wait for a PNG to be composed, so it happens before the
// first ping rather than during it.
func TestIconsAreComposedBeforeTheFirstRequest(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "assets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Path = filepath.Join(home, "config.yml")
	writePNG(t, filepath.Join(dir, "server-icon.png"), 64, 64, color.NRGBA{1, 2, 3, 255})

	assets := NewAssets(&cfg)
	for _, state := range []string{StateSleeping, StateStarting} {
		if _, cached := assets.iconCache["composed:"+state+":"+filepath.Join(dir, "server-icon.png")]; !cached {
			t.Errorf("the %s icon was not composed up front, cache holds %d", state, len(assets.iconCache))
		}
	}
}
