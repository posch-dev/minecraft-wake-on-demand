package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

type Prompter struct {
	in *bufio.Reader
	// Input runs out when the wizard is piped something or has no terminal.
	// Asking again would then spin, so every loop below gives up on it.
	Exhausted bool
}

func NewPrompter() *Prompter {
	return &Prompter{in: bufio.NewReader(os.Stdin)}
}

func NewPrompterFrom(r io.Reader) *Prompter {
	return &Prompter{in: bufio.NewReader(r)}
}

func (p *Prompter) Line(question, fallback string) string {
	if fallback != "" {
		fmt.Printf("%s %s: ", question, Hint("["+fallback+"]"))
	} else {
		fmt.Printf("%s: ", question)
	}
	beginInputColor()
	text, err := p.in.ReadString('\n')
	endInputColor()
	if err != nil {
		p.Exhausted = true
		if text == "" {
			return fallback
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fallback
	}
	return text
}

func (p *Prompter) Validated(question, fallback string, check func(string) error) string {
	for {
		answer := p.Line(question, fallback)
		if err := check(answer); err != nil {
			PrintError("  " + err.Error())
			if p.Exhausted {
				return answer
			}
			continue
		}
		return answer
	}
}

func (p *Prompter) YesNo(question string, fallback bool) bool {
	choices := "y/N"
	if fallback {
		choices = "Y/n"
	}
	for {
		fmt.Printf("%s %s: ", question, Hint("["+choices+"]"))
		beginInputColor()
		text, err := p.in.ReadString('\n')
		endInputColor()
		if err != nil {
			p.Exhausted = true
		}
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "":
			return fallback
		case "y", "yes", "j", "ja":
			return true
		case "n", "no", "nein":
			return false
		}
		if p.Exhausted {
			return fallback
		}
	}
}

func (p *Prompter) Secret(question string) string {
	fmt.Printf("%s: ", question)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		data, err := term.ReadPassword(fd)
		fmt.Println()
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	text, _ := p.in.ReadString('\n')
	return strings.TrimSpace(text)
}

func (p *Prompter) ValidatedPort(question string, fallback int) int {
	answer := p.Validated(question, strconv.Itoa(fallback), func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("that is not a port number between 1 and 65535")
		}
		return nil
	})
	n, _ := strconv.Atoi(answer)
	return n
}

func (p *Prompter) ValidatedInt(question string, fallback, min int) int {
	answer := p.Validated(question, strconv.Itoa(fallback), func(v string) error {
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("that is not a number")
		}
		if parsed < min {
			return fmt.Errorf("it has to be at least %d", min)
		}
		return nil
	})
	parsed, _ := strconv.Atoi(strings.TrimSpace(answer))
	return parsed
}
