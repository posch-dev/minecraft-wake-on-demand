# Changelog

Notable changes per release. The section matching a tag becomes that release's
notes, so a version has to be listed here before it is tagged.

## 2.1.0

### You need to know

- **The watcher no longer uses `~/.ssh/id_ed25519`, and there is no fallback to
  it.** Only the key it generates itself at `~/.ssh/mcwod` is ever used. If your
  install was set up before this version, run `mcwod setup-ssh`.
- `.server-info.json` is keyed per world now. An older file written by 2.0 is
  ignored and the version is learned again on the next boot.
- Transfer mode is part of `init` again. 2.0 moved it to `mcwod config`; it is
  offered in the wizard once more, but only to people who use DuckDNS.

### Added

- `mcwod install` sets the whole thing up from the binary itself. The unit and
  the example assets ride along inside it, so one downloaded file is a complete
  installation and `install.sh` can be piped into a shell. On Linux it asks the
  setup questions as the account that called `sudo`. On Windows it installs into
  your own profile and starts with your session; a real service is still to come.
- `mcwod worlds` keeps several worlds side by side and switches between them.
  Each gets its own folder and compose file, only the active one runs, and
  switching counts down before it disconnects anyone.
- `mcwod worlds` also changes a world's Minecraft version or server kind. It
  backs the world up on the server first, and that is not optional. Going
  backwards says why it will not work and offers a fresh world instead.
- Each world can have its own MOTD and pictures in `assets/worlds/<name>/`,
  falling back to the shared ones.
- `mcwod players`, also reachable as `mcwod whitelist`. Turns the whitelist on
  and off, adds and removes names, and says who is an admin. Removing the last
  admin asks first.
- `mcwod config` changes an existing setup through a menu, also spelled `edit`
  and `settings`. Your own comments in `config.yml` survive it, and it validates
  before writing.
- `mcwod update` installs a newer release. It shows what changed, asks first,
  and verifies the download against the published `checksums.txt`. **Nothing
  ever updates by itself.**
- `init`, `config` and `check` mention a newer release in one line. Cached for a
  day, two second timeout, `update.check: false` turns it off.
- `init` can set the server up over one password login. It reads the MAC and
  broadcast address, lists the containers, checks RCON, suspend support and
  Wake-on-LAN, and installs its own key. Everything it finds is shown first.
- A network card left at `Wake-on: d` is offered a fix, both now and through a
  small unit that re-arms it after every boot. That one line is the most common
  reason this project appears to do nothing.
- `init` and `config` can set the Minecraft container up. They write a compose
  file with `itzg/minecraft-server` and `itzg/mc-backup` on pinned tags, RCON
  on, AUTOPAUSE on, an RCON password in a `.env` with mode 600, and bring it up.
  The version is asked for rather than left at `LATEST`.
- An existing compose file gets the two services added instead. Other services,
  keys and comments stay, a backup is kept, `docker compose config` has to
  accept the result, and an existing service name is refused.
- `mcwod restore-compose` puts back a compose file the watcher replaced, keeping
  the replaced one as a backup.
- The wizard asks for a whitelist and for the admin separately. An enforced but
  empty whitelist is never written.
- `mcwod get-server-icon` copies the icon a running server serves into
  `assets/server-icon.png`. It is a command, not something the proxy picks up,
  because answering a status ping must never write to disk.
- The watcher brings its own server list icon: three blue Z, the largest turning
  into a red exclamation mark while the PC boots. They ship inside the binary.
- Per state overrides. `server-icon-sleeping.png`, `-starting.png` and
  `-live.png` replace an icon outright, `motd-live.json` and `motd.live` do the
  same for the MOTD. A `-live` override swaps only the description and the
  favicon, so the player count and version stay real.
- `assets/motd-login-wait.json` and `motd.login_wait`, the message shown to
  whoever's join woke the server.
- `assets/examples/` with a copyable MOTD for every state, a placeholder icon
  and a README explaining the override order.
- The watcher can put the server PC back to sleep once nobody plays, closing
  issue #6. Set `sleep.enabled: true` after `setup-ssh` installed the helper. An
  answer it cannot read counts as busy, never as empty.
- `setup-ssh` can install `mcwod-remote` on the server, a root owned script that
  accepts six fixed words and nothing else. The key is bound to it, so a leaked
  key cannot run an arbitrary command. The `sudoers` rule is checked with
  `visudo -c` before it goes near `/etc`.
