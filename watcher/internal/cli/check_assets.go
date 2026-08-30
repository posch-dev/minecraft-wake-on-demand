package cli

import (
	"os"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/assets"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

func checkAssets(c *checker, cfg *config.Config) {
	c.section("MOTD and icon")
	dir := cfg.AssetsDir()
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		c.warn("no assets directory at %s", dir)
		c.hint("the MOTD from config.yml is used instead, which is fine")
		return
	}
	c.ok("assets directory %s", dir)

	set := assets.NewAssets(cfg)
	for _, state := range []string{assets.StateSleeping, assets.StateStarting, assets.StateLive} {
		reportMOTDSource(c, cfg, set, state)
	}

	if icon := set.IconSleeping(); icon == "" {
		c.warn("no icon for the sleeping state, the list shows the default block")
	} else {
		c.ok("sleeping icon ready, %d bytes encoded", len(icon))
	}
	if set.IconLive() == "" {
		c.info("no server-icon.png, the running server's own icon is passed through")
	} else {
		c.ok("running server shows your icon, %d bytes encoded", len(set.IconLive()))
	}
}

func reportMOTDSource(c *checker, cfg *config.Config, set *assets.Assets, state string) {
	fromFile := map[string]func() string{
		assets.StateSleeping: set.MOTDSleeping,
		assets.StateStarting: set.MOTDStarting,
		assets.StateLive:     set.MOTDLive,
	}[state]()
	fromConfig := map[string]string{
		assets.StateSleeping: cfg.MOTD.Sleeping,
		assets.StateStarting: cfg.MOTD.Starting,
		assets.StateLive:     cfg.MOTD.Live,
	}[state]

	switch {
	case fromFile == "" && state == assets.StateLive:
		c.info("motd-live.json not set, the running server's own MOTD is passed through")
	case fromFile != fromConfig:
		c.ok("motd-%s.json loaded", state)
	default:
		c.info("motd-%s.json not used, falling back to config.yml", state)
	}
}
