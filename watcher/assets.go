package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Unauthenticated and answered to anyone, so an uncapped icon is an amplifier.
const (
	maxIconBytes = 64 * 1024
	maxMOTDBytes = 8 * 1024
	iconEdge     = 64
)

// Assets are read per request, so editing a MOTD takes effect without a restart.
type Assets struct {
	dir string
	cfg *Config

	iconMu    sync.Mutex
	iconCache map[string]cachedIcon
}

// Encoding a 64x64 PNG on every ping is wasted work, the file rarely changes.
type cachedIcon struct {
	modTime time.Time
	size    int64
	dataURI string
}

func NewAssets(cfg *Config) *Assets {
	return &Assets{dir: cfg.AssetsDir(), cfg: cfg, iconCache: map[string]cachedIcon{}}
}

func (a *Assets) motd(filename, fallback string) string {
	path := filepath.Join(a.dir, filename)
	info, err := os.Stat(path)
	if err != nil {
		return fallback
	}
	if info.Size() > maxMOTDBytes {
		log.Warnf("Ignoring %s: %d bytes is over the %d byte limit", path, info.Size(), maxMOTDBytes)
		return fallback
	}
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

// Empty when there is no usable icon, the status response then omits the field.
func (a *Assets) Icon() string {
	return a.iconAt(filepath.Join(a.dir, "server-icon.png"))
}

func (a *Assets) iconAt(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}

	a.iconMu.Lock()
	defer a.iconMu.Unlock()

	if cached, ok := a.iconCache[path]; ok &&
		cached.modTime.Equal(info.ModTime()) && cached.size == info.Size() {
		return cached.dataURI
	}

	// A rejected icon is cached as empty too, so a bad file is complained about
	// once instead of on every ping.
	dataURI := ""
	switch {
	case info.Size() > maxIconBytes:
		log.Warnf("Ignoring %s: %d bytes is over the %d byte limit", path, info.Size(), maxIconBytes)
	default:
		if data, err := os.ReadFile(path); err == nil {
			dataURI = encodeIconPNG(path, data)
		}
	}

	a.iconCache[path] = cachedIcon{modTime: info.ModTime(), size: info.Size(), dataURI: dataURI}
	return dataURI
}

func encodeIconPNG(path string, data []byte) string {
	width, height, err := pngDimensions(data)
	if err != nil {
		log.Warnf("Ignoring %s: %v", path, err)
		return ""
	}
	// Clients drop the whole status response over a wrongly sized icon, so a
	// silent skip beats a server that vanishes from the list.
	if width != iconEdge || height != iconEdge {
		log.Warnf("Ignoring %s: icon is %dx%d, Minecraft requires %dx%d",
			path, width, height, iconEdge, iconEdge)
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}

var errNotAPNG = errors.New("not a PNG image")

var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

// The IHDR chunk is always first and holds the dimensions in its first 8 bytes,
// which is cheaper than decoding the image just to measure it.
func pngDimensions(data []byte) (int, int, error) {
	if len(data) < 24 || string(data[:8]) != string(pngSignature) {
		return 0, 0, errNotAPNG
	}
	if string(data[12:16]) != "IHDR" {
		return 0, 0, errNotAPNG
	}
	width := binary.BigEndian.Uint32(data[16:20])
	height := binary.BigEndian.Uint32(data[20:24])
	return int(width), int(height), nil
}
