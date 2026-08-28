package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
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

const assetRefreshInterval = time.Minute

//go:embed assets/embed/overlay-sleeping.png
var overlaySleepingPNG []byte

//go:embed assets/embed/overlay-starting.png
var overlayStartingPNG []byte

// Named after the three things the server list can be showing.
const (
	stateSleeping = "sleeping"
	stateStarting = "starting"
	stateLive     = "live"
)

var errNotAPNG = errors.New("not a PNG image")

// Assets are read per request, so editing a MOTD takes effect without a restart.
type Assets struct {
	dir string
	cfg *Config

	iconMu    sync.Mutex
	iconCache map[string]cachedIcon
}

// Composing a PNG on every ping would be wasted work, the files rarely change.
type cachedIcon struct {
	modTime time.Time
	size    int64
	dataURI string
}

func NewAssets(cfg *Config) *Assets {
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

func (a *Assets) MOTDSleeping() string {
	return a.motd(stateSleeping, a.cfg.MOTD.Sleeping)
}

func (a *Assets) MOTDStarting() string {
	return a.motd(stateStarting, a.cfg.MOTD.Starting)
}

// Empty means the real server's own MOTD is passed through untouched.
func (a *Assets) MOTDLive() string {
	return a.motd(stateLive, a.cfg.MOTD.Live)
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
		log.Warnf("Ignoring %s: %d bytes is over the %d byte limit", path, info.Size(), maxMOTDBytes)
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if !json.Valid([]byte(content)) {
		log.Warnf("Failed to load %s: not valid JSON, using fallback", path)
		return ""
	}
	return content
}

func (a *Assets) IconSleeping() string {
	return a.stateIcon(stateSleeping, overlaySleepingPNG)
}

func (a *Assets) IconStarting() string {
	return a.stateIcon(stateStarting, overlayStartingPNG)
}

// The same rule as the other two states: a file for this state wins, otherwise
// the shared icon, which a running server needs no overlay on.
func (a *Assets) IconLive() string {
	if dedicated := a.firstIconFile("server-icon-live.png"); dedicated != "" {
		return dedicated
	}
	return a.firstIconFile("server-icon.png")
}

func (a *Assets) firstIconFile(name string) string {
	for _, path := range a.search(name) {
		if found := a.iconFile(path); found != "" {
			return found
		}
	}
	return ""
}

// A dedicated file is taken as it is, otherwise the overlay is drawn over a
// dimmed copy of the shared icon so the Z read on any artwork.
func (a *Assets) stateIcon(state string, overlay []byte) string {
	if dedicated := a.firstIconFile("server-icon-" + state + ".png"); dedicated != "" {
		return dedicated
	}
	return a.composedIcon(state, overlay)
}

func (a *Assets) composedIcon(state string, overlay []byte) string {
	base := a.firstExisting("server-icon.png")
	info, err := os.Stat(base)

	a.iconMu.Lock()
	defer a.iconMu.Unlock()

	key := "composed:" + state + ":" + base
	if cached, ok := a.iconCache[key]; ok && cached.matches(info, err) {
		return cached.dataURI
	}

	dataURI := composeOverlay(a.readBaseIcon(base, info, err), overlay)
	a.iconCache[key] = newCachedIcon(info, err, dataURI)
	return dataURI
}

// Nil when there is nothing usable to dim, which leaves plain white behind.
func (a *Assets) readBaseIcon(path string, info os.FileInfo, statErr error) image.Image {
	if statErr != nil {
		return nil
	}
	if info.Size() > maxIconBytes {
		log.Warnf("Ignoring %s: %d bytes is over the %d byte limit", path, info.Size(), maxIconBytes)
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	decoded, err := decodeIconPNG(path, data)
	if err != nil {
		log.Warnf("Ignoring %s: %v", path, err)
		return nil
	}
	return decoded
}

// Empty when there is no usable icon, the status response then omits the field.
func (a *Assets) iconFile(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}

	a.iconMu.Lock()
	defer a.iconMu.Unlock()

	if cached, ok := a.iconCache[path]; ok && cached.matches(info, nil) {
		return cached.dataURI
	}

	// A rejected file is cached as empty too, so it is complained about once.
	dataURI := ""
	if info.Size() > maxIconBytes {
		log.Warnf("Ignoring %s: %d bytes is over the %d byte limit", path, info.Size(), maxIconBytes)
	} else if data, err := os.ReadFile(path); err == nil {
		if _, err := decodeIconPNG(path, data); err != nil {
			log.Warnf("Ignoring %s: %v", path, err)
		} else {
			dataURI = encodeDataURI(data)
		}
	}

	a.iconCache[path] = newCachedIcon(info, nil, dataURI)
	return dataURI
}

// The last candidate when none of them exist, so the stat below fails the same
// way it used to and the caller still gets a plain overlay.
func (a *Assets) firstExisting(name string) string {
	paths := a.search(name)
	for _, path := range paths {
		if fileExists(path) {
			return path
		}
	}
	return paths[len(paths)-1]
}

func newCachedIcon(info os.FileInfo, statErr error, dataURI string) cachedIcon {
	if statErr != nil {
		return cachedIcon{dataURI: dataURI}
	}
	return cachedIcon{modTime: info.ModTime(), size: info.Size(), dataURI: dataURI}
}

func (c cachedIcon) matches(info os.FileInfo, statErr error) bool {
	if statErr != nil {
		return c.size == 0 && c.modTime.IsZero()
	}
	return c.size == info.Size() && c.modTime.Equal(info.ModTime())
}

// Opaque white, the base icon at half opacity, then the overlay on top.
func composeOverlay(base image.Image, overlayPNG []byte) string {
	overlay, err := png.Decode(bytes.NewReader(overlayPNG))
	if err != nil {
		log.Errorf("Built-in overlay does not decode: %v", err)
		return ""
	}

	bounds := image.Rect(0, 0, iconEdge, iconEdge)
	canvas := image.NewNRGBA(bounds)
	draw.Draw(canvas, bounds, image.NewUniform(color.White), image.Point{}, draw.Src)
	if base != nil {
		draw.DrawMask(canvas, bounds, base, image.Point{}, image.NewUniform(color.Alpha{A: 128}), image.Point{}, draw.Over)
	}
	draw.Draw(canvas, bounds, overlay, image.Point{}, draw.Over)

	var buf bytes.Buffer
	if err := png.Encode(&buf, canvas); err != nil {
		log.Errorf("Cannot encode the composed icon: %v", err)
		return ""
	}
	return encodeDataURI(buf.Bytes())
}

func encodeDataURI(pngBytes []byte) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
}

// Clients drop the whole status response over a wrongly sized icon, so a silent
// skip beats a server that vanishes from the list.
func decodeIconPNG(path string, data []byte) (image.Image, error) {
	width, height, err := pngDimensions(data)
	if err != nil {
		return nil, err
	}
	if width != iconEdge || height != iconEdge {
		return nil, fmt.Errorf("icon is %dx%d, Minecraft requires %dx%d", width, height, iconEdge, iconEdge)
	}
	return png.Decode(bytes.NewReader(data))
}

var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

// The IHDR chunk is first and holds the dimensions, cheaper than decoding.
func pngDimensions(data []byte) (int, int, error) {
	if len(data) < 24 || !bytes.Equal(data[:8], pngSignature) {
		return 0, 0, errNotAPNG
	}
	if string(data[12:16]) != "IHDR" {
		return 0, 0, errNotAPNG
	}
	width := binary.BigEndian.Uint32(data[16:20])
	height := binary.BigEndian.Uint32(data[20:24])
	return int(width), int(height), nil
}