- `setup-ssh` works out whether the server runs Linux or Windows and what it
  has, instead of asking. On Windows it prints the helper and the `icacls` calls
  that go with it.
- `check` asks the server real questions once the helper is there: the marker,
  the container state, the player count, and whether Wake-on-LAN is armed. It
  also reports the sleep setup.
- The version and player slots are learned from a status probe and cached, so a
  sleeping server reports its real version instead of protocol -1.
- Concurrent connections are capped. Logins are held to the server's own slots,
  status pings get their own pool, and `limits.max_per_ip` applies to each so a
  household behind one NAT is not locked out.
- `motd.server_full`, shown when the login pool is full.
- `server.compose_dir` remembers where the compose file lives.
- `server.remote_helper` and the `sleep` block. The watcher refuses to start
  with `sleep.enabled` and no helper.
- The `worlds` block in `config.yml`. An older config describes one world and is
  read as that.
- `server.ip` takes a hostname as well as an IP, as long as it resolves.
- Optional `watcher.allowed_hostnames`. Connections from outside whose handshake
  names something else are dropped, which keeps port scanners from getting an
  answer.

### Changed

- **The tool is called `mcwod` now.** The binary, the release assets,
  `/opt/mcwod`, the unit, the helper, the SSH key and the environment variables
  all follow. `MC_WOL_*` still works and warns once.
- A player whose join wakes the server is told to come back instead of being
  held on the connection. Waking takes longer than any client waits.
- One `server-icon.png` feeds all three states, plain while the server runs and
  at half opacity under the Z while it sleeps. `server-icon-live.png` is now an
  ordinary override like the other two.
- Icons are composed on a one minute tick, never inside a request.
- A generated compose always sets `ACCEPTS_TRANSFERS`, so turning transfer mode
  on later is a change to the watcher and nothing else.
- A generated compose sets `ENFORCE_SECURE_PROFILE: "FALSE"`, so chat is
  unsigned and nothing is held back or reportable. `ONLINE_MODE` is untouched.
- Waking brings the whole compose project up, so the backup service comes back
  with the server.
- `mcwod` on its own shows a menu instead of the help. Started as a service it
  still runs the watcher.
- The wizard is written for somebody who has not used a terminal. Choices are
  numbered, the reason sits above the question in grey, and the jargon is gone.
- The wizard and `check` use colour: what you type green, side notes grey,
  warnings amber, failures red. Off whenever nothing is attached to a terminal,
  and `NO_COLOR` turns it off.
- The server type is asked for, so a mod server is a choice. The heap size is
  suggested from the RAM the server reports.
- The DuckDNS token stays visible while you type it. It is pasted off a page,
  and hiding it only makes it harder to check.
- `get-server-icon` warns before it replaces a picture and keeps the old one
  only if you say so.
- `install.sh` stops an `mc-wol-proxy` service if it finds one. Two watchers on
  port 25565 would have failed in ways that look random.
- Transfer mode needs 1.20.5 (protocol 766), the first version with the
  Transfer packet. Proxy mode still supports 1.7.6+.

### Fixed

- Login Success carries the session id that clients from protocol 776 expect.
  Minecraft 26.2 refused every login without it. The packet has changed shape
  three times now and each range is written the way it expects.
- Login Success no longer carries a stale strict error handling byte outside
  766-767, which crashed 1.21.2+ with `DecoderException: 1 extra byte`.
- The status request is forwarded to the real server. Clients may send it apart
  from the handshake, and only the handshake was passed on, so a running server
  showed up in the list as asleep.
- The connection is drained before the wait message's socket closes. Closing on
  an unread login packet sends a reset, and the client showed a socket error
  instead of the message.
- The watcher waits for Minecraft to answer before calling the server live. The
  published port came about 24 seconds earlier.
- The learned version survives a restart and is kept per world. It was written
  under one name and read back under another.
- The container image is pinned without a Java suffix, so the image picks the
  runtime that fits.
- The watcher generates its own SSH key and never adopts `~/.ssh/id_ed25519`.
  An internet facing service ended up holding the key its owner logs in with
  everywhere else.
- `setup-ssh` replaces an outdated entry for its own key instead of leaving it.
  Every other line in `authorized_keys` stays as it was.
