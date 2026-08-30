# Changelog

Notable changes per release. The section matching a tag becomes that release's
notes, so a version has to be listed here before it is tagged.

## [Unreleased]

### Added

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
- `setup-ssh` can now install `mc-wol-remote` on the server, a helper script
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

### Fixed

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

- `mc-wol-proxy init` asks for your settings and writes `config.yml`. It finds
  the server's MAC address itself by pinging the IP and reading the ARP cache,
  and derives the broadcast address from the same IP.
- `mc-wol-proxy setup-ssh` creates the key and installs it in `authorized_keys`
  over a one time password login, restricted to `docker start` by default. It
  shows the host key fingerprint and asks before trusting it.
- `mc-wol-proxy check` tests the whole setup and names the step that is broken.
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
