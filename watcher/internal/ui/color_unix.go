//go:build !windows

package ui

// Every terminal that matters on Unix already understands ANSI.
func enableVirtualTerminal() bool { return true }
