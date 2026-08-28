# Security

This project puts a service on the internet and gives it the ability to power on
a PC in your home network. That combination deserves a written down threat model,
so this file explains what is exposed, what the defaults protect against, and
what you can tighten further. The [README](README.md) stays focused on getting it
running, everything technical lives here.

## What is exposed

| Component | Reachable from | Authentication |
|-----------|----------------|----------------|
| Watcher, port 25565 | the internet, via your router | none, by design |
| Minecraft server | the watcher, or the internet in transfer mode | Mojang account (online mode) |
| SSH on the server PC | the watcher, on the LAN | key only, forced command |
| RCON | the docker network on the server PC | password from `server/.env` |

The watcher cannot authenticate anyone. A Minecraft client speaks its handshake
before any account check happens, so the proxy has to accept and parse packets
from strangers. Everything below exists because of that.

## Design decisions

**The proxy never blocks on network calls.** Every connection is handled in its
own goroutine, and reachability is probed once and cached for two seconds behind
a mutex. Without that, a handful of connections per second could each open their
own probe against a server that is asleep, which is its normal state.

**Waking the server is rate limited.** A login attempt on a sleeping server
triggers Wake-on-LAN, an SSH call and a container start. Without a limit anyone
could replay that endlessly. `limits.boot_cooldown` (default 10 seconds) is the
minimum gap between attempts. It is deliberately short: waking the PC and
starting the container takes longer than that anyway, so a player who finds the
server down is never left waiting on the limiter, while an attacker still cannot
turn one packet per second into one wake per second.

The real protection against repeated abuse is the failure backoff. After a wake
attempt that does not end with a reachable server, the gap grows from
`limits.boot_failure_backoff` and doubles with every further failure up to
`limits.boot_max_backoff`, so a server that cannot come up is not retried on
every connection. The counter resets on the first success. Clients that arrive
during a cooldown get a proper disconnect message, not a dropped socket.

**Concurrent connections are capped, in two separate pools.** Logins are
limited to the player slots the server itself reports, since more players
cannot join anyway, and `limits.max_logins` overrides that with a fixed number.
Status pings get their own pool of five times that with a floor of 64. Sharing
one pool would let a handful of server list entries take every slot a player
could have used, and a full server would blank the entry in everyone else's
list. `limits.max_per_ip` (default 8) applies to each pool on its own, which is
why they count separately: a household behind one NAT has to be able to play and
keep the list open at the same time. Without any of this, the accept loop
started a goroutine per connection with no limit, so a plain connection flood
could exhaust memory and file descriptors on a Pi.

**What the status response can contain is bounded.** A status ping is
unauthenticated and answered to anyone who asks, so an oversized icon would turn
a 30 byte request into a multi megabyte reply, which is a usable amplifier
against the watcher's own uplink. Icons are capped at 64 kB and must be the
64x64 that Minecraft requires, MOTD files at 8 kB, and anything larger is
skipped with a line in the log. The response from the real server is read by its
length prefix and capped at 256 kB rather than trusted to fit one read.

**The packet parser is defensive.** VarInts are rejected past 35 bits,
incomplete reads return an error instead of indexing out of bounds, and the
initial client exchange has timeouts so a half open connection cannot be held
forever. Usernames are limited to the protocol's 16 characters, must decode as
UTF-8, must come with a complete 16 byte UUID, and are stripped of non-printable
characters before they reach the log.

**Transfer targets depend on where the client came from.** In transfer mode the
watcher hands the player a new address to connect to. Clients from a private
address get `server.ip` on the LAN, everyone else gets the public
`transfer.host`. The decision is made from the socket's peer address, which a
client cannot choose for itself, and `transfer.local_networks` narrows what
counts as local. The only thing a local client gains is the address it would
have reached anyway.

**Nothing is handed to a shell.** The watcher spawns no processes at all in its
normal operation. SSH and ICMP are libraries compiled into the binary, so there
is no command line for a config value to break out of. `server.container_name`
is still checked against what Docker accepts as a name, because it is the one
value that ends up inside the remote command string.

