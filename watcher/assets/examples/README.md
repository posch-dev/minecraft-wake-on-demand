# Example assets

Copy a file from here into `assets/` to use it. Nothing in `examples/` is read
by the watcher.

## MOTD

| File | Shown when |
|------|------------|
| `motd-sleeping.json` | the server PC is asleep |
| `motd-starting.json` | it is waking up |
| `motd-live.json` | it is running, **overrides the server's own MOTD** |

Each file holds one Minecraft text component. `\n` splits the two lines the
server list shows. A file beats the matching `motd.*` entry in `config.yml`,
which in turn beats the built-in default. Leave `motd-live.json` out and the
running server's own MOTD is passed through untouched, which is the default.

## Icons

The watcher ships its own sleeping and waking icons, three blue Z that grow, the
largest turning into a red exclamation mark while the PC boots. They are drawn
over an opaque white background, with `assets/server-icon.png` showing through
at half opacity if you put one there.

To replace an icon outright rather than have it drawn over, put a 64x64 PNG at:

| File | Replaces |
|------|----------|
| `assets/server-icon-sleeping.png` | the whole sleeping icon |
| `assets/server-icon-starting.png` | the whole waking icon |
| `assets/server-icon-live.png` | the running server's own icon |

Icons must be exactly 64x64 and under 64 kB, anything else is skipped with a
warning in the log.
