package cli

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/compose"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/players"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/remote"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/sshx"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/ui"
)

func RunPlayers() int {
	cfg, err := config.Load()
	if err != nil {
		ui.PrintError("Config error: " + err.Error())
		return 1
	}
	if cfg.Server.ComposeDir == "" {
		ui.PrintWarning("MCWOD did not set this server up, so it does not know where its")
		ui.PrintWarning("settings live.")
		ui.PrintHint("Set it up with mcwod, or edit the server's compose file yourself.")
		return 1
	}

	p := ui.NewPrompter()
	fmt.Printf("\nLogging in to %s.\n", cfg.Server.IP)
	ui.PrintHint("Your password is used for this one login and is never saved.")
	password := p.Secret(fmt.Sprintf("Password for %s@%s", cfg.Server.SSHUser, cfg.Server.IP))
	if password == "" {
		fmt.Println("Nothing was changed.")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	session, err := remote.DialServerSession(ctx, sshx.NewSSHRunner(cfg), password, p)
	if err != nil {
		ui.PrintError(err.Error())
		return 1
	}
	defer session.Close()
	session.Detect()

	target := compose.InspectComposeTarget(session, cfg.Server.ComposeDir)
	if !target.Exists() {
		ui.PrintError("No compose file in " + cfg.Server.ComposeDir)
		return 1
	}

	list, err := players.ReadPlayerList(target.Existing, cfg.Server.ContainerName)
	if err != nil {
		ui.PrintError(err.Error())
		return 1
	}

	changed := editPlayers(p, &list, cfg.Server.ContainerName)
	if !changed {
		fmt.Println("Nothing was changed.")
		return 0
	}
	return applyPlayerList(p, session, cfg, target, list)
}

func editPlayers(p *ui.Prompter, list *players.List, world string) bool {
	changed := false
	for {
		printPlayerList(list, world)

		switch strings.ToLower(strings.TrimSpace(p.Line("Choose", "q"))) {
		case "1":
			changed = letSomeoneIn(p, list) || changed
		case "2":
			changed = kickSomeoneOff(p, list) || changed
		case "3":
			changed = toggleAdmin(p, list) || changed
		case "q", "quit", "exit", "":
			return changed
		default:
			ui.PrintError("Pick one of the numbers, or q to go back.")
		}
	}
}

func printPlayerList(list *players.List, world string) {
	fmt.Printf("\nWho can play on %s\n\n", world)
	if !list.Enforced {
		ui.PrintHint("Anyone who knows the address can join.")
		if len(list.Admins) > 0 {
			fmt.Printf("  Admins: %s\n", strings.Join(list.Admins, ", "))
		}
	}
	for _, name := range list.Whitelist {
		if slices.Contains(list.Admins, name) {
			fmt.Printf("  %-16s %s\n", name, ui.Hint("admin"))
			continue
		}
		fmt.Printf("  %s\n", name)
	}

	fmt.Println("")
	if list.Enforced {
		fmt.Println("  1) Let someone in")
		fmt.Println("  2) Kick someone off the list")
	} else {
		fmt.Println("  1) Turn on a whitelist")
	}
	fmt.Println("  3) Make someone an admin, or take it away")
	fmt.Println("  q) Back")
}

func letSomeoneIn(p *ui.Prompter, list *players.List) bool {
	if !list.Enforced {
		ui.PrintHint("Only the players you name will be able to join from now on.")
	}
	name := strings.TrimSpace(p.Line("Which Minecraft name?", ""))
	if name == "" {
		return false
	}
	if slices.Contains(list.Whitelist, name) {
		ui.PrintHint(name + " is already allowed in.")
		return false
	}

	list.Whitelist = append(list.Whitelist, name)
	list.Enforced = true
	fmt.Printf("  %s may now join.\n", name)
	return true
}

func kickSomeoneOff(p *ui.Prompter, list *players.List) bool {
	if len(list.Whitelist) == 0 {
		ui.PrintHint("Nobody is on the list.")
		return false
	}
	name, picked := pickName(p, list.Whitelist, "Who should lose access?")
	if !picked {
		return false
	}

	list.Whitelist = players.WithoutName(list.Whitelist, name)
	fmt.Printf("  %s can no longer join.\n", name)
	if len(list.Whitelist) == 0 {
		list.Enforced = false
		ui.PrintHint("The list is empty now, so anyone who knows the address can join.")
	}
	return true
}

// One entry that goes both ways, so the menu stays short.
func toggleAdmin(p *ui.Prompter, list *players.List) bool {
	choices := list.Whitelist
	if !list.Enforced {
		choices = list.Admins
	}
	if len(choices) == 0 {
		name := strings.TrimSpace(p.Line("Which Minecraft name should be the admin?", ""))
		if name == "" {
			return false
		}
		list.Admins = append(list.Admins, name)
		fmt.Printf("  %s is now an admin.\n", name)
		return true
	}

	name, picked := pickName(p, choices, "Who?")
	if !picked {
		return false
	}
	if !slices.Contains(list.Admins, name) {
		list.Admins = append(list.Admins, name)
		fmt.Printf("  %s is now an admin.\n", name)
		return true
	}

	// A server with no admin can only be fixed by editing files on the PC.
	if len(list.Admins) == 1 {
		ui.PrintWarning(name + " is your only admin. Without one, nobody can run")
		ui.PrintWarning("commands in the game any more.")
		if !p.YesNo("Take it away anyway?", false) {
			return false
		}
	}
	list.Admins = players.WithoutName(list.Admins, name)
	fmt.Printf("  %s is not an admin any more.\n", name)
	return true
}

func pickName(p *ui.Prompter, names []string, question string) (string, bool) {
	fmt.Println("")
	for i, name := range names {
		fmt.Printf("  %d) %s\n", i+1, name)
	}
	answer := p.Validated(question, "1", func(v string) error {
		index, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || index < 1 || index > len(names) {
			return fmt.Errorf("pick a number from 1 to %d", len(names))
		}
		return nil
	})
	index, _ := strconv.Atoi(strings.TrimSpace(answer))
	return names[index-1], true
}

func applyPlayerList(p *ui.Prompter, s *remote.ServerSession, cfg *config.Config, target compose.ComposeTarget, list players.List) int {
	updated, err := players.WritePlayerList(target.Existing, cfg.Server.ContainerName, list)
	if err != nil {
		ui.PrintError(err.Error())
		return 1
	}
	if _, err := compose.BackupComposeFile(s, target); err != nil {
		ui.PrintError(err.Error())
		return 1
	}
	if err := compose.WriteRemoteFile(s, target.File, updated); err != nil {
		ui.PrintError(err.Error())
		return 1
	}
	if err := compose.ValidateComposeFile(s, target); err != nil {
		ui.PrintError("The server settings were rejected: " + err.Error())
		ui.PrintHint("Put the old ones back with: mcwod restore-compose")
		return 1
	}

	fmt.Println("")
	if !p.YesNo("The server has to restart for this. Do it now?", true) {
		ui.PrintHint("The change takes effect the next time it starts.")
		return 0
	}
	if !players.CountdownBeforeRestart(p) {
		ui.PrintHint("Left running. The change takes effect the next time it starts.")
		return 0
	}
	if out, err := compose.ComposeUp(s, target); err != nil {
		ui.PrintError("Could not restart it: " + logging.Sanitize(out, 300))
		return 1
	}
	fmt.Println("Done.")
	return 0
}
