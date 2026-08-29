package logging

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Same shape as the Python logging output so existing log filters keep working:
// 2006-01-02 15:04:05 [INFO] message
type logger struct {
	mu  sync.Mutex
	out io.Writer
}

var std = &logger{out: os.Stdout}

// Commands print a laid out report, so their log lines go to stderr instead.
func SetOutput(w io.Writer) {
	std.mu.Lock()
	defer std.mu.Unlock()
	std.out = w
}

func (l *logger) write(level, format string, args ...any) {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	line := fmt.Sprintf("%s [%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), level, msg)
	l.mu.Lock()
	defer l.mu.Unlock()
	io.WriteString(l.out, line)
}

func Infof(format string, args ...any)  { std.write("INFO", format, args...) }
func Warnf(format string, args ...any)  { std.write("WARNING", format, args...) }
func Errorf(format string, args ...any) { std.write("ERROR", format, args...) }

// Client supplied text reaches the log, so newlines could forge log lines.
func Sanitize(value string, maxLen int) string {
	runes := []rune(value)
	if len(runes) > maxLen {
		runes = runes[:maxLen]
	}
	var b strings.Builder
	for _, r := range runes {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('?')
		}
	}
	return b.String()
}
