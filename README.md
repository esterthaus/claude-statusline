# claude-statusline

Statusline für **Claude Code** und **GitHub Copilot CLI**. Ein Go-Binary, liest das JSON der CLI von stdin, gibt drei Zeilen aus.

```
Fable  •  🧠 Ctx: 64k/200k ██████░░░░░░░░░░░░░░ 32%  •  ⏱ 5h: ██████░░░░░░░░░ 42% 11:54 →74%  •  📅 7d: █░░░░░ 18% →26%  •  📅 Fable: ░░░░░░ 12% →92%
📂 ~/projects/statusline  •  ⎇ Änd: 3 | Stg: 1 | ↑2 | ↓0
💻 CPU: █░░░░░░ 12%  •  🎛 RAM: ███░░░░ 41%  •  ⏳ 1h 5m  •  🧊 47m 99%  •  💰 $2.31
```

Zeile 1: Modell, Context-Fenster, Rate Limits (5h / 7d / modellspezifisches Wochenlimit) bzw. AIU bei Copilot.
Hinter jedem Limit steht die Hochrechnung auf das Fensterende (`→74%`): der bisherige Durchschnittsverbrauch
des Fensters auf dessen Ende projiziert — grün unter 90 %, amber bis 110 %, darüber rot. Restdauern ab einem
Tag werden als `2d 5h` abgekürzt, Reset-Zeitpunkte jenseits von 24 h bekommen den Wochentag (`Sa 14:44`).
Zeile 2: Verzeichnis, Git-Status. Zeile 3: CPU/RAM, Sitzungsdauer, Restwärme des Prompt-Caches (nur Claude Code), Kosten. Passt sich der Terminalbreite an.

## Installation

Binary vom [Release](https://github.com/esterthaus/claude-statusline/releases/latest) laden oder selbst bauen (`make build`, Go ≥ 1.25).

### Claude Code

```bash
make install   # kopiert nach ~/.claude/claude-statusline
```

`~/.claude/settings.json`:

```json
{
  "statusLine": {
    "type": "command",
    "command": "~/.claude/claude-statusline"
  }
}
```

### GitHub Copilot CLI

```bash
make install-copilot   # kopiert nach ~/.copilot/statusline
```

`~/.copilot/settings.json`:

```json
{
  "statusLine": {
    "type": "command",
    "command": "~/.copilot/statusline"
  }
}
```

Windows: Pfad mit Vorwärts-Slashes angeben (`C:/Users/<name>/.claude/claude-statusline.exe`), Backslashes werden von Git Bash verschluckt.
