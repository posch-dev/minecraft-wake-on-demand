package testsupport

import (
	"bytes"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/mcproto"
)

// Stands in for the real Minecraft server so the paths that only run when the
// server is up can be exercised.
type FakeMCServer struct {
	Port     int
	mu       sync.Mutex
	received []byte
}

// answerStatus false accepts the connection and stays silent, which is what a
// container looks like while it is still starting.
func StartFakeMCServer(t *testing.T, answerStatus bool, echo []byte) *FakeMCServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	_, portString, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portString)
	server := &FakeMCServer{Port: port}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				buf := make([]byte, 4096)
				n, err := conn.Read(buf)
				if err != nil {
					return
				}
				// The watcher probes readiness with protocol version -1, which
				// no client sends. A real server answers it, so this one does.
				hs, hsErr := mcproto.ParseHandshake(buf[:n])
				if echo != nil && hsErr == nil && hs.ProtocolVersion == -1 {
					probe, _ := mcproto.MakeStatusResponse(config.DefaultMOTDSleeping, 42, 0, "", "1.21.4", 769)
					conn.Write(probe)
					conn.Read(buf)
					return
				}

				server.mu.Lock()
				server.received = append(server.received, buf[:n]...)
				server.mu.Unlock()

				if answerStatus {
					motd := "{\"text\":\"the real server\",\"color\":\"green\"}"
					response, _ := mcproto.MakeStatusResponse(motd, 42, 7, "", "1.21.4", 769)
					conn.Write(response)
				}
				if echo != nil {
					conn.Write(echo)
				}
				if answerStatus || echo != nil {
					// Closing straight after the write races the proxy copying
					// it on, so the peer is left to hang up first.
					conn.Read(buf)
				}
				if !answerStatus && echo == nil {
					// Hold the connection open without ever answering.
					time.Sleep(6 * time.Second)
				}
			}()
		}
	}()
	return server
}

// Same fake, but the status response carries a favicon.
func StartFakeMCServerWithIcon(t *testing.T, favicon string) *FakeMCServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	_, portString, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portString)
	server := &FakeMCServer{Port: port}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				buf := make([]byte, 4096)
				if _, err := conn.Read(buf); err != nil {
					return
				}
				response, _ := mcproto.MakeStatusResponse(config.DefaultMOTDSleeping, 20, 0, favicon, "1.21.4", 769)
				conn.Write(response)
				conn.Read(buf)
			}()
		}
	}()
	return server
}

// learnServerInfo works on the packet body, the test helpers build whole frames.
func StatusBody(t *testing.T, frame []byte) []byte {
	t.Helper()
	body, err := mcproto.ReadFramedPacket(bytes.NewReader(frame), mcproto.MaxStatusResponseBytes)
	if err != nil {
		t.Fatalf("readFramedPacket: %v", err)
	}
	return body
}

func ReadFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (s *FakeMCServer) Got() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte{}, s.received...)
}