- A first install works. Several things assumed a setup that already existed.
- `init` remembers the world it just created.
- `check` reports the container state instead of the refusal a restricted key
  answers with.
- The setup questions no longer repeat forever when their answers run out. A
  closed input gives empty reads forever, and one validated question filled a
  log with 197 MB in seconds.
- `duckdns.domain` takes the address either way round, with or without the
  suffix. It used to be a hard startup error.
- `mcwod` with no argument at a terminal no longer silently starts the proxy.
  The unit, the image and both Windows starters say `run` outright.
- The broadcast address is read off the watcher's own interface instead of
  assuming a `/24`. On a `/16` the guess was wrong and waking simply never
  worked.
- The status response is read by its length prefix instead of one 4096 byte
  read. A response with an icon never arrives in one segment, so version
  learning did nothing on any server that had one.
- An icon over 64 kB or not 64x64 is skipped with a warning. A status ping is
  answered to anyone, so an oversized icon turned a 30 byte request into a
  multi megabyte reply. MOTD files are capped at 8 kB.
- `watcher.allowed_hostnames` no longer rejects Forge players and players behind
  a proxy. Both append fields after a NUL byte, so only the part before the
  first NUL is compared now.

## 2.0.0

The watcher is written in Go and ships as a single binary. Your `config.yml`
keeps working unchanged.

### You need to know

- **Docker users:** the `known_hosts` mount changed from a file to a directory.
  Replace the `./known_hosts:/root/.ssh/known_hosts` line with
  `./state:/state` and add `SERVER_SSH_KNOWN_HOSTS: /state/known_hosts` to the
  environment. The updated `watcher/docker-compose.yml` has it. The `touch
  known_hosts` step is gone for good.
- **Everyone else:** re-run `sudo ./watcher/install.sh`, or download the new
  `.exe` on Windows. Python and PyYAML are no longer needed anywhere.
- SSH keys with a passphrase are now refused with an explanation instead of
  failing later. An unattended service cannot type one, so use a key without.

### Added

- `mcwod init` asks for your settings and writes `config.yml`. It finds
  the server's MAC address itself by pinging the IP and reading the ARP cache,
  and derives the broadcast address from the same IP.
- `mcwod setup-ssh` creates the key and installs it in `authorized_keys`
  over a one time password login, restricted to `docker start` by default. It
  shows the host key fingerprint and asks before trusting it.
- `mcwod check` tests the whole setup and names the step that is broken.
- Release binaries for linux amd64, arm64, armv7 and armv6 and for windows
  amd64, published with a `checksums.txt` and build provenance attestation.
  `install.sh` downloads the right one and refuses to install it unverified.

### Fixed

- The readiness probe could hang forever. `write_varint` shifted a signed
  integer right, which in Python keeps the sign, so encoding the protocol
  version `-1` never terminated and grew a buffer until memory ran out. The
  container was started before that probe ran, so the symptom was a stuck
  thread and a boot lock that was never released rather than an obvious
  failure.
- Custom MOTDs and the server icon were ignored on Windows. The batch file
  pointed the config path at the repository root, and assets were looked for
  next to it instead of in `watcher/assets`.
- The accepted SSH host key was thrown away on every container recreate unless
  `known_hosts` had been created by hand first, because Docker puts a directory
  in place of a bind mounted file that does not exist.
- Shutdown waits for connections in flight instead of tearing them down, so a
  restart no longer cuts a player off mid session.

### Changed

- SSH runs through `golang.org/x/crypto/ssh` and ICMP through
  `golang.org/x/net/icmp`. Neither `openssh-client` nor `iputils-ping` is
  needed, which takes the container image from roughly 150 MB to about 8 MB on
  a `scratch` base.
- A changed SSH host key is now a hard failure in both `accept-new` and `yes`,
  with an error naming the two things it can mean.
- The config is validated at startup, with messages saying what to put in a
  field rather than what is wrong with it.
- The systemd unit gained `CAP_NET_RAW` as an ambient capability plus
  `NoNewPrivileges`, `ProtectSystem=strict` and a read only home.
- `server.container_name` is checked against what Docker accepts, since it is
  the one config value that reaches the remote command string.

### Removed

- `watcher/mc_wol_proxy.py` and the Python and PyYAML requirement.
