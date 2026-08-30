package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Always asks first. An unattended service replacing its own binary without
// being told to is exactly what nobody wants from a thing that guards a PC.
func runUpdate() int {
	fmt.Printf("Installed version: %s\n", version)
	if version == "dev" {
		fmt.Println("\nThis is a development build, there is nothing to compare it against.")
		fmt.Println("Build from source or install a release first.")
		return 1
	}

	release, err := fetchLatestReleaseNow()
	if err != nil {
		fmt.Printf("\nCould not reach the release API: %v\n", err)
		return 1
	}

	fmt.Printf("Latest release:    %s\n", release.Tag)
	if !isNewerVersion(release.Tag, version) {
		fmt.Println("\nAlready up to date.")
		return 0
	}
	printReleaseNotes(release.Body)

	asset, err := releaseAssetName()
	if err != nil {
		fmt.Printf("\n%v\n", err)
		fmt.Println("Build from source instead: sudo ./install.sh --build")
		return 1
	}

	target, err := os.Executable()
	if err != nil {
		fmt.Printf("\nCannot find the running binary: %v\n", err)
		return 1
	}
	target, _ = filepath.EvalSymlinks(target)

	p := newPrompter()
	if !p.yesNo(fmt.Sprintf("\nReplace %s with %s", target, release.Tag), false) {
		fmt.Println("Nothing was changed.")
		return 0
	}
	if !canWriteBinary(target) {
		fmt.Printf("\nNo permission to replace %s.\n", target)
		fmt.Println("Run it again with: sudo mcwod update")
		return 1
	}

	fmt.Printf("\nDownloading %s...\n", asset)
	downloadCtx, cancelDownload := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancelDownload()

	binary, err := downloadRelease(downloadCtx, release.Tag, asset)
	if err != nil {
		fmt.Printf("%v\n", err)
		return 1
	}
	fmt.Println("Checksum verified.")

	if err := replaceBinary(target, binary); err != nil {
		fmt.Printf("\n%v\n", err)
		return 1
	}
	fmt.Printf("Installed %s to %s\n", release.Tag, target)

	restartService()
	fmt.Println("\nConfirm the result with: mcwod check")
	return 0
}

func printReleaseNotes(body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	fmt.Println("\nWhat changed:")
	for i, line := range strings.Split(body, "\n") {
		if i >= 20 {
			fmt.Println("  ...")
			break
		}
		fmt.Println("  " + strings.TrimRight(line, "\r"))
	}
}

// Written next to the old binary and renamed over it, so a failed download
// cannot leave a half written file where the service expects a program.
func replaceBinary(target string, binary []byte) error {
	dir := filepath.Dir(target)
	staged, err := os.CreateTemp(dir, ".mcwod-update-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	stagedPath := staged.Name()

	if _, err := staged.Write(binary); err != nil {
		staged.Close()
		os.Remove(stagedPath)
		return fmt.Errorf("cannot write %s: %w", stagedPath, err)
	}
	if err := staged.Close(); err != nil {
		os.Remove(stagedPath)
		return err
	}
	if err := os.Chmod(stagedPath, 0o755); err != nil {
		os.Remove(stagedPath)
		return err
	}

	// Windows refuses to replace a running image, so the old one is moved aside.
	if runtime.GOOS == "windows" {
		backup := target + ".old"
		os.Remove(backup)
		if err := os.Rename(target, backup); err != nil {
			os.Remove(stagedPath)
			return fmt.Errorf("cannot move the running binary aside: %w", err)
		}
	}
	if err := os.Rename(stagedPath, target); err != nil {
		os.Remove(stagedPath)
		return fmt.Errorf("cannot replace %s: %w", target, err)
	}
	return nil
}

func canWriteBinary(target string) bool {
	f, err := os.OpenFile(target, os.O_WRONLY, 0)
	if err != nil {
		// Windows blocks writing to the running image, the rename still works.
		return runtime.GOOS == "windows" && os.IsPermission(err)
	}
	f.Close()
	return true
}

// Best effort. A watcher installed some other way is simply left running the
// old code until whoever installed it restarts the thing themselves.
func restartService() {
	if runtime.GOOS != "linux" {
		fmt.Println("\nRestart the watcher so the new version takes over.")
		return
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		fmt.Println("\nRestart the watcher so the new version takes over.")
		return
	}

	fmt.Println("Restarting mcwod...")
	if out, err := exec.Command("systemctl", "restart", "mcwod").CombinedOutput(); err != nil {
		fmt.Printf("  could not restart it: %v: %s\n", err, sanitizeForLog(string(out), 200))
		fmt.Println("  restart it yourself with: sudo systemctl restart mcwod")
		return
	}
	fmt.Println("  restarted.")
}
