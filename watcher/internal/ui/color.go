package ui

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

const (
	ansiReset = "\x1b[0m"
	ansiGreen = "\x1b[32m"
	ansiGrey  = "\x1b[90m"
	ansiAmber = "\x1b[33m"
	ansiRed   = "\x1b[31m"
)

// Off unless a person is looking at it, so control characters never reach the
// journal or a pipe. NO_COLOR is the convention for turning it off by hand.
var colorEnabled = detectColorSupport()

func detectColorSupport() bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return false
	}
	return enableVirtualTerminal()
}

func paint(code, text string) string {
	if !colorEnabled || text == "" {
		return text
	}
	return code + text + ansiReset
}

// Side information somebody can skip and still get through the question.
func Hint(text string) string { return paint(ansiGrey, text) }

func Warn(text string) string { return paint(ansiAmber, text) }

func bad(text string) string { return paint(ansiRed, text) }

// Indented under the question it belongs to, one line at a time.
func PrintHint(lines ...string) {
	for _, line := range lines {
		fmt.Println(Hint("  " + line))
	}
}

func PrintWarning(lines ...string) {
	for _, line := range lines {
		fmt.Println(Warn(line))
	}
}

func PrintError(lines ...string) {
	for _, line := range lines {
		fmt.Println(bad(line))
	}
}

// The terminal echoes what is typed, so the colour is set before the read and
// taken back after it. Without the reset a crash would leave the shell green.
func beginInputColor() {
	if colorEnabled {
		fmt.Print(ansiGreen)
	}
}

func endInputColor() {
	if colorEnabled {
		fmt.Print(ansiReset)
	}
}

func resetColorOnExit() {
	if colorEnabled {
		fmt.Print(ansiReset)
	}
}

// Nothing to strip when colour is off, which keeps the tests readable.
func stripANSI(text string) string {
	for {
		start := strings.Index(text, "\x1b[")
		if start < 0 {
			return text
		}
		end := strings.IndexByte(text[start:], 'm')
		if end < 0 {
			return text
		}
		text = text[:start] + text[start+end+1:]
	}
}
