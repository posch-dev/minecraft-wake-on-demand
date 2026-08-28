//go:build !windows

package main

// Every terminal that matters on Unix already understands ANSI.
func enableVirtualTerminal() bool { return true }
