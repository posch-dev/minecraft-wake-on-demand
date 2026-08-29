package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/sshx"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/ui"
	"golang.org/x/crypto/ssh"
)

// One password login, used to look around and install things.
// The password never reaches a command line, a log line or an error.
type ServerSession struct {
	client   *ssh.Client
	password string
	platform ServerPlatform
	detach   func()
}

// What the server runs, decided once so every later command can be phrased for
// it instead of guessing.
type ServerPlatform struct {
	Windows bool
	// Absolute path to systemctl on Linux, empty when there is none.
	SystemctlPath string
	HasDocker     bool
	// The account can run sudo without being asked for a password again.
	PasswordlessSudo bool
}

func (p ServerPlatform) Name() string {
	if p.Windows {
		return "Windows"
	}
	return "Linux"
}

// Both auth methods are offered because plenty of sshd configurations expose
// only keyboard-interactive and would otherwise refuse a correct password.
func passwordAuth(password string) []ssh.AuthMethod {
	return []ssh.AuthMethod{
		ssh.Password(password),
		ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				answers[i] = password
			}
			return answers, nil
		}),
	}
}

func DialServerSession(ctx context.Context, runner *sshx.SSHRunner, password string, p *ui.Prompter) (*ServerSession, error) {
	callback, err := interactiveHostKeyCallback(runner, p)
	if err != nil {
		return nil, err
	}
	clientCfg := &ssh.ClientConfig{
		User:            runner.Config().Server.SSHUser,
		Auth:            passwordAuth(password),
		HostKeyCallback: callback,
		Timeout:         15 * time.Second,
	}

	dialer := net.Dialer{Timeout: clientCfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", runner.Address())
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", runner.Address(), err)
	}
	conn.SetDeadline(time.Now().Add(45 * time.Second))

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, runner.Address(), clientCfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("login as %s failed: %w", runner.Config().Server.SSHUser, err)
	}
	conn.SetDeadline(time.Time{})

	return &ServerSession{client: ssh.NewClient(sshConn, chans, reqs), password: password}, nil
}

func (s *ServerSession) Close() {
	s.password = ""
	if s.client != nil {
		s.client.Close()
	}
	if s.detach != nil {
		s.detach()
	}
}

func (s *ServerSession) Platform() ServerPlatform {
	return s.platform
}

// Combined output, trimmed. An error means the remote command exited non-zero.
func (s *ServerSession) Run(command string) (string, error) {
	return s.runWithStdin(command, "")
}

// Runs the command as root. sudo reads the password from stdin so it never
// appears in the server's process list.
func (s *ServerSession) RunSudo(command string) (string, error) {
	if s.platform.Windows {
		return "", fmt.Errorf("there is no sudo on Windows, the SSH account has to be an administrator")
	}
	if s.platform.PasswordlessSudo {
		return s.runWithStdin("sudo -n sh -c "+shellQuote(command), "")
	}
	return s.runWithStdin("sudo -S -p '' sh -c "+shellQuote(command), s.password+"\n")
}

func (s *ServerSession) runWithStdin(command, stdin string) (string, error) {
	session, err := s.client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	if stdin != "" {
		session.Stdin = strings.NewReader(stdin)
	}
	out, err := session.CombinedOutput(command)
	return strings.TrimSpace(string(out)), err
}

// Runs once, right after the login, so every later command knows which shell
// and which paths it is talking to.
func (s *ServerSession) Detect() ServerPlatform {
	platform := ServerPlatform{}

	// Windows OpenSSH defaults to cmd.exe, where uname does not exist. Anything
	// that is not a recognisable uname answer is treated as Windows.
	if out, err := s.Run("uname -s"); err != nil || !looksLikeUnix(out) {
		platform.Windows = true
	}

	if platform.Windows {
		_, err := s.Run("docker version --format {{.Server.Os}}")
		platform.HasDocker = err == nil
		s.platform = platform
		return platform
	}

	if out, err := s.Run("command -v systemctl"); err == nil {
		platform.SystemctlPath = firstLine(out)
	}
	_, err := s.Run("command -v docker")
	platform.HasDocker = err == nil
	_, err = s.Run("sudo -n true")
	platform.PasswordlessSudo = err == nil

	s.platform = platform
	return platform
}

func looksLikeUnix(unameOutput string) bool {
	switch strings.ToLower(firstLine(unameOutput)) {
	case "linux", "darwin", "freebsd", "openbsd", "netbsd":
		return true
	}
	return false
}

func firstLine(value string) string {
	if idx := strings.IndexAny(value, "\r\n"); idx >= 0 {
		return strings.TrimSpace(value[:idx])
	}
	return strings.TrimSpace(value)
}
