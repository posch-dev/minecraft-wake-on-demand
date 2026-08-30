# Minecraft Wake-on-Demand

[![Release](https://img.shields.io/github/v/release/posch-dev/minecraft-wake-on-demand)](https://github.com/posch-dev/minecraft-wake-on-demand/releases)
[![Minecraft](https://img.shields.io/badge/Minecraft-Java%201.7.6%2B-brightgreen)](#faq)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

Your Minecraft server PC is switched off. Somebody clicks **Join**, the PC turns
itself on, the server starts, and everyone plays. When the last player leaves,
the PC goes back to sleep. Nobody installs anything, and nobody has to phone you
to ask whether you could please turn the server on.

A tiny always-on device in your home, a Raspberry Pi is plenty, answers in the
server's place while it sleeps.

<p align="center">
  <img src="https://raw.githubusercontent.com/posch-dev/minecraft-wake-on-demand/main/.github/assets/wake-up.gif" alt="Joining a sleeping server: the list says it is starting, then the world loads" width="856">
</p>

## Features

- **No Installation for players**: no mod, no launcher, no instructions. They add the server and click Join.
- **Wake-on-LAN**: Your Server wakes up when somebody joins.
- **Back to sleep**: once nobody has played for a while the PC suspends itself again. Until someone joins again.
- **Interactive Setup**: Just answer some questions, the program takes care of everything else for you.
- **Minecraft Server management**: version, server kind, whitelist and automatic backups.
- **Multiple Worlds**: keep them side by side, switch with two keystrokes, only the one you picked runs.
- **Whitelist and Admins**: manageable through the watcher.
- **Server Status in Minecraft**: Know exactly if the server is asleep, waking up, or online, through the Minecraft Serverlist..

Runs on Linux and Windows.

## How it works

```
        Player clicks Join
                |
       [ Watcher, always on ]
                |
        Is the server awake?
           /          \
         yes           no
          |             |
   pass the player   send the wake-up packet
     through to      -> the PC switches on
     the server      -> Minecraft starts
```

While the server PC sleeps the watcher answers the server list itself, and when somebody really wants to play on your Server, it
wakes the PC and gets out of the way.

## What you need

|                              |                                                                                      |
|------------------------------|--------------------------------------------------------------------------------------|
| **A PC that runs Minecraft** | It sleeps most of the time. Needs Docker and SSH, and Wake-on-LAN switched on.       |
| **An always-on device**      | Any Linux or Windows machine that is on 24/7. (Raspberry Pi Zero, Homeserver, Phone, etc.) |
| **Your Internet Router**     | Port `25565` forwarded to the **watcher**, and a fixed local address for the server PC. |
| **A free DuckDNS name**      | Only if friends join from outside your home. [duckdns.org](https://www.duckdns.org/) |

The watcher and the server PC have to be **two different machines**. A PC cannot
wake itself up.

## Download & Installation

**Linux:**

```bash
curl -fsSL https://raw.githubusercontent.com/posch-dev/minecraft-wake-on-demand/main/watcher/install.sh | sudo bash
```

**Windows**:

```powershell
iwr -useb https://github.com/posch-dev/minecraft-wake-on-demand/releases/latest/download/mcwod_windows_amd64.exe -OutFile mcwod.exe; .\mcwod.exe install
```

Or Download from the [releases page](https://github.com/posch-dev/minecraft-wake-on-demand/releases/latest).
Then pick the right file for the **watcher** (always-on device).

| Your watcher | File |
|--------------|------|
| Raspberry Pi 3/4/5 with a 64-bit system | `mcwod_linux_arm64` |
| An older Raspberry Pi, or a 32-bit system | `mcwod_linux_armv7`, very old ones `armv6` |
| A normal Linux PC or server | `mcwod_linux_amd64` |
| Windows | `mcwod_windows_amd64.exe` |

## Quick start

The command above installs MCWOD and starts the setup right away. If you
downloaded the file instead, run it and the same thing happens:

```bash
sudo ./mcwod_linux_arm64 install     # Linux, the file you downloaded
.\mcwod.exe install                  # Windows
```

Answer the questions and you are set up. One of them asks for your password for
the server PC: it is used for a single login, to install MCWOD's own key, and is
never saved.

```
This writes a config file to /opt/mcwod/config.yml
Press Enter to accept the value in brackets.

Nothing is set up yet, so let's do that now.
  You can change all of this later.

  Look it up in the network settings on that PC, or in your router.
Enter the IP address of the PC that will run Minecraft (192.168.178.xxx): 192.168.178.40

What is your username on that PC? [pi]:

I can log in to that PC once and set everything up for you.
  Your password is used for this one login and is never saved.
Let me do that? [Y/n]:
```

**The words that trip people up:**

| Word | What it means |
|------|---------------|
| **IP address** | The number your server PC has at home, like `192.168.178.40`. Your router lists it. |
| **Username on that PC** | The name you log into the server PC with. |
| **MAC address** | The serial number of a network card, like `A8:A1:59:22:0B:7C`. |
| **Container** | The box Minecraft runs in, usually called `minecraft`. |
| **SSH** | How the watcher tells the server PC to start Minecraft. |
| **Wake-on-LAN** | The network card listening while the PC sleeps. On in the BIOS *and* in the system. |
| **DuckDNS** | A free name that follows your home address when your provider changes it. |
| **Port forwarding** | A router rule sending Minecraft traffic to one device. |
| **Broadcast address** | Where the wake-up packet goes. MCWOD works it out itself. |
| **Whitelist** | Who may join. Everybody else is turned away. |
| **RCON** | The channel MCWOD uses to ask the server who is online. |

### Before your friends can join

Two things only your router can do:

| In your router | What to set |
|----------------|-------------|
| **Port forwarding** | Port `25565`, TCP, to the **watcher**. Not to the server PC. |
| **A fixed address for the server PC** | A "DHCP reservation", so it never changes. |

Routers call the first one "port forwarding", "port sharing", "virtual server"
or "NAT". Skip both if only people in your own home play.

Running a mod with its own port, **Simple Voice Chat** for instance? See
[Ports and mods](#ports-and-mods).

### Connect and play

Everybody, you included, connects to the **watcher**. Never to the server PC. The
server PC is asleep, there is nothing there to answer.

| Who | What they type into Minecraft                                   |
|-----|-----------------------------------------------------------------|
| Friends, from outside | `yourname.duckdns.org:25565`                                    |
| You, at home | the watcher's local address, for example `192.168.165.50:25565` |

**Always type the port**, the `:25565` part. Minecraft is supposed to fill it in
for you and depending on the version it does not, and the address then simply
fails.

The first join after a sleep goes like this:

1. The server list shows **"Server currently asleep"**, and under it "Join to wake it up".
2. Click **Join**. The PC starts up and Minecraft loads.
3. You are sent back with **"Server is waking up. Please reconnect in a moment."**
   That is normal. Waking takes 30 to 60 seconds, longer than Minecraft is
   willing to wait, so it tells you instead of leaving you on a loading bar that
   then fails.
4. Wait until the list shows the server as up, then click **Join** again. You are in.

Only the first person after a sleep sees this. Everybody joining a server that is
already running sees no difference to joining any other server.

## Everyday use

Type `mcwod` on the watcher and you get a menu:

```
Minecraft Wake-on-Demand

  1) Check that everything works
  2) Change settings
  3) Manage worlds
  4) Manage players
  5) Use the picture from your server
  6) Update MCWOD
  q) Quit
```

Everything in that menu is also a command of its own:

| Command | What it does |
|---------|--------------|
| `mcwod` | the menu above, or the watcher itself when the system starts it |
| `mcwod check` | goes through the whole setup and names the one thing that is wrong |
| `mcwod config` | change any setting, guided. Also `edit` and `settings` |
| `mcwod worlds` | switch worlds, make a new one, change a world's version |
| `mcwod players` | who may join and who is an admin. Also `whitelist` |
| `mcwod update` | install a newer MCWOD, after showing you what changed |
| `mcwod get-server-icon` | copy your running server's picture into the sleeping server list entry |
| `mcwod init` | write a fresh config, the same questions as the wizard |
| `mcwod setup-ssh` | create the key and install it on the server |
| `mcwod restore-compose` | undo a change MCWOD made to your server's setup file |
| `mcwod run` | start the watcher in this terminal |
| `mcwod version` | which version this is |

**`check` is the answer to nearly every problem.** It tests the config, the key,
the server PC, the container and DuckDNS in the order they depend on each other,
and stops at the first thing that is broken, in plain words.

### Worlds

```
Your worlds

 > survival     26.2 Paper
   creative     26.2 Vanilla
   modded       1.21.1 Fabric

  1) Play a different world
  2) Make a new world
  3) Change version or server kind
  4) Remove world
```

Changing a world's version always makes a backup first.
Every world can have its own message and picture in the server list, see
[Your own message and picture](#your-own-message-and-picture).

### Players

`mcwod players` switches the whitelist on and off, adds and removes names, and
says who is an admin. Removing the last admin asks first.

## Ports and mods

**MCWOD works with every mod and plugin.** It never touches the game itself, it
only forwards the connection.

What it does not forward is a second connection a mod opens for itself, voice
chat being the usual one. Those get their own port, forwarded straight to the
server PC.

### Simple Voice Chat

[Simple Voice Chat](https://modrepo.de/minecraft/voicechat/wiki) does not use
Minecraft's connection. It uses **UDP port `24454`**, and that traffic never
touches the watcher.

Two things to do:

1. **In your router**, forward port `24454` **UDP** to the **Minecraft Server PC**, not to
   the watcher.
2. **In the Minecraft container**, publish the port. In the server's
   `docker-compose.yml`, under the Minecraft service:

   ```yaml
       ports:
         - "25565:25565"
         - "24454:24454/udp"
   ```

   Then run `docker compose up -d` on the server once.

If voice works for your friends but not for people sitting at home with you, set
`voice_host` in the mod's `voicechat/voicechat-server.properties` to your DuckDNS
address, so everybody is sent to the same place.

The same goes for anything else with a port of its own: **Dynmap** (`8123` TCP),
**BlueMap** (`8100` TCP), a **Geyser** bridge for Bedrock players (`19132` UDP).
Forward it to the server PC and publish it in the container.


## Extra options

### Letting MCWOD build the server (recommended)

Say yes when the wizard offers it, or pick it later in `mcwod config`. It writes
the compose file on the server, adds automatic backups, generates the RCON
password and starts everything, without you opening a terminal on the server PC.

It asks which kind of server you want:

| Kind | What it is |
|------|------------|
| `VANILLA` | the game exactly as Mojang ships it |
| `PAPER`, `PURPUR` | faster, and they take plugins. Players notice nothing |
| `FABRIC`, `FORGE`, `NEOFORGE`, `QUILT` | mods. Every player then needs the same mods installed |

It asks for a concrete version, never "latest", so a restart cannot move your
world to a new Minecraft version on its own.

An existing compose file is added to, not replaced. A backup is kept first and
`mcwod restore-compose` puts it back. Accepting the Minecraft EULA is its own
question.

### Auto-sleep

The watcher can send the server PC back to sleep once nobody is playing. It is
**off until you turn it on**, in `mcwod config` or with `sleep.enabled: true`.

It asks the server how many players are online, waits, and asks again. Somebody
joining in that window cancels it, and an answer it cannot read counts as busy.

| Setting | Default | What it does |
|---------|---------|--------------|
| `sleep.action` | `suspend` | `suspend`, `hibernate`, `shutdown` or `custom` |
| `sleep.idle_after` | 900 | seconds with nobody playing before sleep is considered |
| `sleep.confirm_delay` | 60 | seconds to wait, then check once more before acting |
| `sleep.grace_period` | 900 | seconds after a wake in which it never sleeps |
| `sleep.poll_interval` | 300 | seconds between checks, transfer mode only |

`hibernate` and `shutdown` stop the container first, so the world is saved.
Waking from a full shutdown needs Wake-on-LAN for the powered-off state in the
BIOS, which not every mainboard can do.

### Transfer mode

In transfer mode the watcher only handles the wake-up and then hands the player
straight to the server PC. Faster, and it takes the load off off the always-on device.

The cost: a second port forward.

The wizard offers it if you use DuckDNS, and `mcwod config` turns it on later. By
hand:

1. Forward a second port, `25566` straight to the **Server PC**
2. `accepts-transfers=true` in the server's `server.properties`, unless MCWOD
   built the server, which always sets it
3. In `config.yml`:
   ```yaml
   transfer:
     enabled: true
     host: "yourname.duckdns.org"
     port: 25566
   ```
Transfer mode needs **Minecraft 1.20.5 or newer**.

### Your own message and picture

Out of the box the sleeping server shows three blue Z, the largest one turning
into a red exclamation mark while the PC boots.

Anything you put in `assets/` replaces it. `mcwod install` puts copyable
examples there for you, in `assets/examples/`.

| File | What it does                                        |
|------|-----------------------------------------------------|
| `server-icon.png` | your own 64x64 picture: As the Servers Icon         |
| `server-icon-sleeping.png` | replaces the sleeping picture outright              |
| `server-icon-starting.png` | replaces the starting picture outright              |
| `server-icon-live.png` | same as server-icon.png                             |
| `motd-sleeping.json` | the text in the server list while the server is off |
| `motd-starting.json` | the text while it is booting                        |
| `motd-live.json` | replaces the running server's own text              |
| `motd-login-wait.json` | what the person whose join woke the server reads    |

`mcwod get-server-icon` copies the picture your running server already uses, so
you do not have to go looking for the file. The server has to be awake for it,
and whatever was there is kept as a `.bak`.

Pictures have to be exactly 64x64 and under 64 kB.

If you keep several worlds, each can have its own files in
`assets/worlds/<name>/`, and anything a world does not have of its own comes from
`assets/`.

### Wake-up packet modes

`broadcast`, the default, sends the wake-up packet to your whole network. 
`unicast` sends it straight at the server's address, which can fail once your router has forgotten the machine.
`wol.mode` in `config.yml`.

## Manual setup

Everything in this section is optional. The wizard does all of it too.

### Writing the config by hand

```bash
cp config.example.yml config.yml
```

The bare minimum:

| Setting | What to put |
|---------|-------------|
| `server.mac` | MAC address of the server PC |
| `server.ip` | its local address |
| `server.ssh_user` | your login name on it |
| `server.container_name` | the name of the Minecraft container, usually `minecraft` |
| `duckdns.enabled` | `false` if you are not using DuckDNS, then ignore the two rows below |
| `duckdns.domain` | your address, with or without `.duckdns.org`, both work |
| `duckdns.token` | your DuckDNS token |

Everything else has a sensible default and is explained in the comments in
`config.example.yml`. Then:

```bash
mcwod setup-ssh   # creates the key and installs it on the server
mcwod check       # confirms it all hangs together
```

The config is looked for in `MCWOD_CONFIG`, then next to the program, then one
folder above it. Your own comments in the file survive `mcwod config`.

### Setting the Minecraft server up yourself

If you would rather write the server's setup file yourself, this is the shape
MCWOD expects. On the server PC:

```bash
cd server
cp .env.example .env
```

| Variable | What to put |
|----------|-------------|
| `RCON_PASSWORD` | any password you make up, the backup container uses it too |
| `MC_VERSION` | the version you want, for example `26.2` |

```bash
docker compose up -d
```

Start it once, then never again by hand. The watcher takes over from there.

### Docker

The watcher itself can run in Docker:

```bash
cp config.example.yml config.yml
cd watcher && docker compose up -d
```

Fill `config.yml` in by hand first. The image is about 11 MB.

### Building it yourself

```bash
cd watcher
go build -o mcwod .
```

`sudo ./watcher/install.sh --build` does the same and installs the result, which
is how to get MCWOD onto a machine with no published build for it.
`sudo ./watcher/install.sh --uninstall` removes it again.


## Security

MCWOD is reachable from the internet and it can switch on a PC in your home, so
it has to be safe. It is: the defaults are made for a home setup and ask nothing
of you.

What exactly is exposed, and what you can lock down further, is in
[SECURITY.md](SECURITY.md).

## FAQ

### Before you start

>**Do my friends have to install anything?**<br>
No. They add the address to their server list like any other server.

>**Can the watcher and the Minecraft server be the same PC?**<br>
No. A sleeping PC cannot wake itself, so the watcher has to be a second machine
that is always on.

>**Do I have to buy a Raspberry Pi?**<br>
Only if you have nothing else running 24/7. Anything that is on anyway and can
run one small program will do.

>**Does this work with Bedrock, phones or consoles?**<br>
No, unless you run a Geyser bridge on your server. Geyser has its own port and
wakes nothing, see [Ports and mods](#ports-and-mods).

>**Which Minecraft versions work?**<br>
1.7.6 and newer. Transfer mode needs 1.20.5 and newer.

>**Does it work with modded servers?**<br>
Yes, any of them. MCWOD only cares that the server runs in a container it can
start.

>**Do I need DuckDNS?**<br>
Only if people outside your home join. Skip it if everybody plays in your house.

>**Does this cost anything?**<br>
No. MCWOD and DuckDNS are free. You save the electricity of a PC running around
the clock.

>**Is it safe to open port 25565 to the internet?**<br>
It is what every home Minecraft server does, and the watcher is written for it.
Turn the whitelist on and only your friends get in. Details in
[SECURITY.md](SECURITY.md).

>**Can the watcher read my chat or my password?**<br>
It forwards bytes without looking at them, and your account is checked between
your game and Mojang. In transfer mode it is out of the picture entirely.

### Playing

>**Why do I have to click Join twice?**<br>
Waking takes longer than Minecraft is willing to wait, so it sends you back
instead of failing. Only the first player after a sleep sees it.

>**How long does waking take?**<br>
30 to 60 seconds: the PC starting, then Minecraft loading.

>**The server list says asleep, but the PC is on.**<br>
Press refresh. If it stays that way the watcher cannot reach Minecraft, and
`mcwod check` says why.

>**Does anything happen when everybody leaves?**<br>
Minecraft pauses itself. The PC only switches off if you turn
[auto-sleep](#auto-sleep) on.

>**Can I keep several worlds?**<br>
Yes, `mcwod worlds`. Only the one you picked runs.

>**How do I change the Minecraft version?**<br>
`mcwod worlds`, then "Change version or server kind". It backs the world up
first. Going backwards is not possible in Minecraft, so it offers a fresh world.

>**How do I whitelist a player?**<br>
`mcwod players`, then "1". Same place for admins.

>**Can I put my own picture and text in the server list?**<br>
Yes, see [Your own message and picture](#your-own-message-and-picture).

### Mods and extra ports

>**I use Simple Voice Chat and nobody can hear anybody.**<br>
Forward UDP `24454` to the server PC and publish it in the container, see
[Simple Voice Chat](#simple-voice-chat).

>**Voice works for my friends but not for me at home.**<br>
Set `voice_host` in the mod's server settings. Some routers cannot send traffic
from inside back in through your public address.

>**What about Dynmap, BlueMap or Geyser?**<br>
Same idea: their port goes to the server PC. They answer only while the server
is awake.

>**Can a mod's port wake the server?**<br>
No. Only Minecraft's own connection does.

### When something is broken

>**Where do I start?**<br>
`mcwod check` on the watcher. It stops at the first broken step and says what it
is.

>**The PC does not wake up.**<br>
Almost always Wake-on-LAN:

1. In the BIOS/UEFI: "Wake on LAN", "Power on by PCI-E", "Resume by LAN".
2. In the system. On Linux `ethtool eth0` shows `Wake-on: d` for off, `g` for
   on. Many cards forget it on every boot, and MCWOD offers to keep it fixed.
3. The MAC address in the config. `mcwod check` compares it.

Cable only. Wi-Fi cards almost never wake a PC.

>**My friends can join but I cannot, or the other way round.**<br>
At home use the watcher's local address, from outside the DuckDNS one. Many
routers refuse to send traffic from inside out and straight back in.

>**"Can't connect to server" or "Connection refused".**<br>
Is the `:25565` in the address, is the watcher running (`mcwod check`), and is
the port forwarded to the watcher rather than to the server PC.

>**Nobody outside can reach me at all.**<br>
If the address your router got from your provider starts with `100.64.` to
`100.127.`, you are behind their router and no port forwarding helps. Ask them
for a real address.

>**Minecraft does not start after the wake-up.**<br>
On the server PC: `docker ps -a` and `docker logs minecraft`. Check that Docker
starts with the machine, otherwise nothing comes back after a suspend.

>**Where are the logs?**<br>

| Where the watcher runs | How to read them |
|------------------------|------------------|
| Linux, installed normally | `journalctl -u mcwod -f` |
| Docker | `docker compose logs -f` in the `watcher` folder |
| Windows | nothing is written to a file, run `mcwod.exe run` in a window to watch it |

>**"No ICMP socket and no ping command available"**<br>
The watcher cannot tell when the PC has booted. Keep `cap_add: NET_RAW` in
Docker, or `AmbientCapabilities=CAP_NET_RAW` in the systemd unit.

>**It went to sleep while I was playing.**<br>
Then RCON is switched off in the container, so it never got a real player count.
`mcwod check` reports that.

### Keeping it running

>**How do I update?**<br>
`mcwod update`. It shows what changed, asks first, and verifies the download.

>**Does it update itself?**<br>
Never. It only mentions a new version in one line. `update.check: false` turns
that off.

>**How do I uninstall it?**<br>
`sudo ./watcher/install.sh --uninstall` on Linux. On Windows delete `mcwod.vbs`
from `shell:startup` and the `%LOCALAPPDATA%\mcwod` folder. Your Minecraft
server is untouched.

>**Can I use a port other than 25565?**<br>
Yes, `watcher.listen_port` in the config. Forward that one instead.

>**Does it work over Tailscale, WireGuard or another VPN?**<br>
Probably, as long as the wake-up packet reaches the server PC's own network. Not
something this project tests.

>**Can I use a Minecraft server that is not in Docker?**<br>
Not yet. MCWOD starts your server by starting its container.

>**What happens during a power cut?**<br>
Nothing special. The watcher starts with its machine and the server PC waits to
be woken, as long as its BIOS is set to stay off.

## Credits

[itzg/minecraft-server](https://github.com/itzg/docker-minecraft-server), the
Minecraft server image everything here is built on.

## License

MIT, see [LICENSE](LICENSE).
