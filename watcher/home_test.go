package main

import (
	"strings"
	"testing"
)

// Every number the menu prints has to lead somewhere, and every branch has to
// be printed, or somebody types a number that silently does nothing.
func TestHomeMenuAndItsBranchesAgree(t *testing.T) {
	source := readRepoFile(t, "watcher", "cmd_home.go")

	printed := []string{}
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, `fmt.Println("  `) {
			continue
		}
		for _, choice := range []string{"1)", "2)", "3)", "4)", "5)", "6)"} {
			if strings.Contains(trimmed, `"  `+choice) {
				printed = append(printed, strings.TrimSuffix(choice, ")"))
			}
		}
	}
	if len(printed) == 0 {
		t.Fatal("no numbered menu entries found")
	}

	for _, number := range printed {
		if !strings.Contains(source, `case "`+number+`":`) {
			t.Errorf("the menu offers %s but nothing handles it", number)
		}
	}
	// One past the last entry must not be handled either.
	next := string(rune('0' + len(printed) + 1))
	if strings.Contains(source, `case "`+next+`":`) {
		t.Errorf("%s is handled but never printed", next)
	}
}

func TestHomeMenuAlwaysOffersAWayOut(t *testing.T) {
	source := readRepoFile(t, "watcher", "cmd_home.go")

	if !strings.Contains(source, `case "q", "quit", "exit", "":`) {
		t.Error("pressing enter or typing q has to leave the menu")
	}
	if !strings.Contains(source, `fmt.Println("  q) Quit")`) {
		t.Error("the way out has to be printed too")
	}
}
