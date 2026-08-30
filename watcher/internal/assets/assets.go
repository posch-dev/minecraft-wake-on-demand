package assets

import (
	"context"
	_ "embed"
	"path/filepath"
	"sync"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/fsx"
)

// Unauthenticated and answered to anyone, so an uncapped icon is an amplifier.
const (
	MaxIconBytes = 64 * 1024
	maxMOTDBytes = 8 * 1024
	IconEdge     = 64
)

const assetRefreshInterval = time.Minute

// The three things the server list can be showing, and the message a player
// gets when their own attempt to join is what starts the server.
const (
	StateSleeping  = "sleeping"
	StateStarting  = "starting"
	StateLive      = "live"
	stateLoginWait = "login-wait"
)

// Assets are read per request, so editing a MOTD takes effect without a restart.
type Assets struct {
	dir string
	cfg *config.Config

	iconMu    sync.Mutex
	iconCache map[string]cachedIcon
}

func NewAssets(cfg *config.Config) *Assets {
	assets := &Assets{dir: cfg.AssetsDir(), cfg: cfg, iconCache: map[string]cachedIcon{}}
	assets.warm()
	return assets
}

// Composing an icon is milliseconds, but no client should ever wait for it, so
// it happens on a tick. A ping then only stats files it has already composed.
func (a *Assets) KeepFresh(ctx context.Context) {
	ticker := time.NewTicker(assetRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.warm()
		}
	}
}

func (a *Assets) warm() {
	a.MOTDSleeping()
	a.MOTDStarting()
	a.MOTDLive()
	a.MOTDLoginWait()
	a.IconSleeping()
	a.IconStarting()
	a.IconLive()
}

// A world can look different without repeating what it shares, so its own
// folder is looked at first and the shared one after.
func (a *Assets) search(name string) []string {
	if world := a.cfg.ActiveWorldName(); world != "" {
		return []string{
			filepath.Join(a.dir, "worlds", world, name),
			filepath.Join(a.dir, name),
		}
	}
	return []string{filepath.Join(a.dir, name)}
}

// The last candidate when none of them exist, so the stat below fails the same
// way it used to and the caller still gets a plain overlay.
func (a *Assets) firstExisting(name string) string {
	paths := a.search(name)
	for _, path := range paths {
		if fsx.Exists(path) {
			return path
		}
	}
	return paths[len(paths)-1]
}