**The config is validated at startup.** A bad MAC, a port outside 1 to 65535, an
unknown `wol.mode` or a MOTD that is not valid JSON stops the watcher with a
message naming the field, rather than failing later at the moment a player tries
to join.

## Your secrets

`config.yml` holds your DuckDNS token, the server's MAC address and your local
IPs. It is git-ignored and only `config.example.yml` with placeholders is
tracked. Never move your real values into the example file.

The installer copies the config to `/opt/mcwod/config.yml`, chowns it to
the service user and sets mode 600, so the token is not world readable on the
watcher. `mcwod init` writes it with mode 600 for the same reason, and
reads the token without echoing it to the screen.

`mcwod config` writes the file back with mode 600 explicitly, because
writing to an existing file leaves its mode alone and the token has to stay
unreadable to other accounts on the watcher. It edits the parsed YAML rather
than rewriting the file from the config struct, so a comment you put next to
your own token is not silently dropped.

`server/.env` holds the RCON password. Compose refuses to start without it
rather than falling back to an image default. RCON's port 25575 is not published
and must stay that way.

## SSH

The watcher holds a private key that can reach your server PC. Restrict what
that key is allowed to do, on the server, in `~/.ssh/authorized_keys`:

```
command="docker start minecraft",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty ssh-ed25519 AAAA... watcher@host
```

Even if the key leaks, it can then only start that one container.
`mcwod setup-ssh` installs the key in exactly this form by default, so
this is what you get unless you decline it.

### When the watcher may also send the PC to sleep

Answering yes to the sleep question in `setup-ssh` widens what the key can do,
and that is worth understanding before you agree to it. The forced command
becomes a script instead of a single command:

```
command="/usr/local/bin/mcwod-remote",no-port-forwarding,... ssh-ed25519 AAAA... mcwod
```

The script accepts exactly six words and refuses everything else:

| Word | Runs |
|------|------|
| `hello` | prints a marker so `check` can tell the script is really installed |
| `start` | `docker start <container>` |
| `stop` | `docker stop <container>` |
| `status` | `docker inspect -f '{{.State.Status}}' <container>` |
| `players` | `docker exec <container> rcon-cli list` |
| `sleep` | the one power command you picked |

`$SSH_ORIGINAL_COMMAND` is only ever compared against those words, never
executed, so the watcher cannot ask the script for anything that is not on the
list. The script is installed with `install -o root -g root -m 0755`. That
matters: if the SSH account could write to it, anyone holding the key could
rewrite the script and the restriction would be worth nothing.

The sleep command needs root, because `systemctl suspend` over SSH runs into
polkit, which does not treat an SSH session as an active local session. The
sudoers rule is therefore one line naming one exact subcommand:

```
youruser ALL=(root) NOPASSWD: /usr/bin/systemctl suspend
```

No wildcard, so it cannot be talked into running another `systemctl` command.
It is written to `/etc/sudoers.d/mcwod` only after `visudo -c` accepts
it, because a malformed file there locks you out of your own machine.

What you are accepting: a watcher that is compromised can now switch the server
PC off as well as on. The blast radius is annoyance rather than data loss, since
the same watcher can wake it again, and `docker stop` runs before a hibernate or
shutdown so the world is written out. If that trade is not worth it to you,
decline the question and the key stays limited to `docker start`.

The password you type during `setup-ssh` is used for that one login, handed to
`sudo` over stdin so it never appears in the server's process list, and is not
written anywhere.

The watcher generates its own key at `~/.ssh/mcwod` and uses no other unless
`server.ssh_key_path` names one. It never reaches for `~/.ssh/id_ed25519`,
because a key found at the default path is the one its owner logs in with
everywhere else, and that does not belong inside a service facing the internet.
Point the setting at such a key by hand and `check` warns about it.

The key must not be readable by other users and must not have a passphrase. The
watcher refuses to start otherwise, the first because it is the rule OpenSSH
applies and the second because an unattended service cannot type one.

### The one password login

`init` offers to log in to the server once with a password and set everything up
from there, and `setup-ssh` does the same for the key alone. That login is the
only time this project handles your server password, and it is handled like
this:

- It is read without echoing to the screen, kept in memory for the life of that
  one connection, and never written anywhere.
