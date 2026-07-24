# minecraft-wake-on-demand

A self-hosted Minecraft on-demand server system. A lightweight always-on watcher intercepts Minecraft connections, wakes the sleeping server PC via Wake-on-LAN, starts the Minecraft Docker container via SSH, and forwards the connection. When no players are online, the server auto-pauses and the host PC goes back to sleep.

The watcher also keeps a DuckDNS domain updated with your current public IP.

## How it works

When a player opens the multiplayer list, the watcher responds with a custom MOTD showing the server is asleep. When the player clicks Join, the watcher sends a WoL packet, waits for the PC to boot, starts the container over SSH, and forwards the TCP connection once Minecraft is ready. No reconnect needed.

```
[Player] --> your-domain.duckdns.org:25565
                    |
           [Router Port Forward]
                    |
           [Watcher PC - always on]
            |- Status ping --> own MOTD (no WoL)
            '- Login attempt --> Boot sequence
                    |
           [WoL Magic Packet] (unicast)
                    |
           [Server PC wakes up]
                    |
           [docker start minecraft]
                    |
           [Player connected]
```

## Platform support

| Watcher | Server | Notes |
|---------|--------|-------|
| Linux (Docker) | Linux | Recommended for most setups |
| Linux (systemd) | Linux | Recommended for Pi Zero 2W or no-Docker setups |
| Linux (Docker) | Windows | OpenSSH required on server, absolute docker.exe path in authorized_keys |
| Linux (systemd) | Windows | Same as above |
| Windows | Linux | Script runs directly via .bat + .vbs autostart |
| Windows | Windows | Script directly + absolute docker.exe path in authorized_keys |

## Prerequisites

Watcher (Linux, Docker): Docker, Docker Compose, OpenSSH client

Watcher (Linux, systemd): Python 3, pip, iputils-ping, openssh-client

Watcher (Windows): Python 3.x, PyYAML (`pip install pyyaml`), OpenSSH Client (built-in since Win10)

Server (Linux): Docker, OpenSSH Server

Server (Windows): Docker Desktop (WSL2), OpenSSH Server

## Setup

### 1. Configure

Edit `config.yml`. All values have placeholder defaults with inline comments.

### 2. SSH key

Generate an SSH key on the watcher and copy it to the server:

```bash
ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519 -N ""
ssh-copy-id -i ~/.ssh/id_ed25519.pub user@server-ip
```

The systemd service runs as the user who called `sudo ./install.sh`, so it uses `~/.ssh/id_ed25519` from that user's home directory. You can override this with `ssh_key_path` in `config.yml`.

Restrict the key in the server's `~/.ssh/authorized_keys` so it can only run `docker start` (see [authorized_keys restriction](#authorized_keys-restriction)).

### 3. Enable WoL on the server PC

- Enable Wake-on-LAN in BIOS/UEFI
- Set a static IP or DHCP reservation
- Make sure the network adapter has WoL enabled in OS settings

### 4. Port forward

Forward port `25565` (TCP) on your router to the watcher PC's local IP.

### 5. DuckDNS

1. Create a free subdomain at [duckdns.org](https://www.duckdns.org/)
2. Copy your token and subdomain into `config.yml`
3. The watcher updates your public IP on startup and every `update_interval_hours` hours

### 6. Start the watcher

Linux (Docker):
```bash
cd watcher
docker compose up -d
```

Linux (systemd):
```bash
sudo ./watcher/install.sh
```
Installs, enables, and starts the service automatically.

Windows:
1. Install PyYAML: `pip install pyyaml`
2. Run `watcher\windows-start.bat` to test
3. For autostart: place a shortcut to `watcher\windows-start.vbs` in `shell:startup`

### 7. Start Minecraft (first time only)

```bash
cd server
docker compose up -d
```

After that the watcher starts the container via SSH. `restart: "no"` in the server's `docker-compose.yml` keeps it from starting on its own.

## authorized_keys restriction

Lock down the SSH key so it can only start the container:

Linux server:
```
command="docker start minecraft",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty ssh-ed25519 AAAA... watcher@host
```

Windows server:
```
command="C:\Program Files\Docker\Docker\resources\bin\docker.exe start minecraft",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty ssh-ed25519 AAAA... watcher@host
```

## WoL modes

Broadcast (recommended) sends the magic packet to the subnet broadcast address (e.g. `192.168.1.255`). Works reliably even when the ARP cache has expired (e.g. after the server has been off overnight).

Unicast sends the magic packet directly to the server's IP. This can fail if the ARP entry for the server has expired.

Set `wol.mode` in `config.yml` to `"unicast"` or `"broadcast"`.

## Transfer mode

By default, the watcher proxies the entire Minecraft connection through itself. With transfer mode enabled, the watcher only handles the wake-up sequence and then sends a Minecraft transfer packet to redirect the player directly to the server. This removes the watcher from the data path and gives players a direct connection with better performance.

To enable transfer mode:

1. Forward a second port on your router (e.g. `25566`) directly to the server PC's Minecraft port (`25565`)
2. Set `accepts-transfers=true` in the server's `server.properties`
3. Configure `transfer` in `config.yml`:

```yaml
transfer:
  enabled: true
  host: "your-domain.duckdns.org"
  port: 25566
```

```
[Player] --> :25565 --> [Watcher] --transfer--> :25566 --> [Server PC]
                          (wake up only)              (direct connection)
```

With transfer mode disabled (default), the watcher proxies all traffic:

```
[Player] --> :25565 --> [Watcher] --proxy--> [Server PC]
                     (all traffic flows through watcher)
```

## Customizing MOTD and server icon

The watcher shows a custom MOTD in the Minecraft server list when the server is sleeping or starting. You can customize these by editing the files in the `assets/` directory:

| File | Description |
|------|-------------|
| `assets/motd-sleeping.json` | Shown when the server is off |
| `assets/motd-starting.json` | Shown while the server is booting |
| `assets/server-icon.png` | 64x64 PNG shown in the server list |

Each MOTD file contains a single Minecraft JSON text component, e.g.:
```json
{"text":"Server is sleeping - connect to wake it up!","color":"yellow"}
```

For the server icon, place a 64x64 PNG as `assets/server-icon.png`. The watcher base64-encodes it automatically.

When installed via systemd, assets are at `/opt/mc-wol-proxy/assets/`. Existing assets are never overwritten on reinstall.

If no asset files are found, the values from `config.yml` are used as fallback.

## DuckDNS

The watcher keeps your DuckDNS domain pointed at your public IP. Runs on startup and then every `duckdns.update_interval_hours` hours (default 6). Set `duckdns.enabled: false` to turn it off.

## Troubleshooting

WoL not working:
- Check that WoL is enabled in BIOS and OS network adapter settings
- Verify the MAC address in `config.yml`
- Try switching between unicast and broadcast mode
- Check if the NIC supports WoL: `ethtool <interface> | grep Wake-on`

SSH failing:
- Test manually: `ssh -i ~/.ssh/id_ed25519 user@server-ip docker start minecraft`
- Check that the key is in the server's `authorized_keys`
- Verify user and key path in `config.yml`

Minecraft not starting:
- Check that the container exists: `docker ps -a` on the server
- Check container logs: `docker logs minecraft`
- Verify the container name matches `config.yml`

Viewing logs:
- Docker: `docker compose logs -f` (in the `watcher` directory)
- systemd: `journalctl -u mc-wol-proxy -f`
- Windows: terminal output from `windows-start.bat`

## Credits

[itzg/minecraft-server](https://github.com/itzg/docker-minecraft-server) - Minecraft server Docker image (MIT)

## License

MIT - see [LICENSE](LICENSE).
