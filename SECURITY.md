# Security

Two things about this project deserve a second thought. It puts a program on a
port that anybody on the internet can reach, and that program can switch on a PC
inside your home.

This page says what is exposed, what the settings already protect you from, and
what you can lock down further if you want to. The [README](README.md) is about
getting it running. The reasoning lives here.

The short version, if you stop reading here: the defaults are fine for a home
server. Switch the Minecraft whitelist on. `mcwod setup-ssh` has already limited
its own key to starting your server and nothing else.

## What can reach what

| Part | Who can reach it | What it asks for |
|------|------------------|------------------|
| Watcher, port 25565 | anybody on the internet, through your router | nothing, on purpose |
| Minecraft server | the watcher, or the internet in transfer mode | a real Minecraft account |
| SSH on the server PC | the watcher, inside your network | one key, tied to one command |
| RCON | only the Minecraft container's own network | the password in `server/.env` |

The watcher cannot check who anybody is. A Minecraft client says hello before any
account check happens, so the watcher has to read packets from strangers, and
strangers is who reaches port 25565. Everything below exists because of that one
fact.

## What stops somebody abusing it

### Waking the PC is rate limited

One login attempt on a sleeping server sets off a wake-up packet, an SSH call
and a container start. Without a limit somebody could set that off over and
over. `limits.boot_cooldown`, ten seconds by default, is the shortest gap
between two attempts. Ten seconds is deliberately short: waking the PC takes
longer than that anyway, so a real player never waits on the limiter, while one
packet a second still cannot become one wake a second.

The real protection is what happens after a failure. If a wake-up does not end
with a reachable server, the next attempt waits `limits.boot_failure_backoff`,
and the wait doubles with every further failure up to `limits.boot_max_backoff`.
A server that cannot come up is not retried on every connection. The first
success resets it. Anybody who arrives during a wait gets a proper "not now"
message rather than a dropped connection.

### Only so many connections at once

People logging in are limited to the player slots the server itself reports,
because more players cannot get in anyway, and `limits.max_logins` replaces that
with a fixed number if you want one. Server list pings get their own pool, five
times as large, never smaller than 64.

Keeping the two apart matters. Sharing one pool would let a handful of
open server list entries eat every slot a player could have used, and a full
server would show up as blank in everybody else's list. `limits.max_per_ip`,
eight by default, applies to each pool on its own, so a household behind one
internet connection can play and keep the list open at the same time. Without
any of this a plain flood of connections could run a Raspberry Pi out of memory.

### What it sends back has a size limit

A server list ping is answered to anybody who asks, and the question is 30 bytes
long. If the answer were megabytes, somebody could use the watcher to fling that
traffic at a target of their choosing. So pictures are capped at 64 kB and have
to be the 64x64 that Minecraft demands, message files at 8 kB, and anything
bigger is skipped with a line in the log. The answer from the real server is
read by the length it announces and capped at 256 kB, rather than trusted to fit
whatever the watcher happened to read.

### The part that reads packets assumes the worst

Numbers longer than the protocol allows are rejected, a packet that stops
halfway through returns an error instead of reading past the end, and the first
exchange has time limits so a connection that says nothing cannot be held open
forever. Names are held to the 16 characters the protocol allows, have to be
valid text, have to come with a complete ID, and anything unprintable is
stripped out before the name reaches a log line.

### A player cannot pick where transfer mode sends them

The watcher hands the client an address to reconnect to: the local one for
clients coming from inside your network, the public one for everybody else. It
decides from the address the connection actually came from, which nobody can
fake by asking nicely, and `transfer.local_networks` narrows what counts as
inside. The most a local client can gain is the address it could have typed
itself.

### Nothing is ever handed to a shell

In normal operation the watcher starts no programs at all. SSH and ping are
libraries built into it, so there is no command line for a config value to
escape from. `server.container_name` is still checked against what Docker
accepts, because it is the one value that ends up inside the command sent to the
server.

### Bad settings stop it at startup

