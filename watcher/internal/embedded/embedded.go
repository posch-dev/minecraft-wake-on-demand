// Everything the binary carries with it, so a downloaded release is a complete
// installation without a checkout to copy files from.
package embedded

import (
	"embed"
)

//go:embed overlays/overlay-sleeping.png
var OverlaySleepingPNG []byte

//go:embed overlays/overlay-starting.png
var OverlayStartingPNG []byte

//go:embed service/mcwod.service
var SystemdUnit string

//go:embed examples
var Examples embed.FS

// The directory inside Examples, so callers do not repeat the name.
const ExamplesDir = "examples"
