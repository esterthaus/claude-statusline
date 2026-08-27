# claude-statusline

Statusline für **Claude Code** und **GitHub Copilot CLI**. Ein Go-Binary, liest das JSON der CLI von stdin, gibt drei Zeilen aus.

```
Fable  •  🧠 Ctx: 64k/200k ██████░░░░░░░░░░░░░░ 32%  •  ⏱ 5h: ██████░░░░░░░░░ 42%  •  📅 7d: █░░░░░ 18%  •  📅 Fable: ░░░░░░ 12%
📂 ~/projects/statusline  •  ⎇ Änd: 3 | Stg: 1 | ↑2 | ↓0
💻 CPU: █░░░░░░ 12%  •  🎛 RAM: ███░░░░ 41%  •  ⏳ 1h 5m  •  💰 $2.31
```

Zeile 1: Modell, Context-Fenster, Rate Limits (5h / 7d / modellspezifisches Wochenlimit) bzw. AIU bei Copilot.
Zeile 2: Verzeichnis, Git-Status. Zeile 3: CPU/RAM, Sitzungsdauer, Kosten. Passt sich der Terminalbreite an.

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
