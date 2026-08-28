# Changelog

Notable changes per release. The section matching a tag becomes that release's
notes, so a version has to be listed here before it is tagged.

## [Unreleased]

### Changed

- Running `mcwod` on its own now shows a menu instead of the help. The first
  run goes straight into the setup, later ones show what the server is doing
  and offer check, settings, the server picture and the update, with the new
  version line in there too. Started as a service it still runs the watcher.
- Every question in the wizard is written for somebody who has not used a
  terminal before. Choices that used to need a typed word are numbered, the
  reason for a question sits under it in grey instead of in front of it, and
  the technical vocabulary is gone from the questions themselves.
- The server type is asked for, so a mod server is a choice rather than
  something to edit afterwards. The heap size is suggested from the RAM the
  server actually reports, a quarter of it, no less than 2G and no more than 8G.
- The admin is its own question. It used to be a side effect of naming a
  whitelist, so a server without one had nobody who could run a command in the
  game at all.
- The whitelist is asked in two steps, first whether you want one and only then
  for the names, with the admin filled in so nobody locks themselves out.
- The DuckDNS token stays visible while you type it. It is pasted off a page
  rather than remembered, and hiding it only makes it harder to check.
- Transfer mode is no longer part of `init`. It is an optimisation with an
  unpleasant failure mode and it lives in `mcwod config`.
- `get-server-icon` explains itself before it asks, warns before it replaces a
  picture that is already there, and keeps the old one as `server-icon-old.png`
  only if you say so.

### Added

- `mcwod worlds` keeps several Minecraft worlds side by side and switches
  between them. Each one gets its own folder and its own compose file, so a
  broken world is one broken world, and only the active one is ever started.
  Switching says how many players it is about to disconnect and counts down for
  ten seconds first. Removing a world takes it off the list and leaves the
  files where they are.
- The `worlds` block in `config.yml`. A config written before it existed
  describes exactly one world and is read as that, so nothing has to be edited.

- `mcwod players`, also reachable from the menu and as `mcwod whitelist`. It
  turns the whitelist on and off, adds and removes names, and toggles who is an
  admin from the same list. Taking away the last admin asks first, since a
  server with none can only be repaired by editing files on the PC.
- `server.compose_dir` remembers where the server's compose file lives, so
  `restore-compose` and `players` no longer ask for it.

- Server version and player slots are now learned from a status probe on first
  boot and cached in `.server-info.json` next to the config. The server list
  ping while the server is asleep reports the real version instead of protocol
  -1, so clients no longer show "Incompatible Version", and it shows the real
  number of slots instead of `motd.max_players`.
- Concurrent connections are capped. Logins are limited to the server's own
  player slots, or to `limits.max_logins` when that is set, and status pings get
  a separate pool of five times that with a floor of 64. `limits.max_per_ip`
  (default 8) applies to each pool on its own, so a household behind one NAT is
  not locked out. A rejected login gets `motd.server_full`.
- `motd.server_full`, shown to a player who arrives when the login pool is
  full.
- `mcwod update` installs a newer release. It shows what changed, asks
  before doing anything, verifies the download against the published
  `checksums.txt` and refuses to install on a mismatch or a missing entry, and
  will not follow a redirect off the release host. The new binary is staged
  beside the old one and renamed over it, so a failed download cannot leave a
  half written file where the service expects a program. **Nothing ever updates
  by itself.**
- `init`, `config` and `check` print one line when a newer release exists, with
  the command to install it. The result is cached for a day and the check has a
  two second timeout, so an offline machine barely notices. `update.check:
  false` turns it off, and the README says why you might want to: it is a
  request to GitHub, which tells GitHub the machine's IP.
- `mcwod config`, a menu for changing an existing setup, also reachable
  as `edit` and `settings`. Until now `init` refused to run once `config.yml`
  existed, so every later change meant editing YAML by hand. It writes through
  the parsed document rather than re-marshalling the config, so the comments
  people put in their own file survive, and it validates before writing so a
  menu session cannot leave behind a config the watcher would refuse to start
  with. The config file is also forced back to mode 0600 on save, since it
  holds the DuckDNS token.
- `init` can set the server up over SSH. It asks for the address and the user
  separately, logs in once with a password and then finds out the rest instead
  of asking: the MAC address and broadcast address off the interface carrying
  the default route, the containers and which port the Minecraft one publishes,
  whether RCON is on, whether the kernel can suspend or hibernate, and whether
  Wake-on-LAN is armed in the network driver. It installs its own key and, if
  asked, the sleep helper. Everything found is shown before it is used, and
  answering no falls back to the questions.
