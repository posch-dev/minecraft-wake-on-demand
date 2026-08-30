//go:build windows

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/ui"
)

const (
	exeSuffix      = ".exe"
	startupVBSName = "mcwod.vbs"
)

// Under the user's own profile, so installing needs no administrator and the
// watcher can read its own config afterwards.
func defaultInstallDir() string {
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		return filepath.Join(local, "mcwod")
	}
	return filepath.Join(os.Getenv("USERPROFILE"), "mcwod")
}

func checkInstallPermission() error { return nil }

func handOverInstallDir(string) error { return nil }

// A real Windows service is still to come. Until then the watcher starts with
// the user's session, which is enough for a PC that is on when people play.
func registerAutostart(dir, binary string) error {
	startup := startupFolder()
	if startup == "" {
		return fmt.Errorf("cannot find the Startup folder, APPDATA is not set")
	}
	if err := os.MkdirAll(startup, 0o755); err != nil {
		return err
	}
	script := filepath.Join(startup, startupVBSName)
	if err := os.WriteFile(script, []byte(startupScript(binary)), 0o644); err != nil {
		return err
	}
	fmt.Printf("Autostart set up in %s\n", script)
	ui.PrintHint("A proper Windows service is coming, this starts with your session.")
	return nil
}

func startupFolder() string {
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		return ""
	}
	return filepath.Join(appdata, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
}

// Run with 0 as the window style, so no console flashes up at every logon.
func startupScript(binary string) string {
	return "' Starts the Minecraft Wake-on-Demand watcher without a console window.\r\n" +
		"' Delete this file to stop it starting with Windows.\r\n" +
		"CreateObject(\"WScript.Shell\").Run \"\"\"" + binary + "\"\" run\", 0, False\r\n"
}

func runWizard(binary, configPath string) error {
	fmt.Println("")
	cmd := exec.Command(binary, "init")
	cmd.Env = append(os.Environ(), "MCWOD_CONFIG="+configPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func startWatcher() error {
	script := filepath.Join(startupFolder(), startupVBSName)
	return exec.Command("wscript.exe", script).Start()
}

func startCommandHint() string {
	return "wscript.exe " + quoteForShell(filepath.Join(startupFolder(), startupVBSName))
}

func logCommandHint() string {
	return "No log file yet, run " + quoteForShell(filepath.Join(installDir(), installedBinaryName)) +
		" run in a console to watch it"
}
