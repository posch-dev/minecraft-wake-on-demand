package main

import (
	"strings"
	"testing"
)

func withColor(t *testing.T, on bool) {
	t.Helper()
	previous := colorEnabled
	colorEnabled = on
	t.Cleanup(func() { colorEnabled = previous })
}

// Control characters in a pipe or the journal would make every log line and
// every grep worse, so nothing is painted unless a person is looking.
func TestNothingIsPaintedWhenColorIsOff(t *testing.T) {
	withColor(t, false)

	for _, got := range []string{hint("a"), warn("b"), bad("c")} {
		if strings.Contains(got, "\x1b") {
			t.Errorf("%q carries an escape with colour off", got)
		}
	}
}

func TestPaintedTextAlwaysResets(t *testing.T) {
	withColor(t, true)

	for name, got := range map[string]string{"hint": hint("a"), "warn": warn("b"), "bad": bad("c")} {
		if !strings.HasSuffix(got, ansiReset) {
			t.Errorf("%s produced %q, which would bleed into the next line", name, got)
		}
		if stripANSI(got) != strings.TrimSuffix(strings.TrimPrefix(stripANSI(got), ""), "") {
			t.Errorf("%s changed the text itself", name)
		}
	}
}

func TestPaintingLeavesTheTextAlone(t *testing.T) {
	withColor(t, true)

	if got := stripANSI(hint("the message")); got != "the message" {
		t.Errorf("stripped = %q, want the original text", got)
	}
	if got := hint(""); got != "" {
		t.Errorf("empty text = %q, want nothing at all", got)
	}
}

func TestNoColorEnvIsRespected(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	if detectColorSupport() {
		t.Error("NO_COLOR must switch colour off")
	}
}

func TestDumbTerminalGetsNoColor(t *testing.T) {
	t.Setenv("TERM", "dumb")

	if detectColorSupport() {
		t.Error("a dumb terminal cannot show colour")
	}
}

func TestStripANSIHandlesSeveralCodes(t *testing.T) {
	painted := ansiGreen + "one" + ansiReset + " " + ansiRed + "two" + ansiReset

	if got := stripANSI(painted); got != "one two" {
		t.Errorf("stripped = %q", got)
	}
	if got := stripANSI("nothing to strip"); got != "nothing to strip" {
		t.Errorf("stripped = %q", got)
	}
}
