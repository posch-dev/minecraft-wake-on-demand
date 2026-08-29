package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The server list shows two lines, so an example that only fills one teaches
// the wrong shape.
func TestExampleMOTDsRenderTwoLines(t *testing.T) {
	for _, file := range []string{
		"assets/examples/motd-sleeping.json",
		"assets/examples/motd-starting.json",
		"assets/examples/motd-live.json",
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		lines := motdLines(t, strings.TrimSpace(string(data)))
		if len(lines) != 2 {
			t.Errorf("%s renders %d line(s): %q", file, len(lines), lines)
		}
		for i, line := range lines {
			if strings.TrimSpace(line) == "" {
				t.Errorf("%s line %d is empty", file, i+1)
			}
		}
		// Obviously a placeholder, so nobody keeps it by accident.
		if !strings.Contains(strings.ToUpper(string(data)), "CHANGE THIS") {
			t.Errorf("%s does not read as something to replace", file)
		}
	}
}

// A colour Minecraft does not know makes the whole entry fall back to white.
func TestExampleMOTDsUseKnownColours(t *testing.T) {
	known := map[string]bool{}
	for _, name := range []string{
		"black", "dark_blue", "dark_green", "dark_aqua", "dark_red", "dark_purple",
		"gold", "gray", "dark_gray", "blue", "green", "aqua", "red",
		"light_purple", "yellow", "white",
	} {
		known[name] = true
	}

	for _, file := range []string{
		"assets/examples/motd-sleeping.json",
		"assets/examples/motd-starting.json",
		"assets/examples/motd-live.json",
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var component struct {
			Color string `json:"color"`
			Extra []struct {
				Color string `json:"color"`
			} `json:"extra"`
		}
		if err := json.Unmarshal(data, &component); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		colours := []string{component.Color}
		for _, e := range component.Extra {
			colours = append(colours, e.Color)
		}
		for _, colour := range colours {
			if colour != "" && !known[colour] {
				t.Errorf("%s uses %q, which Minecraft does not know", file, colour)
			}
		}
	}
}

// The picture has to be exactly what the watcher accepts, or the example is a
// trap rather than a starting point.
func TestExampleIconIsAValid64x64PNG(t *testing.T) {
	data, err := os.ReadFile("assets/examples/server-icon.png")
	if err != nil {
		t.Fatal(err)
	}

	width, height, err := pngDimensions(data)
	if err != nil {
		t.Fatalf("the example icon is not a PNG: %v", err)
	}
	if width != iconEdge || height != iconEdge {
		t.Errorf("the example icon is %dx%d, want %dx%d", width, height, iconEdge, iconEdge)
	}
	if len(data) > maxIconBytes {
		t.Errorf("the example icon is %d bytes, over the %d byte limit", len(data), maxIconBytes)
	}
}

func motdLines(t *testing.T, raw string) []string {
	t.Helper()
	var component struct {
		Text  string `json:"text"`
		Extra []struct {
			Text string `json:"text"`
		} `json:"extra"`
	}
	if err := json.Unmarshal([]byte(raw), &component); err != nil {
		t.Fatalf("not valid MOTD JSON: %v\n%s", err, raw)
	}
	text := component.Text
	for _, e := range component.Extra {
		text += e.Text
	}
	return strings.Split(text, "\n")
}
