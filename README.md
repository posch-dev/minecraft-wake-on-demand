# Minecraft Wake-on-Demand

Your Minecraft server sleeps when nobody's playing and wakes up automatically when someone connects. No extra software on the player side — they just click "Join" and wait a few seconds.

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

### 1. Set up the server

On your server PC, start the Minecraft container once:

```bash
cd server
docker compose up -d
```

After this, the watcher handles starting it via SSH. You don't need to touch it again.

### 2. Edit the config

Open `config.yml` and fill in your values. The important ones:

| Setting | What to put |
|---------|-------------|
| `server.mac` | MAC address of your server PC |
| `server.ip` | Local IP of your server PC |
| `server.ssh_user` | Your SSH username on the server |
| `duckdns.domain` | Your DuckDNS subdomain |
| `duckdns.token` | Your DuckDNS token |

Everything else has sensible defaults. Check the comments in the file for details.

### 3. Set up SSH access

The watcher needs to be able to SSH into the server to run `docker start minecraft`. Generate a key on the watcher:

```bash
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519 -N ""
ssh-copy-id -i ~/.ssh/id_ed25519.pub user@server-ip
```

**Recommended:** Lock the key down so it can only start the container. On the server, edit `~/.ssh/authorized_keys` and add this in front of the key:

```
command="docker start minecraft",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty ssh-ed25519 AAAA... watcher@host
```

> On a Windows server, use the full path: `"C:\Program Files\Docker\Docker\resources\bin\docker.exe start minecraft"`

### 4. Enable Wake-on-LAN

- Enable WoL in your server's BIOS/UEFI
- Make sure WoL is also enabled in the OS network adapter settings
- Give the server a static IP or DHCP reservation

### 5. Forward the port

Forward port `25565` (TCP) on your router to the **watcher** PC.

### 6. Set up DuckDNS (optional but recommended)

1. Get a free subdomain at [duckdns.org](https://www.duckdns.org/)
2. Put the subdomain and token in `config.yml`
3. The watcher keeps your IP updated automatically

Set `duckdns.enabled: false` if you don't need it.

### 7. Start the watcher

**Linux with Docker** (recommended):
```bash
cd watcher
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

### Custom MOTD and server icon

You can customize what players see in their server list when the server is sleeping. Edit the files in `assets/`:

| File | What it does |
|------|-------------|
| `assets/motd-sleeping.json` | Text shown when server is off |
| `assets/motd-starting.json` | Text shown while server is booting |
| `assets/server-icon.png` | 64x64 PNG for the server list |

### WoL modes

`broadcast` (default, recommended) — sends the wake packet to the whole subnet. Works reliably even after the server has been off for a while.

`unicast` — sends directly to the server IP. Can fail if the router has forgotten the server's MAC address.

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

**Minecraft not starting:**
- Check the container exists on the server: `docker ps -a`
- Check container logs: `docker logs minecraft`

**Checking watcher logs:**
- Docker: `docker compose logs -f` (in the `watcher` folder)
- systemd: `journalctl -u mc-wol-proxy -f`
- Windows: check the terminal output from `windows-start.bat`

## Credits

[itzg/minecraft-server](https://github.com/itzg/docker-minecraft-server) — Minecraft server Docker image

## License

MIT — see [LICENSE](LICENSE).
