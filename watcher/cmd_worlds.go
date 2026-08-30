package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/compose"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/players"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/remote"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/sshx"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/ui"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/yamledit"
)

func runWorlds() int {
	cfg, err := config.Load()
	if err != nil {
		ui.PrintError("Config error: " + err.Error())
		return 1
	}
	doc, err := yamledit.Load(cfg.Path)
	if err != nil {
		ui.PrintError(err.Error())
		return 1
	}

	p := ui.NewPrompter()
	for {
		printWorlds(cfg)

		switch strings.ToLower(strings.TrimSpace(p.Line("Choose", "q"))) {
		case "1":
			if switchWorld(p, cfg, doc) != 0 {
				return 1
			}
		case "2":
			if makeWorld(p, cfg, doc) != 0 {
				return 1
			}
		case "3":
			if changeWorldVersion(p, cfg, doc) != 0 {
				return 1
			}
		case "4":
			if removeWorld(p, cfg, doc) != 0 {
				return 1
			}
		case "q", "quit", "exit", "":
			return 0
		default:
			ui.PrintError("Pick one of the numbers, or q to go back.")
		}

		reloaded, err := config.Load()
		if err != nil {
			ui.PrintError(err.Error())
			return 1
		}
		cfg = reloaded
	}
}

func printWorlds(cfg *config.Config) {
	fmt.Println("")
	fmt.Println("Your worlds")
	fmt.Println("")
	active := cfg.ActiveWorldName()
	for _, world := range cfg.WorldList() {
		marker := "   "
		if world.Name == active || active == "" {
			marker = " > "
		}
		fmt.Printf("%s%-12s %s\n", marker, world.Name, ui.Hint(describeWorld(world)))
	}

	fmt.Println("")
	fmt.Println("  1) Play a different world")
	fmt.Println("  2) Make a new world")
	fmt.Println("  3) Change version or server kind")
	fmt.Println("  4) Remove a world from this list")
	fmt.Println("  q) Back")
}

func describeWorld(world config.World) string {
	parts := []string{}
	if world.Version != "" {
		parts = append(parts, world.Version)
	}
	if world.Type != "" {
		parts = append(parts, prettyServerType(world.Type))
	}
	if len(parts) == 0 {
		return "set up before MCWOD kept track"
	}
	return strings.Join(parts, " ")
}