A malformed MAC address, a port outside 1 to 65535, an unknown wake-up mode or a
message file that is not valid JSON stops the watcher right away with a line
naming the setting, instead of failing later at the exact moment somebody tries
to join.

### One slow answer cannot hold up the rest

Every connection is handled on its own, and the question "is the server awake"
is asked once and remembered for two seconds. Otherwise a few connections a
second would each start their own check against a PC that is asleep, which is
its normal state.

## Your secrets

`config.yml` holds your DuckDNS token, the MAC address of your server and your
local addresses. It is kept out of git, and only `config.example.yml`, which has
placeholders in it, is tracked. Do not put your real values into the example
file.

The file is written so only its owner can read it, mode 600, every time: by the
installer, by `mcwod init` and by `mcwod config`. That last one matters, because
writing to a file that already exists leaves its permissions alone, and the token
has to stay unreadable to other accounts on the watcher. `config` also edits the
file you have rather than writing a fresh one from scratch, so a comment you put
next to your own token survives.

The token is not echoed back to the screen while you type it.

`server/.env` holds the RCON password. Compose refuses to start without one
instead of falling back to something predictable. RCON's port 25575 is not
published to the outside, and it should stay that way.

## The SSH key

The watcher holds a key that opens your server PC. That is worth restricting, and
`mcwod setup-ssh` restricts it for you. The line it writes into `authorized_keys`
on the server looks like this:

```
command="docker compose --project-directory '/srv/minecraft' up -d",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty ssh-ed25519 AAAA... watcher@host
```

Everything before the key is a fence. Whoever holds that key can bring that one
project up and do nothing else: no shell, no forwarded ports, no other command,
however they ask. Without a known compose folder the command is
`docker start <container>` instead. You get this unless you turn it down.

### If you also let it send the PC to sleep

Saying yes to the sleep question in `setup-ssh` widens what the key can do, and
it is worth understanding before you agree. The fence becomes a small script
instead of a single command:

```
command="/usr/local/bin/mcwod-remote",no-port-forwarding,... ssh-ed25519 AAAA... mcwod
```

That script knows exactly six words and refuses everything else:

| Word | What it runs |
|------|--------------|
| `hello` | prints a marker, so `check` can tell the script is really there |
| `start` | `docker compose up -d`, or `docker start <container>` |
| `stop` | `docker compose stop`, or `docker stop <container>` |
| `status` | asks Docker whether the container is running |
| `players` | asks Minecraft who is online |
| `sleep` | the one power command you picked |

Whatever arrives over SSH is only ever compared against those six words, never
run. There is no seventh thing to ask for. The script is installed owned by root
and not writable by anybody else, which is the part that makes it work: if the
account the key logs into could edit the script, the restriction would be worth
nothing.

Sending the PC to sleep needs root, because `systemctl suspend` over SSH runs
into the permission system, which does not count an SSH session as somebody
sitting at the machine. So there is one line allowing exactly one command:

```
youruser ALL=(root) NOPASSWD: /usr/bin/systemctl suspend
```

No wildcard, so it cannot be talked into running some other `systemctl` command.
It is written to `/etc/sudoers.d/mcwod` only after the system's own checker
accepts it, because a broken file in that folder locks you out of your own
machine.

What you are agreeing to: if somebody takes over the watcher, they can switch the
server PC off as well as on. That is annoying rather than dangerous. The same
watcher can wake it again, and the container is stopped before a hibernate or a
shutdown so the world is written to disk first. If you would rather not, say no
and the key stays limited to starting the server.

### Where the key comes from

The watcher makes its own key at `~/.ssh/mcwod` and uses no other one, unless
`server.ssh_key_path` points somewhere else. It never picks up `~/.ssh/id_ed25519`.
A key sitting at that default path is the one its owner logs into everything with,
and that does not belong to a service facing the internet. Point the setting at
such a key by hand and `check` will say so.

The key must not be readable by other users and must not have a passphrase. The
watcher refuses to start otherwise: the first because that is the rule SSH itself
applies, the second because a program running on its own cannot type one.

### The one time you type your password

