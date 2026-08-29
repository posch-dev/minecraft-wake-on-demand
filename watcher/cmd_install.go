package main

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/fsx"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

//go:embed mcwod.service
var systemdUnitTemplate string

// Carried in the binary so a downloaded release can lay them out without a
// checkout to copy them from.
//
//go:embed assets/examples
var exampleAssets embed.FS

const installedBinaryName = "mcwod" + exeSuffix

// Everything the shell installer used to do, in the binary itself, so that
// downloading one file and running it is a complete installation.
func runInstall() int {
	dir := installDir()
	if err := checkInstallPermission(); err != nil {
		printError(err.Error())
		return 1
	}

	fmt.Printf("Installing to %s\n", dir)
	binary, err := placeBinary(dir)
	if err != nil {
		printError("Cannot install the binary: " + err.Error())
		return 1
	}
	if err := writeExampleAssets(dir); err != nil {
		logging.Warnf("Cannot write the example assets: %v", err)
	}
	if err := registerAutostart(dir, binary); err != nil {
		printError("Cannot set up the autostart: " + err.Error())
		return 1
	}
	// Last, so everything written above belongs to the account that runs it.
	if err := handOverInstallDir(dir); err != nil {
		logging.Warnf("Cannot hand %s to the service user: %v", dir, err)
	}

	configPath := filepath.Join(dir, "config.yml")
	if !fsx.Exists(configPath) {
		if err := runWizard(binary, configPath); err != nil {
			printWarning("The setup questions did not finish: " + err.Error())
		}
	}
	if !fsx.Exists(configPath) {
		printRemainingSteps(binary)
		return 0
	}

	if err := startWatcher(); err != nil {
		printError("Cannot start the watcher: " + err.Error())
		return 1
	}
	fmt.Println("\n=== Installation complete ===")
	printFollowUp(binary, configPath)
	return 0
}

func installDir() string {
	if env := config.RenamedEnv("MCWOD_INSTALL_DIR"); env != "" {
		return env
	}
	return defaultInstallDir()
}

// Copying onto a running binary fails on Linux and Windows alike, so an install
// that is already in place is left where it is.
func placeBinary(dir string) (string, error) {
	source, err := os.Executable()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(dir, installedBinaryName)
	if sameFile(source, target) {
		return target, nil
	}
	if err := copyFile(source, target, 0o755); err != nil {
		return "", err
	}
	fmt.Printf("Installed %s\n", target)
	return target, nil
}

func sameFile(a, b string) bool {
	left, err := os.Stat(a)
	if err != nil {
		return false
	}
	right, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(left, right)
}

// Written to a neighbour first so a failed copy cannot leave a half binary
// behind that systemd would then try to start.
func copyFile(source, target string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	temp := target + ".new"
	out, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(temp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(temp)
		return err
	}
	if err := os.Chmod(temp, mode); err != nil {
		os.Remove(temp)
		return err
	}
	return os.Rename(temp, target)
}

// assets/ starts empty, the icons and MOTD live in the binary until someone
// overrides them. The examples are there to copy from.
func writeExampleAssets(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		return err
	}
	entries, err := fs.ReadDir(exampleAssets, "assets/examples")
	if err != nil {
		return err
	}
	target := filepath.Join(dir, "assets", "examples")
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(target, entry.Name())
		if fsx.Exists(path) {
			continue
		}
		data, err := exampleAssets.ReadFile("assets/examples/" + entry.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func printRemainingSteps(binary string) {
	fmt.Println("\n=== Almost done ===")
	fmt.Println("There is no config yet. Run these, in this order:")
	fmt.Println("")
	for _, command := range []string{"init", "setup-ssh", "check"} {
		fmt.Printf("  %s %s\n", quoteForShell(binary), command)
	}
	fmt.Println("")
	fmt.Println("Then run the install again, or start it yourself:")
	printHint("  " + startCommandHint())
}

func printFollowUp(binary, configPath string) {
	fmt.Printf("Check the setup: %s check\n", quoteForShell(binary))
	printHint("  " + logCommandHint())
	printHint("  Config: " + configPath)
}

// Only the shape a path with spaces needs, not general shell quoting.
func quoteForShell(path string) string {
	if !strings.ContainsAny(path, " \t") {
		return path
	}
	return `"` + path + `"`
}
