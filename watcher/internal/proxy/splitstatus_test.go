package proxy

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/mcproto"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/testsupport"
)

// Answers only once it has both the handshake and the request, which is what a
// real Minecraft server does.
func startPatientMCServer(t *testing.T, motd string) *testsupport.FakeMCServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	_, portString, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portString)
	server := &testsupport.FakeMCServer{Port: port}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				seen := []byte{}
				buf := make([]byte, 4096)
				for {
					n, err := conn.Read(buf)
					if n > 0 {
						seen = append(seen, buf[:n]...)
					}
					if err != nil {
						return
					}
					hs, hsErr := mcproto.ParseHandshake(seen)
					if hsErr != nil || len(trailing(seen, hs.End)) == 0 {
						continue
					}
					response, _ := mcproto.MakeStatusResponse(motd, 42, 7, "", "26.2", 776)
					conn.Write(response)
					conn.Read(buf)
					return
				}
			}()
		}
	}()
	return server
}

// A client that sends the request in its own packet was answered with the
// sleeping MOTD, because the request never reached the server and the read
// from it timed out.
func TestStatusPingIsProxiedWhenTheRequestArrivesSeparately(t *testing.T) {
	server := startPatientMCServer(t, `{"text":"the real server"}`)
	cfg, waker := wakerFor(server)
	waker.MarkReachable()

	handler := NewHandler(cfg, waker)
	client := serveOnce(t, handler)

	if _, err := client.Write(mcproto.MakeHandshake(770, "watcher.local", 25565, 1)); err != nil {
		t.Fatal(err)
	}
	// The gap is what made the two arrive as separate reads.
	time.Sleep(150 * time.Millisecond)
	if _, err := client.Write(mcproto.MakeStatusRequest()); err != nil {
		t.Fatal(err)
	}

	client.SetReadDeadline(time.Now().Add(20 * time.Second))
	payload := decodeStatus(t, readFrame(t, client))
	var description struct {
		Text string `json:"text"`
	}
	json.Unmarshal(payload.Description, &description)
	if description.Text != "the real server" {
		t.Errorf("description = %s, want the running server's own MOTD", payload.Description)
	}
	if strings.Contains(string(payload.Description), "asleep") {
		t.Error("a running server was reported as asleep")
	}
}