`init` offers to log into the server once with your password and set everything
up from there, and `setup-ssh` does the same for the key alone. That is the only
moment this project touches your server password, and it is handled like this:

- It is not echoed to the screen, it lives in memory for that one connection,
  and it is never written to disk.
- Handed to SSH, and where root is needed, to `sudo` through its input. It never
  appears on a command line, because command lines are visible to every other
  account on that machine.
- Never put in a log line or an error message.

The server is identified before the password is sent. On first contact the
fingerprint is printed and you have to say yes, no matter what
`server.ssh_strict_host_key` is set to, because a password given to an
unverified machine is a password given to whoever is in the middle. A
fingerprint that changed after you trusted it is a hard stop with no way to
click past it.

What that one session changes on your server, all of it announced first:

| Change | Needs root |
|--------|------------|
| the public key added to `authorized_keys` | no |
| Wake-on-LAN armed, plus a small unit that re-arms it after every boot | yes |
| `/usr/local/bin/mcwod-remote`, only if you asked for auto-sleep | yes |
| `/etc/sudoers.d/mcwod`, same | yes |

Everything else it does is reading: the MAC address, the network card, the list
of containers, the published port, whether RCON is on, what the machine can do
about sleeping.

### Recognising the server again

`server.ssh_strict_host_key` decides how strict that is:

| Value | What happens |
|-------|--------------|
| `accept-new` (default) | trust the server the first time and note its fingerprint, refuse if it ever changes |
| `yes` | only talk to a server whose fingerprint is already noted |
| `no` | talk to anything, and log the fingerprint of whatever it talked to |

A changed fingerprint is a hard stop in both `accept-new` and `yes`. That is the
entire point of the check, and the message names the two things it can mean: the
server was reinstalled, or somebody is sitting in the middle of the connection.

`accept-new` still takes the very first connection on trust. To close that gap,
note the fingerprint yourself before starting the watcher and switch to `yes`:

```bash
ssh-keyscan -H 192.168.1.100 >> ~/.ssh/known_hosts
```

`mcwod setup-ssh` closes it another way: it shows you the fingerprint and asks
before trusting it, so you can compare it against the server.

Where that list of fingerprints lives depends on how the watcher runs. Installed
normally it is `/opt/mcwod/known_hosts`, which lets the service keep its home
folder read only. In Docker it is `watcher/state/known_hosts`, a mounted folder
rather than a mounted file, because Docker invents a folder where a mounted file
does not exist yet. That is what used to throw the accepted fingerprint away
every time the container was recreated.

### Whose SSH this is

SSH is `golang.org/x/crypto/ssh`, the Go team's own, built into the program. The
watcher implements no cryptography itself. What it does implement is the
fingerprint policy above, on top of the library that reads the same
`known_hosts` file OpenSSH writes.

The trade worth knowing about: if this used your system's `ssh` command, a system
update would patch it for you. Built in, it is patched when a new release of this
project is built. Dependabot watches those libraries weekly and opens a pull
request, and the release workflow turns that into new binaries, but the
responsibility now sits with this repository instead of your package manager. If
you build from source, pull and rebuild after such an update.

`govulncheck` runs on every push and once a week besides, so a freshly published
weakness shows up without anyone having to commit anything. It only reports the
ones this code actually reaches, which keeps its findings worth acting on.

## The container and the service

The Docker image is built on nothing at all: the program and a list of
certificate authorities, and that is the entire image. There is no shell, no
package manager and no interpreter in there for anybody to make use of. It gives
up every special permission except the one ping needs, cannot gain new ones, and
its filesystem is read only.

It has to use the host's network, because a wake-up packet has to go out on the
real network to be heard.

The service file gets the same treatment: no new permissions, the system
protected from writes, a read only home, one writable folder, and the one
capability that lets it ping without being root.

Image tags and the Minecraft version are pinned, so restarting a container never
quietly pulls a different build than the one you looked at.

## Checking what you install