- A network card left at `Wake-on: d` is offered a fix on the spot, both
  immediately and through a small systemd unit that re-arms it after every boot,
  because most distributions reset the setting. That single line is the most
  common reason this project appears to do nothing at all.
- `server.ip` accepts a hostname as well as an IP now. It has to resolve, since
  the MAC lookup and unicast WoL need an address.
- `init` and `config` can set the Minecraft container up on the server, so the
  server PC no longer has to be touched for that either. They write a compose
  file with `itzg/minecraft-server` and `itzg/mc-backup` on pinned tags, RCON
  on, AUTOPAUSE on and only the game port published, generate an RCON password
  into a `.env` with mode 600, and bring the stack up. The server type and the
  Minecraft version are asked for, both with a concrete default rather than
  `LATEST`, so a pull cannot move the server under the world. Accepting the
  Minecraft EULA is its own question that says what it means.
- The wizard asks for a whitelist. Naming players switches
  `ENFORCE_WHITELIST` on and makes the first one the operator, leaving it empty
  keeps the server open, which is what a fresh server is anyway. An enforced but
  empty whitelist would lock out the owner too, so it is never written.
- A compose file that is already there gets the two services added to it
  instead. Other services, the top level keys and every comment stay exactly as
  they were, a copy is kept as `docker-compose.yml.mcwod-bak-<time>` first,
  `docker compose config` has to accept the result before it counts, and a
  service name that already exists is refused rather than overwritten. An
  existing `.env` gets three appended lines and nothing else, under
  `MCWOD_RCON_PASSWORD` so a foreign `RCON_PASSWORD` cannot be shadowed.
- `mcwod restore-compose` puts back a compose file the watcher replaced.
  The version it replaces is kept as a backup on the way, so the restore is
  itself undoable. It uses the password login, not the restricted key, which
  keeps that key at its six verbs.
- The Docker warning now says what is actually different per platform. On
  Windows it points out that Docker Desktop only runs while a user is logged in,
  so a resumed PC can come back without its container.
- `mcwod get-server-icon`, also spelled `learn-server-icon`, copies the
  icon a running server already serves into `assets/server-icon.png`. It is a
  command and not something the proxy picks up on its own, because answering an
  unauthenticated status ping must never write to disk. An icon that was already
  there is kept as `.bak`.
- The watcher brings its own server list icon. Three blue Z that grow, with the
  largest turning into a red exclamation mark while the PC boots. They ship
  inside the binary, so a fresh install shows them without any files being put
  anywhere. `assets/server-icon.png` is composed underneath at half opacity on
  white, so an existing icon still shows through with the Z drawn over it.
- Per state overrides. `assets/server-icon-sleeping.png`, `-starting.png` and
  `-live.png` replace an icon outright, `assets/motd-live.json` and `motd.live`
  do the same for the MOTD. With a `-live` override the watcher rewrites the
  running server's status response instead of forwarding it unchanged, swapping
  only the description and the favicon, so the player count, the version and the
  sample stay real. Nothing set leaves the running server exactly as it was,
  which is still the default.
- `assets/examples/` with a copyable MOTD for all three states and a README
  explaining the override order. The two MOTD files that used to sit in
  `assets/` moved there, they only ever restated the config defaults.
- The watcher can put the server PC back to sleep once nobody is playing,
  closing issue #6. Set `sleep.enabled: true` after `setup-ssh` installed the
  helper. In proxy mode it counts the sessions it forwards, which costs nothing
  and does not touch the Minecraft port, so it never fights the container's own
  autopause. In transfer mode, where sessions skip the watcher entirely, it
  polls over SSH instead. The counter only decides whether the server is worth
  asking, the answer from the server always decides whether it sleeps, and an
  answer that cannot be read counts as busy rather than as empty.
- `setup-ssh` can now install `mcwod-remote` on the server, a helper script
  owned by root that accepts only the fixed words `hello`, `start`, `stop`,
  `status`, `players` and `sleep` and refuses everything else. The key in
  `authorized_keys` is bound to that script, so the watcher can stop the
  container and suspend the PC without the key ever being able to run an
  arbitrary command. On Linux the matching `sudoers` rule is installed too,
  checked with `visudo -c` before it goes anywhere near `/etc`, and it names one
  exact `systemctl` subcommand without a wildcard.
- `setup-ssh` detects whether the server runs Linux or Windows and whether it
  has docker, systemctl and passwordless sudo, instead of asking. On Windows it
  prints the PowerShell helper, the `icacls` calls that keep it writable only by
  administrators, and a note that OpenSSH reads
  `administrators_authorized_keys` rather than the profile for admin accounts.
