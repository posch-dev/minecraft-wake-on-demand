package main

import (
	"os"
	"path/filepath"
	"testing"
)

func homeWith(t *testing.T, names ...string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(home, ".ssh", name), []byte("key"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// A watcher on the internet must not end up holding the key its owner logs in
// with everywhere else.
func TestFreshInstallGetsItsOwnKey(t *testing.T) {
	home := homeWith(t)
	cfg := defaultConfig()

	want := filepath.Join(home, ".ssh", watcherKeyName)
	if got := cfg.ResolvedSSHKeyPath(); got != want {
		t.Errorf("key path = %q, want %q", got, want)
	}
	if cfg.UsesSharedSSHKey() {
		t.Error("a fresh install does not use the shared key")
	}
}

func TestOwnKeyWinsOverTheSharedOne(t *testing.T) {
	home := homeWith(t, watcherKeyName, sharedKeyName)
	cfg := defaultConfig()

	want := filepath.Join(home, ".ssh", watcherKeyName)
	if got := cfg.ResolvedSSHKeyPath(); got != want {
		t.Errorf("key path = %q, want the watcher's own key", got)
	}
}

// A personal key lying at the default path is not adopted, not even when the
// watcher has no key of its own yet.
func TestSharedKeyIsNeverPickedUp(t *testing.T) {
	home := homeWith(t, sharedKeyName)
	cfg := defaultConfig()

	want := filepath.Join(home, ".ssh", watcherKeyName)
	if got := cfg.ResolvedSSHKeyPath(); got != want {
		t.Errorf("key path = %q, want %q", got, want)
	}
	if cfg.UsesSharedSSHKey() {
		t.Error("the shared key was picked up")
	}
}

func TestConfiguredPathAlwaysWins(t *testing.T) {
	homeWith(t, sharedKeyName, watcherKeyName)
	cfg := defaultConfig()
	cfg.Server.SSHKeyPath = filepath.Join(t.TempDir(), "somewhere-else")

	if got := cfg.ResolvedSSHKeyPath(); got != cfg.Server.SSHKeyPath {
		t.Errorf("key path = %q, an explicit setting must win", got)
	}
	if cfg.UsesSharedSSHKey() {
		t.Error("an explicit path is never the shared key")
	}
}

// Writing the path in by hand is allowed, check warns about it.
func TestConfiguredSharedKeyStillWarns(t *testing.T) {
	home := homeWith(t, sharedKeyName)
	cfg := defaultConfig()
	cfg.Server.SSHKeyPath = filepath.Join(home, ".ssh", sharedKeyName)

	if !cfg.UsesSharedSSHKey() {
		t.Error("check has to be able to warn about this case")
	}
}
