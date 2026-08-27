# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Cross-platform CLI statusline renderer for Claude Code **and GitHub Copilot CLI**. Single Go binary that reads JSON from stdin (provided by the host CLI) and outputs 3 ANSI-colored lines to stdout showing model info, context usage, rate limits/AIU, git status, system stats, session duration, and cost.

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

**Single-file application** (`main.go`, ~1450 lines). No packages, no modules beyond main.

### Data Flow

```
stdin JSON → parse StatusLineInput → normalize() → Statusline → fetch external data → render 3 lines → stdout
```

`StatusLineInput` is a superset of both JSON dialects. `detectFlavor()` picks the flavor from
Copilot-only fields (`remote`, `ai_used`, `context_window.current_context_tokens`), and `normalize()`
maps it to the flavor-independent `Statusline` struct that all render functions consume.

### Three Output Lines

1. **Model + Context + Usage**: Model name, context window progress bar, then flavor-specific trailing elements — Claude Code: 5h/7d rate limits with reset times (`rate_limits`) plus one element per model-specific weekly limit (e.g. `Fable`, fetched from the OAuth usage endpoint, see below); Copilot: AIU consumption (`ai_used.formatted`) + session lines added/removed. Elements are dropped from the right as the terminal narrows (`termWidth/40` elements max: <80 → 1, <120 → 2, ≥120 → 3)
2. **Workspace + Git + Worktree**: Shortened CWD, git status (changes, staged, stash, unpushed, unpulled), worktree name if active (Claude Code only)
3. **System + Session + Cost**: CPU/RAM mini progress bars, session duration, then session cost in USD (Claude Code) or API time (Copilot, which has no USD cost)

### Data Sources

All model, context, 5h/7d rate limit, AIU, cost, and worktree data comes from the host CLI's native stdin JSON. External sources:

| Source | Function | Notes |
|--------|----------|-------|
| Git CLI commands | `getGitData()` | Uses `--no-optional-locks` and `core.useBuiltinFSMonitor=false` flags |
| gopsutil library | `getSystemStats()` | CPU (100ms sample) and RAM percentage |
| Parent process | `getSessionDuration()` | Session duration from parent process creation time (Claude Code; Copilot uses `cost.total_duration_ms`) |
| Anthropic OAuth usage endpoint | `fetchScopedLimits()` | Model-specific weekly limits (e.g. Fable). Claude Code flavor only, and only when stdin has `rate_limits` (subscription session). Runs in a goroutine parallel to git/sysstats |

### Usage Endpoint (model-specific weekly limits)

Claude Code's `/usage` dialog shows a per-model weekly limit ("Current week (Fable)"), but the statusline stdin JSON
does **not** carry it — `rate_limits` is built from the header-based in-memory state and only ever has `five_hour`
and `seven_day` (verified against the 2.1.247 bundle; the statusline JSON has no other undocumented fields apart from
`remote.session_id` in `--remote` mode). The dialog uses a different path, which this binary calls itself:

- `GET https://api.anthropic.com/api/oauth/usage` with `Authorization: Bearer <accessToken>`,
  `Content-Type: application/json`, `anthropic-beta: oauth-2025-04-20`. **Undocumented API**, reverse-engineered from
  Claude Code 2.1.247 — may change without notice. Claude Code itself uses a 5 s timeout; we use 3 s.
- Token comes from `$CLAUDE_CONFIG_DIR/.credentials.json` (default `~/.claude/`), field `claudeAiOauth.accessToken`.
  An expired token (`expiresAt`, ms) is treated as an error. The **refresh token is never used** — Claude Code rotates
  it itself and a foreign refresh would break its session. macOS Keychain is not read, so the element is simply absent there.
- Only `limits[]` entries with `kind == "weekly_scoped"` and a `scope.model.display_name` are used; the name becomes
  the label (`📅 Fable: 6% 09:59`, rendered via `renderRateLimitElement()` with the usual traffic-light colors).
- Cache: `$TMPDIR/statusline-usage-cache.json` (`fetched_at`, `checked_at`, raw `body`), written atomically via
  temp file + rename because the statusline runs every few hundred ms while streaming and the endpoint returns 429 when
  polled too often. TTL 60 s (`checked_at`, also bumped after failures → backoff); on failure data up to 15 min old
  (`fetched_at`) is still shown, otherwise the element is dropped. Never prints to stdout, never exits non-zero.
- `$TMPDIR/statusline-debug.log` gets a `usage=<cache|network|stale: …|none: …|skipped> scoped=<n>` line per run.

### Key Conventions

