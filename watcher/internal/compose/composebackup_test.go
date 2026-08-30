package compose

import (
	"strings"
	"testing"
)

// Newest first, which is what someone restoring almost always wants.
func TestComposeBackupNamesSortNewestFirst(t *testing.T) {
	names := []string{
		composeBackupPrefix + "20260101-120000",
		composeBackupPrefix + "20260828-093000",
		composeBackupPrefix + "20260615-235959",
	}
	sortComposeBackups(names)

	if names[0] != composeBackupPrefix+"20260828-093000" {
		t.Errorf("newest first failed: %v", names)
	}
	if names[len(names)-1] != composeBackupPrefix+"20260101-120000" {
		t.Errorf("oldest last failed: %v", names)
	}
}

// The timestamp has to sort as text, otherwise the listing lies about the order.
func TestComposeBackupNameIsSortable(t *testing.T) {
	name := composeBackupName("20260828-093000")

	if !strings.HasPrefix(name, composeBackupPrefix) {
		t.Errorf("name = %q, it has to be recognisable as ours", name)
	}
	stamp := strings.TrimPrefix(name, composeBackupPrefix)
	if len(stamp) != len("20060102-150405") {
		t.Errorf("timestamp %q is not the fixed width form that sorts", stamp)
	}
}

// A file somebody else left lying around must not show up as one of ours.
func TestOnlyOurBackupsAreListed(t *testing.T) {
	lines := []string{
		composeBackupPrefix + "20260828-093000",
		"docker-compose.yml",
		"docker-compose.yml.bak",
		".env",
		composeBackupPrefix + "20260101-120000",
	}

	found := filterComposeBackups(lines)
	if len(found) != 2 {
		t.Fatalf("found %v, want only the two mcwol backups", found)
	}
	for _, name := range found {
		if !strings.HasPrefix(name, composeBackupPrefix) {
			t.Errorf("%q is not one of ours", name)
		}
	}
}
