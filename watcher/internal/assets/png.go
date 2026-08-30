package assets

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
)

// Opaque white, the base icon at half opacity, then the overlay on top.
func composeOverlay(base image.Image, overlayPNG []byte) string {
	overlay, err := png.Decode(bytes.NewReader(overlayPNG))
	if err != nil {
		logging.Errorf("Built-in overlay does not decode: %v", err)
		return ""
	}

	bounds := image.Rect(0, 0, IconEdge, IconEdge)
	canvas := image.NewNRGBA(bounds)
	draw.Draw(canvas, bounds, image.NewUniform(color.White), image.Point{}, draw.Src)
	if base != nil {
		draw.DrawMask(canvas, bounds, base, image.Point{}, image.NewUniform(color.Alpha{A: 128}), image.Point{}, draw.Over)
	}
	draw.Draw(canvas, bounds, overlay, image.Point{}, draw.Over)

	var buf bytes.Buffer
	if err := png.Encode(&buf, canvas); err != nil {
		logging.Errorf("Cannot encode the composed icon: %v", err)
		return ""
	}
	return encodeDataURI(buf.Bytes())
}

func encodeDataURI(pngBytes []byte) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
}

// Clients drop the whole status response over a wrongly sized icon, so a silent
// skip beats a server that vanishes from the list.
func DecodeIconPNG(path string, data []byte) (image.Image, error) {
	width, height, err := PngDimensions(data)
	if err != nil {
		return nil, err
	}
	if width != IconEdge || height != IconEdge {
		return nil, fmt.Errorf("icon is %dx%d, Minecraft requires %dx%d", width, height, IconEdge, IconEdge)
	}
	return png.Decode(bytes.NewReader(data))
}

var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

// The IHDR chunk is first and holds the dimensions, cheaper than decoding.
func PngDimensions(data []byte) (int, int, error) {
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

var errNotAPNG = errors.New("not a PNG image")
