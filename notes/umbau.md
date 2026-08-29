# Umbau des Watchers: ein Thema pro Ordner, eine Aufgabe pro Datei

## Ausgangslage

`watcher/` ist ein einziges flaches `package main`: 44 Quelldateien, 38 Testdateien,
16.400 Zeilen, alles im selben Namensraum. Jede Funktion kann jede andere sehen,
also tut sie es auch.

Die größten Brocken und was sie jeweils alles gleichzeitig sind:

| Datei | Zeilen | Wieviele Themen darin |
|-------|--------|-----------------------|
| `config.go` | 638 | Struktur, Suchpfade, Laden, ENV-Overrides, Validierung, abgeleitete Pfade, plus vier allgemeine Helfer (`fileExists`, `expandHome`, `contains`, `dedupe`) |
| `handler.go` | 479 | Verbindungsannahme, Handshake, Status-Pfad, Login-Pfad, Transfer-Pfad, rohes Weiterleiten, Hostname-Filter |
| `wake.go` | 475 | `.server-info.json` lesen/schreiben, Magic Packet bauen, Erreichbarkeit prüfen, Boot-Ablauf steuern, Cooldown und Backoff, Sessions zählen |
| `cmd_init.go` | 407 | der Prompter (wiederverwendbare Eingabe), der Wizard selbst, Broadcast-Ermittlung, Config schreiben, Dateirechte |
| `cmd_check.go` | 402 | zwölf inhaltlich unabhängige Prüfungen plus der Report-Formatierer |
| `assets.go` | 341 | Dateiauflösung pro Welt, MOTD, Icon, PNG dekodieren, Overlay komponieren, Cache |

Dazu kommt: `runInit()` ist 115 Zeilen und macht acht Dinge nacheinander,
`Waker` ist ein Typ mit fünf Zuständigkeiten, und der `checker` mischt Prüflogik
mit Ausgabeformatierung.

Nichts davon ist kaputt. Es ist nur nicht mehr die Struktur, die zu 16.000
Zeilen passt.

## Ziel

Fachliche Trennung wie in `umschreiben.md` für das Smart Pixel Dashboard: unten
die Bausteine, die nichts voneinander wissen müssen, darüber die Abläufe, ganz
oben die Kommandos, die ein Mensch aufruft.

```text
watcher/
├── main.go                 nur Versionsvariable und Dispatch
├── mcwod.service
├── assets/                 bleibt der Laufzeitordner für eigene MOTDs und Bilder
└── internal/
    │
    ├── config/             die config.yml: Typen, Laden, ENV, Validierung, abgeleitete Pfade
    ├── logging/            der Logger
    ├── ui/                 Prompter, Farben, Hinweise, Menü-Bausteine
    ├── embedded/           alles was per go:embed im Binary landet
    │
    ├── mcproto/            Minecraft-Pakete kodieren und dekodieren, kein Netzwerk
    ├── netprobe/           ICMP, ARP, TCP-Dial, Interface- und Broadcast-Rechnerei
    ├── wol/                Magic Packet, broadcast und unicast, die beiden Build-Tags
    ├── sshx/               SSH-Client, Host-Key-Politik, Schlüsselverwaltung
    ├── remote/             was auf dem Server läuft: Helper-Skript, Docker- und Compose-Befehle
    ├── serverinfo/         die gelernte Version und Slots, .server-info.json
    │
    ├── boot/               der Weckablauf: WoL, warten, starten, warten, Sessions, Backoff
    ├── assets/             MOTD- und Icon-Auflösung, Overlays, Cache
    ├── proxy/              Listener, Verbindungslimits, Status-, Login- und Transferpfad
    ├── sleep/              der Auto-Sleep-Beobachter
    │
    ├── worlds/             Weltenliste, anlegen, wechseln, Version ändern
    ├── players/            Whitelist und Admins
    ├── compose/            Compose-Datei erzeugen, einfügen, sichern, zurückholen
    ├── update/             Release suchen, Prüfsummen, sich selbst ersetzen
    ├── yamledit/           YAML ändern ohne Kommentare zu verlieren
    │
    └── cli/                ein Kommando pro Datei: init, check, config, setup-ssh,
                            worlds, players, install, update, get-server-icon, home
```

19 Pakete auf 16.400 Zeilen, im Schnitt 800 Zeilen pro Paket. Die Reihenfolge im
Baum ist die Abhängigkeitsrichtung: alles darf nach oben zeigen, nie nach unten.
`config` und `logging` kennen niemanden, `cli` kennt alle.

## Warum `internal/` und warum `main.go` oben bleibt

