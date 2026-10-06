# Design — add-agent-accounts

## D1. Mechanism: a per-process config dir, not credential swapping

Rejected: swapping the global Keychain login (moves every live session to the
other account); `CLAUDE_CODE_OAUTH_TOKEN` per backend (works today via
`backend.EnvVars`, but leaves one shared `~/.claude`, so settings, history and the
background-session supervisor stay shared). Chosen: one config dir per account,
exported per process. `CLAUDE_CONFIG_DIR` is the officially recommended
multi-account approach for Claude Code; the Keychain entry, `claude agents` /
`claude stop` and the background-session supervisor are all scoped to it.
`CODEX_HOME` is the only isolation point Codex offers. This gives full isolation
and parallel sessions on different accounts, and Argus handles no secret.

## D2. Tool-neutral account on the task, not on the backend

One `[accounts.<name>]` entry is an identity ("work") that can carry a Claude
config dir and a Codex home (`config.Account{Label, ClaudeConfigDir, CodexHome,
Inherit}`). `Config.AccountSupports(name, backendCommand)` decides which backends
it can run. It classifies by the basename of the command's first word: `claude`
needs `claude_config_dir`, `codex` needs `codex_home`, and everything else
(pi, opencode, custom commands such as `sh -c …`) runs only on `default`.

An `account` column on `tasks` mirrors `sandbox_override`: it is resolved once at
the single creation call site (`agent.CreateAndStart`) and then never changes.
Resume and restart read the stored value and never re-resolve it, so changing a
default cannot move an existing task's transcripts out from under it. `default`
is stored as `""`.

## D3. Resolution, explicit vs inherited, and fail-loud

`Config.ResolveAccount(explicit, project)`: explicit, else `project_accounts`,
else `default_account`, else `default`. Invalid project/global names are ignored.

`CreateAndStart` resolves the backend *before* the worktree so the support check
has no side effect:

- An explicit account that is unknown, or that does not support the backend,
  is rejected (`account "X" does not support backend "Y"`).
- An account that came from a project/global default, or was inherited
  (`CreateInput.InheritedAccount`: hera spawns, MCP `task_create` caller
  inheritance), falls back to `default` with an `[account]` log. Without this a
  Claude-only coordinator could never spawn a Codex worker.

**Explicit `"default"` vs `""`.** Clients send the selected name, `"default"`
included. `""` means "no choice was offered" and is resolved through the
defaults. An explicit `"default"` is never re-resolved, so picking `default` on a
project whose default is `work` really runs on `default`. Inheritance uses
`agent.PinnedAccount(stored)`, which turns a parent's stored `""` into
`"default"` so a default-account coordinator's workers do not drift onto a
project default.

**Removed account.** Spawning or resuming a task whose stored account was
removed from config fails with an error naming the account. It never falls back
to `default`: billing the wrong subscription is worse than a refusal. The same
applies to a Codex task whose account no longer defines `codex_home`.

## D4. Single resolver per tool

`Config.ClaudeConfigDir(name)` and `Config.CodexHome(name)` return
`(dir, explicit, err)` and are the only places that map an account to a
directory (`~` expanded, absolute, cleaned). `default` returns `~/.claude` /
`~/.codex` with `explicit=false` (no env var exported). Consumers take the dir
from these helpers via `ClaudeConfigDirForTask` / `CodexHomeForTask`:
`claudesession.ListIn/ProjectDirIn`, `RefreshResumeSessionID`,
`CaptureCodexSessionIDIn`, the reaper, the doctor check, the skills loader, and
the sandbox rules.

## D5. Claude account dir bootstrap

On first spawn, Argus creates the dir with mode 0700 and symlinks each `inherit`
entry that exists in `~/.claude` and is absent in the account dir. The default
list is `CLAUDE.md`, `skills`, `commands`, `agents`. Never linked:
`.credentials*`, `.claude.json`, `projects/`, and `plugins/` (the plugin cache
holds absolute install paths).

`settings.json` is **seeded as a one-time copy**, not symlinked. Whether Claude
writes `settings.json` atomically (rename over the path, replacing a symlink) is
undocumented; a symlink would either be broken by Claude or let one account's
settings writes land in another account's file. The seed opens the target with
`O_EXCL` (mode 0600), so it never overwrites and never writes through an existing
symlink. It drops `apiKeyHelper` (Claude ranks it above the stored
`/login`) and the entire `env` block, which can carry Anthropic auth overrides and
any other secret, none of which may be duplicated into another account's dir. An unparsable source is skipped with a log. A user
can still list `settings.json` in `inherit` to opt back into a symlink.

Credentials are only ever created by the tool's own `/login` / `codex login` run
inside a session on that account. Argus never reads, copies, logs or writes them
(this includes the Keychain, `.credentials.json` and Codex `auth.json`).

## D6. Codex account home

On first spawn the account's `codex_home` is created with mode 0700; nothing is
copied from `~/.codex`. Argus already runs Codex under an Argus-only overlay home
for builtin skills (`skills.EnsureCodexSkills`). The overlay now takes the
source home: `""` gives today's `~/.local/share/argus/codex-home`, and an account
home gets its own `custom-<digest>` overlay that links only that account's
entries (`auth.json` is symlinked, never read). The spawn exports
`CODEX_HOME=<overlay>` (the account home itself if the overlay fails) and
`CODEX_SQLITE_HOME=<account home>`, so session capture and `codex resume` read
that account's `state_5.sqlite`.

