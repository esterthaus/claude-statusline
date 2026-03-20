# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Cross-platform CLI statusline renderer for Claude Code. Single Go binary that reads JSON from stdin (provided by Claude Code CLI) and outputs 3 ANSI-colored lines to stdout showing model info, context usage, rate limits, git status, system stats, session duration, and cost.

## Build & Run Commands

```bash
make build          # Build for current platform
make build-all      # Cross-compile for all 6 targets (windows/linux/darwin × amd64/arm64)
make test           # Pipe sample JSON into the binary to verify it runs
make install        # Copy binary to ~/.claude/
make clean          # Remove build artifacts
```

Build uses `go build -ldflags="-s -w"` for stripped, size-optimized binaries. No Go test files exist; `make test` is an integration smoke test only.

## Architecture

**Single-file application** (`main.go`, ~550 lines). No packages, no modules beyond main.

### Data Flow

```
stdin JSON → parse StatusLineInput → fetch external data in parallel → render 3 lines → stdout
```

### Three Output Lines

1. **Model + Context + Usage**: Model name, context window progress bar (tokens, uses native `used_percentage`), 5h/7d rate limits with reset times (from native `rate_limits` JSON)
2. **Workspace + Git + Worktree**: Shortened CWD, git status (changes, staged, stash, unpushed, unpulled), worktree name if active
3. **System + Session + Cost**: CPU/RAM mini progress bars, session duration, session cost in USD

### Data Sources

All model, context, rate limit, cost, and worktree data comes from Claude Code's native stdin JSON. External sources:

| Source | Function | Notes |
|--------|----------|-------|
| Git CLI commands | `getGitStatus()` | Uses `--no-optional-locks` and `core.useBuiltinFSMonitor=false` flags |
| gopsutil library | `getSystemStats()` | CPU (100ms sample) and RAM percentage |
| Parent process | `getSessionDuration()` | Session duration from parent process creation time |

### Key Conventions

- All comments and git status labels are in **German** (e.g., "Änderungen", "Sekunden")
- Traffic light color scheme via `getColorForPercentage()`: green (0-30%) → amber (50-70%) → red (85%+)
- Graceful degradation: missing credentials → "N/A", no git repo → "N/A", API timeout → cached/N/A
- `renderProgressBar()` for usage bars, `renderMiniBar()` for compact system stats

## Dependencies

Only one external dependency: `github.com/shirou/gopsutil/v3` for CPU/memory stats. Everything else uses Go stdlib.

## Integration

Binary is configured in `~/.claude/settings.json`:
```json
{
  "statusLine": {
    "type": "command",
    "command": "/path/to/claude-statusline"
  }
}
```

### Windows Path Gotcha

On Windows with Git Bash / MSYS2, the command path **must** use forward slashes or MSYS2 notation. Backslashes are interpreted as escape characters by Bash and will silently fail (statusline simply doesn't appear).

```jsonc
// WRONG — Bash interprets \U, \e, \. as escape sequences:
"command": "C:\\Users\\username\\.claude\\claude-statusline.exe"

// CORRECT — MSYS2/Git Bash path notation:
"command": "/c/Users/username/.claude/claude-statusline.exe"

// ALSO WORKS — forward slashes without MSYS prefix:
"command": "C:/Users/username/.claude/claude-statusline.exe"
```

`make install` copies the binary to `~/.claude/` which resolves correctly on all platforms.

## Release

GitHub Actions workflow (`.github/workflows/release.yml`) triggers on `v*` tags. Builds all 6 platform binaries and creates a GitHub release.
