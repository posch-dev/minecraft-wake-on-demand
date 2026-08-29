// The Minecraft version and player slots learned from a status probe, kept so
// the watcher can answer for a server that is asleep.
package serverinfo

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
)

// What a status probe told us about the running server, kept across restarts so
// the watcher can answer for it while it sleeps.
type Info struct {
	Name       string    `json:"name"`
	Protocol   int       `json:"protocol"`
	MaxPlayers int       `json:"max_players"`
	Updated    time.Time `json:"updated"`
}

// What the last status probe learned, for the world the config points at.
func Load(cfg *config.Config) *Info {
	cache := readCache(cfg.ServerInfoPath())
	return cache[cfg.ServerInfoKey()]
}

func Save(cfg *config.Config, info *Info) {
	path := cfg.ServerInfoPath()
	cache := readCache(path)
	cache[cfg.ServerInfoKey()] = info
	writeCache(path, cache)
}

// Empty rather than nil on any problem, so a caller can always write into it.
// An older single world file does not fit the shape and is left to be replaced.
func readCache(path string) map[string]*Info {
	cache := map[string]*Info{}
	data, err := os.ReadFile(path)
	if err != nil {
		return cache
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		logging.Infof("Replacing the server info cache %s, it is from an older version", path)
		return map[string]*Info{}
	}
	return cache
}

// The cache holds nothing secret, 0600 only keeps other accounts on the watcher
// from feeding the proxy a version it never probed.
func writeCache(path string, cache map[string]*Info) {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		logging.Warnf("Cannot encode server info cache: %v", err)
		return
	}
	tmpPath := fmt.Sprintf("%s.tmp.%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		logging.Warnf("Cannot write server info cache %s: %v", tmpPath, err)
		return
	}
	if err := os.Rename(tmpPath, path); err != nil {
		logging.Warnf("Cannot update server info cache %s: %v", path, err)
		os.Remove(tmpPath)
	}
}

// A world that changed its Minecraft version would otherwise keep claiming the
// old one until someone connects.
func Forget(cfg *config.Config, world string) {
	path := cfg.ServerInfoPath()
	cache := readCache(path)
	if _, ok := cache[world]; !ok {
		return
	}
	delete(cache, world)
	writeCache(path, cache)
}