- All comments and git status labels are in **German** (e.g., "Änderungen", "Sekunden")
- Traffic light color scheme via `getColorForPercentage()`: green (0-30%) → amber (50-70%) → red (85%+)
- Graceful degradation: no git repo → "N/A", missing rate limit → "N/A", usage-endpoint failure → stale cache (≤15 min) or element dropped
- `renderProgressBar()` for usage bars, `renderMiniBar()` for compact system stats

### Copilot Context Window Gotcha

Copilot's `context_window` exposes two different views, and only one of them is usable for a statusline:

- **Raw view** (`used_percentage`, `remaining_percentage`, `last_call_*`) covers **only the last API call**
  against the full model window. It jumps around erratically and must not be shown as context usage.
- **Display view** (`current_context_tokens`, `displayed_context_limit`, `current_context_used_percentage`)
  is what Copilot's own `/context` shows. `copilotContext()` uses this one.

`displayed_context_limit` and `current_context_used_percentage` are **absent** (not zero) when not > 0,
and several other fields are nullable — hence the pointer types in `StatusLineInput`. Without a limit the
context element degrades to "Ctx: N/A" rather than guessing a window size.

Percentages are recomputed from tokens via integer division, so the value can read 1% lower than
Copilot's own rounded display (e.g. 25% vs 26%). This matches how the Claude Code path already behaves.

## Dependencies

Only one external dependency: `github.com/shirou/gopsutil/v3` for CPU/memory stats. Everything else uses Go stdlib.

## Integration

### Claude Code

Binary is configured in `~/.claude/settings.json` (`make install`):
```json
{
  "statusLine": {
    "type": "command",
    "command": "/path/to/claude-statusline"
  }
}
```

### GitHub Copilot CLI

Same binary, installed as `~/.copilot/statusline` via `make install-copilot`, configured in
`~/.copilot/settings.json`. Verified against CLI **1.0.80** by reading the bundle
(`~/.cache/copilot/pkg/<platform>/<version>/app.js`, functions `Hxi`/`Gxi`):

```json
{
  "statusLine": {
    "type": "command",
    "command": "~/.copilot/statusline"
  }
}
```

Contract, as implemented in 1.0.80 — worth knowing because it differs from Claude Code:

| Aspect | Behavior |
|--------|----------|
| `type` | Optional; if present must be `"command"` |
| `command` | Expands `~`, `$VAR`, `${VAR:-default}`. **Relative paths containing `/` resolve against the session cwd, not `~/.copilot`** — use `~/` or an absolute path |
| `padding` | Spaces prepended to *each* line (default 0). Leave at 0: the renderer already uses the full terminal width, so padding causes overflow |
| `refreshInterval` | Integer seconds (1–2147483). Omitted = refresh on events only |
| Multi-line | Supported — output is split on `\n`, padded per line, rejoined. No line limit in the code |
| Timeout | **10 s hard**, then SIGTERM/SIGKILL. This binary needs ~0.6 s |
| Exit code | Must be 0, otherwise the CLI surfaces the error and stderr |
| stdin | JSON without trailing newline; read to EOF |
| stdio | stdout **and stderr are piped**, so `term.GetSize(stderr)` fails — see the width gotcha below |

Copilot has no experimental flag for this any more; `STATUS_LINE` does not appear in the 1.0.80 bundle.
On Windows the installed name is `statusline.exe` — adjust `command` accordingly and mind the path gotcha below.

### `tput cols` Width Gotcha

**`tput cols` returns the terminfo default (usually 80) instead of an error when stdout is not a TTY.**
Because it "succeeds", it swallows every later probe in `getTerminalWidth()`. Claude Code leaves stderr
on the TTY so step 2 wins there, but Copilot pipes stdout *and* stderr — so every statusline rendered
under Copilot was stuck at 80 columns (no progress bars, compact CPU/RAM) regardless of real width.

Fix: probe `/dev/tty` **before** `tput cols` on Unix. `/dev/tty` is the controlling terminal and reports
the true size no matter which streams are piped. `tput` stays as the fallback for Mintty/Git Bash, where
the Windows console APIs fail; on Windows the `CONOUT$` probe keeps its original position after `tput`.

Order matters — do not reshuffle without re-testing. Verify with a pseudoterminal harness (pty of known
width as controlling terminal, stdout/stderr piped), not from a normal shell: a plain terminal has stderr
on the TTY and so never exercises this path. `$TMPDIR/statusline-debug.log` records every probe's result
(`stderrW`, `ttyW`, `tput`) for the last invocation.

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
