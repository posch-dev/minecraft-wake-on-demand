package main

import (
	"strings"
	"testing"
	"time"
)

// The wizard once spun forever when its answers ran out: a validated question
// re-asked on every EOF and filled a 197 MB log in seconds.
func TestQuestionsGiveUpWhenTheAnswersRunOut(t *testing.T) {
	done := make(chan string, 1)
	go func() {
		p := newPrompterFrom(strings.NewReader("192.168.0.5\n"))
		p.validated("First", "", validateHostOrIP)
		done <- p.validated("Second", "", validateHostOrIP)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the wizard kept asking after its input ended")
	}
}

func TestYesNoGivesUpWhenTheAnswersRunOut(t *testing.T) {
	done := make(chan bool, 1)
	go func() {
		p := newPrompterFrom(strings.NewReader("maybe\n"))
		done <- p.yesNo("Well?", true)
	}()

	select {
	case answer := <-done:
		if !answer {
			t.Error("it should fall back to the default, not to no")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("yesNo kept asking after its input ended")
	}
}
