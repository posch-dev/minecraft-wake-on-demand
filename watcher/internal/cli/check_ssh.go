package cli

import (
	"context"
	"strings"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/remote"
	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/sshx"
	"golang.org/x/crypto/ssh"
)

func checkSSHKey(c *checker, cfg *config.Config) bool {
	c.section("SSH key")
	path := cfg.ResolvedSSHKeyPath()
	signer, err := sshx.LoadPrivateKey(path)
	if err != nil {
		c.fail("%v", err)
		c.hint("run: mcwod setup-ssh")
		return false
	}
	c.ok("%s, type %s", path, signer.PublicKey().Type())
	if cfg.UsesSharedSSHKey() {
		c.warn("this is the account's own SSH key, not one made for the watcher")
		c.hint("a service on the internet should not hold the key you log in with")
		c.hint("run setup-ssh to give it its own, then remove the old line from")
		c.hint("authorized_keys on the server")
	}
	c.ok("fingerprint %s", ssh.FingerprintSHA256(signer.PublicKey()))

	if cfg.Server.SSHStrictHostKey == "no" {
		c.warn("server.ssh_strict_host_key is 'no', any host key is accepted")
		c.hint("set it to accept-new unless you have a reason not to")
	} else {
		known := cfg.ResolvedKnownHostsPath()
		if err := sshx.EnsureKnownHostsFile(known); err != nil {
			c.fail("%v", err)
			return false
		}
		c.ok("known_hosts at %s", known)
	}
	return true
}

func checkSSHLogin(c *checker, cfg *config.Config, ctx context.Context) {
	c.section("SSH login and container")
	runner := sshx.NewSSHRunner(cfg)

	if cfg.Server.RemoteHelper {
		checkRemoteHelper(c, cfg, runner, ctx)
		return
	}

	out, err := remote.RunVerb(ctx, runner, remote.RemoteVerbStatus)
	if err != nil {
		if isAuthFailure(err) {
			c.fail("SSH login as %s failed: %v", cfg.Server.SSHUser, err)
			c.hint("the public key is missing from authorized_keys on the server")
			c.hint("run: mcwod setup-ssh")
			return
		}
		// A restricted key refuses everything except its forced command, which
		// is the recommended setup and not something to warn about loudly.
		c.ok("SSH login as %s works", cfg.Server.SSHUser)
		c.info("the key did not run the inspect command: %v", err)
		c.hint("expected when the key is restricted to 'docker start', as recommended")
		c.hint("run setup-ssh again to install the helper, which check can question properly")
		return
	}

	c.ok("SSH login as %s works", cfg.Server.SSHUser)
	reportContainerState(c, cfg, out)
}

func isAuthFailure(err error) bool {
	return strings.Contains(err.Error(), "handshake") || strings.Contains(err.Error(), "unable to authenticate")
}
