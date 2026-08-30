package cli

import (
	"testing"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/compose"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

// A world MCWOD created itself must not show up as one it knows nothing about.
func TestInitRecordsTheWorldItCreated(t *testing.T) {
	cfg := config.Default()
	spec := compose.ComposeSpec{ServiceName: "survival", MCPort: 25565, MCVersion: "LATEST", ServerType: "FABRIC"}

	rememberFirstWorld(&cfg, spec, "/srv/survival")

	if cfg.ActiveWorldName() != "survival" {
		t.Errorf("active world = %q", cfg.ActiveWorldName())
	}
	world, ok := cfg.ActiveWorld()
	if !ok {
		t.Fatal("the world was not recorded")
	}
	if world.Dir != "/srv/survival" || world.Type != "FABRIC" || world.Version != "LATEST" {
		t.Errorf("world = %+v", world)
	}
}

// A second world does not steal the active one.
func TestRememberingAWorldKeepsTheActiveOne(t *testing.T) {
	cfg := config.Default()
	cfg.Worlds.List = []config.World{{Name: "creative"}}
	cfg.Worlds.Active = "creative"

	rememberFirstWorld(&cfg, compose.ComposeSpec{ServiceName: "survival"}, "/srv/survival")

	if cfg.Worlds.Active != "creative" {
		t.Errorf("active world = %q, want creative", cfg.Worlds.Active)
	}
	if len(cfg.Worlds.List) != 2 {
		t.Errorf("world count = %d, want 2", len(cfg.Worlds.List))
	}
}