- It goes to the SSH client and, for the commands that need root, to `sudo` over
  stdin. It never appears on a command line, because command lines show up in
  the server's own process list where any other user on that machine can read
  them.
- It is never put into a log line or an error message.

The host key is confirmed before the password is sent. On first contact the
fingerprint is printed and you have to answer yes, whatever
`server.ssh_strict_host_key` says, because a password handed to an unverified
host is a password handed to whoever is in the middle. A key that changed after
being trusted is a hard failure with no way to click through it.

What that session changes on your server, all of it announced first:

| Change | Needs root |
|--------|-----------|
| the public key in `authorized_keys` | no |
| `ethtool -s <iface> wol g` and a systemd unit that re-arms it on boot | yes |
| `/usr/local/bin/mcwod-remote`, only when you asked for auto-sleep | yes |
| `/etc/sudoers.d/mcwod`, same | yes |

Everything it reads is read only: the MAC address, the interface, the container
list, the published port, whether RCON is on, what the kernel can do about
sleeping.

### Which SSH implementation

SSH is `golang.org/x/crypto/ssh`, the Go team's implementation, compiled into
the binary. The watcher does not implement any cryptography of its own. What it
does implement is the host key policy below, on top of
`golang.org/x/crypto/ssh/knownhosts`, which parses the same `known_hosts` format
OpenSSH writes.

The tradeoff worth knowing about: with the system `ssh` binary, a distro update
patched a vulnerability for you. Compiled in, it is only patched when a new
release of this project is built. Dependabot watches `golang.org/x/*` weekly and
opens a pull request for it, and the release workflow turns that into new
binaries, but the responsibility now sits with this repository rather than with
your package manager. If you build from source, pull and rebuild after such an
update.

`govulncheck` runs on every push and once a week on a schedule, so a newly
disclosed vulnerability surfaces without anyone having to commit first. It
reports only the ones the code actually reaches, which keeps a finding worth
acting on.

### Host key checking

Controlled by `server.ssh_strict_host_key`:

| Value | Behaviour |
|-------|-----------|
| `accept-new` (default) | trust the key on first sight and log its fingerprint, refuse if it ever changes |
| `yes` | only accept a key that is already in `known_hosts` |
| `no` | accept anything, logs the fingerprint of every key it accepts |

A changed host key is a hard failure in both `accept-new` and `yes`. That is the
case host key checking exists for, and the error names the two things it can
mean: the server was reinstalled, or someone is intercepting the connection.

`accept-new` still takes the first connection on trust. To close that window,
pin the key before starting the watcher and switch to `yes`:

```bash
ssh-keyscan -H 192.168.1.100 >> ~/.ssh/known_hosts
```

`mcwod setup-ssh` closes it differently: it shows you the fingerprint and
asks before trusting it, so you can compare it against the server.

Where the file lives depends on the deployment. Under systemd it is
`/opt/mcwod/known_hosts`, so the unit can keep the home directory read
only. In Docker it is `watcher/state/known_hosts`, on a mounted directory rather
than a mounted file, because Docker creates a directory in place of a bind
mounted file that does not exist yet. That is what used to silently throw the
accepted key away on every container recreate.

## Container hardening

The image is built on `scratch` and contains the binary and a CA bundle, nothing
else. There is no shell, no package manager and no interpreter to abuse if
something does go wrong. It drops all capabilities and adds back only `NET_RAW`,
which ICMP needs, runs with `no-new-privileges` and has a read-only root
filesystem.

Host networking is required because Wake-on-LAN broadcasts on the real LAN.

The systemd unit gets the same treatment: `NoNewPrivileges`,
`ProtectSystem=strict`, a read only home, a single writable path and `CAP_NET_RAW`
as an ambient capability so it does not need root to open an ICMP socket.

Image tags and the Minecraft version are pinned, so restarting a container never
pulls a different build than the one you reviewed.

## Verifying what you install

Release binaries are built by the workflow in `.github/workflows/release.yml`
and published with a `checksums.txt`. `install.sh` downloads that file and
refuses to install a binary whose hash does not match, or one that is not listed
in it at all. If you would rather not trust the release at all, build from
source with `sudo ./install.sh --build`.

