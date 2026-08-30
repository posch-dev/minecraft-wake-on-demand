package main

import (
	"fmt"
	"strings"
)

// One Minecraft world and the container that runs it. Only ever one at a time,
// the watcher points at whichever is active.
type World struct {
	Name      string `yaml:"name"`
	Container string `yaml:"container"`
	Port      int    `yaml:"port"`
	Version   string `yaml:"version"`
	Type      string `yaml:"type"`
	Dir       string `yaml:"dir"`
}

type WorldsConfig struct {
	Active string  `yaml:"active"`
	List   []World `yaml:"list"`
}

// A config written before worlds existed describes exactly one, so it is read
// as that rather than as none.
func (c *Config) worldsOrSingle() []World {
	if len(c.Worlds.List) > 0 {
		return c.Worlds.List
	}
	if c.Server.ContainerName == "" {
		return nil
	}
	return []World{{
		Name:      c.Server.ContainerName,
		Container: c.Server.ContainerName,
		Port:      c.Server.MCPort,
		Dir:       c.Server.ComposeDir,
	}}
}

func (c *Config) ActiveWorld() (World, bool) {
	worlds := c.worldsOrSingle()
	if len(worlds) == 0 {
		return World{}, false
	}
	for _, world := range worlds {
		if world.Name == c.Worlds.Active {
			return world, true
		}
	}
	return worlds[0], true
}

// Empty when there is only the implied single world, so nothing looks for a
// per world folder that was never created.
func (c *Config) ActiveWorldName() string {
	if len(c.Worlds.List) == 0 {
		return ""
	}
	world, ok := c.ActiveWorld()
	if !ok {
		return ""
	}
	return world.Name
}

// The watcher only ever talks to the active world, so these follow it.
func (c *Config) applyActiveWorld() {
	world, ok := c.ActiveWorld()
	if !ok || len(c.Worlds.List) == 0 {
		return
	}
	c.Server.ContainerName = world.Container
	c.Server.MCPort = world.Port
	c.Server.ComposeDir = world.Dir
}

func (c *Config) findWorld(name string) (World, bool) {
	for _, world := range c.worldsOrSingle() {
		if strings.EqualFold(world.Name, name) {
			return world, true
		}
	}
	return World{}, false
}

// Downward from the Minecraft port, because transfer mode publishes the one
// directly above it and a second world there would collide with it.
func (c *Config) nextFreeWorldPort() int {
	taken := map[int]bool{}
	for _, world := range c.worldsOrSingle() {
		taken[world.Port] = true
	}
	if c.Transfer.Enabled {
		taken[c.Transfer.Port] = true
	}

	for port := defaultMinecraftPort; port > 1024; port-- {
		if !taken[port] {
			return port
		}
	}
	return defaultMinecraftPort
}

const defaultMinecraftPort = 25565

// Vanilla, then plugin servers, then mods. A world can move up but not back
// down, mods write blocks the plainer servers cannot read.
func serverTypeTier(name string) int {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "FABRIC", "FORGE", "NEOFORGE", "QUILT":
		return 2
	case "PAPER", "PURPUR", "SPIGOT", "BUKKIT":
		return 1
	}
	return 0
}

// Empty when the move is fine, otherwise why it is not.
func worldMoveProblem(fromType, toType, fromVersion, toVersion string) string {
	if serverTypeTier(toType) < serverTypeTier(fromType) {
		return fmt.Sprintf("Going from %s to %s cannot take the world along. Mods write "+
			"their own blocks into it, and the plainer server does not know them.",
			strings.ToUpper(fromType), strings.ToUpper(toType))
	}
	if compareVersions(toVersion, fromVersion) < 0 {
		return fmt.Sprintf("%s is older than %s. Minecraft upgrades a world's format the "+
			"first time it opens it, and the older server will refuse to load it afterwards.",
			toVersion, fromVersion)
	}
	return ""
}

// LATEST counts as the newest thing there is, which is what it behaves like.
func compareVersions(left, right string) int {
	if strings.EqualFold(left, right) {
		return 0
	}
	if strings.EqualFold(left, "LATEST") {
		return 1
	}
	if strings.EqualFold(right, "LATEST") {
		return -1
	}
	a, b := versionParts(left), versionParts(right)
	for i := range 3 {
		if a[i] != b[i] {
			if a[i] > b[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}