- `check` asks the server real questions once the helper is installed instead of
  guessing from a refused command. It confirms the helper answers `hello` with
  its marker, reports the container state, reads the player count, and reports
  whether Wake-on-LAN is armed in the network driver. A card with
  `Wake-on: d` is the most common reason this project appears to do nothing at
  all, and nothing else in the setup would have revealed it.
- `check` gained a Sleep section that reports the configured action, warns when
  `sleep.grace_period` is shorter than a boot takes, and points out that
  transfer mode makes the sleep monitor poll over SSH rather than count the
  sessions it forwards.
- `server.remote_helper` and the `sleep` config block. The watcher refuses to
  start with `sleep.enabled` and no helper, because an older forced command
  would silently run `docker start` for every verb it is sent, including
  `sleep`.
- Optional `watcher.allowed_hostnames` list. When non-empty, the watcher drops
  connections from non-local IPs whose handshake ServerAddress is not in the
  list, keeping port scanners and internet crawlers from getting any response.
  Auto-populated from the DuckDNS domain when DuckDNS is enabled.

### Changed

- The wizard and `check` use colour: what you type is green, side notes are
  grey, warnings amber and failures red. It is off whenever nothing is attached
  to the terminal, so pipes and the journal stay clean, `NO_COLOR` and
  `TERM=dumb` switch it off, and on Windows the console is put into virtual
  terminal mode first because the older `conhost` otherwise prints the escape
  codes as text.

- **The tool is called `mcwod` now.** The binary, the release assets, the
  install directory `/opt/mcwod`, the systemd unit, the helper on the server,
  its sudoers file, the Wake-on-LAN unit, the SSH key and the environment
  variables all follow. `MC_WOL_*` still works and warns once, so a mirror or a
  script that sets them keeps running. The repository and the Go module path
  are unchanged.
- `install.sh` stops and disables an `mc-wol-proxy` service if it finds one and
  says where the old config is. Two watchers on port 25565 would have produced
  failures that look random rather than one clear message.

### Added

- `assets/examples/` ships a placeholder `server-icon.png` and the MOTD files
  now say `CHANGE THIS TOP LINE` rather than repeating the built-in text, so it
  is obvious what to edit. Its README lists the colours Minecraft accepts and
  spells out that a file beats `config.yml`, which beats the built-in text.

### Fixed

- `duckdns.domain` takes the address either way round. `eliahmc` and
  `eliahmc.duckdns.org` both work, and the suffix is trimmed on load. It used to
  be a hard startup error, which is an odd thing to refuse given that the long
  form is what the DuckDNS page shows you.
- Running `mcwod` with no argument at a terminal prints the help instead
  of silently starting the proxy. Started as a service, where nothing is
  attached, it still runs the proxy, so existing installations are unaffected.
  The systemd unit, the Docker image and both Windows starters now say `run`
  outright rather than relying on that.
- The broadcast address is worked out from the watcher's own network interface
  instead of assuming a `/24`. On a `/16` or `/22` the guess was simply wrong,
  and the only symptom was that waking never worked. `init` no longer asks for
  it at all, except when the server turns out to be in a different network,
  where there is nothing local to work it out from.
- The watcher generates its own SSH key at `~/.ssh/mcwod` instead of
  reusing `~/.ssh/id_ed25519`. Finding a key already at the default path meant
  adopting it, so an internet facing service ended up holding the key its owner
  logs in with everywhere else. Existing installs keep using the key they have
  and `check` points out how to move off it.

- The status response from the real server is now read by its length prefix
  instead of from a single 4096 byte read. A response carrying a server icon is
  around 10 kB and never arrives in one segment, so version learning silently
  did nothing on every server that had an icon configured.
- A server icon over 64 kB or with dimensions other than 64x64 is skipped with a
  warning instead of being sent. An unauthenticated status ping is answered to
  anyone, so an oversized icon turned a 30 byte request into a multi megabyte
  reply. MOTD files are capped at 8 kB for the same reason, and the icon is
  base64 encoded once per change instead of once per ping.
- `watcher.allowed_hostnames` no longer rejects Forge players and players
  behind a forwarding proxy. Both append their own fields after a NUL byte to
  the address in the handshake, so `mc.example.org` arrived as
  `mc.example.org\0FML3\0` and never matched. Only the part before the first
  NUL is compared now, and a trailing dot is ignored.
- The Login Success packet no longer carries a stale `0x01` strict error
  handling byte for clients whose protocol is outside 766-767, which crashed
  1.21.2+ and other versions with `DecoderException: 1 extra byte`.

### Changed

- Minimum supported version in transfer mode is 1.20.5 (protocol 766), the
  first version with the clientbound Transfer packet. Proxy mode still supports
  1.7.6+.

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
