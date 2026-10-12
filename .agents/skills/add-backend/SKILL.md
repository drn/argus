---
name: add-backend
description: >-
  Add support for a new agent CLI (e.g. a new coding-agent binary) as an Argus backend at full
  parity with Claude/Codex/Pi/OpenCode/Antigravity: research the CLI, declare its backend family,
  wire session capture/resume, MCP, skills, routing, permissions, sandbox grants, defaults, tests,
  and docs. Use when the user asks to "add <tool> as a backend", "support <CLI> in argus", or to
  extend an existing backend family with a new capability. Not for tweaking an existing backend's
  command template (that is config, not code).
---

# Add a backend

Every per-backend difference lives in ONE place: an `agent.Family` entry in
`internal/agent/backends.go`, plus its static half in `internal/backendkind`. Nothing else in the
tree tests backend identity — BuildCmd, capture, resume refresh, task creation, TUI, REST, daemon
client, prelaunch, sandbox, and global injection all read the registry. If you find yourself
writing `if IsFooBackend(...)` outside `backends.go`, add a Family field instead.

The full capability matrix (what each existing family declares, and why) is in
[context/knowledge/backends.md](../../../context/knowledge/backends.md). Read it first.

## 1. Research the CLI (verify, don't trust cheat sheets)

Install it for real (`brew info --cask <name>` / `brew info <name>`; if `/opt/homebrew` isn't
writable, download the cask URL from `brew info --json=v2 --cask <name>`, verify `sha256`, and copy
the binary to `~/.local/bin`). An Argus sandbox blocks writes to most of `$HOME`, so run probes
with `HOME=<scratch dir>` and inspect what the CLI creates. Record:

