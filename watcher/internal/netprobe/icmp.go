package netprobe

import (
	"bytes"
	"context"
	"crypto/rand"
	"net"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/logging"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

type pingMode int

const (
	pingUDP pingMode = iota
	pingRaw
	pingExec
)

func (m pingMode) String() string {
	switch m {
	case pingUDP:
		return "unprivileged ICMP"
	case pingRaw:
		return "raw ICMP"
	default:
		return "ping command"
	}
}

type Pinger struct {
	once sync.Once
	mode pingMode
	seq  atomic.Int32
}

// Unprivileged ICMP works on Linux when net.ipv4.ping_group_range allows it,
// raw ICMP needs CAP_NET_RAW, and the ping binary needs neither.
func (p *Pinger) detect() {
	p.once.Do(func() {
		if conn, err := icmp.ListenPacket("udp4", "0.0.0.0"); err == nil {
			conn.Close()
			p.mode = pingUDP
		} else if conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0"); err == nil {
			conn.Close()
			p.mode = pingRaw
		} else {
			p.mode = pingExec
		}
		logging.Infof("Host reachability checks use %s", p.mode)

		// The container image ships no ping binary, so falling back to it
		// there means the wake sequence would never see the PC come up.
		if p.mode == pingExec {
			if _, err := exec.LookPath("ping"); err != nil {
				logging.Errorf("No ICMP socket and no ping command available, " +
					"the watcher cannot tell when the server PC has booted. " +
					"Grant CAP_NET_RAW to the process or install iputils-ping.")
			}
		}
	})
}

func (p *Pinger) Mode() pingMode {
	p.detect()
	return p.mode
}

func (p *Pinger) Ping(ctx context.Context, host string, timeout time.Duration) bool {
	p.detect()

	ip := net.ParseIP(host)
	if p.mode == pingExec || ip == nil || ip.To4() == nil {
		return execPing(ctx, host, timeout)
	}
	ok, err := p.icmpPing(ctx, ip.To4(), timeout)
	if err != nil {
		// A working socket can still be refused later, for example when a
		// container drops CAP_NET_RAW at runtime.
		return execPing(ctx, host, timeout)
	}
	return ok
}

func (p *Pinger) icmpPing(ctx context.Context, ip net.IP, timeout time.Duration) (bool, error) {
	network := "udp4"
	var target net.Addr = &net.UDPAddr{IP: ip}
	if p.mode == pingRaw {
		network = "ip4:icmp"
		target = &net.IPAddr{IP: ip}
	}

	conn, err := icmp.ListenPacket(network, "0.0.0.0")
	if err != nil {
		return false, err
	}
	defer conn.Close()

	// The kernel rewrites the ID on unprivileged sockets, so replies are
	// matched on the payload instead.
	payload := make([]byte, 16)
	if _, err := rand.Read(payload); err != nil {
		return false, err
	}
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   os.Getpid() & 0xFFFF,
			Seq:  int(p.seq.Add(1) & 0xFFFF),
			Data: payload,
		},
	}
	encoded, err := msg.Marshal(nil)
	if err != nil {
		return false, err
	}

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return false, err
	}
	if _, err := conn.WriteTo(encoded, target); err != nil {
		return false, err
	}

	buf := make([]byte, 1500)
	for {
		if ctx.Err() != nil {
			return false, nil
		}
		n, peer, err := conn.ReadFrom(buf)
		if err != nil {
			// Deadline reached without a matching reply.
			return false, nil
		}
		if !sameIP(peer, ip) {
			continue
		}
		parsed, err := icmp.ParseMessage(ipv4.ICMPTypeEchoReply.Protocol(), buf[:n])
		if err != nil {
			continue
		}
		echo, ok := parsed.Body.(*icmp.Echo)
		if !ok || parsed.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		if bytes.Equal(echo.Data, payload) {
			return true, nil
		}
	}
}

func sameIP(addr net.Addr, want net.IP) bool {
	switch a := addr.(type) {
	case *net.UDPAddr:
		return a.IP.Equal(want)
	case *net.IPAddr:
		return a.IP.Equal(want)
	}
	return false
}

func execPing(ctx context.Context, host string, timeout time.Duration) bool {
	var args []string
	if runtime.GOOS == "windows" {
		args = []string{"-n", "1", "-w", "1000", host}
	} else {
		args = []string{"-c", "1", "-W", "1", host}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ping", args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}
