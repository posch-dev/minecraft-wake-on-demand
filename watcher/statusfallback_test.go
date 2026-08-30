package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Docker publishes the port the moment the container starts, so a bare dial
// says yes while Minecraft is still loading its world.
func TestAnOpenPortWithoutAnAnswerIsNotLive(t *testing.T) {
	silent := startFakeMCServer(t, false, nil)
	_, waker := wakerFor(silent)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if waker.MCPortReachable(ctx, true) {
		t.Error("a port that never answers must not count as live")
	}

	answering := startFakeMCServer(t, true, nil)
	_, ready := wakerFor(answering)
	if !ready.MCPortReachable(ctx, true) {
		t.Error("a server that answers a status request is live")
	}
}

// The server went away between the readiness check and the ping, which is what
// happens when the machine suspends. The entry must not look broken.
func TestStatusPingFallsBackWhenTheServerGoesQuiet(t *testing.T) {
	silent := startFakeMCServer(t, false, nil)
	cfg, waker := wakerFor(silent)
	waker.markReachable()

	handler := NewHandler(cfg, waker)
	client := serveOnce(t, handler)

	if _, err := client.Write(buildHandshake(770, "watcher.local", 25565, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(makeStatusRequest()); err != nil {
		t.Fatal(err)
	}

	client.SetReadDeadline(time.Now().Add(20 * time.Second))
	payload := decodeStatus(t, readFrame(t, client))
	if !strings.Contains(string(payload.Description), "asleep") {
		t.Errorf("description = %s, want the watcher's own MOTD", payload.Description)
	}
}

// A server sends fields the watcher does not model, and swapping the MOTD must
// not quietly drop the mod list or the player sample.
func TestRewritingAStatusResponseKeepsUnknownFields(t *testing.T) {
	original := `{"version":{"name":"26.2","protocol":776},` +
		`"players":{"max":10,"online":1,"sample":[{"name":"KampfKroete_","id":"x"}]},` +
		`"description":{"text":"theirs"},"enforcesSecureChat":false,` +
		`"modinfo":{"type":"FML","modList":[]}}`
	body := append(writeVarInt(packetIDStatus), writeString(original)...)

	framed, err := rewriteStatusResponse(body, `{"text":"ours"}`, "data:image/png;base64,AAAA")
	if err != nil {
		t.Fatal(err)
	}

	payload := decodeStatus(t, framed)
	if string(payload.Description) != `{"text":"ours"}` {
		t.Errorf("description = %s", payload.Description)
	}
	if payload.Favicon != "data:image/png;base64,AAAA" {
		t.Errorf("favicon = %q", payload.Favicon)
	}
	for _, want := range []string{"enforcesSecureChat", "modinfo", "sample", "KampfKroete_"} {
		if !strings.Contains(string(framed), want) {
			t.Errorf("the rewritten response lost %q", want)
		}
	}
}

// Nothing is swapped without an override, so the answer is passed on unchanged.
func TestAStatusResponseWithoutOverridesIsPassedOnUnchanged(t *testing.T) {
	original := `{"version":{"name":"26.2","protocol":776},"players":{"max":10,"online":0},` +
		`"description":{"text":"theirs"},"enforcesSecureChat":false}`
	body := append(writeVarInt(packetIDStatus), writeString(original)...)

	cfg := sleepingConfig()
	handler := NewHandler(cfg, NewWaker(cfg))

	framed, err := handler.dressStatusResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(framed) != string(framePacket(body)) {
		t.Error("the server's own answer was rewritten")
	}
}
