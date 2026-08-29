//go:build ignore

// Draws the two overlays that ship inside the binary. Run with:
//
//	go run geniconoverlays.go
//
// The PNGs are committed, this only exists so the artwork has a source.
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

const canvas = 64

var (
	blue  = color.NRGBA{0x3C, 0x78, 0xF0, 0xFF}
	red   = color.NRGBA{0xDC, 0x32, 0x32, 0xFF}
	halo  = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
	clear = color.NRGBA{0, 0, 0, 0}
)

type stroke struct {
	x1, y1, x2, y2 float64
}

// Three Z ascending from bottom left to top right, the biggest last.
var glyphBoxes = []struct{ x, y, size float64 }{
	{8, 44, 12},
	{22, 28, 18},
	{36, 6, 26},
}

func main() {
	write("assets/embed/overlay-sleeping.png", overlay(false))
	write("assets/embed/overlay-starting.png", overlay(true))
}

// The largest glyph becomes a red exclamation mark while the server wakes.
func overlay(waking bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, canvas, canvas))
	fill(img, clear)

	type glyph struct {
		strokes   []stroke
		thickness float64
		ink       color.NRGBA
	}
	glyphs := make([]glyph, 0, len(glyphBoxes))

	for i, box := range glyphBoxes {
		thickness := math.Max(2, box.size/6)
		last := i == len(glyphBoxes)-1
		if waking && last {
			glyphs = append(glyphs, glyph{exclamation(box.x, box.y, box.size), thickness, red})
			continue
		}
		glyphs = append(glyphs, glyph{letterZ(box.x, box.y, box.size), thickness, blue})
	}

	// Halo first, so the glyphs stay readable on any icon showing through.
	for _, g := range glyphs {
		draw(img, g.strokes, g.thickness+2.5, halo)
	}
	for _, g := range glyphs {
		draw(img, g.strokes, g.thickness, g.ink)
	}
	return img
}

func letterZ(x, y, size float64) []stroke {
	return []stroke{
		{x, y, x + size, y},
		{x + size, y, x, y + size},
		{x, y + size, x + size, y + size},
	}
}

func exclamation(x, y, size float64) []stroke {
	center := x + size/2
	return []stroke{
		{center, y, center, y + size*0.62},
		{center, y + size*0.88, center, y + size*0.90},
	}
}

// Distance to the segment, so every stroke gets the same rounded ends and the
// corners of a Z join cleanly.
func draw(img *image.NRGBA, strokes []stroke, thickness float64, ink color.NRGBA) {
	radius := thickness / 2
	for py := range canvas {
		for px := range canvas {
			cx, cy := float64(px)+0.5, float64(py)+0.5
			for _, s := range strokes {
				if distanceToSegment(cx, cy, s) <= radius {
					img.SetNRGBA(px, py, ink)
					break
				}
			}
		}
	}
}

func distanceToSegment(px, py float64, s stroke) float64 {
	dx, dy := s.x2-s.x1, s.y2-s.y1
	length := dx*dx + dy*dy
	if length == 0 {
		return math.Hypot(px-s.x1, py-s.y1)
	}
	t := ((px-s.x1)*dx + (py-s.y1)*dy) / length
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(s.x1+t*dx), py-(s.y1+t*dy))
}

func fill(img *image.NRGBA, c color.NRGBA) {
	for y := range canvas {
		for x := range canvas {
			img.SetNRGBA(x, y, c)
		}
	}
}

func write(path string, img *image.NRGBA) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s", path)
}
