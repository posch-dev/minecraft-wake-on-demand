package main

import (
	"fmt"
	"os"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/cli"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/proxy"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/ui"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/update"
)

// Set via ldflags in the release build.
var version = "dev"

func main() {
	update.Current = version

	command := ""
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case "":
		// systemd calls the binary with no argument, so a menu here would
		// replace the service with a prompt nobody ever answers.
		if !ui.AttachedToTerminal() {
			os.Exit(proxy.Run())
		}
		logging.SetOutput(os.Stderr)
		os.Exit(cli.RunHome())
	case "run":
		os.Exit(proxy.Run())
	case "install", "check", "init", "setup-ssh", "config", "edit", "settings", "update", "get-server-icon", "learn-server-icon", "restore-compose", "players", "whitelist", "worlds", "world":
		// These print a laid out report, so log lines go to stderr instead of
		// interleaving with it.
		logging.SetOutput(os.Stderr)
		switch command {
		case "install":
			os.Exit(cli.RunInstall())
		case "check":
			os.Exit(cli.RunCheck())
		case "init":
			os.Exit(cli.RunInit())
		case "setup-ssh":
			os.Exit(cli.RunSetupSSH())
		case "update":
			os.Exit(cli.RunUpdate())
		case "get-server-icon", "learn-server-icon":
			os.Exit(cli.RunGetServerIcon())
		case "restore-compose":
			os.Exit(cli.RunRestoreCompose())
		case "players", "whitelist":
			os.Exit(cli.RunPlayers())
		case "worlds", "world":
			os.Exit(cli.RunWorlds())
		default:
			os.Exit(cli.RunConfigEdit())
		}
	case "version", "--version", "-v":
		fmt.Printf("mcwod %s\n", version)
	case "help", "--help", "-h":
		printUsage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %q\n\n", command)
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

func printUsage(w *os.File) {
	fmt.Fprint(w, `mcwod, Minecraft Wake-on-Demand watcher

Usage:
  mcwod                    a menu, or the watcher when run as a service
  mcwod run                start the watcher
  mcwod install            install this binary and start it with the machine
  mcwod init               answer a few questions and write config.yml
  mcwod config             change the configuration, guided
  mcwod setup-ssh          create the SSH key and install it on the server
  mcwod check              test the setup and say what is missing
  mcwod update             install a newer release, after asking
  mcwod get-server-icon    copy the running server's icon into assets/
  mcwod worlds             switch between worlds, make a new one
  mcwod players            who may join and who is an admin
  mcwod restore-compose    put back a docker-compose.yml this tool replaced
  mcwod version            print the version
  mcwod help               print this text

"edit" and "settings" do the same as "config".

Setting up from scratch is install, which asks the same questions as init and
then starts the watcher. Doing it by hand is init, then setup-ssh, then check.
When init sets the server up over SSH, setup-ssh is already done.

The config is read from MCWOD_CONFIG, then config.yml next to the binary
or one directory above it.
`)
}
