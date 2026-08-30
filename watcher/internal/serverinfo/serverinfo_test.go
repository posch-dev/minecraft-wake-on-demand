package serverinfo

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

func TestSaveAndLoadServerInfoRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Path = filepath.Join(dir, "config.yml")

	sv := &Info{Name: "1.21.4", Protocol: 769, Updated: time.Now()}
	Save(&cfg, sv)

	cached := Load(&cfg)
	if cached == nil {
		t.Fatal("cached version was not loaded")
	}
	if cached.Name != "1.21.4" || cached.Protocol != 769 {
		t.Errorf("cached = %+v", cached)
	}
}

// Two worlds run two Minecraft versions, so one must not answer for the other.
func TestServerInfoIsKeptPerWorld(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Path = filepath.Join(dir, "config.yml")
	cfg.Worlds.List = []config.World{{Name: "survival"}, {Name: "creative"}}

	cfg.Worlds.Active = "survival"
	Save(&cfg, &Info{Name: "1.21.4", Protocol: 769})
	cfg.Worlds.Active = "creative"
	Save(&cfg, &Info{Name: "26.2", Protocol: 776})

	cfg.Worlds.Active = "survival"
	if got := Load(&cfg); got == nil || got.Protocol != 769 {
		t.Errorf("survival = %+v, want protocol 769", got)
	}
	cfg.Worlds.Active = "creative"
	if got := Load(&cfg); got == nil || got.Protocol != 776 {
		t.Errorf("creative = %+v, want protocol 776", got)
	}
}
