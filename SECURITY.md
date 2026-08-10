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
| SSH on the server PC | the watcher, on the LAN | key only, restricted command |
| RCON | the docker network on the server PC | password from `server/.env` |

The watcher cannot authenticate anyone. A Minecraft client speaks its handshake
before any account check happens, so the proxy has to accept and parse packets
from strangers. Everything below exists because of that.

## Design decisions

**The proxy never blocks on network calls.** Reachability is probed with
`asyncio.open_connection` and the result is cached for two seconds behind a lock.
A synchronous probe in the event loop would let a handful of connections per
second freeze the whole watcher while the server is asleep, which is its normal
state.

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

**The packet parser is defensive.** VarInts are rejected past 35 bits,
incomplete reads raise instead of indexing out of bounds, and the initial client
exchange has timeouts so a half open connection cannot be held forever.
Usernames are limited to the protocol's 16 characters, must decode as UTF-8, must
come with a complete 16 byte UUID, and are stripped of non-printable characters
before they reach the log.

**Transfer targets depend on where the client came from.** In transfer mode the
watcher hands the player a new address to connect to. Clients from a private
address get `server.ip` on the LAN, everyone else gets the public
`transfer.host`. The decision is made from the socket's peer address, which a
client cannot choose for itself, and `transfer.local_networks` narrows what
counts as local. The only thing a local client gains is the address it would
have reached anyway.

**Commands are argument lists.** No `shell=True` anywhere, so an address or
container name cannot break out into a shell. Config is read with
`yaml.safe_load`.

## Your secrets

`config.yml` holds your DuckDNS token, the server's MAC address and your local
IPs. It is git-ignored and only `config.example.yml` with placeholders is
tracked. Never move your real values into the example file.

The installer copies the config to `/opt/mc-wol-proxy/config.yml`, chowns it to
the service user and sets mode 600, so the token is not world readable on the
watcher.

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

Host key checking is controlled by `server.ssh_strict_host_key`:

| Value | Behaviour |
|-------|-----------|
| `accept-new` (default) | trust the key on first sight, refuse if it ever changes |
| `yes` | only accept a key that is already in `known_hosts` |
| `no` | accept anything, logs a warning at startup |

`accept-new` still takes the first connection on trust. To close that window,
pin the key before starting the watcher and switch to `yes`:

```bash
ssh-keyscan -H 192.168.1.100 >> ~/.ssh/known_hosts
```

In Docker the accepted key is stored in `watcher/known_hosts`, which is mounted
into the container. Without that file the key would be trusted anew after every
container recreate, which defeats the point.

## Container hardening

The watcher container drops all capabilities and adds back only `NET_RAW` for
ping, runs with `no-new-privileges`, and has a read-only root filesystem with a
tmpfs on `/tmp`. Host networking is required because Wake-on-LAN broadcasts on
the real LAN.

Image tags and the Minecraft version are pinned, so restarting a container never
pulls a different build than the one you reviewed.

## Things you can tighten

- Set `watcher.listen_address` to a single LAN IP if the watcher has more than
  one interface and only one of them should serve Minecraft.
- Turn on the Minecraft whitelist if the server is meant for a fixed group.
- Pin the SSH host key and switch to `ssh_strict_host_key: "yes"`.
- Keep the pinned image tags current, they do not update themselves.

## Reporting a vulnerability

Open an issue for anything low risk. For something that could be exploited
against a running deployment, please report it privately through GitHub's
security advisories rather than in a public issue.
