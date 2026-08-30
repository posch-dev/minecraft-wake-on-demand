package assets

import (
	"os"
	"time"
)

// Composing a PNG on every ping would be wasted work, the files rarely change.
type cachedIcon struct {
	modTime time.Time
	size    int64
	dataURI string
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
