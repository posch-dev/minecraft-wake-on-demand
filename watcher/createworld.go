package main

import (
	"fmt"
	"path"
	"strings"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/remote"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/ui"
)

// A world of its own, in its own folder, so a broken one is one broken world
// and restore-compose stays unambiguous.
func createWorld(p *ui.Prompter, s *remote.ServerSession, cfg *config.Config, facts ServerFacts) (config.World, bool) {
	if !facts.Platform.HasDocker {
		ui.PrintWarning("Docker is not installed on that PC, so there is nothing to set up.")
		return config.World{}, false
	}

	spec := defaultComposeSpec("survival", cfg.NextFreeWorldPort())
	spec.ServiceName = p.Validated("\nWhat should this world be called?", spec.ServiceName, validateContainerName)
	if _, taken := cfg.FindWorld(spec.ServiceName); taken {
		ui.PrintWarning("There is already a world called " + spec.ServiceName + ".")
		return config.World{}, false
	}
	spec.BackupName = spec.ServiceName + "-backup"

	dir := worldDirectory(s, cfg, spec.ServiceName)
	target := inspectComposeTarget(s, dir)
	if target.Command == "" {
		ui.PrintWarning("Docker Compose does not work on that PC.")
		ui.PrintHint("Install the compose plugin there, then try again.")
		return config.World{}, false
	}
	if target.Exists() {
		ui.PrintWarning("There is already a server set up in " + dir + ".")
		ui.PrintHint("Pick another name, or remove what is there first.")
		return config.World{}, false
	}
	if !acceptEULAOnce(p, cfg) {
		return config.World{}, false
	}

	spec.ServerType = askServerType(p, spec.ServerType)
	spec.MCVersion = askMCVersion(p, spec.MCVersion)
	spec.Memory = askMemory(p, facts.MemoryGB, spec.Memory)
	spec.Admin = askAdmin(p)
	spec.Whitelist = askWhitelist(p, spec.Admin)

	spec.Backups = p.YesNo("Make a backup of your world automatically?", true)
	if spec.Backups {
		spec.BackupInterval = p.Line("How often?", spec.BackupInterval)
		spec.KeepBackupDays = p.ValidatedInt("How many days of backups should be kept?", spec.KeepBackupDays, 1)
	}

	fmt.Printf("\nThis world will live in %s\n", dir)
	if !p.YesNo("Create it?", true) {
		fmt.Println("Nothing was created.")
		return config.World{}, false
	}

	password, err := prepareRCONPassword(s, target)
	if err != nil {
		ui.PrintError(err.Error())
		return config.World{}, false
	}
	content, err := newComposeFile(spec)
	if err != nil {
		ui.PrintError(err.Error())
		return config.World{}, false
	}
	if !writeComposeFiles(p, s, target, spec, content, password) {
		return config.World{}, false
	}

	return config.World{
		Name:      spec.ServiceName,
		Container: spec.ServiceName,
		Port:      spec.MCPort,
		Version:   spec.MCVersion,
		Type:      spec.ServerType,
		Dir:       dir,
	}, true
}

// Next to the worlds that already exist, so they end up together rather than
// scattered wherever somebody happened to be.
func worldDirectory(s *remote.ServerSession, cfg *config.Config, name string) string {
	base := ""
	if world, ok := cfg.ActiveWorld(); ok && world.Dir != "" {
		base = parentDirectory(s, world.Dir)
	}
	if base == "" {
		base = parentDirectory(s, defaultComposeDir(s, cfg))
	}
	return joinRemote(s, base, name)
}

func parentDirectory(s *remote.ServerSession, dir string) string {
	trimmed := strings.TrimRight(dir, `\/`)
	if s.Platform().Windows {
		if cut := strings.LastIndexAny(trimmed, `\/`); cut > 0 {
			return trimmed[:cut]
		}
		return trimmed
	}
	return path.Dir(trimmed)
}

// Asked once and remembered, nobody wants to accept the same licence per world.
func acceptEULAOnce(p *ui.Prompter, cfg *config.Config) bool {
	if cfg.EULAAccepted {
		return true
	}
	if !acceptEULA(p) {
		fmt.Println("Without accepting it the server cannot run, so nothing was created.")
		return false
	}
	cfg.EULAAccepted = true
	return true
}
