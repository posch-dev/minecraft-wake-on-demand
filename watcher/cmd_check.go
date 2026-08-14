package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type checker struct {
	failures int
	warnings int
}

func (c *checker) section(title string) {
	fmt.Printf("\n%s\n", title)
}

func (c *checker) ok(format string, args ...any) {
	fmt.Printf("  ok    %s\n", fmt.Sprintf(format, args...))
}

func (c *checker) fail(format string, args ...any) {
	c.failures++
	fmt.Printf("  FAIL  %s\n", fmt.Sprintf(format, args...))
}

func (c *checker) warn(format string, args ...any) {
	c.warnings++
	fmt.Printf("  warn  %s\n", fmt.Sprintf(format, args...))
}

func (c *checker) info(format string, args ...any) {
	fmt.Printf("  ..    %s\n", fmt.Sprintf(format, args...))
}

func (c *checker) hint(format string, args ...any) {
	fmt.Printf("        %s\n", fmt.Sprintf(format, args...))
}

// Walks the setup in the order things depend on each other and stops digging
// once a step fails, so the first FAIL is the one worth fixing.
func runCheck() int {
	cfg, err := LoadConfig()

	c := &checker{}
	fmt.Println("Checking the Minecraft Wake-on-Demand setup")

	c.section("Config")
	if err != nil {
		c.fail("%v", err)
		fmt.Printf("\n%d problem(s) found.\n", c.failures)
		return 1
	}
	c.ok("read from %s", cfg.Path)
	c.ok("server %s (%s), Minecraft port %d, container '%s'",
		cfg.Server.IP, cfg.Server.MAC, cfg.Server.MCPort, cfg.Server.ContainerName)
	if cfg.Transfer.Enabled {
		c.ok("transfer mode to %s:%d", cfg.Transfer.Host, cfg.Transfer.Port)
	} else {
		c.ok("proxy mode, all traffic goes through the watcher")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	checkAssets(c, cfg)
	checkListenPort(c, cfg)
	sshOK := checkSSHKey(c, cfg)
	hostUp := checkServer(c, cfg, ctx)
	if hostUp && sshOK {
		checkSSHLogin(c, cfg, ctx)
	}
	checkDuckDNS(c, cfg, ctx)

	fmt.Println()
	switch {
	case c.failures > 0:
		fmt.Printf("%d problem(s) found, %d warning(s).\n", c.failures, c.warnings)
		return 1
	case c.warnings > 0:
		fmt.Printf("No problems found, %d warning(s).\n", c.warnings)
	default:
		fmt.Println("Everything looks good.")
	}
	return 0
}

func checkAssets(c *checker, cfg *Config) {
	c.section("MOTD and icon")
	dir := cfg.AssetsDir()
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		c.warn("no assets directory at %s", dir)
		c.hint("the MOTD from config.yml is used instead, which is fine")
		return
	}
	c.ok("assets directory %s", dir)

	assets := NewAssets(cfg)
	if assets.MOTDSleeping() == cfg.MOTD.Sleeping {
		c.info("motd-sleeping.json not used, falling back to config.yml")
	} else {
		c.ok("motd-sleeping.json loaded")
	}
	if icon := assets.Icon(); icon == "" {
		c.info("no server-icon.png, the server list shows the default icon")
	} else {
		c.ok("server-icon.png loaded, %d bytes encoded", len(icon))
	}
}

func checkListenPort(c *checker, cfg *Config) {
	c.section("Listen port")
	address := net.JoinHostPort(cfg.Watcher.ListenAddress, strconv.Itoa(cfg.Watcher.ListenPort))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		// An already running watcher is the usual reason and not a problem.
		c.warn("cannot bind %s: %v", address, err)
		c.hint("this is expected when the watcher is already running")
		return
	}
	listener.Close()
	c.ok("%s is free", address)
}

func checkSSHKey(c *checker, cfg *Config) bool {
	c.section("SSH key")
	path := cfg.ResolvedSSHKeyPath()
	signer, err := loadPrivateKey(path)
	if err != nil {
		c.fail("%v", err)
		c.hint("run: mc-wol-proxy setup-ssh")
		return false
	}
	c.ok("%s, type %s", path, signer.PublicKey().Type())
	c.ok("fingerprint %s", ssh.FingerprintSHA256(signer.PublicKey()))

	if cfg.Server.SSHStrictHostKey == "no" {
		c.warn("server.ssh_strict_host_key is 'no', any host key is accepted")
		c.hint("set it to accept-new unless you have a reason not to")
	} else {
		known := cfg.ResolvedKnownHostsPath()
		if err := ensureKnownHostsFile(known); err != nil {
			c.fail("%v", err)
			return false
		}
		c.ok("known_hosts at %s", known)
	}
	return true
}

func checkServer(c *checker, cfg *Config, ctx context.Context) bool {
	c.section("Server PC")
	pinger := &Pinger{}
	c.info("reachability checks use %s", pinger.Mode())

	if !pinger.Ping(ctx, cfg.Server.IP, 2*time.Second) {
		// Asleep is the normal state, so this is not a failure by itself.
		c.info("%s does not answer, the PC is asleep or switched off", cfg.Server.IP)
		c.hint("that is the normal resting state, the watcher wakes it on demand")
		c.hint("to test the rest of this check, wake the PC and run it again")
		return false
	}
	c.ok("%s answers", cfg.Server.IP)

	mcAddress := net.JoinHostPort(cfg.Server.IP, strconv.Itoa(cfg.Server.MCPort))
	if dialSucceeds(ctx, mcAddress, 3*time.Second) {
		c.ok("Minecraft is listening on %s", mcAddress)
	} else {
		c.info("nothing on %s yet, the container is stopped", mcAddress)
	}
	return true
}

func checkSSHLogin(c *checker, cfg *Config, ctx context.Context) {
	c.section("SSH login and container")
	runner := NewSSHRunner(cfg)

	out, err := runner.Run(ctx, "docker inspect -f {{.State.Status}} "+cfg.Server.ContainerName)
	if err != nil {
		if strings.Contains(err.Error(), "handshake") || strings.Contains(err.Error(), "unable to authenticate") {
			c.fail("SSH login as %s failed: %v", cfg.Server.SSHUser, err)
			c.hint("the public key is missing from authorized_keys on the server")
			c.hint("run: mc-wol-proxy setup-ssh")
			return
		}
		// A restricted key refuses everything except docker start, which is
		// the recommended setup and not something to warn about loudly.
		c.ok("SSH login as %s works", cfg.Server.SSHUser)
		c.info("the key did not run the inspect command: %v", err)
		c.hint("expected when the key is restricted to 'docker start', as recommended")
		return
	}

	c.ok("SSH login as %s works", cfg.Server.SSHUser)
	switch out {
	case "":
		c.warn("container '%s' did not report a state", cfg.Server.ContainerName)
	case "running":
		c.ok("container '%s' is running", cfg.Server.ContainerName)
	default:
		c.ok("container '%s' exists, state '%s'", cfg.Server.ContainerName, sanitizeForLog(out, 32))
	}
}

func checkDuckDNS(c *checker, cfg *Config, ctx context.Context) {
	c.section("DuckDNS")
	if !cfg.DuckDNS.Enabled {
		c.info("disabled in config.yml")
		return
	}
	if err := updateDuckDNS(ctx, cfg); err != nil {
		c.fail("update for %s.duckdns.org failed: %v", cfg.DuckDNS.Domain, err)
		c.hint("check duckdns.domain and duckdns.token")
		return
	}
	c.ok("update for %s.duckdns.org accepted", cfg.DuckDNS.Domain)
}
