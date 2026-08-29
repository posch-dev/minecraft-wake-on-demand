package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/ui"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/yamledit"
)

// Where a world can actually be lost, so the backup runs before anything else
// and is not something to decline.
func changeWorldVersion(p *ui.Prompter, cfg *config.Config, doc *yamledit.Document) int {
	worlds := cfg.WorldList()
	world, picked := pickWorld(p, worlds, "Which world?")
	if !picked {
		return 0
	}
	if world.Dir == "" {
		ui.PrintWarning("MCWOD did not set " + world.Name + " up, so it cannot change it.")
		return 0
	}

	fmt.Printf("\n  %s is %s\n", world.Name, describeWorld(world))
	version := strings.TrimSpace(p.Line("\nWhich Minecraft version?", world.Version))
	serverType := askServerType(p, world.Type)
	if version == world.Version && serverType == world.Type {
		ui.PrintHint("Nothing changed.")
		return 0
	}

	keepWorld, ok := decideAboutTheWorld(p, world, serverType, version)
	if !ok {
		return 0
	}

	session, code := openServerSession(p, cfg)
	if code != 0 {
		return code
	}
	defer session.Close()

	return applyWorldChange(p, session, cfg, doc, world, serverType, version, keepWorld)
}

// Refused moves are still offered, but only after the backup has been made and
// only once somebody has said out loud that they want it.
func decideAboutTheWorld(p *ui.Prompter, world config.World, serverType, version string) (bool, bool) {
	problem := worldMoveProblem(world.Type, serverType, world.Version, version)
	if problem == "" {
		fmt.Println("")
		fmt.Println("  1) Keep the world and upgrade it")
		fmt.Println("  2) Start a fresh world, the old one is moved aside")
		answer := p.Validated("What should happen to the world?", "1", func(v string) error {
			if v == "1" || v == "2" {
				return nil
			}
			return fmt.Errorf("pick 1 or 2")
		})
		return answer == "1", true
	}

	fmt.Println("")
	ui.PrintWarning(problem)
	fmt.Println("")
	fmt.Println("  1) Start a fresh world, the current one is moved aside")
	fmt.Println("  2) Restore a backup from before the upgrade")
	fmt.Println("  3) Do it anyway with the world as it is")
	fmt.Println("  4) Cancel")

	answer := p.Validated("What would you like to do?", "4", func(v string) error {
		if index, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && index >= 1 && index <= 4 {
			return nil
		}
		return fmt.Errorf("pick a number from 1 to 4")
	})

	switch strings.TrimSpace(answer) {
	case "1":
		return false, true
	case "2":
		ui.PrintHint("Your backups are in " + world.Dir + "/backups.")
		ui.PrintHint("Unpack the one you want over the world folder, then try again.")
		return false, false
	case "3":
		ui.PrintHint("A backup is made first either way, so there is a way back.")
		ui.PrintWarning("The server will very likely refuse to start.")
		return p.YesNo("Go ahead?", false), true
	}
	return false, false
}