| Question | How to find out | Family field |
|---|---|---|
| Binary name(s) | cask artifacts, `which` | `backendkind.Spec.Binaries` |
| How a prompt is passed | `--help`; try positional, `--`, `-p`, `-i` | `DefaultPromptFlag` / `PromptPositional` |
| Can a session ID be pinned at start? | `--session-id`-style flag | `PinsSessionID` |
| Resume form | `--resume`, `--session`, `--conversation`, subcommand | `ResumeFlag` / `ResumeCmd` |
| Where sessions are stored | files under the scratch HOME; `sqlite3 <db> .schema` | `Capture` |
| Can it switch conversations in-session (`/clear`, `/new`)? | slash-command docs | `RecaptureEveryExit` |
| Model flag | `--model`? config-only? | `ModelDelivery`, `KnownModels` |
| Permission/approval flags | `--help` | `PermissionFlags`, `PermissionFlagTokens` |
| Per-launch MCP config? | flag, env var, or only a global file | `MCPFlag` or `InjectGlobal` |
| Skill discovery | per-launch flag, env, config dir, global manifest | `SkillDelivery` |
| Instruction files it reads natively | docs (AGENTS.md? CLAUDE.md?) | `RoutingDelivery` |
| Env vars / config-dir override | `strings <binary> \| grep -E '^[A-Z_]+$'` | accounts, child-only config |
| State dirs it writes under `$HOME` | scratch HOME after a run | `SandboxGrants` (+ `PrecreateSandboxGrants` when their parent isn't writable in the sandbox) |
| Slow startup work before the TUI? | | `Prelaunch`, `StartTimeout` |
| Skill invocation prefix in prompts | docs | `Spec.SkillPrefix` |

Prefer child-scoped delivery (flag or env on the spawned process) over editing the user's global
config. Only fall back to `InjectGlobal` when the CLI offers nothing else, and say so in the
proposal.

## 2. Write the OpenSpec change first

`openspec/changes/add-<name>-backend/` with proposal (verified facts + Non-Goals for anything the
CLI can't support), delta specs (`agent-execution`, `config-management`, plus `mcp-injection`,
`sandbox-execution`, `skill-provisioning` as applicable), and tasks. Get approval.

## 3. Declare the family

1. `internal/backendkind/backendkind.go`: add a `Kind` constant and a `specs` entry (binaries,
   label, skill prefix). `TestAll`'s count assertion will need the new total.
2. `internal/agent/backends.go`: add the `Family` entry. `TestFamilyRegistry_CoversEveryKind` fails
   until every `backendkind` spec has one; `TestFamilyRegistry_Invariants` rejects inconsistent
   combos (capture-style without `Capture`, `PermissionFlags` without tokens, `SkillsGlobal`
   without `InjectGlobal`).
3. If capture-style: add `internal/agent/<name>.go` with `Capture<Name>SessionID(worktree)` —
   open SQLite read-only via `mode=ro` (not `immutable=1`, which misses WAL rows), compare
   canonical paths (`canonPath`), newest first, fail open with an error when nothing matches.
   See `antigravity.go` / `CaptureOpencodeSessionID`.
4. If global config is required: add `internal/inject/<name>/` using `inject/atomicfile`
   (`ReadJSONObject` + `WriteJSON`): idempotent, preserves unrelated keys, leaves unparseable
   files untouched. Point the family's `InjectGlobal` at it; the daemon calls every family's hook
   at MCP-server startup (`injectGlobalBackendConfig`, skipped under `go test`).
5. `internal/config/config.go` `DefaultConfig()`: add the backend entry (command + prompt flag).
   `db.fixupBackends` inserts it into existing databases on next start.
6. A new ModelDelivery/SkillDelivery/RoutingDelivery mechanism (not just a new value of an
   existing one) means extending the matching `switch` in `BuildCmd` — keep it data-driven.

Only touch account code (`claudeaccount.go`, `codexaccount.go`, `config.AccountSupports`) if the
CLI supports a per-process config-dir override; only touch `internal/backendtier` if it exposes
usage numbers. Otherwise list them as Non-Goals.

## 4. Tests

- `internal/backendkind`: classification cases (bare, absolute path, flags, look-alike prefix).
- `internal/agent/backends_test.go`: a `TestBuildCmd_<Name>` table — new session, family-default
  prompt flag, resume with/without ID, model flag, hand-edited permission flag wins, each mapped
  permission mode, MCP port adds no flag (if global).
- Capture against a fixture DB built from the CLI's REAL schema (paste `.schema` output).
- Injector tests with `t.Setenv("HOME", t.TempDir())` — never touch the real `$HOME`.
- Adding a default backend changes sorted backend order and default counts: grep tests for
  hardcoded `"claude"`-is-first or backend-count assertions (`internal/tui/settings_test.go`,
  `smoke_test.go`, `internal/db/db_test.go` were hit by antigravity) and derive them instead.

## 5. Frontends

None of the three clients hardcode backends anymore: `GET /api/backends` returns `kind`, `label`,
`skill_prefix`, `session_switcher` per backend (`newBackendJSON`), and the web SPA and macOS app
read them. Only add the new name to the macOS name-based fallbacks
(`NewTaskAccount.tool(forBackend:)`, `AppState.isLikelyClaudeBacked`) — those exist only for a
daemon too old to send `kind`. Run `make mac-test` if you touch Swift.

## 6. Finish

1. Text that enumerates backends: `internal/mcp/server.go` + `internal/mcp/hera.go` model
   descriptions, `internal/skills/builtin/hera/SKILL.md`, README Reference backend table.
2. `internal/daemon/surface.go`: bump `SupervisorSpawnSurface` with a history bullet, then run
   `go test ./internal/daemon/ -run Surface` and paste the printed `SpawnSurfaceDigest`.
3. Add the new family's row to `context/knowledge/backends.md` and any non-obvious gotcha to
   `context/knowledge/gotchas/misc.md`.
4. Archive the OpenSpec change into `openspec/specs/` in the same PR.
5. `make pre-pr` clean.

## Verify end to end

Create a task on the new backend in a scratch project, confirm it launches in the sandbox (no
EPERM under the CLI's state dir), exit, confirm `task.SessionID` was captured (`argus` task detail
or the DB), resume, and confirm the same conversation reopens. Check the Argus MCP tools appear
inside the session.
