package yamledit

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"gopkg.in/yaml.v3"
)

const commentedConfig = `# The real config, with notes people wrote themselves.
server:
  # my gaming PC in the cupboard
  ip: "192.168.1.100"
  mac: "AA:BB:CC:DD:EE:FF"
  ssh_user: "eliah"            # not root
  container_name: "minecraft"

duckdns:
  enabled: true
  domain: "mine"
  token: "secret-token"

motd:
  sleeping: '{"text":"asleep"}'
`

func documentFrom(t *testing.T, body string) (*Document, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return doc, path
}

func saved(t *testing.T, doc *Document, path string) string {
	t.Helper()
	if err := doc.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The comments are a good part of the documentation, an edit must not eat them.
func TestEditingKeepsTheComments(t *testing.T) {
	doc, path := documentFrom(t, commentedConfig)

	if err := doc.Set([]string{"server", "ip"}, "192.168.1.50"); err != nil {
		t.Fatal(err)
	}
	out := saved(t, doc, path)

	for _, want := range []string{
		"# The real config, with notes people wrote themselves.",
		"# my gaming PC in the cupboard",
		"not root",
		"192.168.1.50",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the saved file lost %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "192.168.1.100") {
		t.Errorf("the old value is still there:\n%s", out)
	}
}

func TestEditingKeepsUntouchedKeys(t *testing.T) {
	doc, path := documentFrom(t, commentedConfig)

	if err := doc.Set([]string{"duckdns", "domain"}, "other"); err != nil {
		t.Fatal(err)
	}
	out := saved(t, doc, path)

	var parsed config.Config
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("the saved file no longer parses: %v", err)
	}
	if parsed.DuckDNS.Domain != "other" {
		t.Errorf("domain = %q, want other", parsed.DuckDNS.Domain)
	}
	if parsed.DuckDNS.Token != "secret-token" {
		t.Errorf("token = %q, editing one key must not disturb its neighbours", parsed.DuckDNS.Token)
	}
	if parsed.Server.ContainerName != "minecraft" {
		t.Errorf("container_name = %q", parsed.Server.ContainerName)
	}
}

// A config written before a feature existed has no section for it.
func TestSettingCreatesAMissingSection(t *testing.T) {
	doc, path := documentFrom(t, commentedConfig)

	if err := doc.Set([]string{"sleep", "enabled"}, true); err != nil {
		t.Fatal(err)
	}
	if err := doc.Set([]string{"sleep", "action"}, "hibernate"); err != nil {
		t.Fatal(err)
	}
	out := saved(t, doc, path)

	var parsed config.Config
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("the saved file no longer parses: %v", err)
	}
	if !parsed.Sleep.Enabled || parsed.Sleep.Action != "hibernate" {
		t.Errorf("sleep = %+v", parsed.Sleep)
	}
}

// Reflowing every quoted MOTD on an unrelated edit would be a noisy diff.
func TestSettingKeepsTheQuotingStyle(t *testing.T) {
	doc, path := documentFrom(t, commentedConfig)

	if err := doc.Set([]string{"motd", "sleeping"}, `{"text":"still asleep"}`); err != nil {
		t.Fatal(err)
	}
	out := saved(t, doc, path)

	if !strings.Contains(out, `'{"text":"still asleep"}'`) {
		t.Errorf("the single quoted style was not kept:\n%s", out)
	}
}

func TestSettingAListWritesAYAMLSequence(t *testing.T) {
	doc, path := documentFrom(t, commentedConfig)

	if err := doc.Set([]string{"watcher", "allowed_hostnames"}, []string{"mine.duckdns.org", "192.168.1.50"}); err != nil {
		t.Fatal(err)
	}
	out := saved(t, doc, path)

	var parsed config.Config
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("the saved file no longer parses: %v", err)
	}
	if len(parsed.Watcher.AllowedHostnames) != 2 {
		t.Errorf("allowed_hostnames = %v, want 2 entries", parsed.Watcher.AllowedHostnames)
	}
}

func TestSettingAnEmptyListClearsIt(t *testing.T) {
	doc, path := documentFrom(t, commentedConfig+"\nwatcher:\n  allowed_hostnames: [\"a\", \"b\"]\n")

	if err := doc.Set([]string{"watcher", "allowed_hostnames"}, []string{}); err != nil {
		t.Fatal(err)
	}
	out := saved(t, doc, path)

	var parsed config.Config
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("the saved file no longer parses: %v", err)
	}
	if len(parsed.Watcher.AllowedHostnames) != 0 {
		t.Errorf("allowed_hostnames = %v, want it emptied", parsed.Watcher.AllowedHostnames)
	}
}

func TestSavedConfigStaysMode600(t *testing.T) {
	// Windows does not map POSIX bits, the check is meaningful on Unix only.
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not POSIX bits on Windows")
	}
	doc, path := documentFrom(t, commentedConfig)

	if err := doc.Set([]string{"server", "ip"}, "192.168.1.51"); err != nil {
		t.Fatal(err)
	}
	if err := doc.Save(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("mode = %04o, the file holds the DuckDNS token", mode)
	}
}

func TestLoadingRejectsBrokenYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("server:\n  ip: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Error("broken YAML should not load")
	}
}