A login performed inside an Argus session lands in the overlay (as for the
default account today), so the sign-in probe checks the overlay when one exists.

## D7. Environment hygiene

`spawnBaseEnv` filters `os.Environ()` before backend `env_vars` and the op
bootstrap are appended (so an explicit backend mapping still wins):

- An inherited `CLAUDE_CONFIG_DIR` is always removed, for every task. Someone
  who launches Argus with it set globally gets `~/.claude` for default tasks,
  matching `Config.ClaudeConfigDir("default")`.
- A Claude task on an explicit account also loses `ANTHROPIC_API_KEY`,
  `ANTHROPIC_AUTH_TOKEN` and `CLAUDE_CODE_OAUTH_TOKEN`; a Codex task on an
  explicit account loses `OPENAI_API_KEY` and `CODEX_API_KEY`. These rank above
  the stored login and would silently bill another identity.

Only variable names are logged (`[account]`), never values.

## D8. Supervisor wire

The session-supervisor rebuilds the task for `BuildCmd` from the RPC request,
not from the DB. `StartReq`, `KickReq` and `RecycleReq` carry `Account`, and also
`SandboxOverride`, which `BuildCmd` reads and the wire was already dropping.
`ProtocolVersion` 7 -> 8. The supervisor spawn/stream surfaces are bumped and the
account files are added to the fingerprinted path lists, so `argus doctor` and the
TUI flag an old supervisor. Until it is restarted, an old supervisor spawns on
the default account and ignores per-task sandbox overrides.

## D9. Sandbox

SBPL already allows writes under `$HOME/.claude` and `$HOME/.codex`. The profile
adds a write allow for **only the task's own** account dir, and only when it lies
outside the default dir (`sandboxWithClaudeConfigDir` for Claude backends,
`sandboxWithCodexHome` for Codex). Without it `/login` token persistence and
`.claude.json` atomic writes EPERM-loop. A task never gets write access to another
account's dir. Credential read denies are unchanged.

## D10. MCP stays per-launch

The argus MCP server reaches Claude tasks through the per-process `--mcp-config`
flag and Codex tasks through `-c mcp_servers.argus.url=…`. An explicit-account
task gets the same flag, so nothing is injected into `<dir>/.claude.json`,
`settings.json` or the account's `config.toml`, and no MCP trust write is needed
(`SetClaudeProjectMcpTrust` governs project `.mcp.json` servers, not
`--mcp-config`). An earlier draft's per-account inject helpers were deleted.

## D11. Background-session reaper

`claude agents` / `claude stop` only see sessions under the same
`CLAUDE_CONFIG_DIR`. `claudeagents.List/Stop` take a config dir and set the
command env via `claudeagents.Env` (which removes `CLAUDE_CONFIG_DIR` for the
default account). `Runner.Stop` reaps with the dir the session was actually
spawned with (read from `Session.Cmd.Env`); the resume-time reap uses the task's
account and is skipped with a log if the account is unknown.

## D12. Sign-in labels

The selector shows e.g. `work — you@company.com (Team)` from a cached,
off-UI-thread `CLAUDE_CONFIG_DIR=<dir> claude auth status` (JSON,
`internal/claudeaccount`). A logged-out account shows "not logged in" and is still
selectable. Failures degrade to the bare name and never block the form. For Codex,
`agent.CodexLoginStatus` runs `codex login status` with a 3s timeout, caches for a
minute, uses only the exit code, and discards output unread (it can echo a masked
API key). It is inert under `go test`.

## D13. Inheritance

Hera workers, sub-coordinators and freelancers inherit the coordinator's account
via `resolveOrchestratorAccount` + `PinnedAccount`, with `InheritedAccount` set.
Fork-create keeps the source task's account. MCP `task_create` takes an optional
`account`; without it, a caller identified by `caller_id` or `cwd` passes its
account on as inherited. A personal-account coordinator must not fan workers out
onto the work account.

## Implementation notes (as built)

- The per-project default is the top-level `project_accounts` map, not a field on
  `[projects.<name>]`. This avoids a DB schema change and the config.toml
  overlay's wholesale replacement of project entries (a partial entry would zero
  it). Account definitions live in config.toml only.
- `GET /api/accounts` returns a bare array of `{name, label, claude_config_dir,
  codex_home, supports: {claude, codex}, is_default, logged_in, email?, org?,
  plan?, codex_logged_in?}`, `default` first then configured accounts by name.
  `?project=` makes `is_default` mark that project's default. Lookups run in
  parallel under a 3s budget and degrade to `logged_in: false`.
- `/api/skills` takes optional `account` and `task`; an unknown account or task is
  400; an account with no `claude_config_dir` falls back to `~/.claude`.
- The TUI new-task form takes `[]AccountOption` (`AccountOptionsFromConfig`) and
  filters by backend with the same rule as `AccountSupports`. In remote mode it
  takes names/labels from the daemon (`apistore.Accounts`).
- The macOS client sees backend names, not commands, so `NewTaskAccount.tool
  (forBackend:)` guesses: `codex` is Codex, `pi`/`opencode` neither, anything else
  Claude. The daemon's check is the authority.
- Existing local databases may keep a stale `claude_account` column from an
  earlier draft next to `account`. Harmless; no migration (single-user policy).
- Open questions resolved: `settings.json` is seeded as a copy, not symlinked
  (D5); hera per-worker account override deferred (Non-Goals).
