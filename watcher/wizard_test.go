package main

import (
	"testing"

	"slices"
)

// Numbers, because a wrong word means retyping and these people are reading a
// terminal for the first time.
func TestServerTypePickedByNumber(t *testing.T) {
	for answer, want := range map[string]string{
		"1": "VANILLA",
		"2": "PAPER",
		"3": "FABRIC",
		"4": "FORGE",
	} {
		got, ok := serverTypeByChoice(answer)
		if !ok || got != want {
			t.Errorf("choice %q = %q, %v, want %q", answer, got, ok, want)
		}
	}
}

// Somebody who knows what PURPUR is should not have to find it in the list.
func TestServerTypeAlsoTakesTheName(t *testing.T) {
	for _, answer := range []string{"purpur", "PURPUR", " neoforge "} {
		if _, ok := serverTypeByChoice(answer); !ok {
			t.Errorf("%q should be accepted", answer)
		}
	}
}

func TestServerTypeRefusesNonsense(t *testing.T) {
	for _, answer := range []string{"0", "9", "", "bedrock"} {
		if got, ok := serverTypeByChoice(answer); ok {
			t.Errorf("%q was accepted as %q", answer, got)
		}
	}
}

func TestSleepActionPickedByNumberOrWord(t *testing.T) {
	for answer, want := range map[string]string{
		"1":         "suspend",
		"2":         "hibernate",
		"3":         "shutdown",
		"hibernate": "hibernate",
	} {
		got, ok := sleepActionByChoice(answer)
		if !ok || got != want {
			t.Errorf("choice %q = %q, %v, want %q", answer, got, ok, want)
		}
	}
	if _, ok := sleepActionByChoice("4"); ok {
		t.Error("there is no fourth option")
	}
}

// A quarter of the machine, with room at both ends: under 2G is not worth
// running, and past 8G the garbage collector costs more than the heap gains.
func TestSuggestedMemoryStaysInAUsefulRange(t *testing.T) {
	cases := map[int]string{
		0:   "4G",
		2:   "2G",
		4:   "2G",
		8:   "2G",
		16:  "4G",
		32:  "8G",
		64:  "8G",
		128: "8G",
	}
	for total, want := range cases {
		if got := suggestedMemory(total); got != want {
			t.Errorf("%d GB of RAM suggests %q, want %q", total, got, want)
		}
	}
}

func TestMemoryIsReadFromWhatTheServerReports(t *testing.T) {
	if got := kilobytesToGB("16384000"); got != 15 {
		t.Errorf("16384000 kB = %d GB, want 15", got)
	}
	if got := bytesToGB("17179869184"); got != 16 {
		t.Errorf("17179869184 bytes = %d GB, want 16", got)
	}
	for _, rubbish := range []string{"", "lots", "-1x"} {
		if got := kilobytesToGB(rubbish); got != 0 {
			t.Errorf("kilobytesToGB(%q) = %d, want 0", rubbish, got)
		}
	}
}

// Every choice offered has to be a type the compose file can actually use.
func TestOfferedServerTypesAreAllKnown(t *testing.T) {
	for _, choice := range serverTypeChoices {
		if !slices.Contains(serverTypes, choice.name) {
			t.Errorf("%q is offered but not a known server type", choice.name)
		}
		if choice.what == "" {
			t.Errorf("%q is offered without saying what it is", choice.name)
		}
	}
}

func TestOfferedSleepActionsAreAllInstallable(t *testing.T) {
	for _, choice := range sleepChoices {
		if !slices.Contains(installableSleepActions, choice.action) {
			t.Errorf("%q is offered but setup-ssh cannot install it", choice.action)
		}
	}
}
