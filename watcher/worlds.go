package main

import (
	"fmt"
	"strings"
)

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
