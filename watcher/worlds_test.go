package main

import (
	"testing"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/compose"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

func configWithWorlds(active string, worlds ...config.World) *config.Config {
	cfg := config.Default()
	cfg.Worlds = config.WorldsConfig{Active: active, List: worlds}
	return &cfg
}

// Vanilla to mods is fine, mods back to vanilla is not, and neither is going
// back a version. Both leave a world the server refuses to load.
func TestWorldMoveDirections(t *testing.T) {
	allowed := [][4]string{
		{"VANILLA", "FABRIC", "1.21.4", "1.21.4"},
		{"VANILLA", "PAPER", "1.21.4", "1.21.8"},
		{"PAPER", "FORGE", "1.21.4", "1.21.4"},
		{"FABRIC", "FORGE", "1.21.4", "1.21.4"},
		{"VANILLA", "VANILLA", "1.20.6", "1.21.4"},
		{"VANILLA", "VANILLA", "1.21.4", "LATEST"},
	}
	for _, c := range allowed {
		if problem := worldMoveProblem(c[0], c[1], c[2], c[3]); problem != "" {
			t.Errorf("%s %s to %s %s should be allowed: %s", c[0], c[2], c[1], c[3], problem)
		}
	}

	refused := [][4]string{
		{"FABRIC", "VANILLA", "1.21.4", "1.21.4"},
		{"PAPER", "VANILLA", "1.21.4", "1.21.4"},
		{"FORGE", "PAPER", "1.21.4", "1.21.4"},
		{"VANILLA", "VANILLA", "1.21.4", "1.20.6"},
		{"VANILLA", "VANILLA", "LATEST", "1.21.4"},
	}
	for _, c := range refused {
		if worldMoveProblem(c[0], c[1], c[2], c[3]) == "" {
			t.Errorf("%s %s to %s %s should be refused", c[0], c[2], c[1], c[3])
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"1.21.4", "1.21.4", 0},
		{"1.21.8", "1.21.4", 1},
		{"1.21.4", "1.21.8", -1},
		{"1.21.10", "1.21.9", 1},
		{"LATEST", "1.21.4", 1},
		{"1.21.4", "LATEST", -1},
		{"LATEST", "latest", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.left, c.right); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.left, c.right, got, c.want)
		}
	}
}

func TestServerTypeTiers(t *testing.T) {
	if serverTypeTier("VANILLA") != 0 {
		t.Error("vanilla is the plainest")
	}
	if serverTypeTier("paper") <= serverTypeTier("VANILLA") {
		t.Error("a plugin server is above vanilla")
	}
	if serverTypeTier("FABRIC") <= serverTypeTier("PAPER") {
		t.Error("mods are above plugins, they write their own blocks")
	}
	if serverTypeTier("something-else") != 0 {
		t.Error("an unknown type is treated as the plainest, which refuses the most")
	}
}

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
