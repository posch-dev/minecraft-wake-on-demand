# Examples

Nothing in this folder is used. Copy a file one level up into `assets/` and
change the text, that is all there is to it.

```bash
cp examples/motd-sleeping.json .
```

## The message people see

`motd-sleeping.json` is shown while your PC is asleep, `motd-starting.json`
while it is waking up, and `motd-live.json` while it is running.

Leave `motd-live.json` out and the running server's own message is shown, which
is what most people want. Put it in and yours replaces it, so you can set the
message here instead of on the server.

Each file holds two lines. `\n` is where the first line ends and the second
begins, and it has to stay where it is.

Colours Minecraft accepts:

```
black        dark_blue    dark_green   dark_aqua
dark_red     dark_purple  gold         gray
dark_gray    blue         green        aqua
red          light_purple yellow       white
```

A file beats the matching `motd.*` line in `config.yml`, which beats the
built-in text. So you only need a file for what you actually want to change.

## The picture

`server-icon.png` is the small square people see next to your server. Ours is a
placeholder, replace it with your own.

It has to be a **64x64 PNG** and under 64 kB. Anything else is skipped, and the
reason is written to the log.

Without one you get the built-in picture, three blue Z that turn into a red
exclamation mark while the PC wakes up. Your own picture does not replace that,
it shows through underneath it.

To replace the whole thing instead, use one of these names in `assets/`:

| File | Replaces |
|------|----------|
| `server-icon-sleeping.png` | the sleeping picture, Z and all |
| `server-icon-starting.png` | the waking picture |
| `server-icon-live.png` | the running server's own picture |

`mcwod get-server-icon` copies the picture your running server already uses, so
you do not have to find the file yourself.
