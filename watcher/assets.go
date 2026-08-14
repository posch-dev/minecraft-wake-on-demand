package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Assets are read per request, so editing a MOTD takes effect without a restart.
type Assets struct {
	dir string
	cfg *Config
}

func NewAssets(cfg *Config) *Assets {
	return &Assets{dir: cfg.AssetsDir(), cfg: cfg}
}

func (a *Assets) motd(filename, fallback string) string {
	path := filepath.Join(a.dir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	content := strings.TrimSpace(string(data))
	if !json.Valid([]byte(content)) {
		log.Warnf("Failed to load %s: not valid JSON, using fallback", path)
		return fallback
	}
	return content
}

func (a *Assets) MOTDSleeping() string {
	return a.motd("motd-sleeping.json", a.cfg.MOTD.Sleeping)
}

func (a *Assets) MOTDStarting() string {
	return a.motd("motd-starting.json", a.cfg.MOTD.Starting)
}

// Empty when there is no icon, the status response then omits the field.
func (a *Assets) Icon() string {
	path := filepath.Join(a.dir, "server-icon.png")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}
