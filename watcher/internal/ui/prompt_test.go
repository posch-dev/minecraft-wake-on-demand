package ui

import (
	"errors"
	"net"
	"strings"
	"testing"
)

func TestPrompterUsesFallbackOnEmptyInput(t *testing.T) {
	p := NewPrompterFrom(strings.NewReader("\n\n\n"))
	if got := p.Line("q", "default"); got != "default" {
		t.Errorf("got %q, want the fallback", got)
	}
	if !p.YesNo("q", true) {
		t.Error("empty answer should take the true fallback")
	}
	if p.YesNo("q", false) {
		t.Error("empty answer should take the false fallback")
	}
}

func TestPrompterYesNoAcceptsGermanAndEnglish(t *testing.T) {
	p := NewPrompterFrom(strings.NewReader("yes\nja\nn\nnein\n"))
	for i, want := range []bool{true, true, false, false} {
		if got := p.YesNo("q", !want); got != want {
			t.Errorf("answer %d = %v, want %v", i, got, want)
		}
	}
}

// The wizard must not give up on a typo, it asks again.
func TestPrompterRetriesUntilValid(t *testing.T) {
	p := NewPrompterFrom(strings.NewReader("nonsense\n999.999.1.1\n192.168.1.50\n"))
	got := p.Validated("ip", "", func(v string) error {
		if net.ParseIP(v) == nil {
			return errors.New("that is not an IP address")
		}
		return nil
	})
	if got != "192.168.1.50" {
		t.Errorf("got %q, want the third answer", got)
	}
}
