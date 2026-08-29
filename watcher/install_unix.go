//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/fsx"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/ui"
)

const (
	exeSuffix       = ""
	systemdUnitPath = "/etc/systemd/system/mcwod.service"
)

func defaultInstallDir() string { return "/opt/mcwod" }

func checkInstallPermission() error {
	if os.Geteuid() == 0 {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		self = "mcwod"
	}
	return fmt.Errorf("writing a systemd unit needs root, run: sudo %s install", quoteForShell(self))
}

// The account the watcher runs under. Under sudo that is whoever called it, so
// the SSH key and the config land in a real home and not in root's.
func serviceUser() *user.User {
	if name := os.Getenv("SUDO_USER"); name != "" && name != "root" {
		if found, err := user.Lookup(name); err == nil {
			return found
		}
	}
	if found, err := user.Current(); err == nil {
		return found
	}
	return nil
}

func serviceUserIDs() (int, int, bool) {
	owner := serviceUser()
	if owner == nil {
		return 0, 0, false
	}
	uid, err := strconv.Atoi(owner.Uid)
	if err != nil {
		return 0, 0, false
	}
	gid, err := strconv.Atoi(owner.Gid)
	if err != nil {
		return 0, 0, false
	}
	return uid, gid, true
}

// The watcher caches the learned Minecraft version and its known_hosts next to
// the binary, so the directory has to belong to the service user, not to root.
func handOverInstallDir(dir string) error {
	uid, gid, ok := serviceUserIDs()
	if !ok {
		return nil
	}
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(path, uid, gid)
	})
}

func registerAutostart(dir, binary string) error {
	owner := serviceUser()
	name := "root"
	if owner != nil {
		name = owner.Username
	}
	if name == "root" {
		ui.PrintWarning("No SUDO_USER found, the watcher will run as root.")
		ui.PrintHint("Install it with sudo from your own account instead: sudo mcwod install")
	}

	if err := retireOlderService(); err != nil {
		return err
	}
	// The unit keeps the home read only, so known_hosts cannot live in ~/.ssh.
	knownHosts := filepath.Join(dir, "known_hosts")
	if !fsx.Exists(knownHosts) {
		if err := os.WriteFile(knownHosts, nil, 0o600); err != nil {
			return err
		}
	}

	if err := os.WriteFile(systemdUnitPath, []byte(renderSystemdUnit(dir, name)), 0o644); err != nil {
		return err
	}
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return err
	}
	fmt.Printf("Installed the systemd service, running as %s\n", name)
	return nil
}

// The unit ships with the default paths, so they follow the install dir.
func renderSystemdUnit(dir, runUser string) string {
	unit := strings.ReplaceAll(systemdUnitTemplate, "MCWOD_USER", runUser)
	return strings.ReplaceAll(unit, defaultInstallDir(), dir)
}

// Two watchers would fight over port 25565 and the failures would look random,
// so the one this replaces is stopped rather than left running.
func retireOlderService() error {
	const old = "/etc/systemd/system/mc-wol-proxy.service"
	if !fsx.Exists(old) {
		return nil
	}
	fmt.Println("Found the older mc-wol-proxy service. Stopping it, mcwod replaces it.")
	exec.Command("systemctl", "stop", "mc-wol-proxy").Run()
	exec.Command("systemctl", "disable", "mc-wol-proxy").Run()
	if err := os.Rename(old, old+".replaced-by-mcwod"); err != nil {
		return err
	}
	ui.PrintHint("Its unit file is kept as " + old + ".replaced-by-mcwod")
	return nil
}

// Asked as the service user, so the SSH key it creates is one that user can
// read afterwards. Root would answer the same questions into the wrong home.
func runWizard(binary, configPath string) error {
	fmt.Println("")
	cmd := exec.Command(binary, "init")
	cmd.Env = append(os.Environ(), "MCWOD_CONFIG="+configPath)
	cmd.Stdin = wizardInput()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if owner := serviceUser(); owner != nil {
		uid, gid, ok := serviceUserIDs()
		if ok && uid != os.Geteuid() {
			cmd.SysProcAttr = &syscall.SysProcAttr{
				Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)},
			}
			cmd.Env = append(cmd.Env, "HOME="+owner.HomeDir, "USER="+owner.Username)
		}
	}
	return cmd.Run()
}

// Piping the installer into a shell leaves stdin on the pipe, where the wizard
// would read the rest of the script instead of an answer.
func wizardInput() *os.File {
	if attachedToTerminal() {
		return os.Stdin
	}
	if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		return tty
	}
	return os.Stdin
}

func startWatcher() error {
	if err := exec.Command("systemctl", "enable", "mcwod").Run(); err != nil {
		return err
	}
	return exec.Command("systemctl", "restart", "mcwod").Run()
}

func startCommandHint() string { return "sudo systemctl enable --now mcwod" }

func logCommandHint() string { return "Logs: journalctl -u mcwod -f" }
