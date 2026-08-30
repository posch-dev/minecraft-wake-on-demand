# Minecraft Wake-on-Demand

Your Minecraft server sleeps when nobody's playing and wakes up automatically when someone connects. No extra software on the player side, they just click "Join" and wait a few seconds.

A small always-on device (like a Raspberry Pi) sits in your network, intercepts the connection, sends a Wake-on-LAN packet to your server PC, starts the Minecraft container, and connects the player. When everyone leaves, the server goes back to sleep.

## How it works

```
Player connects
       |
  [Watcher PC]  (always on, e.g. Raspberry Pi)
       |
  Is someone joining?
   /         \
 No           Yes
 |             |
Show MOTD     Send WoL packet
"sleeping"     → Server PC wakes up
               → Minecraft container starts
               → Player gets connected
```

## What you need

**Watcher** (the always-on device):
- A Linux or Windows machine that's always running (e.g. Raspberry Pi)
- Nothing preinstalled, it's a single binary

**Server** (the PC that sleeps):
- Docker with [itzg/minecraft-server](https://github.com/itzg/docker-minecraft-server)
- SSH server running
- Wake-on-LAN enabled in BIOS and OS

**Network:**
- Port `25565` forwarded to the watcher on your router
- Server PC has a static IP or DHCP reservation

## Quick start

### 1. Prepare the server PC

- Enable Wake-on-LAN in the BIOS/UEFI
- Enable WoL in the operating system's network adapter settings
- Give the PC a static IP or a DHCP reservation in your router

- Write down the **local IP** and **MAC address** of the PC.

The watcher can also put the PC back to sleep once nobody is playing, see [Auto-sleep](#auto-sleep) below. It is off until you turn it on, and the Minecraft container pauses itself once the last player leaves either way, so the machine really does go idle.

### 2. Get a DuckDNS subdomain (recommended)

Most home connections change their public IP every few days, which would leave your friends with a dead address. DuckDNS gives you a name that follows it.

1. Get a free subdomain at [duckdns.org](https://www.duckdns.org/)
2. Write down both, the subdomain and the token, step 5 asks for them

You can skip this if you have a fixed public IP, or if nobody outside your network is going to join.

### 3. Forward the port

Forward port `25565` (TCP) on your router to the **watcher** PC, not to the server PC.

Skip this too if you only play on your own network.

### 4. Start the Minecraft server once

On your server PC:

```bash
cd server
cp .env.example .env
```

Open `.env` and set both values, compose refuses to start without them:

| Variable | What to put |
|----------|-------------|
| `RCON_PASSWORD` | any password you make up, the backup container uses it |
| `MC_VERSION` | the Minecraft version you want to play, for example `26.2` |

Then start it once:

```bash
docker compose up -d
```

After this, the watcher handles starting it via SSH. You don't need to touch it again.

### 5. Install and set up the watcher

**Linux:**

```bash
sudo ./watcher/install.sh
```

**Windows:**

1. Download `mcwod_windows_amd64.exe` from the [releases page](https://github.com/posch-dev/minecraft-wake-on-demand/releases)
2. Rename it to `mcwod.exe` and put it in the `watcher` folder
3. For autostart later, put a shortcut to `watcher\windows-start.vbs` in your `shell:startup` folder

**Docker:**

```bash
cp config.example.yml config.yml
```

Fill in `config.yml` by hand, then `cd watcher && docker compose up -d`. The three commands below are available in the other two setups, the table further down lists what goes in the file.

---

Now let the watcher set itself up. On Linux, `install.sh` prints these three lines with the right paths already filled in:

```bash
mcwod init        # asks a handful of questions and writes config.yml
mcwod setup-ssh   # creates the SSH key and installs it on the server
mcwod check       # confirms everything is wired up
```

**`init`** asks for the server's address, the user to log in as, and your DuckDNS details if you want them. Then it offers to log in once with that user's password and set the rest up itself: it reads the MAC address and broadcast address off the server, lists the containers so you can pick one instead of typing the name, checks whether Wake-on-LAN is armed in the network driver and offers to turn it on, and installs its own SSH key. The password is used for that one login and is not stored anywhere. Say no and it asks the questions instead, which works just as well.

**`update`** installs a newer release. It shows what changed, asks first, and verifies the download against the checksums published with the release. Nothing is ever installed without being asked, and the watcher never updates itself in the background. `init`, `config` and `check` mention a new version in one line when there is one. That check asks GitHub once a day, which tells GitHub your IP, so set `update.check: false` in `config.yml` if you would rather it did not.

**`mcwod`** on its own is the place to start. The first time it walks you through the setup, after that it shows what your server is doing and asks what you want to do.

**`worlds`** keeps more than one world and switches between them. Each gets its own folder on the server and only one runs at a time, so your friends always reach the one you picked. Switching warns you if anyone is playing.

It also changes a world's Minecraft version or server kind. That always makes a backup first, because a world that has been opened in a newer version cannot go back to an older one. Going backwards is allowed if you insist, but it tells you what will happen first.

**`players`** is where you say who may join and who is an admin. It reads and writes your server's own settings, so what you set here survives the container being rebuilt. `whitelist` is the same command.

**`config`** changes an existing setup through a menu, so nothing has to be edited by hand. `edit` and `settings` are the same command. Your own comments in `config.yml` are kept.

**`setup-ssh`** creates the key, shows you the server's host key fingerprint so you can confirm it is the right machine, asks for your server password once, and installs the key. By default it restricts the key so it can only start your server, which means a leaked key cannot do anything else. The password is used for that one login and is not stored.

**`check`** goes through the whole setup and tells you which step is wrong in plain words. Run it any time something misbehaves.

If you would rather fill the config in by hand, copy `config.example.yml` to `config.yml` and set these:

| Setting | What to put |
|---------|-------------|
| `server.mac` | MAC address of your server PC |
| `server.ip` | Local IP of your server PC, from step 1 |
| `server.ssh_user` | Your login name on the server |
| `duckdns.enabled` | `false` if you skipped step 2, then ignore the two rows below |
| `duckdns.domain` | Your DuckDNS address, with or without `.duckdns.org` |
| `duckdns.token` | Your DuckDNS token |

Everything else has sensible defaults. Check the comments in the file for details.

### 6. Connect and play

Everyone connects to the **watcher**, never to the server PC directly. The server PC is asleep, so nothing there would answer.

| Who | Address to enter in Minecraft |
|-----|-------------------------------|
| Friends outside your network | `your-domain.duckdns.org:25565`, or your public IP if you skipped step 2 |
| You, on the same network as the server | the watcher's local IP and port, for example `192.168.1.50:25565` |

Always add the port. Minecraft is supposed to assume `25565` on its own, but depending on the client it doesn't, and the address then just fails to connect. Writing it out always works. If you changed `listen_port` in `config.yml`, use that number instead.

**The first join:**

1. Add the server in Minecraft. The list shows "Server currently asleep" and under it "Join to wake it up"
2. Click Join. The watcher wakes the PC and starts the container.
3. This takes roughly 30 to 60 seconds. Minecraft usually gives up before that and shows a timeout, or you get "Server is waking up, please reconnect in a moment".
4. Click Join again. Now you're in.

Only the first player after a sleep goes through this. Everyone joining while the server is already up connects straight away.

---

## Commands

| Command | What it does |
|---------|--------------|
| `mcwod` | starts the watcher, this is what the service runs |
| `mcwod init` | asks for your settings and writes `config.yml` |
| `mcwod setup-ssh` | creates the SSH key and installs it on the server |
| `mcwod check` | tests the setup and reports what is missing |
| `mcwod version` | prints the version |

The config is looked for in `MCWOD_CONFIG`, then next to the binary, then one directory above it.

## Extra options

### Transfer mode

By default, all traffic flows through the watcher. With transfer mode, the watcher only handles the wake-up part, then redirects the player directly to the server. Better performance, but needs an extra port forward.

`mcwod init` offers it when you set up DuckDNS, and `mcwod config` turns it on or off later. To do it by hand:

1. Forward a second port (e.g. `25566`) on your router directly to the server PC
2. Set `accepts-transfers=true` in the server's `server.properties`, unless MCWOD set the server up, which always does
3. In `config.yml`:
```yaml
transfer:
  enabled: true
  host: "your-domain.duckdns.org"
  port: 25566
```

`mcwod init` offers this as a question, so you can also set it up there.

Players still connect to the watcher as described in step 6, the redirect happens by itself.

If your network uses addresses the watcher doesn't recognise as local, list them explicitly:

```yaml
transfer:
  local_networks: ["192.168.1.0/24"]
```

Left empty, every private address counts as local, which is what you want in a normal home network.

### Minecraft version compatibility

The watcher speaks the modern Minecraft protocol (Java Edition) and supports:

- **Transfer mode** (`transfer.enabled: true`): **1.20.5** or newer. This is the earliest version with the clientbound Transfer packet that the watcher uses to hand players off.
- **Proxy mode** (`transfer.enabled: false`): all versions back to **1.7.6** (protocol 5), since it is plain TCP forwarding.

The server list ping the watcher answers while the server is asleep now reports the real server version, learned from a previous status probe and cached in `.server-info.json` next to the config. Before the server has been reached once, it echoes the client's own protocol version so the signal bars stay green.

**Strict error handling** is a boolean field inside the Login Success packet that only exists in protocols 766-767 (1.20.5 and 1.21/1.21.1). The watcher includes it for those versions and omits it everywhere else, because an extra byte crashes modern clients with a `DecoderException`.

### Custom MOTD and server icon

Out of the box the watcher answers with its own icon, three blue Z that grow, the largest turning into a red exclamation mark while the PC boots. It is built into the binary, so there is nothing to install for it.

Everything below is optional and goes in `watcher/assets/`. Ready to copy examples live in `watcher/assets/examples/`.

| File | What it does |
|------|-------------|
| `motd-sleeping.json` | text shown while the server is off |
| `motd-starting.json` | text shown while it is booting |
| `motd-live.json` | text shown while it is running, replacing the server's own MOTD |
| `motd-login-wait.json` | text the person whose join woke the server reads on the disconnect screen |
| `server-icon.png` | your 64x64 icon: plain while the server runs, at half opacity under the Z while it does not |
| `server-icon-sleeping.png` | replaces the sleeping icon outright, no Z drawn over it |
| `server-icon-starting.png` | same for the booting icon |
| `server-icon-live.png` | same for the running server, when it should differ from `server-icon.png` |

If you keep several worlds, each can have its own files in `watcher/assets/worlds/<name>/`, and anything a world does not have of its own comes from `watcher/assets/`.

A file beats the matching `motd.*` entry in `config.yml`, which beats the built-in default. One `server-icon.png` is enough for all three states, so setting your icon here means never configuring one on the Minecraft server. Without any icon file the running server's own is passed through, and `motd-live.json` does the same for the text.

`mcwod get-server-icon` copies the icon your running Minecraft server already serves into `assets/server-icon.png`, so you do not have to find the file yourself. A running watcher picks up a changed icon within a minute. `learn-server-icon` is the same command. The server has to be awake for it, and anything that was there is kept as `.bak`.

Icons have to be exactly 64x64 and under 64 kB. Anything else is skipped with a line in the log, because clients drop the whole server list entry over a wrongly sized icon.

Assets are read fresh when they change, so editing one takes effect without restarting the watcher.

### Setting the container up from the watcher

`init` offers it, and `config` has it as a menu entry. The watcher writes a `docker-compose.yml` on the server with the Minecraft container and an automatic backup container next to it, puts a generated RCON password in a `.env` with mode 600, and starts them. You never open a terminal on the server PC for this.

It asks which server you want. `VANILLA` is the unmodified game, `PAPER` and `PURPUR` are faster and take plugins, and `FABRIC`, `FORGE`, `NEOFORGE` and `QUILT` run mods, which every player then needs installed too. The Minecraft version is asked for as well, with a concrete number rather than `LATEST`, because `LATEST` means the next image pull can move your server to a new version on its own. The container images themselves are pinned for the same reason.

If there is already a compose file in that directory, the two services are added to it. Everything else in the file, other services, the top level keys, your comments, stays exactly as it was. Before anything is written a copy is kept as `docker-compose.yml.mcwod-bak-<time>`, the result has to pass `docker compose config`, and a service name that is already taken is refused rather than overwritten.

`mcwod restore-compose` puts one of those backups back, and keeps the current file as a backup on the way, so the restore itself is undoable.

It also asks for a whitelist. Name the players who may join and only they can, with the first name becoming the server operator. Leave it empty and anyone who knows the address can connect, which is how a fresh Minecraft server behaves. Worth setting if the address is reachable from the internet.

Accepting the Minecraft EULA is a separate question. Saying yes writes `EULA=TRUE` into the compose file, which is the same as accepting it yourself.

### Auto-sleep

The watcher can send the server PC back to sleep once nobody is playing. It is off by default. Turn it on in `mcwod config`, or set `sleep.enabled: true` after `setup-ssh` has installed the helper script on the server.

The operating system cannot do this for you, which is why it lives here. Windows counts user input and power requests when it decides to sleep, and a running Java process issues neither, so it would suspend in the middle of a game. Linux `logind` counts login sessions, which on a headless box means either always idle or never. Whether anyone is playing is the one fact only the watcher knows.

How it decides:

1. In proxy mode every session runs through the watcher, so counting them is free and shows a join the moment it happens. In transfer mode the client reconnects past the watcher, so there is nothing to count and it polls over SSH instead.
2. The counter only decides whether the server is worth asking. Before anything happens the watcher asks the server itself how many players are online.
3. Nobody online means it waits `sleep.confirm_delay` seconds and asks once more. Someone joining in that window cancels it.
4. An answer it cannot read counts as busy, never as empty. Leaving the machine running an hour longer is better than suspending it under someone.

| Setting | Default | What it does |
|---------|---------|--------------|
| `sleep.action` | `suspend` | `suspend`, `hibernate`, `shutdown` or `custom` |
| `sleep.idle_after` | 900 | seconds without players before sleeping is considered |
| `sleep.confirm_delay` | 60 | seconds to wait before the confirming check |
| `sleep.grace_period` | 900 | seconds after a wake in which sleeping is never allowed |
| `sleep.poll_interval` | 300 | seconds between checks, transfer mode only |

`hibernate` and `shutdown` stop the container first so the world is written out. `suspend` does not need to, the process simply resumes. Waking from a full shutdown needs Wake-on-LAN enabled in the BIOS for the powered-off state, which not every board supports.

### WoL modes

`broadcast` (default, recommended) sends the wake packet to the whole subnet. Works reliably even after the server has been off for a while.

`unicast` sends directly to the server IP. Can fail if the router has forgotten the server's MAC address.

Set `wol.mode` in `config.yml`.

### Building it yourself

The binary is built from `watcher/`. With Go installed:

```bash
cd watcher
go build -o mcwod .
```

`sudo ./watcher/install.sh --build` does the same thing and installs the result, which is the way to go on an architecture without a published release.

---

## Troubleshooting

**Start here:**

```bash
mcwod check
```

It walks through the config, the SSH key, the server PC, the container and DuckDNS in the order they depend on each other, and names the step that is broken. Most of the cases below turn into one line of output.

**Server won't wake up:**
- Double-check WoL is enabled in BIOS *and* OS network settings
- Verify the MAC address in `config.yml`
- Try switching between broadcast and unicast mode

**"No ICMP socket and no ping command available" in the log:**
- The watcher cannot tell when the PC has finished booting. In Docker, make sure `cap_add: NET_RAW` is still in `docker-compose.yml`. Under systemd, make sure `AmbientCapabilities=CAP_NET_RAW` is still in the unit file.

**Friends can connect but you can't, or the other way round:**
- From inside your network, use the watcher's local IP, not the public address (see step 6)
- From outside, check that port `25565` is forwarded to the watcher and not to the server PC

**Server shows up as offline in the list:**
- Make sure the address includes the port, for example `your-domain.duckdns.org:25565`
- The watcher isn't running or isn't reachable, check its logs below

**Minecraft not starting:**
- Check the container exists on the server: `docker ps -a`
- Check container logs: `docker logs minecraft`

**Checking watcher logs:**
- Docker: `docker compose logs -f` (in the `watcher` folder)
- systemd: `journalctl -u mcwod -f`
- Windows: check the terminal output from `windows-start.bat`

## Security

The watcher sits on a port that anyone on the internet can reach, and it can
power on a PC in your home network. If you want to know what that means, what
the defaults already protect against, and what you can lock down further, it is
all written up in [SECURITY.md](SECURITY.md).

The short version: the defaults are safe for a home setup, and the one thing
worth doing beyond this guide is restricting the SSH key, which `setup-ssh` does
for you unless you tell it not to.

## Credits

[itzg/minecraft-server](https://github.com/itzg/docker-minecraft-server): Minecraft server Docker image

## License

MIT, see [LICENSE](LICENSE).
