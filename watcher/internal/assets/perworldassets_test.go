package assets

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

func assetsForWorld(t *testing.T, world string) (*Assets, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Path = filepath.Join(dir, "config.yml")
	cfg.Worlds = config.WorldsConfig{Active: world, List: []config.World{{Name: world, Container: world}}}

	assets := NewAssets(&cfg)
	assets.dir = dir
	if err := os.MkdirAll(filepath.Join(dir, "worlds", world), 0o755); err != nil {
		t.Fatal(err)
	}
	return assets, dir
}

func TestWorldMOTDBeatsTheSharedOne(t *testing.T) {
	assets, dir := assetsForWorld(t, "creative")
	shared := `{"text":"shared"}`
	own := `{"text":"just for creative"}`

	if err := os.WriteFile(filepath.Join(dir, "motd-sleeping.json"), []byte(shared), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := assets.MOTDSleeping(); got != shared {
		t.Fatalf("with only a shared file, got %q", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "worlds", "creative", "motd-sleeping.json"), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := assets.MOTDSleeping(); got != own {
		t.Errorf("the world's own file should win, got %q", got)
	}
}

// A world that only overrides one thing keeps the rest of what it shares.
func TestWorldFallsBackToTheSharedFiles(t *testing.T) {
	assets, dir := assetsForWorld(t, "creative")
	shared := `{"text":"shared starting"}`

	if err := os.WriteFile(filepath.Join(dir, "motd-starting.json"), []byte(shared), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "worlds", "creative", "motd-sleeping.json"),
		[]byte(`{"text":"own sleeping"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := assets.MOTDStarting(); got != shared {
		t.Errorf("starting should still come from the shared file, got %q", got)
	}
}

func TestWorldIconBeatsTheSharedOne(t *testing.T) {
	assets, dir := assetsForWorld(t, "creative")
	writePNG(t, filepath.Join(dir, "server-icon-live.png"), 64, 64, color.NRGBA{1, 1, 1, 255})
	shared := assets.IconLive()

	writePNG(t, filepath.Join(dir, "worlds", "creative", "server-icon-live.png"), 64, 64, color.NRGBA{9, 9, 9, 255})
	own := assets.IconLive()

	if shared == "" || own == "" {
		t.Fatal("both icons should have been served")
	}
	if shared == own {
		t.Error("the world's own picture should win")
	}
}

// The base picture the Z are drawn over follows the same order.
func TestComposedIconUsesTheWorldsBasePicture(t *testing.T) {
	assets, dir := assetsForWorld(t, "creative")
	writePNG(t, filepath.Join(dir, "server-icon.png"), 64, 64, color.NRGBA{0, 0, 0, 255})
	shared := assets.IconSleeping()

	writePNG(t, filepath.Join(dir, "worlds", "creative", "server-icon.png"), 64, 64, color.NRGBA{220, 20, 20, 255})
	own := assets.IconSleeping()

	if shared == own {
		t.Error("the world's own base picture should change the composed icon")
	}
}

// A config with no worlds block must not look for a folder nobody made.
func TestSingleWorldSetupLooksOnlyAtTheSharedFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Path = filepath.Join(dir, "config.yml")
	cfg.Server.ContainerName = "minecraft"
	assets := NewAssets(&cfg)
	assets.dir = dir

	paths := assets.search("motd-sleeping.json")
	if len(paths) != 1 {
		t.Errorf("search looked at %v, want only the shared file", paths)
	}
}

// The message that greets whoever woke the server is overridable like the rest,
// and per world like the rest.
func TestLoginWaitMessageCanBeOverridden(t *testing.T) {
	assets, dir := assetsIn(t)

	if !strings.Contains(assets.MOTDLoginWait(), "waking up") {
		t.Errorf("without a file the config text is used, got %s", assets.MOTDLoginWait())
	}

	shared := `{"text":"shared wait"}`
	if err := os.WriteFile(filepath.Join(dir, "motd-login-wait.json"), []byte(shared), 0o644); err != nil {
		t.Fatal(err)
	}
	if assets.MOTDLoginWait() != shared {
		t.Errorf("login wait = %s, want the shared file", assets.MOTDLoginWait())
	}

	assets.cfg.Worlds.List = []config.World{{Name: "creative"}}
	assets.cfg.Worlds.Active = "creative"
	world := filepath.Join(dir, "worlds", "creative")
	if err := os.MkdirAll(world, 0o755); err != nil {
		t.Fatal(err)
	}
	own := `{"text":"creative wait"}`
	if err := os.WriteFile(filepath.Join(world, "motd-login-wait.json"), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if assets.MOTDLoginWait() != own {
		t.Errorf("login wait = %s, want the world's own file", assets.MOTDLoginWait())
	}
}
