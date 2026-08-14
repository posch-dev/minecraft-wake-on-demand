package main

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
type Logger struct {
	mu  sync.Mutex
	out io.Writer
}

var log = &Logger{out: os.Stdout}

func (l *Logger) write(level, format string, args ...any) {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	line := fmt.Sprintf("%s [%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), level, msg)
	l.mu.Lock()
	defer l.mu.Unlock()
	io.WriteString(l.out, line)
}

func (l *Logger) Infof(format string, args ...any)  { l.write("INFO", format, args...) }
func (l *Logger) Warnf(format string, args ...any)  { l.write("WARNING", format, args...) }
func (l *Logger) Errorf(format string, args ...any) { l.write("ERROR", format, args...) }

// Client supplied text reaches the log, so newlines could forge log lines.
func sanitizeForLog(value string, maxLen int) string {
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