**`internal/`** heißt in Go: außerhalb dieses Moduls nicht importierbar. Damit
kostet das Exportieren von Bezeichnern, das beim Paketwechsel unvermeidlich ist,
keine öffentliche API. Nichts davon wird jemals versehentlich zur Schnittstelle
für Fremde.

**`main.go` bleibt in `watcher/`**, weil jeder Build-Aufruf im Projekt
`go build .` aus `watcher/` heraus ist und weil die Version über
`-ldflags "-X main.version=..."` gesetzt wird, an vier Stellen: `ci.yml`,
`release.yml`, `Dockerfile`, `install.sh`. Bleibt `version` in `package main`,
ändert sich an Workflows, Dockerfile und Installer keine Zeile. Ein
`cmd/mcwod/`-Unterordner wäre die reine Lehre und würde vier Build-Rezepte
anfassen, ohne dass irgendetwas besser wird.

## Die drei Stellen, an denen es wehtut

### 1. `go:embed` kann nicht nach oben zeigen

Eingebettet werden heute `assets/embed/*.png` (die Z-Overlays), `assets/examples/`
und `mcwod.service`. Ein `//go:embed`-Pfad ist immer relativ zum Paketordner und
darf kein `../` enthalten. Die Dateien müssen also dorthin, wo das einbettende
Paket liegt:

```text
internal/embedded/
├── embed.go
├── overlays/     overlay-sleeping.png, overlay-starting.png
├── examples/     die vier MOTDs, das Platzhalter-Icon, die README
└── service/      mcwod.service
```

Das räumt nebenbei eine Doppelbelegung weg: `watcher/assets/` ist zur Laufzeit
der Ordner, in den **der Benutzer** seine MOTDs und Bilder legt, und im
Repository gleichzeitig die Quelle für das, was **ins Binary** kompiliert wird.
Zwei verschiedene Dinge unter einem Namen. Danach ist `watcher/assets/` nur noch
das eine, und `internal/embedded/` das andere.

Der Preis: der README-Verweis auf `watcher/assets/examples/` muss auf
`internal/embedded/examples/` zeigen, oder besser ganz verschwinden, weil `mcwod
install` die Beispiele seit 2.1.0 sowieso aus dem Binary schreibt.

### 2. Testdateien, die mehrere Schichten gleichzeitig fahren

Die meisten der 38 Testdateien ziehen einfach mit ihrem Code um. Vier tun das
nicht, weil sie den ganzen Stapel testen: `mcserver_test.go` (der falsche
Minecraft-Server), `handler_test.go`, `coldstart_test.go`,
`statusfallback_test.go`. Dazu die SSH-Attrappe in `sshclient_test.go`.

Plan: die Attrappen wandern in ein `internal/testsupport`, die
schichtübergreifenden Tests werden Integrationstests neben `proxy` und benutzen
nur exportierte Aufrufe. Das ist der einzige Teil des Umbaus, bei dem Tests
inhaltlich angefasst werden, und genau deshalb bekommt er einen eigenen Commit.

### 3. Bezeichner werden exportiert, ohne dass es öffentlich wird

Über Paketgrenzen hinweg braucht jeder Zugriff einen Großbuchstaben. Das macht
aus `readVarInt` ein `mcproto.ReadVarInt`. Zwei Regeln dagegen:

- Nur exportieren, was wirklich über die Grenze muss. Alles andere bleibt klein,
  auch wenn es beim Verschieben kurz verlockend ist.
- Nach jedem Paket einmal prüfen, ob das entstandene Paket-API kleiner als zehn
  Namen ist. Wenn nicht, ist der Schnitt an der falschen Stelle.

## Was innerhalb der Pakete passiert

Der Ordner allein macht noch nichts besser. Die groben Dateien werden dabei
zerlegt:

**`config.go`, 638 Zeilen → `internal/config/`**
`types.go` (nur die Structs), `load.go` (Suchpfade und Einlesen), `env.go` (die
`MCWOD_*`-Overrides und die `MC_WOL_*`-Warnung), `validate.go`, `paths.go` (die
abgeleiteten Pfade: Assets, ServerInfo, SSH-Key, KnownHosts). Die vier
allgemeinen Helfer gehen nach `internal/fsx` oder werden dort inline, wo sie
gebraucht werden.

**`wake.go`, 475 Zeilen → drei Pakete**
Der Cache nach `serverinfo`, das Magic Packet nach `wol`, das Dialen und Pingen
nach `netprobe`, und was übrig bleibt, ist `boot`: die Ablaufsteuerung mit
Cooldown, Backoff und Sessionzählung. `Waker` hört damit auf, fünf Dinge zu sein.

