package assets

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
)

func (a *Assets) MOTDSleeping() string {
	return a.motd(StateSleeping, a.cfg.MOTD.Sleeping)
}

func (a *Assets) MOTDStarting() string {
	return a.motd(StateStarting, a.cfg.MOTD.Starting)
}

// Empty means the real server's own MOTD is passed through untouched.
func (a *Assets) MOTDLive() string {
	return a.motd(StateLive, a.cfg.MOTD.Live)
}

// Shown to whoever's attempt to join woke the server, on the disconnect screen.
func (a *Assets) MOTDLoginWait() string {
	return a.motd(stateLoginWait, a.cfg.MOTD.LoginWait)
}

// World file beats shared file beats config beats the built-in default.
func (a *Assets) motd(state, fallback string) string {
	for _, path := range a.search("motd-" + state + ".json") {
		if found := a.motdAt(path); found != "" {
			return found
		}
	}
	return fallback
}

// Empty when nothing usable is there, so the caller keeps looking.
func (a *Assets) motdAt(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if info.Size() > maxMOTDBytes {
		logging.Warnf("Ignoring %s: %d bytes is over the %d byte limit", path, info.Size(), maxMOTDBytes)
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if !json.Valid([]byte(content)) {
		logging.Warnf("Failed to load %s: not valid JSON, using fallback", path)
		return ""
	}
	return content
}