Each binary also carries build provenance, so you can check that a download
really came out of that workflow and that commit rather than from someone who
got hold of the release page:

```bash
gh attestation verify mcwod_linux_arm64 --repo posch-dev/minecraft-wake-on-demand
```

This is not a code signature. Windows will still warn about an unsigned
executable downloaded from the internet, because signing needs a certificate
from a certificate authority and this project does not have one.

`mcwod update` applies the same rule. It downloads `checksums.txt`
alongside the asset and refuses to install on a mismatch, or when the asset is
not listed at all, because that check is the only thing between a release URL
and running whatever came back. It also refuses to follow a redirect off the
release host, so a hijacked redirect cannot point the download somewhere else.
The new binary is written next to the old one and renamed over it, so a failed
download cannot leave a half written file where the service expects a program.

**The watcher never updates itself.** `update` asks before it does anything, and
nothing in the running proxy will ever replace its own binary.

`install.sh` and `update` both read `MCWOD_REPO`, `MCWOD_API_BASE` and
`MCWOD_DOWNLOAD_BASE` from the environment if they are set, and `install.sh`
also reads `MCWOD_INSTALL_DIR`. These are there for mirrors and for testing.
Anything you point them at is trusted to serve the binary, so only use them with
a source you control.

### The update check and your IP

`init`, `config` and `check` ask GitHub once a day whether a newer release
exists and print one line if so. That request tells GitHub the IP the watcher is
behind, which for most people is their home connection. The answer is cached for
24 hours in `.update-check.json` next to the config, the request has a two
second timeout and failing is silent, so nothing depends on it.

Set `update.check: false` in `config.yml` to switch it off entirely. `update`
itself still works when you run it by hand.

## Things you can tighten

- Set `watcher.listen_address` to a single LAN IP if the watcher has more than
  one interface and only one of them should serve Minecraft.
- Set `watcher.allowed_hostnames` to your public domain (and LAN IP, if both
  are used). When non-empty, the watcher drops any connection from a non-local
  IP whose handshake ServerAddress does not match the list. That keeps port
  scanners and internet crawlers from getting even a sleeping MOTD back, at the
  cost of rejecting players who connect by raw IP instead of the domain name.
  When DuckDNS is enabled, this list is populated automatically with your
  DuckDNS domain. Forge clients and forwarding proxies append their own fields
  to the address, everything after the first NUL byte is ignored when matching,
  so they are not affected.
- Turn on the Minecraft whitelist if the server is meant for a fixed group.
- Protect the transfer port (25566 in transfer mode) with a host firewall.
  That port is published directly to the Minecraft container and the watcher
  cannot filter it, so a port scanner reaches the server without passing
  through the watcher's hostname check. An iptables rule that only allows new
  connections to 25566 from IPs that recently connected to 25565 closes the
  gap:

  ```bash
  # IPs that hit the watcher within the last 120 seconds get 25566 opened.
  # Everything else is dropped before it reaches the container.
  # Works with UFW via `ufw insert` or as raw iptables rules on the host.
  iptables -A INPUT -p tcp --dport 25565 -m recent --set --name MCKNOWN
  iptables -A INPUT -p tcp --dport 25566 -m recent --rcheck --seconds 120 --name MCKNOWN -j ACCEPT
  iptables -A INPUT -p tcp --dport 25566 -j DROP
  ```

  UFW does not expose the `recent` match directly, so either use raw iptables
  rules (saved in `/etc/iptables/rules.v4` or a netfilter-persistent unit) or
  let UFW manage the broad allow/drop stanzas and append the `recent` rules
  after with `iptables -A` so they land in the right chain.
- Pin the SSH host key and switch to `ssh_strict_host_key: "yes"`.
- Keep the pinned image tags current, they do not update themselves.
- Watch for Dependabot pull requests on `golang.org/x/crypto` and cut a release
  when one lands.

## Reporting a vulnerability

Open an issue for anything low risk. For something that could be exploited
against a running deployment, please report it privately through GitHub's
security advisories rather than in a public issue.
