package ui

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The wizard once spun forever when its answers ran out: a validated question
// re-asked on every EOF and filled a 197 MB log in seconds.
func TestQuestionsGiveUpWhenTheAnswersRunOut(t *testing.T) {
	done := make(chan string, 1)
	go func() {
		p := NewPrompterFrom(strings.NewReader("192.168.0.5\n"))
		p.Validated("First", "", mustNotBeEmpty)
		done <- p.Validated("Second", "", mustNotBeEmpty)
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
		p := NewPrompterFrom(strings.NewReader("maybe\n"))
		done <- p.YesNo("Well?", true)
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

func mustNotBeEmpty(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("this cannot be empty")
	}
	return nil
}