**`handler.go`, 479 Zeilen → `internal/proxy/`**
`handler.go` (nur noch annehmen und verzweigen), `status.go`, `login.go`,
`transfer.go`, `pipe.go`, `hostfilter.go`.

**`cmd_init.go`, 407 Zeilen → drei Orte**
Der Prompter nach `ui/prompt.go`, die Broadcast-Rechnerei nach
`netprobe/broadcast.go`, das Schreiben nach `config/write.go`, und `runInit()`
zerfällt in `askServer`, `askProvisioning`, `askNetwork`, `askDuckDNS`,
`askTransfer`, `writeAndReport`. Danach ist der Wizard eine Liste von Fragen und
nicht mehr eine Funktion mit acht Abschnitten.

**`cmd_check.go`, 402 Zeilen → `internal/cli/check/`**
Der Formatierer wird zu `report.go`, jede Prüfung bekommt ihre eigene Datei nach
Thema: `assets.go`, `ssh.go`, `server.go`, `helper.go`, `sleep.go`, `duckdns.go`.
`runCheck()` ist dann die Liste der Schritte in der Reihenfolge, in der sie
voneinander abhängen, und sonst nichts.

**`assets.go`, 341 Zeilen → `internal/assets/`**
`resolve.go` (welche Datei gilt für welche Welt), `motd.go`, `icon.go`,
`overlay.go`, `cache.go`, `png.go`.

## Reihenfolge

Ein Commit pro Schritt, und nach jedem Schritt sind `go build ./...`,
`go vet ./...`, `go test ./...` und `gofmt -l .` grün. Kein Schritt verschiebt
und ändert gleichzeitig: erst umziehen, Verhalten identisch, danach in einem
eigenen Commit zerlegen.

1. `internal/logging` und `internal/config`. Das Fundament, das alle anderen
   brauchen, und der Schritt, der am meisten Dateien anfasst, weil jeder
   `cfg *Config` zu `*config.Config` wird.
2. `internal/mcproto`. Hängt von nichts ab, hat den dichtesten Testsatz, ist
   damit der beste Beweis, dass die Methode trägt.
3. `internal/embedded`, mit dem Verschieben der eingebetteten Dateien.
4. `internal/ui`, `internal/netprobe`, `internal/wol`. Kleine, unabhängige Kisten.
5. `internal/sshx`, `internal/remote`, `internal/serverinfo`.
6. `internal/boot`, `internal/assets`, `internal/proxy`, `internal/sleep`. Hier
   kommt `mcwod run` im Ganzen zusammen.
7. `internal/worlds`, `internal/players`, `internal/compose`, `internal/update`,
   `internal/yamledit`.
8. `internal/cli` und `main.go` auf reines Dispatch eindampfen.
9. `internal/testsupport` und die vier Integrationstests.
10. Die Zerlegung innerhalb der Pakete aus dem Abschnitt oben, thematisch
    gruppiert in mehreren Commits.

Schritt 1 bis 8 sind mechanisch: verschieben, Paketnamen setzen, exportieren,
Importe geraderücken. Schritt 9 und 10 sind die, bei denen wirklich gedacht wird.

## Wann

**Nach dem 2.1.0-Release.** `dev` liegt 61 Commits vor `main`, und die
Code-Commits davon sollen einzeln per Cherry-Pick hinüber. Ein Umbau, der 44 Dateien verschiebt, macht jeden
noch nicht übernommenen Cherry-Pick zu einem Konflikt. Erst 2.1.0 aus `main`
taggen, dann `dev` auf den Stand von `main` bringen, dann umbauen. Der Umbau
selbst wandert danach als ein einziger zusammenhängender Block nach `main` und
ist damit auch für jemanden, der die Historie liest, eine Entscheidung und nicht
ein Rauschen aus 44 Umbenennungen.

## Was ausdrücklich nicht passiert

- Kein Verhalten ändert sich. Keine Konfigurationsschlüssel, keine
  Kommandonamen, keine Ausgaben.
- Keine neue Abhängigkeit. Der Umbau ist reines Verschieben und Umbenennen.
- Kein `pkg/`-Ordner und keine öffentliche Bibliothek. Alles bleibt `internal/`.
- Kein `cmd/mcwod/`-Unterordner, siehe oben, das kostet vier Build-Rezepte für
  nichts.
- Keine Interfaces auf Vorrat. Eine Schnittstelle entsteht, wenn ein zweiter
  Aufrufer sie braucht, nicht weil ein Paket jetzt eigenständig aussieht.
