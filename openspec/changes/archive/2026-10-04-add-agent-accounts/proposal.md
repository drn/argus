# Add agent accounts (work / personal) selectable per task

## Why

Argus can only run Claude Code and Codex as whichever account the daemon's
environment resolves to (`~/.claude` + the default Keychain login, `~/.codex` +
its `auth.json`). A user with a work and a personal subscription has no way to
choose which one a task bills to. Third-party switchers (clauth, claude-swap,
ccswitch, …) do not help: they wrap the interactive `claude` command or swap the
*global* login. Argus builds the child environment itself in `BuildCmd` and runs
many concurrent sessions from a launchd daemon, so a global swap would silently
move every live session to the other account.

Verified (claude 2.1.289 + the official Claude Code authentication docs): a
process started with `CLAUDE_CONFIG_DIR=<dir>` is a fully isolated Claude Code.
It has its own login (the macOS Keychain entry is keyed per config dir), its own
`projects/` transcripts, its own global config at `<dir>/.claude.json`, and its
own `claude agents` / `claude stop` / background-session supervisor scope. This is
the officially recommended multi-account approach. Codex keeps all state and
`auth.json` under `CODEX_HOME` and has no first-class multi-account support;
swapping `CODEX_HOME` per process is the only workable approach.

## What Changes

- New named, tool-neutral **accounts**: `[accounts.<name>]` in `config.toml`. Each
  has a `claude_config_dir` and/or a `codex_home` (at least one required), an
  optional `label`, and an `inherit` list of shared entries symlinked from
  `~/.claude` into the Claude config dir (default `CLAUDE.md`, `skills`,
  `commands`, `agents`). `settings.json` is seeded as a one-time copy instead of a
  symlink, with auth-overriding keys removed. A global `default_account` and a
  top-level `project_accounts` map (project name -> account) choose the default.
- The implicit account `default` means each tool's own default dir. When nothing
  is configured, behavior is the same as before.
- An account supports a Claude backend only if it defines `claude_config_dir`,
  a Codex backend only if it defines `codex_home`, and no other backend
  (pi, opencode, custom commands run on `default` only).
- New tasks record an `account` (column on `tasks`). Resolution at creation:
  explicit selection, then the project default, then the global default, then
  `default`. An explicitly chosen account that is unknown or does not support the
  backend is rejected before any side effect. A project/global default or an
  inherited account that does not support the backend falls back to `default`
  with an `[account]` log. The account is stored on the task and **immutable**.
- Spawn: Claude tasks on an explicit account get `CLAUDE_CONFIG_DIR`. Codex tasks
  get `CODEX_HOME` (Argus's per-account skills overlay) plus `CODEX_SQLITE_HOME`
  (the account's own home). Inherited auth overrides that outrank a stored login
  are stripped (`ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`,
  `CLAUDE_CODE_OAUTH_TOKEN`; `OPENAI_API_KEY`, `CODEX_API_KEY`). An inherited
  `CLAUDE_CONFIG_DIR` is always dropped. A stored account later removed from
  config fails the spawn instead of falling back.
- The supervisor RPC carries `Account` (and the previously dropped
  `SandboxOverride`) on `StartReq` / `KickReq` / `RecycleReq`; `ProtocolVersion`
  goes from 7 to 8.
- Account-aware consumers: Claude session discovery, resume-time session-ID
  recapture, Codex session capture, the background-session reaper (`claude agents`
  / `claude stop` under the session's own `CLAUDE_CONFIG_DIR`), the doctor
  per-account Stop-hook + login check, skill autocomplete, and the sandbox write
  rules (only the task's own account dir).
- The argus MCP server reaches every task through the existing per-launch flag
  (`--mcp-config` for Claude, `-c mcp_servers.argus.url=…` for Codex). Nothing is
  written into an account's `.claude.json`, `settings.json` or `config.toml`.
- **New-task form** (TUI, web, macOS) gains an "Account" selector listing
  `default` plus the accounts that support the selected backend. It is
  preselected from the project/global default, labelled with the Claude sign-in
  identity when known, and hidden when only `default` applies.
- Inheritance: hera workers, sub-coordinators and freelancers inherit the
  spawning coordinator's account (a coordinator on `default` pins `default`).
  Fork-create keeps the source task's account. MCP `task_create` inherits the
  calling task's account when `caller_id` / `cwd` identifies it, and accepts an
  explicit `account`.
- REST: `account` on `POST /api/tasks` and in task JSON; `GET /api/accounts`
  (with optional `?project=`); `/api/skills` takes optional `account` / `task`.

## Non-Goals (named follow-ups)

- **Switching a running task's account.** Not possible by construction: the
  transcript lives in the account dir. Fork to continue under another account.
- **Settings-UI editing of accounts.** v1 is `config.toml`-only (like backend
  `models`); a TUI/web/macOS editor is a follow-up.
- **Per-account usage-budget / tier routing.** The Claude `/usage` probe and the
  Codex usage probe (`internal/backendtier/codexprobe.go`) measure the default
  account only.
- **Per-schedule `account`.** Schedules resolve via the project/global default.
- **API-key / Bedrock / Vertex accounts.** Only subscription logins stored in a
  config dir / Codex home.
- **Hera per-worker account override.** The TUI Hera spawn form shows the Account
  field but its handlers ignore it; spawns always inherit the coordinator's
  account. Hide or honor the field in a follow-up.
- **`hera_new_orchestrator` coordinator inheritance.** `SpawnHeraCoordinator`
  does not inherit the calling task's account; it resolves through the
  project/global default.
- **Codex sign-in in clients.** `GET /api/accounts` returns `codex_logged_in`, but
  no client renders it yet, and it is unverified against a real `codex` binary.
- **Remote-TUI skill autocomplete per account.** Remote mode still reads the local
  `~/.claude`, because the daemon's paths do not exist on the client host.
- **Writing MCP entries into account config.** Not needed (per-launch flags) and
  not built; the unused `inject.InjectGlobalAt` / `InjectAccounts` /
  `SetClaudeProjectMcpTrustAt` helpers from an earlier draft were deleted.

## Frontend Parity

TUI new-task form, REST (`account` field, `GET /api/accounts`, `/api/skills`
params), web SPA new-task form, and macOS new-task sheet via ArgusKit are all in
scope. Hera mutations remain TUI-only, as before.

## Impact

Specs: `agent-execution`, `config-management`, `sandbox-execution`,
`skill-provisioning`, `llm-backends`, `mcp-server`, `rest-api`,
`forms-and-modals`. `mcp-injection` is unchanged.

Code: `internal/config` (`accounts.go`), `internal/model`, `internal/db`,
`internal/agent` (`agent.go`, `create.go`, `claudeaccount.go`,
`codexaccount.go`, `hera_spawn.go`, `resume.go`, `runner.go`, `bgsessionreap.go`),
`internal/claudeaccount`, `internal/claudeagents`, `internal/claudesession`,
`internal/skills`, `internal/usagebudget`, `internal/backendtier`,
`internal/daemon` (wire + surface), `internal/mcp`, `internal/api` (+ `static/`,
`SW_VERSION`), `internal/apiclient`, `internal/apistore`,
`internal/tui/newtaskform.go`, `cmd/argus/doctor.go`, `macos/`.