Release files are built by the workflow in `.github/workflows/release.yml` and
published with a `checksums.txt`, which is a list of fingerprints. `install.sh`
downloads that list and refuses to install a file whose fingerprint does not
match, or one that is not on the list at all. If you would rather not trust the
release at all, build it yourself with `sudo ./install.sh --build`.

Every file also carries proof of where it was built, so you can check that your
download really came out of that workflow and that commit, rather than from
somebody who got at the release page:

```bash
gh attestation verify mcwod_linux_arm64 --repo posch-dev/minecraft-wake-on-demand
```

That is not the same as a signed program. Windows will still warn you about an
unsigned download, because signing needs a certificate bought from a certificate
authority and this project does not have one.

`mcwod update` follows the same rule. It fetches the fingerprint list next to the
file and refuses to install on a mismatch, or when the file is not listed, since
that check is the only thing standing between a download link and running
whatever came back. It also refuses to follow a redirect away from the release
host, so a hijacked link cannot send the download somewhere else. The new program
is written next to the old one and renamed over it, so a download that dies
halfway cannot leave a half written file where the service expects a program.

**The watcher never updates itself.** `update` asks before it does anything, and
the running watcher will never replace its own program.

`install.sh` and `update` both read `MCWOD_REPO`, `MCWOD_API_BASE` and
`MCWOD_DOWNLOAD_BASE` from the environment when they are set, and `install.sh`
also reads `MCWOD_INSTALL_DIR`. They exist for mirrors and for testing. Whatever
you point them at is trusted completely, so only point them at something you run
yourself.

### The update check and your address

`init`, `config` and `check` ask GitHub once a day whether a newer release
exists, and print one line if there is. That request tells GitHub the address the
watcher sits behind, which for most people is their home connection. The answer
is kept for 24 hours in `.update-check.json` next to the config, the request
gives up after two seconds, and failing is silent, so nothing depends on it.

`update.check: false` in `config.yml` switches it off completely. Running
`mcwod update` by hand still works.

## Things you can tighten

- Set `watcher.listen_address` to a single local address, if the watcher has more
  than one network card and only one of them should be answering Minecraft.

- Set `watcher.allowed_hostnames` to your public domain, and your local address if
  people use that too. Anybody coming
  from outside who asks for something else is dropped, so port scanners and
  crawlers do not even get a sleeping server back. The cost is that players who
  type your raw IP instead of the name are turned away as well. With DuckDNS
  switched on, your domain is filled in automatically. Forge and proxies tack
  their own bits onto the address, which is ignored when matching, so those
  players are unaffected.

- Switch the Minecraft whitelist on if the server is for a fixed group of people.
  This is the one that matters most, and it is two clicks in `mcwod players`.

- Put a firewall in front of the transfer port, 25566, if you use transfer mode. That port goes straight
  to the Minecraft container, so the watcher cannot filter it and a port scanner
  reaches the server without passing the hostname check. This closes the gap by
  only opening 25566 to addresses that recently talked to the watcher:

  ```bash
  # Addresses that hit the watcher in the last 120 seconds get 25566 opened.
  # Everything else is dropped before it reaches the container.
  iptables -A INPUT -p tcp --dport 25565 -m recent --set --name MCKNOWN
  iptables -A INPUT -p tcp --dport 25566 -m recent --rcheck --seconds 120 --name MCKNOWN -j ACCEPT
  iptables -A INPUT -p tcp --dport 25566 -j DROP
  ```

  UFW cannot express the "recently seen" part, so either write raw iptables rules
  and save them (`/etc/iptables/rules.v4`, or a netfilter-persistent unit), or let
  UFW handle the broad allow and drop and append these afterwards with
  `iptables -A` so they land in the right place.

- Note the server's fingerprint yourself and switch to
  `ssh_strict_host_key: "yes"`.

- Keep the pinned image tags current. They do not move on their own, which is the
  point, and which makes updating them your job.

- Watch for Dependabot pull requests on `golang.org/x/crypto`, and cut a release
  when one lands.

## Reporting something

Open an issue for anything low risk. For something that could actually be used
against a running setup, please report it privately through GitHub's security
advisories instead of in a public issue.
