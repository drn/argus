# Tasks — add-agent-accounts

TDD per task; `make pre-pr` must pass before any push. Tests use `t.Setenv("HOME", t.TempDir())` and fake `claude` / `codex` binaries; no test runs the real tools or reads real credentials.

## 1. Config + resolver
- [x] `config.Account{Label, ClaudeConfigDir, CodexHome, Inherit}`; `[accounts.<name>]`, `default_account`, `[project_accounts]` (config.toml overlay)
- [x] `ClaudeConfigDir` / `CodexHome` / `ResolveAccount` (explicit → project → global → default) / `AccountSupports` / `AccountNames` / `AccountNamesFor` + validation (at least one dir, absolute or `~`, `default` reserved)
- [x] Claude dir bootstrap (0700, `inherit` symlinks skip-if-present, never credentials/`.claude.json`/projects/plugins) + one-time `settings.json` seed copy (O_EXCL, 0600, auth keys dropped)
- [x] Codex home bootstrap (0700, nothing copied)

## 2. Task model + persistence
- [x] `model.Task.Account`; `tasks.account` column (schema column ordering gotcha)
- [x] `agent.CreateAndStart` resolves the backend first, then validates the account before any side effect; explicit-unsupported rejects, default/inherited-unsupported falls back with `[account]` log (`CreateInput.InheritedAccount`)
- [x] `agent.PinnedAccount`: inherited `""` pins `"default"`; hera worker/sub-coordinator/freelance spawn + fork-create + MCP `task_create` caller inheritance
- [x] `apistore` convert + remote round-trip

## 3. Spawn
- [x] `BuildCmd`: `CLAUDE_CONFIG_DIR` for Claude explicit accounts; `CODEX_HOME` (per-account overlay) + `CODEX_SQLITE_HOME` (account home) for Codex explicit accounts; fail spawn on removed/unknown stored account; uxlog `[account]`
- [x] `spawnBaseEnv`: always drop inherited `CLAUDE_CONFIG_DIR`; drop Claude / Codex auth overrides for explicit accounts; backend `env_vars` still win
- [x] Sandbox SBPL: write allow for the task's own account dir only
- [x] Supervisor wire: `Account` + `SandboxOverride` on `StartReq` / `KickReq` / `RecycleReq`; `ProtocolVersion` 8; surface path lists + bumps
- [x] MCP stays per-launch flag for both tools; unused inject helpers deleted

## 4. Account-aware consumers
- [x] `claudesession.ListIn/ProjectDirIn` + callers; `RefreshResumeSessionID` reads the account dir
- [x] `CaptureCodexSessionIDIn` / `CodexHomeForTask`
- [x] `claudeagents.List/Stop(configDir)` + `Runner.Stop` reap with the spawn's own `CLAUDE_CONFIG_DIR`
- [x] skills loader (`LoadSkillsFrom`) per account: TUI form + `/api/skills?account=&task=`
- [x] usage probes: default account only (Non-Goal), documented
- [x] `argus doctor`: per-account Stop-hook check + `claude auth status` line
- [x] `agent.CodexLoginStatus` (exit code only, output discarded, inert in tests)

## 5. Surfaces
- [x] TUI new-task form: Account selector filtered by backend, hidden when only `default`, preselect via `ResolveAccount`, label via cached `claude auth status`, remote mode via `apistore.Accounts`
- [x] REST: `account` on create + task JSON; `GET /api/accounts[?project=]` (incl. `codex_logged_in`)
- [x] Web SPA new-task form + skills cache keyed by project/account/task + bump `SW_VERSION`
- [x] macOS: ArgusKit `Account` model + `accounts()` + new-task sheet filtered by backend; `make mac-test`

## 6. Docs + closeout
- [x] gotchas: `misc.md` Accounts section, `sandbox.md`; index row
- [x] README: Also In The Box entry; Reference Accounts section, REST + MCP tables
- [x] Archive in the same PR (`openspec/changes/archive/2026-10-04-add-agent-accounts/`)
