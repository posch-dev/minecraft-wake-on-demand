package main

import "testing"

func configWithWorlds(active string, worlds ...World) *Config {
	cfg := defaultConfig()
	cfg.Worlds = WorldsConfig{Active: active, List: worlds}
	return &cfg
}

// A config written before worlds existed describes exactly one, so it has to
// keep working without anybody editing it.
func TestOldConfigReadsAsASingleWorld(t *testing.T) {
	cfg := defaultConfig()
	cfg.Server.ContainerName = "minecraft"
	cfg.Server.MCPort = 25565
	cfg.Server.ComposeDir = "/home/eliah/minecraft"

	worlds := cfg.worldsOrSingle()
	if len(worlds) != 1 {
		t.Fatalf("got %d worlds, want the one it describes", len(worlds))
	}
	if worlds[0].Container != "minecraft" || worlds[0].Dir != "/home/eliah/minecraft" {
		t.Errorf("world = %+v", worlds[0])
	}
	// Nothing should look for a per world folder that was never created.
	if cfg.ActiveWorldName() != "" {
		t.Errorf("active name = %q, want empty for an implied world", cfg.ActiveWorldName())
	}
}

func TestActiveWorldDrivesWhatTheWatcherTalksTo(t *testing.T) {
	cfg := configWithWorlds("creative",
		World{Name: "survival", Container: "mc-survival", Port: 25565, Dir: "/srv/survival"},
		World{Name: "creative", Container: "mc-creative", Port: 25564, Dir: "/srv/creative"})

	cfg.applyActiveWorld()

	if cfg.Server.ContainerName != "mc-creative" {
		t.Errorf("container = %q", cfg.Server.ContainerName)
	}
	if cfg.Server.MCPort != 25564 {
		t.Errorf("port = %d", cfg.Server.MCPort)
	}
	if cfg.Server.ComposeDir != "/srv/creative" {
		t.Errorf("dir = %q", cfg.Server.ComposeDir)
	}
}

// A name that is not in the list would otherwise leave the watcher pointing at
// nothing at all.
func TestUnknownActiveFallsBackToTheFirstWorld(t *testing.T) {
	cfg := configWithWorlds("deleted",
		World{Name: "survival", Container: "mc-survival", Port: 25565})

	world, ok := cfg.ActiveWorld()
	if !ok || world.Name != "survival" {
		t.Errorf("world = %+v, ok = %v", world, ok)
	}
}

// Downward, because transfer mode publishes the port directly above the
// Minecraft one and a second world there would collide with it.
func TestPortsAreHandedOutDownwards(t *testing.T) {
	cfg := configWithWorlds("survival",
		World{Name: "survival", Port: 25565},
		World{Name: "creative", Port: 25564})

	if got := cfg.nextFreeWorldPort(); got != 25563 {
		t.Errorf("next port = %d, want 25563", got)
	}
}

func TestTransferPortIsNeverHandedOut(t *testing.T) {
	cfg := configWithWorlds("survival", World{Name: "survival", Port: 25565})
	cfg.Transfer.Enabled = true
	cfg.Transfer.Port = 25564

	if got := cfg.nextFreeWorldPort(); got != 25563 {
		t.Errorf("next port = %d, it must not take the transfer port", got)
	}
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

func TestFindWorldIgnoresCase(t *testing.T) {
	cfg := configWithWorlds("survival", World{Name: "Survival"})

	if _, ok := cfg.findWorld("survival"); !ok {
		t.Error("names should match regardless of case")
	}
	if _, ok := cfg.findWorld("creative"); ok {
		t.Error("a world that is not there must not be found")
	}
}

// A world MCWOD created itself must not show up as one it knows nothing about.
func TestInitRecordsTheWorldItCreated(t *testing.T) {
	cfg := defaultConfig()
	spec := ComposeSpec{ServiceName: "survival", MCPort: 25565, MCVersion: "LATEST", ServerType: "FABRIC"}

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
	cfg := defaultConfig()
	cfg.Worlds.List = []World{{Name: "creative"}}
	cfg.Worlds.Active = "creative"

	rememberFirstWorld(&cfg, ComposeSpec{ServiceName: "survival"}, "/srv/survival")

	if cfg.Worlds.Active != "creative" {
		t.Errorf("active world = %q, want creative", cfg.Worlds.Active)
	}
	if len(cfg.Worlds.List) != 2 {
		t.Errorf("world count = %d, want 2", len(cfg.Worlds.List))
	}
}
