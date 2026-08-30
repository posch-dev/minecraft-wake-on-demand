package assets

import (
	"image"
	"os"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/embedded"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
)

func (a *Assets) IconSleeping() string {
	return a.stateIcon(StateSleeping, embedded.OverlaySleepingPNG)
}

func (a *Assets) IconStarting() string {
	return a.stateIcon(StateStarting, embedded.OverlayStartingPNG)
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
	if info.Size() > MaxIconBytes {
		logging.Warnf("Ignoring %s: %d bytes is over the %d byte limit", path, info.Size(), MaxIconBytes)
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	decoded, err := DecodeIconPNG(path, data)
	if err != nil {
		logging.Warnf("Ignoring %s: %v", path, err)
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
	if info.Size() > MaxIconBytes {
		logging.Warnf("Ignoring %s: %d bytes is over the %d byte limit", path, info.Size(), MaxIconBytes)
	} else if data, err := os.ReadFile(path); err == nil {
		if _, err := DecodeIconPNG(path, data); err != nil {
			logging.Warnf("Ignoring %s: %v", path, err)
		} else {
			dataURI = encodeDataURI(data)
		}
	}

	a.iconCache[path] = newCachedIcon(info, nil, dataURI)
	return dataURI
}
