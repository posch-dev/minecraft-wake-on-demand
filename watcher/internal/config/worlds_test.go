package config

import (
	"testing"
)

func configWithWorlds(active string, worlds ...World) *Config {
	cfg := Default()
	cfg.Worlds = WorldsConfig{Active: active, List: worlds}
	return &cfg
}

// A config written before worlds existed describes exactly one, so it has to
// keep working without anybody editing it.
func TestOldConfigReadsAsASingleWorld(t *testing.T) {
	cfg := Default()
	cfg.Server.ContainerName = "minecraft"
	cfg.Server.MCPort = 25565
	cfg.Server.ComposeDir = "/home/eliah/minecraft"

	worlds := cfg.WorldList()
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

	if got := cfg.NextFreeWorldPort(); got != 25563 {
		t.Errorf("next port = %d, want 25563", got)
	}
}

func TestTransferPortIsNeverHandedOut(t *testing.T) {
	cfg := configWithWorlds("survival", World{Name: "survival", Port: 25565})
	cfg.Transfer.Enabled = true
	cfg.Transfer.Port = 25564

	if got := cfg.NextFreeWorldPort(); got != 25563 {
		t.Errorf("next port = %d, it must not take the transfer port", got)
	}
}

func TestFindWorldIgnoresCase(t *testing.T) {
	cfg := configWithWorlds("survival", World{Name: "Survival"})

	if _, ok := cfg.FindWorld("survival"); !ok {
		t.Error("names should match regardless of case")
	}
	if _, ok := cfg.FindWorld("creative"); ok {
		t.Error("a world that is not there must not be found")
	}
}