// VANILLA reads as shouting in a list, Vanilla does not.
func prettyServerType(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func switchWorld(p *ui.Prompter, cfg *config.Config, doc *yamledit.Document) int {
	worlds := cfg.WorldList()
	if len(worlds) < 2 {
		ui.PrintHint("There is only one world, so there is nothing to switch to.")
		return 0
	}
	target, picked := pickWorld(p, worlds, "Which world do you want to play?")
	if !picked || target.Name == cfg.ActiveWorldName() {
		return 0
	}

	session, code := openServerSession(p, cfg)
	if code != 0 {
		return code
	}
	defer session.Close()

	if !confirmPlayersWillDrop(p, cfg, session) {
		return 0
	}
	if !players.CountdownBeforeRestart(p) {
		ui.PrintHint("Left as it was.")
		return 0
	}

	stopActiveWorld(cfg, session)
	if err := doc.Set([]string{"worlds", "active"}, target.Name); err != nil {
		ui.PrintError(err.Error())
		return 1
	}
	if err := doc.Save(); err != nil {
		ui.PrintError(err.Error())
		return 1
	}
	fmt.Printf("%s is ready. Joining will wake it up.\n", target.Name)
	return 0
}

// Only one world may hold the Minecraft port, and docker keeps it for a moment
// after the container stops.
func stopActiveWorld(cfg *config.Config, session *remote.ServerSession) {
	active, ok := cfg.ActiveWorld()
	if !ok {
		return
	}
	fmt.Printf("Stopping %s...\n", active.Container)
	target := compose.InspectComposeTarget(session, active.Dir)
	if _, err := session.Run(compose.ComposeInvocation(session, target, "stop")); err != nil {
		ui.PrintWarning("It did not stop cleanly, the new world may fail to start.")
	}
}

func confirmPlayersWillDrop(p *ui.Prompter, cfg *config.Config, session *remote.ServerSession) bool {
	online, ok := onlinePlayers(cfg, session)
	if !ok || online == 0 {
		return true
	}
	ui.PrintWarning(fmt.Sprintf("%d player(s) are on %s right now. They will be disconnected.",
		online, cfg.ActiveWorldName()))
	return p.YesNo("Continue?", false)
}

func onlinePlayers(cfg *config.Config, session *remote.ServerSession) (int, bool) {
	active, ok := cfg.ActiveWorld()
	if !ok {
		return 0, false
	}
	out, err := session.Run("docker exec " + remote.ShellQuote(active.Container) + " rcon-cli list")
	if err != nil {
		return 0, false
	}
	return remote.ParsePlayerCount(out)
}

func makeWorld(p *ui.Prompter, cfg *config.Config, doc *yamledit.Document) int {
	session, code := openServerSession(p, cfg)
	if code != 0 {
		return code
	}
	defer session.Close()

	facts := ServerFacts{Platform: session.Detect()}
	discoverContainers(session, &facts)

	world, ok := CreateWorld(p, session, cfg, facts)
	if !ok {
		return 0
	}
	if err := appendWorld(doc, cfg, world); err != nil {
		ui.PrintError(err.Error())
		return 1
	}

	if p.YesNo("Make "+world.Name+" the world people reach now?", false) {
		if err := doc.Set([]string{"worlds", "active"}, world.Name); err != nil {
			ui.PrintError(err.Error())
			return 1
		}
	}
	if err := doc.Save(); err != nil {
		ui.PrintError(err.Error())
		return 1
	}
	fmt.Println("Added. Switch to it any time with: mcwod worlds")
	return 0
}

func removeWorld(p *ui.Prompter, cfg *config.Config, doc *yamledit.Document) int {
	worlds := cfg.WorldList()
	if len(worlds) < 2 {
		ui.PrintHint("There is only one world, so removing it would leave nothing.")
		return 0
	}
	target, picked := pickWorld(p, worlds, "Which one should be removed from the list?")
	if !picked {
		return 0
	}
	if target.Name == cfg.ActiveWorldName() {
		ui.PrintWarning("That is the world people reach right now.")
		ui.PrintHint("Switch to another one first, then remove this.")
		return 0
	}

	fmt.Println("")
	ui.PrintHint("This only removes "+target.Name+" from MCWOD's list.",
		"The world in "+target.Dir+" stays where it is.")
	if !p.YesNo("Remove "+target.Name+"?", false) {
		return 0
	}

	kept := []config.World{}
	for _, world := range worlds {
		if !strings.EqualFold(world.Name, target.Name) {
			kept = append(kept, world)
		}
	}
	if err := doc.Set([]string{"worlds", "list"}, kept); err != nil {
		ui.PrintError(err.Error())
		return 1
	}
	if err := doc.Save(); err != nil {
		ui.PrintError(err.Error())
		return 1
	}
	fmt.Println("Removed.")
	ui.PrintHint("Add it back any time by making a world in the same folder.")
	return 0
}

func appendWorld(doc *yamledit.Document, cfg *config.Config, world config.World) error {
	list := append(cfg.WorldList(), world)
	if err := doc.Set([]string{"worlds", "list"}, list); err != nil {
		return err
	}
	if cfg.Worlds.Active == "" {
		return doc.Set([]string{"worlds", "active"}, list[0].Name)
	}
	return nil
}

func pickWorld(p *ui.Prompter, worlds []config.World, question string) (config.World, bool) {
	fmt.Println("")
	for i, world := range worlds {
		fmt.Printf("  %d) %-12s %s\n", i+1, world.Name, ui.Hint(describeWorld(world)))
	}
	answer := p.Validated(question, "1", func(v string) error {
		index, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || index < 1 || index > len(worlds) {
			return fmt.Errorf("pick a number from 1 to %d", len(worlds))
		}
		return nil
	})
	index, _ := strconv.Atoi(strings.TrimSpace(answer))
	return worlds[index-1], true
}

// Every one of these needs to write files on the server, which the restricted
// key cannot do, so they all start the same way.
func openServerSession(p *ui.Prompter, cfg *config.Config) (*remote.ServerSession, int) {
	fmt.Printf("\nLogging in to %s.\n", cfg.Server.IP)
	ui.PrintHint("Your password is used for this one login and is never saved.")
	password := p.Secret(fmt.Sprintf("Password for %s@%s", cfg.Server.SSHUser, cfg.Server.IP))
	if password == "" {
		fmt.Println("Nothing was changed.")
		return nil, 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	session, err := remote.DialServerSession(ctx, sshx.NewSSHRunner(cfg), password, p)
	if err != nil {
		cancel()
		ui.PrintError(err.Error())
		return nil, 1
	}
	session.OnClose(cancel)
	session.Detect()
	return session, 0
}
