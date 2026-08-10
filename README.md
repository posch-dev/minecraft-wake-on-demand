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
- A Linux or Windows machine that's always running
- Python 3 or Docker

**Server** (the PC that sleeps):
- Docker with [itzg/minecraft-server](https://github.com/itzg/docker-minecraft-server)
- SSH server running
- Wake-on-LAN enabled in BIOS and OS

**Network:**
- Port `25565` forwarded to the watcher on your router
- Server PC has a static IP or DHCP reservation

## Quick start

The steps are ordered so the reboots come first and you only edit the config once, when you already have every value it asks for.

### 1. Prepare the server PC

This is the BIOS and reboot part, so it goes first:

- Enable Wake-on-LAN in the BIOS/UEFI
- Enable WoL in the operating system's network adapter settings as well, both have to be on
- Give the PC a static IP or a DHCP reservation in your router
- Set the operating system to sleep after 30 minutes of inactivity or so

Write down the **MAC address** and the **local IP** of the PC, you need both in step 6.

Nothing in this project sends the server PC to sleep, that part is the power settings above. The Minecraft container pauses itself when the last player leaves, so the PC goes idle on its own and the sleep timer takes over from there.

### 2. Get a DuckDNS subdomain (recommended)

Most home connections change their public IP every few days, which would leave your friends with a dead address. DuckDNS gives you a name that follows it.

1. Get a free subdomain at [duckdns.org](https://www.duckdns.org/)
2. Write down the subdomain and the token, they go into the config in step 6

You can skip this if you have a fixed public IP, or if nobody outside your network is going to join. Set `duckdns.enabled: false` in step 6 and your friends use your public IP instead.

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

### 5. Set up SSH access

The watcher needs to be able to SSH into the server to run `docker start minecraft`. Generate a key on the watcher:

```bash
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519 -N ""
ssh-copy-id -i ~/.ssh/id_ed25519.pub user@server-ip
```

**Recommended:** lock the key down so it can only start the container. On the server, edit `~/.ssh/authorized_keys` and put this in front of the key:

```
command="docker start minecraft",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty ssh-ed25519 AAAA... watcher@host
```

> On a Windows server, use the full path: `"C:\Program Files\Docker\Docker\resources\bin\docker.exe start minecraft"`

### 6. Edit the watcher config

On the watcher, copy the example config:

```bash
cp config.example.yml config.yml
```

Edit `config.yml`, never `config.example.yml`. Everything you noted down in the earlier steps goes in here:

| Setting | What to put |
|---------|-------------|
| `server.mac` | MAC address of your server PC, from step 1 |
| `server.ip` | Local IP of your server PC, from step 1 |
| `server.ssh_user` | Your SSH username on the server |
| `duckdns.enabled` | `false` if you skipped step 2, then ignore the two rows below |
| `duckdns.domain` | Your DuckDNS subdomain, without `.duckdns.org` |
| `duckdns.token` | Your DuckDNS token |

Everything else has sensible defaults. Check the comments in the file for details.

### 7. Start the watcher

**Linux with Docker** (recommended):
```bash
cd watcher
touch known_hosts
docker compose up -d
```

**Linux with systemd** (good for Raspberry Pi):
```bash
sudo ./watcher/install.sh
```

**Windows:**
1. Install PyYAML: `pip install pyyaml`
2. Run `watcher\windows-start.bat`
3. For autostart: put a shortcut to `watcher\windows-start.vbs` in your `shell:startup` folder

### 8. Connect and play

Everyone connects to the **watcher**, never to the server PC directly. The server PC is asleep, so nothing there would answer.

| Who | Address to enter in Minecraft |
|-----|-------------------------------|
| Friends outside your network | `your-domain.duckdns.org:25565`, or your public IP if you skipped step 2 |
| You, on the same network as the server | the watcher's local IP and port, for example `192.168.1.50:25565` |

Always add the port. Minecraft is supposed to assume `25565` on its own, but depending on the client it doesn't, and the address then just fails to connect. Writing it out always works. If you changed `listen_port` in `config.yml`, use that number instead.

Why the split: your router only forwards connections that arrive from the outside. Many routers can't send a connection from inside your network back in through your own public address. If yours can (it's called NAT loopback or hairpinning), the public address works from home too and you can use one address everywhere.

**The first join:**

1. Add the server in Minecraft. The list shows "Server is sleeping, connect to wake it up!"
2. Click Join. The watcher wakes the PC and starts the container.
3. This takes roughly 30 to 60 seconds. Minecraft usually gives up before that and shows a timeout, or you get "Server is waking up, please reconnect in a moment".
4. Click Join again. Now you're in.

Only the first player after a sleep goes through this. Everyone joining while the server is already up connects straight away.

---

## Extra options

### Transfer mode

By default, all traffic flows through the watcher. With transfer mode, the watcher only handles the wake-up part, then redirects the player directly to the server. Better performance, but needs an extra port forward.

To enable:
1. Forward a second port (e.g. `25566`) on your router directly to the server PC
2. Set `accepts-transfers=true` in the server's `server.properties`
3. In `config.yml`:
```yaml
transfer:
  enabled: true
  host: "your-domain.duckdns.org"
  port: 25566
```

Players still connect to the watcher as described in step 8, the redirect happens by itself.

Players on your own network are not sent out to your public address. The watcher looks at where the connection came from and redirects anyone with a local IP straight to `server.ip` instead, so no router loopback is involved and you get the same direct connection your friends get.

If your network uses addresses the watcher doesn't recognise as local, list them explicitly:

```yaml
transfer:
  local_networks: ["192.168.1.0/24"]
```

Left empty, every private address counts as local, which is what you want in a normal home network.

### Custom MOTD and server icon

You can customize what players see in their server list when the server is sleeping. Edit the files in `assets/`:

| File | What it does |
|------|-------------|
| `assets/motd-sleeping.json` | Text shown when server is off |
| `assets/motd-starting.json` | Text shown while server is booting |
| `assets/server-icon.png` | 64x64 PNG for the server list |

### WoL modes

`broadcast` (default, recommended) sends the wake packet to the whole subnet. Works reliably even after the server has been off for a while.

`unicast` sends directly to the server IP. Can fail if the router has forgotten the server's MAC address.

Set `wol.mode` in `config.yml`.

---

## Troubleshooting

**Server won't wake up:**
- Double-check WoL is enabled in BIOS *and* OS network settings
- Verify the MAC address in `config.yml`
- Try switching between broadcast and unicast mode

**SSH not working:**
- Test it manually: `ssh -i ~/.ssh/id_ed25519 user@server-ip docker start minecraft`
- Make sure the key is in the server's `authorized_keys`

**Friends can connect but you can't, or the other way round:**
- From inside your network, use the watcher's local IP, not the public address (see step 8)
- From outside, check that port `25565` is forwarded to the watcher and not to the server PC

**Server shows up as offline in the list:**
- Make sure the address includes the port, for example `your-domain.duckdns.org:25565`
- The watcher isn't running or isn't reachable, check its logs below

**Minecraft not starting:**
- Check the container exists on the server: `docker ps -a`
- Check container logs: `docker logs minecraft`

**Checking watcher logs:**
- Docker: `docker compose logs -f` (in the `watcher` folder)
- systemd: `journalctl -u mc-wol-proxy -f`
- Windows: check the terminal output from `windows-start.bat`

## Security

The watcher sits on a port that anyone on the internet can reach, and it can
power on a PC in your home network. If you want to know what that means, what
the defaults already protect against, and what you can lock down further, it is
all written up in [SECURITY.md](SECURITY.md).

The short version: the defaults are safe for a home setup, and the one thing
worth doing beyond this guide is restricting the SSH key as shown in step 5.

## Credits

[itzg/minecraft-server](https://github.com/itzg/docker-minecraft-server): Minecraft server Docker image

## License

MIT, see [LICENSE](LICENSE).
