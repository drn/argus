# Design — add-opencode-backend

The only non-obvious part is **post-exit session capture**, because opencode's
on-disk layout differs from codex/pi in two ways that bit the obvious approach.

## opencode CLI facts (source: sst/opencode @ v1.0.0 and v1.17.11)

- **Launch:** bare `opencode` is the interactive TUI (runs in Argus's PTY like
  `claude`/`codex`). Initial prompt via `--prompt <text>`; model via
  `--model provider/model`; resume via `--session <id>` (or `--continue`).
- **No start-time session id.** Unlike Claude's `--session-id`, opencode has no
  way to pin a chosen ID at launch — it mints its own `ses_…` ID. So opencode is
  a capture-style backend (codex/pi family), not a pin-style one (Claude).
- **Session ID shape:** `ses_` + 12 hex + 14 base62 (`^ses_[0-9A-Za-z]+$`).

## Capture: two storage formats, one logical model

opencode stores sessions under `$XDG_DATA_HOME/opencode` (default
`~/.local/share/opencode`), keyed by **project = git root-commit hash** which is
**shared across all worktrees of a repo**. The distinguishing field is the
per-session absolute **directory** (the cwd the session ran in = the Argus
worktree). So "find this worktree's session" is always: filter rows by
`directory == worktree`, pick the max updated-time, return its id. Never assume
one project bucket = one worktree.

- **v1.14+ (current): SQLite** at `~/.local/share/opencode/opencode.db`, table
  `session`, columns `id` (TEXT, `ses_…`), `directory` (TEXT, absolute cwd),
  `time_updated` (INTEGER epoch millis). WAL mode — open read-only.
  Query: `SELECT id FROM session WHERE directory = ? ORDER BY time_updated DESC LIMIT 1`.
- **≤v1.13 (legacy): JSON files** at
  `~/.local/share/opencode/storage/session/<projectID>/<ses_id>.json`, each with
  `directory` (string) and `time.updated` (number). Walk all `<projectID>/*.json`,
  filter by `directory`, pick max `time.updated`, return `id`.

### Capture algorithm (`CaptureOpencodeSessionID(worktree)`)

1. Resolve data root: `$XDG_DATA_HOME/opencode` else `~/.local/share/opencode`.
2. If `opencode.db` exists, run the SQLite query (bound with the absolute,
   symlink-resolved worktree path). If it returns a row, validate `ses_…` and
   return it.
3. Else (or if the DB query found nothing / errored), walk the JSON
   `storage/session/*/*.json` tree, filter by `directory`, take max
   `time.updated`.
4. **Fail open:** if neither path yields a match, return an error the callers
   already treat as "leave the pinned ID intact" — for opencode there is no
   pinned ID, so the effect is simply "no resume this round." A schema or
   version mismatch therefore degrades to baseline, never to a broken launch.

Match on the absolute path; resolve symlinks on both sides (opencode normalizes
with `absolute()`; Argus worktrees can sit under symlinked temp roots in tests).

## Where opencode slots into the existing backend branches

opencode == codex/pi everywhere the code currently asks "is this a pin-style
(Claude) backend?" via `!IsCodexBackend && !IsPiBackend`:

- `agent.BuildCmd`: add `isOpencode`; exclude from start-time `--session-id`;
  include in `--model` injection; resume appends `--session <id>` (pi-shaped).
- `agent.CreateAndStart` (create.go) and `App.startSession` (app.go): exclude
  opencode from session-ID pre-minting (it cannot pin one).
- `agent.CaptureSessionID` / `NeedsSessionRecapture`: dispatch opencode to the
  new capture fn; recapture only while `SessionID == ""` (codex/pi semantics).
- `App` ctrl+r session switcher: opencode is **not** Claude → switcher
  unavailable (must be added explicitly; opencode is neither codex nor pi, so it
  would otherwise fall through and be wrongly treated as Claude-capable).
- Session-exit logging: `kind = "opencode"`.

## MCP injection

`internal/inject/opencode.InjectGlobal(port)` reads/writes
`~/.config/opencode/opencode.json` (JSON, like the Claude injector), touching
only `mcp.argus = {type:"remote", url:"http://localhost:<port>/mcp",
enabled:true}` and preserving all other keys. opencode uses `type:"remote"` (not
Claude's `type:"http"`). Idempotent; atomic temp-file write (reuse the
`inject.writeJSON` pattern). Wired into `daemon.go`'s post-`ListenAndServe`
injection goroutine.

## Testing strategy

- `CaptureOpencodeSessionID`: table tests over a `t.TempDir()` fake data root
  (set `XDG_DATA_HOME`/`HOME`) — one seeding a SQLite `opencode.db`, one seeding
  JSON session files, one with mixed-directory rows (asserts the directory
  filter), one empty (asserts fail-open error), one malformed id (rejected).
- `BuildCmd`: resume appends `--session`, new session omits `--session-id`,
  `--prompt` carries the prompt, `--model` injected; mirror existing
  `TestBuildCmd_*` cases.
- `IsOpencodeBackend`, `KnownModels`/`BackendModels` (opencode → custom-only),
  injection idempotency (mirror `claude_test.go` / `codex_test.go`).
