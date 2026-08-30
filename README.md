# Minecraft Wake-on-Demand

[![Release](https://img.shields.io/github/v/release/posch-dev/minecraft-wake-on-demand)](https://github.com/posch-dev/minecraft-wake-on-demand/releases)
[![CI](https://github.com/posch-dev/minecraft-wake-on-demand/actions/workflows/ci.yml/badge.svg)](https://github.com/posch-dev/minecraft-wake-on-demand/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![Minecraft](https://img.shields.io/badge/Minecraft-Java%201.7.6%2B-brightgreen)](#faq)

Your Minecraft server PC is switched off. Somebody clicks **Join**, the PC turns
itself on, the server starts, and everyone plays. When the last player leaves,
the PC goes back to sleep. Nobody installs anything, and nobody has to phone you
to ask whether you could please turn the server on.

A tiny always-on device in your home, a Raspberry Pi is plenty, answers in the
server's place while it sleeps.

```bash
curl -fsSL https://raw.githubusercontent.com/posch-dev/minecraft-wake-on-demand/main/watcher/install.sh | sudo bash
```

<!-- GIFs and screenshots go here. Drop the files in .github/assets/ and uncomment:
<table>
<tr>
<td valign="top"><img src="https://raw.githubusercontent.com/posch-dev/minecraft-wake-on-demand/main/.github/assets/serverlist.gif" height="230" alt="The server list waking the PC"></td>
<td valign="top"><img src="https://raw.githubusercontent.com/posch-dev/minecraft-wake-on-demand/main/.github/assets/wizard.gif" height="230" alt="The setup wizard"></td>
<td valign="top"><img src="https://raw.githubusercontent.com/posch-dev/minecraft-wake-on-demand/main/.github/assets/check.png" height="230" alt="mcwod check"></td>
</tr>
</table>
-->

> **Never done anything like this before?** Go to [Download](#download), then
> [Quick start](#quick-start), and answer the questions. That is the whole job.
> Confused by a word, or something is not working? It is answered in the
> **[FAQ](#faq)** at the bottom.

## Features

- **Nothing on the player side**: no mod, no launcher, no instructions. They add the server and click Join.
- **Wake-on-LAN**: the sleeping PC is switched on by a network packet, the way the hardware already supports.
- **Back to sleep**: once nobody has played for a while the PC suspends itself again. Off until you turn it on.
- **A setup wizard for people who have never used a terminal**: it logs into your server once and does the rest itself.
- **It can build the Minecraft server for you**: version, server kind, whitelist and automatic backups, all from the wizard.
- **Several worlds**: keep them side by side, switch with two keystrokes, only the one you picked runs.
- **Whitelist and admins**: decide who may join without ever opening a console.
- **Its own server list entry**: "Server currently asleep", with a picture, so people can see what is going on.
- **Safe by default**: the SSH key it creates can start your server and nothing else.
- **One file**: a single program. No Python, no Java, nothing to install alongside it. About 8 MB.

Runs on Linux, Raspberry Pi included, and on Windows.

## How it works

```
        Player clicks Join
                |
       [ Watcher, always on ]        a Raspberry Pi, about 2 watts
                |
        Is the server awake?
           /          \
         yes           no
          |             |
   pass the player   send the wake-up packet
     through to      -> the PC switches on
     the server      -> Minecraft starts
                     -> "reconnect in a moment"
```

The watcher takes the place of your Minecraft server on the network. Your router
sends Minecraft traffic to it, not to the server PC. While the server PC sleeps
the watcher answers the server list itself, and when somebody really wants in, it
wakes the PC and gets out of the way.

## What you need

| | |
|---|---|
| **A PC that runs Minecraft** | It sleeps most of the time. Needs Docker and SSH, and Wake-on-LAN switched on. |
| **A small always-on device** | Any Linux or Windows machine that is on 24/7. A Raspberry Pi Zero is enough. This runs the watcher. |
| **Your router** | Port `25565` forwarded to the **watcher**, and a fixed local address for the server PC. |
| **A free DuckDNS name** | Only if friends join from outside your home. [duckdns.org](https://www.duckdns.org/), takes a minute. |

The watcher and the server PC have to be **two different machines**. A PC cannot
wake itself up.

## Download

Pick the file for the machine that stays on, the watcher.

| Your watcher | File |
|--------------|------|
| Raspberry Pi 3/4/5 with a 64-bit system | `mcwod_linux_arm64` |
| An older Raspberry Pi, or a 32-bit system | `mcwod_linux_armv7`, very old ones `armv6` |
| A normal Linux PC or server | `mcwod_linux_amd64` |
| Windows | `mcwod_windows_amd64.exe` |

They are all on the [releases page](https://github.com/posch-dev/minecraft-wake-on-demand/releases/latest).
Not sure which one you are? Type `uname -m`: `aarch64` means `arm64`, anything
starting with `arm` means `armv7`, `x86_64` means `amd64`.

**Linux, the short way:**

```bash
curl -fsSL https://raw.githubusercontent.com/posch-dev/minecraft-wake-on-demand/main/watcher/install.sh | sudo bash
```

That picks the right file for your machine, checks it against the fingerprints
published with the release, refuses to install anything that does not match, and
goes straight into the setup.

**Linux, doing it yourself:**

```bash
curl -fsSLO https://github.com/posch-dev/minecraft-wake-on-demand/releases/latest/download/mcwod_linux_arm64
chmod +x mcwod_linux_arm64
sudo ./mcwod_linux_arm64 install
```

Swap `arm64` for whatever the table above says. The program carries its own
service file and example files inside it, so this one download is everything.

**Windows, the short way**, in PowerShell:

```powershell
iwr -useb https://github.com/posch-dev/minecraft-wake-on-demand/releases/latest/download/mcwod_windows_amd64.exe -OutFile mcwod.exe; .\mcwod.exe install
```

**Windows, doing it yourself:** download `mcwod_windows_amd64.exe` from the
releases page, rename it to `mcwod.exe`, then run `.\mcwod.exe install` in
PowerShell.

On Windows the watcher installs into `%LOCALAPPDATA%\mcwod`, needs no
administrator, and starts silently when you log in, with no window popping up.
A proper Windows service is still to come, so for now the watcher runs while you
are signed in.

## Quick start

`install` puts everything in place and then asks you a handful of questions.
That conversation is the entire setup, and it takes about two minutes.

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

**How to read that:**

- **Grey lines above a question** are the explanation. You do not type anything there.
- **Something in `[brackets]`** is what it will use if you just press Enter. `[Y/n]` means yes unless you type `n`.
- **What you type shows up green.** If your screen has no colour, nothing is broken, some terminals just do not do colour.
- A wrong answer is not a disaster. It says what it did not like and asks again, and `mcwod config` changes anything later.

**The words that trip people up:**

| Word | What it actually means |
|------|------------------------|
| **IP address** | The number your server PC has inside your home, like `192.168.178.40`. Your router lists it, usually under "devices" or "network". Not the address your friends type. |
| **Username on that PC** | The name you log into the server PC with. On a Raspberry Pi that is usually `pi`. |
| **MAC address** | The permanent serial number of a network card, like `A8:A1:59:22:0B:7C`. The wake-up packet is addressed to it. MCWOD reads it off the server itself, so you rarely type it. |
| **Container** | Minecraft runs inside a box called a container, so it cannot make a mess of the rest of the PC. The box has a name, usually `minecraft`. |
| **SSH** | How the watcher tells the server PC "start Minecraft". A remote control with a lock on it. |
| **Wake-on-LAN** | The network card staying awake while the PC sleeps, listening for the wake-up packet. Has to be switched on in the BIOS *and* in the operating system. |
| **DuckDNS** | A free name like `mymates.duckdns.org` that always points at your home, even though your internet provider keeps changing your address. |
| **Port forwarding** | A rule in your router: "Minecraft traffic from outside goes to this device." Without it only people in your house can join. |
| **Broadcast address** | The "everybody in this network" address the wake-up packet goes to. MCWOD works it out itself. |
| **Whitelist** | The list of people allowed to join. Everybody else is turned away, even if they know the address. |
| **RCON** | A back door into the running Minecraft server, which MCWOD uses to ask "is anybody playing right now". |

### Letting it log in once

The wizard offers to log into the server PC one time using your password. Say
yes. It reads the MAC address, works out the broadcast address, lists the
containers so you can pick yours from a numbered list instead of typing a name,
checks whether Wake-on-LAN is really armed and offers to fix it, and installs its
own key so it never needs your password again.

Your password is used for that single login and is not written anywhere.

Say no and it asks you those things instead. Both ways work.

**If there is no Minecraft server on that PC yet**, MCWOD offers to build one: it
asks which kind of server and which version, writes the setup file, adds an
automatic backup, and starts it. See
[Letting MCWOD build the server](#letting-mcwod-build-the-server).

### Before your friends can join

Two things happen outside MCWOD, and nothing can do them for you:

1. **Forward port `25565`, TCP, in your router to the watcher**, the always-on
   device, *not* the server PC. Every router words this differently: "port
   forwarding", "port sharing", "virtual server", "NAT". You need one rule:
   outside port `25565` to the watcher's local address, port `25565`, TCP.
2. **Give the server PC a fixed local address**, a "DHCP reservation" or
   "always assign this address" in your router. If that address changes, MCWOD
   wakes a device that is not there any more.

Skip both if only people in your own home are ever going to play.

Running a mod that needs its own port, **Simple Voice Chat** for instance? See
[Ports and mods](#ports-and-mods).

### Connect and play

Everybody, you included, connects to the **watcher**. Never to the server PC. The
server PC is asleep, there is nothing there to answer.

| Who | What they type into Minecraft |
|-----|-------------------------------|
| Friends, from outside | `yourname.duckdns.org:25565` |
| You, at home | the watcher's local address, for example `192.168.178.50:25565` |

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
already running goes straight in.

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
  4) Remove a world from this list
```

Each world gets its own folder on the server and only the one marked `>` ever
runs, so your friends always land on the world you picked. Switching while
somebody is playing counts down and warns them first.

Changing a world's version always makes a backup first, and that part is not
optional. A world opened in a newer Minecraft cannot go back to an older one, so
going backwards tells you what will happen and offers you a fresh world instead.

Every world can have its own message and picture in the server list, see
[Your own message and picture](#your-own-message-and-picture).

### Players

`mcwod players` switches the whitelist on and off, adds and removes names, and
says who is an admin. It writes into the Minecraft server's own files, so what
you set here survives the container being rebuilt. Removing the last admin asks
first.

## Ports and mods

MCWOD forwards Minecraft's own connection and nothing else. A mod that opens a
second connection needs its own rule in your router, straight to the server PC.

### Simple Voice Chat

[Simple Voice Chat](https://modrepo.de/minecraft/voicechat/wiki) does not use
Minecraft's connection. It uses **UDP port `24454`**, and that traffic never
touches the watcher.

Two things to do:

1. **In your router**, forward port `24454` **UDP** to the **server PC**, not to
   the watcher. This one goes straight through.
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
address, so everybody is sent to the same place. Some routers will not let
traffic from inside go out and come straight back in, and then the people at home
need the server PC's local address there instead.

The same goes for anything else with a port of its own: **Dynmap** (`8123` TCP),
**BlueMap** (`8100` TCP), a **Geyser** bridge for Bedrock players (`19132` UDP).
Forward it to the server PC and publish it in the container.

Two things to keep in mind. A mod's port has no waking magic on it, so it only
answers once the server is awake and somebody has joined the game normally. And
everything you forward is reachable from the internet, so only open what you
actually use.

## Manual setup

Everything in this section is optional. The wizard does all of it.

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

## Extra options

### Letting MCWOD build the server

The wizard offers this, and `mcwod config` has it as a menu entry later. It
writes a `docker-compose.yml` on the server with Minecraft and an automatic
backup next to it, puts a generated RCON password in a `.env` only its owner can
read, and starts them. You never open a terminal on the server PC for it.

It asks which kind of server you want:

| Kind | What it is |
|------|------------|
| `VANILLA` | the game exactly as Mojang ships it |
| `PAPER`, `PURPUR` | faster, and they take plugins. Players notice nothing |
| `FABRIC`, `FORGE`, `NEOFORGE`, `QUILT` | mods. Every player then needs the same mods installed |

It asks for a concrete Minecraft version rather than defaulting to "latest",
because "latest" means the next restart can move your world to a new version on
its own. The container images are pinned for the same reason.

If a setup file is already there, the two services are added to it and everything
else in the file, your other services and your comments, stays exactly as it was.
A copy is kept as `docker-compose.yml.mcwod-bak-<time>` first, the result has to
pass `docker compose config`, and a name that is already taken is refused rather
than overwritten. `mcwod restore-compose` puts a backup back, keeping the current
file on the way, so the undo is itself undoable.

Accepting the Minecraft EULA is a separate question. Saying yes is the same as
accepting it yourself.

### Auto-sleep

The watcher can send the server PC back to sleep once nobody is playing. It is
**off until you turn it on**, in `mcwod config` or with `sleep.enabled: true`.

Your operating system cannot do this for you, which is why it lives here. Windows
counts keyboard and mouse input, and a running Minecraft is neither, so it would
suspend in the middle of your game. Linux counts login sessions, which on a
machine with no screen means either always idle or never. Whether anybody is
playing is the one thing only the watcher knows.

It never guesses. Before anything happens it asks the Minecraft server itself how
many players are online, waits, and asks again. Somebody joining in that window
cancels it, and an answer it cannot read counts as busy, never as empty.

| Setting | Default | What it does |
|---------|---------|--------------|
| `sleep.action` | `suspend` | `suspend`, `hibernate`, `shutdown` or `custom` |
| `sleep.idle_after` | 900 | seconds with nobody playing before sleep is considered |
| `sleep.confirm_delay` | 60 | seconds to wait, then check once more before acting |
| `sleep.grace_period` | 900 | seconds after a wake in which it never sleeps |
| `sleep.poll_interval` | 300 | seconds between checks, transfer mode only |

`hibernate` and `shutdown` stop the container first so the world is written out.
`suspend` does not have to. Waking from a full shutdown needs Wake-on-LAN enabled
for the powered-off state in the BIOS, which not every mainboard can do.

### Transfer mode

Normally everything flows through the watcher for as long as you play. In
transfer mode the watcher only handles the wake-up and then hands the player
straight to the server PC. Faster, and it takes the load off a Raspberry Pi.

The cost: a second port forward, and the watcher no longer sees who is playing,
so auto-sleep has to ask over SSH instead.

The wizard offers it if you use DuckDNS, and `mcwod config` turns it on later. By
hand:

1. Forward a second port, `25566` say, straight to the **server PC**
2. `accepts-transfers=true` in the server's `server.properties`, unless MCWOD
   built the server, which always sets it
3. In `config.yml`:
   ```yaml
   transfer:
     enabled: true
     host: "yourname.duckdns.org"
     port: 25566
   ```

Players still connect to the watcher exactly as before, the redirect happens by
itself. Transfer mode needs **Minecraft 1.20.5 or newer**, that being the first
version a client can be redirected on at all.

If your home network uses addresses the watcher does not recognise as local, name
them:

```yaml
transfer:
  local_networks: ["192.168.1.0/24"]
```

### Your own message and picture

Out of the box the sleeping server shows three blue Z, the largest one turning
into a red exclamation mark while the PC boots. That picture is inside the
program, there is nothing to install for it.

Anything you put in `assets/` replaces it. `mcwod install` puts copyable
examples there for you, in `assets/examples/`.

| File | What it does |
|------|--------------|
| `server-icon.png` | your own 64x64 picture: plain while the server runs, dimmed under the Z while it sleeps |
| `server-icon-sleeping.png` | replaces the sleeping picture outright, with no Z drawn over it |
| `server-icon-starting.png` | the same while it boots |
| `server-icon-live.png` | the same while it runs |
| `motd-sleeping.json` | the text in the server list while the server is off |
| `motd-starting.json` | the text while it is booting |
| `motd-live.json` | replaces the running server's own text |
| `motd-login-wait.json` | what the person whose join woke the server reads |

`mcwod get-server-icon` copies the picture your running server already uses, so
you do not have to go looking for the file. The server has to be awake for it,
and whatever was there is kept as a `.bak`.

Pictures have to be exactly 64x64 and under 64 kB. Anything else is skipped with
a line in the log, because Minecraft drops the whole server list entry over a
wrongly sized picture. Changes are picked up within a minute, no restart needed.

If you keep several worlds, each can have its own files in
`assets/worlds/<name>/`, and anything a world does not have of its own comes from
`assets/`.

### Wake-up packet modes

`broadcast`, the default, sends the wake-up packet to your whole network. It
keeps working after the PC has been off for days. `unicast` sends it straight at
the server's address, which can fail once your router has forgotten the machine.
`wol.mode` in `config.yml`.

## Security

The watcher sits on a port anybody on the internet can reach, and it can switch
on a PC in your home. That deserves writing down, and it is written down in
[SECURITY.md](SECURITY.md): what is exposed, what the defaults already stop, and
what you can tighten further.

The short version: the defaults are fine for a home setup. The one thing worth
knowing is that the SSH key MCWOD creates is restricted to starting your server
and nothing else, so a stolen key cannot be used to log in and poke around.

## FAQ

### Before you start

**Do my friends have to install anything?**
No. No mod, no launcher, no add-on. They add the address to their server list
like any other server.

**Can the watcher and the Minecraft server be the same PC?**
No. A sleeping PC cannot wake itself, so the watcher has to be a second machine
that is always on. It does almost nothing, so a Raspberry Pi Zero, an old laptop,
a NAS or a mini PC all work.

**Do I have to buy a Raspberry Pi?**
Only if you have nothing else running 24/7. Anything that is switched on anyway
and can run one small program will do.

**Does this work with Bedrock, phones or consoles?**
Not on its own. MCWOD speaks Minecraft Java Edition. If you run a Geyser bridge
on your server so Bedrock players can join, that keeps working, but it has its
own port and no waking magic, see [Ports and mods](#ports-and-mods).

**Which Minecraft versions work?**
Normal mode: **1.7.6 and newer**, which is everything anybody still plays.
Transfer mode: **1.20.5 and newer**, because older clients cannot be redirected.

**Does it work with modded servers?**
Yes. Fabric, Forge, NeoForge, Quilt, Paper, Purpur. MCWOD does not care what the
server is, only that it lives in a container it can start.

**Do I need DuckDNS?**
Only if people outside your home join. Home internet addresses change every few
days and DuckDNS gives you a name that follows yours. If you have a fixed
address, or everybody plays in your house, skip it.

**Does this cost anything?**
No. MCWOD is free and DuckDNS is free. What you save is the electricity of a PC
running around the clock for the two hours a week anybody plays.

**Is it safe to open port 25565 to the internet?**
It is what every home Minecraft server does. The watcher is written for it: it
never runs anything a stranger sends, it limits how many connections one address
may open, and it refuses oversized files. Switch the whitelist on and only your
friends can actually get in. [SECURITY.md](SECURITY.md) has the details.

**Can the watcher read my chat or my password?**
In normal mode your traffic passes through it, the same way it passes through
your router. It forwards bytes without looking at them, and your Minecraft
account is checked between your game and Mojang, which the watcher is not part
of. In transfer mode it is out of the picture entirely after the wake-up.

### Playing

**Why do I have to click Join twice?**
Waking a PC takes 30 to 60 seconds and Minecraft gives up long before that. So
instead of leaving you on a loading bar that then fails, MCWOD tells you to come
back in a moment. Only the first person after a sleep sees it, everybody else
goes straight in.

**How long does waking take?**
Roughly 30 to 60 seconds: the PC starting, then Minecraft loading. An SSD makes
the first half quick, the second half is mostly down to the size of your world.

**The server list says asleep, but the PC is on.**
Give it a moment, the list only refreshes when you press refresh. If it stays
that way the watcher cannot reach the Minecraft server. `mcwod check` says why,
usually the wrong container name, or Minecraft not actually running.

**Does anything happen when everybody leaves?**
Minecraft pauses itself, so the PC stops working hard. It only switches off if
you turn [auto-sleep](#auto-sleep) on.

**Can I keep several worlds?**
Yes, `mcwod worlds`. Each one is separate and only the one you picked runs, so
nobody ends up in last year's world by accident.

**How do I change the Minecraft version?**
`mcwod worlds`, then "Change version or server kind". It backs the world up
first. Going to an older version is not really possible in Minecraft, so it
offers you a fresh world instead.

**How do I let a new friend in?**
`mcwod players`, then "1". Add their name and that is it. Same place for making
somebody an admin, or throwing them off again.

**Can I put my own picture and text in the server list?**
Yes, see [Your own message and picture](#your-own-message-and-picture).
`mcwod get-server-icon` takes the one your server already uses.

### Mods and extra ports

**I use Simple Voice Chat and nobody can hear anybody.**
Voice chat has its own connection, UDP port `24454`, which does not go through
the watcher. Forward that port in your router to the **server PC** and publish it
in the container. Step by step in [Simple Voice Chat](#simple-voice-chat).

**Voice works for my friends but not for me at home.**
Set `voice_host` in the mod's server settings, details in the same section. Some
routers cannot send traffic from inside back in through your public address.

**What about Dynmap, BlueMap or Geyser?**
Same idea: forward their port to the server PC and publish it in the container.
They answer only while the server is awake.

**Can a mod's port wake the server?**
No. Only Minecraft's own connection wakes it. Somebody joins the game normally
first, everything else follows.

### When something is broken

**Start here.** On the watcher:

```bash
mcwod check
```

It stops at the first broken step and says what it is, in words.

**The PC does not wake up.**
Almost always Wake-on-LAN. Three places to look:

1. In the BIOS/UEFI of the server PC: "Wake on LAN", "Power on by PCI-E", "Resume by LAN". Switch it on.
2. In the operating system. On Linux, `ethtool eth0` showing `Wake-on: d` means off and `Wake-on: g` means on. Many network cards forget this on every boot, which is the single most common reason this project appears to do nothing at all. MCWOD offers to fix it and to keep it fixed after every reboot.
3. The MAC address in the config. `mcwod check` compares it.

Also: cable only. Wi-Fi cards almost never wake a sleeping PC.

**My friends can join but I cannot, or the other way round.**
From inside your house use the watcher's **local** address, from outside the
DuckDNS one. Many routers refuse to send traffic from inside out and straight
back in, so the public address can simply not work at home.

**"Can't connect to server" or "Connection refused".**
In this order: is the `:25565` in the address, is the watcher actually running
(`mcwod check`), and is port `25565` forwarded to the **watcher** rather than to
the server PC.

**Nobody outside can reach me at all.**
Look at the address your router says it got from your provider. If it starts with
`100.64.` up to `100.127.`, you are sitting behind your provider's own router and
no port forwarding on earth will help. Ask them for a real address, or use a
tunnel service. That one is not something MCWOD can fix.

**Minecraft does not start after the wake-up.**
On the server PC, `docker ps -a` shows whether the container exists and
`docker logs minecraft` says what it did. Check as well that Docker itself starts
with the machine, otherwise nothing comes back after a suspend.

**Where are the logs?**

| Where the watcher runs | How to read them |
|------------------------|------------------|
| Linux, installed normally | `journalctl -u mcwod -f` |
| Docker | `docker compose logs -f` in the `watcher` folder |
| Windows | nothing is written to a file. Run `mcwod.exe run` in a PowerShell window to watch it live |

**"No ICMP socket and no ping command available"**
The watcher cannot tell when the PC has finished booting. In Docker, keep
`cap_add: NET_RAW` in `docker-compose.yml`; under systemd, keep
`AmbientCapabilities=CAP_NET_RAW` in the unit file.

**It went to sleep while I was playing.**
It should not: it asks the server how many players are online, twice, and treats
an answer it cannot read as busy. If it happens anyway, RCON is probably switched
off in the container, so it never gets a real answer. `mcwod check` reports that.

### Keeping it running

**How do I update?**
`mcwod update`. It shows what changed, asks first, verifies the download against
the published fingerprints, and swaps itself out.

**Does it update itself?**
Never. It mentions a new version in one line and that is all it does. That check
asks GitHub once a day, which tells GitHub your address, so `update.check: false`
turns it off.

**How do I uninstall it?**
`sudo ./watcher/install.sh --uninstall` on Linux. On Windows, delete
`mcwod.vbs` from your `shell:startup` folder, then the `%LOCALAPPDATA%\mcwod`
folder. Your Minecraft server is untouched by all of it.

**Can I use a port other than 25565?**
Yes, `watcher.listen_port` in the config, and forward that one instead.
Everybody then types it as part of the address.

**Does it work over Tailscale, WireGuard or another VPN?**
Nothing in MCWOD stands in the way, as long as the wake-up packet can reach the
server PC's own network, which usually means running the watcher inside that
network rather than at the far end of the tunnel. It is not something this
project tests, so treat it as your own adventure.

**Can I use a Minecraft server that is not in Docker?**
Not as it stands. MCWOD starts your server by starting its container. A plain
`java -jar` server would need a different start command, and there is no setting
for that yet.

**What happens during a power cut?**
Nothing special. When the power comes back the watcher starts with its machine
and the server PC waits to be woken as usual, as long as its BIOS is set to stay
off rather than to power itself on.

## Credits

[itzg/minecraft-server](https://github.com/itzg/docker-minecraft-server), the
Minecraft server image everything here is built on.

## License

MIT, see [LICENSE](LICENSE).