func applyWorldChange(p *ui.Prompter, s *ServerSession, cfg *config.Config, doc *yamledit.Document,
	world config.World, serverType, version string, keepWorld bool) int {

	target := inspectComposeTarget(s, world.Dir)
	if !target.Exists() {
		ui.PrintError("No server settings in " + world.Dir)
		return 1
	}

	if !backUpWorld(s, world, version) {
		return 1
	}
	if !countdownBeforeRestart(p) {
		ui.PrintHint("Left as it was. The backup is kept.")
		return 0
	}

	fmt.Printf("Stopping %s...\n", world.Container)
	if _, err := s.Run(composeInvocation(s, target, "stop")); err != nil {
		ui.PrintWarning("It did not stop cleanly.")
	}
	if !keepWorld && !moveWorldAside(s, world, version) {
		return 1
	}

	updated, err := setWorldEnvironment(target.Existing, world.Container, serverType, version)
	if err != nil {
		ui.PrintError(err.Error())
		return 1
	}
	if _, err := backupComposeFile(s, target); err != nil {
		ui.PrintError(err.Error())
		return 1
	}
	if err := writeRemoteFile(s, target.File, updated); err != nil {
		ui.PrintError(err.Error())
		return 1
	}
	if err := validateComposeFile(s, target); err != nil {
		ui.PrintError("The new settings were rejected: " + err.Error())
		ui.PrintHint("Put the old ones back with: mcwod restore-compose")
		return 1
	}

	if err := recordWorldChange(doc, cfg, world.Name, serverType, version); err != nil {
		ui.PrintError(err.Error())
		return 1
	}

	fmt.Printf("Starting %s on %s %s...\n", world.Container, version, prettyServerType(serverType))
	if out, err := composeUp(s, target); err != nil {
		ui.PrintError("It did not start: " + logging.Sanitize(out, 300))
		ui.PrintHint("Restore the backup in " + world.Dir + "/backups if it stays down.")
		return 1
	}
	fmt.Printf("%s is now %s %s.\n", world.Name, version, prettyServerType(serverType))
	return 0
}

// Runs on the server, so a large world does not travel over the SSH connection
// only to be written back again.
func backUpWorld(s *ServerSession, world config.World, version string) bool {
	name := "before-" + version + ".tar.gz"
	fmt.Println("")
	ui.PrintHint("Once a world has been opened in a newer version it cannot go back.",
		"A backup is made first, and it is the only way back.")
	fmt.Printf("Backing up %s...\n", world.Name)

	command := backupWorldCommand(s, world.Dir, name)
	if out, err := s.Run(command); err != nil {
		ui.PrintError("The backup failed, so nothing was changed: " + logging.Sanitize(out, 300))
		return false
	}
	fmt.Printf("  Backup written to %s\n", joinRemote(s, world.Dir, "backups/"+name))
	return true
}

func backupWorldCommand(s *ServerSession, dir, name string) string {
	if s.Platform().Windows {
		return "New-Item -ItemType Directory -Force -Path " + powerShellQuote(dir+`\backups`) +
			" | Out-Null; Compress-Archive -Force -Path " + powerShellQuote(dir+`\data`) +
			" -DestinationPath " + powerShellQuote(dir+`\backups\`+strings.TrimSuffix(name, ".tar.gz")+".zip")
	}
	return "set -e; cd " + shellQuote(dir) + "; mkdir -p backups; tar czf " +
		shellQuote("backups/"+name) + " data"
}

// Moved, never deleted, because somebody who picked a fresh world may still
// want what was there.
func moveWorldAside(s *ServerSession, world config.World, version string) bool {
	aside := "data.before-" + version + "-" + time.Now().UTC().Format("20060102-150405")

	var command string
	if s.Platform().Windows {
		command = "Move-Item -LiteralPath " + powerShellQuote(world.Dir+`\data`) +
			" -Destination " + powerShellQuote(world.Dir+`\`+aside)
	} else {
		command = "cd " + shellQuote(world.Dir) + " && mv data " + shellQuote(aside)
	}
	if out, err := s.Run(command); err != nil {
		ui.PrintError("Could not move the old world aside: " + logging.Sanitize(out, 200))
		return false
	}
	fmt.Printf("  The old world is now %s\n", joinRemote(s, world.Dir, aside))
	return true
}

func recordWorldChange(doc *yamledit.Document, cfg *config.Config, name, serverType, version string) error {
	worlds := cfg.WorldList()
	for i := range worlds {
		if strings.EqualFold(worlds[i].Name, name) {
			worlds[i].Type = serverType
			worlds[i].Version = version
		}
	}
	if err := doc.Set([]string{"worlds", "list"}, worlds); err != nil {
		return err
	}
	forgetServerInfo(cfg, name)
	if cfg.Worlds.Active == "" {
		if err := doc.Set([]string{"worlds", "active"}, worlds[0].Name); err != nil {
			return err
		}
	}
	return doc.Save()
}
