package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/mcproto"
)

// A client that sends the login packet on its own must not be cut off with a
// reset, which it reports as a socket error instead of showing the message.
func TestColdStartLoginIsClosedCleanly(t *testing.T) {
	cfg := sleepingConfig()
	handler := NewHandler(cfg, NewWaker(cfg))
	client := serveOnce(t, handler)

	if _, err := client.Write(mcproto.MakeHandshake(770, "watcher.local", 25565, 2)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	if _, err := client.Write(mcproto.MakeLoginStart("KampfKroete_", bytes.Repeat([]byte{0x07}, 16))); err != nil {
		t.Fatal(err)
	}

	client.SetReadDeadline(time.Now().Add(30 * time.Second))
	frame := readFrame(t, client)
	if !strings.Contains(string(frame), "waking up") {
		t.Errorf("the wait message was not sent, got %q", frame)
	}

	// Whatever follows has to be an orderly close.
	buf := make([]byte, 64)
	if _, err := client.Read(buf); !errors.Is(err, io.EOF) {
		t.Errorf("connection ended with %v, want a clean EOF", err)
	}
}
