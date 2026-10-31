## 1. Config

- [ ] 1.1 Tests first: `auto` accepted by `ValidateAccount` / `ResolveAccount` for `default_account`, `project_accounts`, explicit; `[accounts.auto]` invalid; `AccountNames` excludes `auto`; `[account_routing]` thresholds parse with default 90 and out-of-range → default + warning
- [ ] 1.2 Add `config.AutoAccountName`, the reserved-name handling, and `AccountRouting{FiveHourThresholdPct, WeeklyThresholdPct}` with toml loading and accessors

## 2. Usage ledger and selection (`internal/accountusage`)

- [ ] 2.1 Tests first: `Record` keeps the newest reading per (account, window), ignores invalid/past readings, and `Snapshot` drops readings past `resets_at` (injected clock); `""` normalizes to `default`
- [ ] 2.2 Tests first: `Pick` over the spec scenarios (capped skipped, weekly counts, unknown available, all unknown → first, all over threshold → least over + flag, tie → candidate order, exclude list)
- [ ] 2.3 Implement `Ledger` (mutex, clock seam) and the pure `Pick(thresholds, candidates, snapshot, exclude)` returning the choice and scores

## 3. Persistence

- [ ] 3.1 Tests first: `tasks.account_auto` round-trips through `Add` / `Get` / `Update` / `Tasks`, and the migration adds the column to an existing DB with default 0
- [ ] 3.2 Add `Task.AccountAuto`, the column migration, scan/insert/update wiring, and `account_auto` in task JSON / raw shapes

## 4. Creation-time resolution and inheritance

- [ ] 4.1 Tests first: `resolveTaskAccount` with `auto` (explicit, project, global) stores the `Pick` choice and sets `AccountAuto`; project pin beats global `auto`; non-auto resolution leaves the flag false; empty `AccountUsageSource` → `default`
- [ ] 4.2 Tests first: backend-tier `claude_usage` reads the account `auto` would pick for Claude (D5)
- [ ] 4.3 Implement `agent.AccountUsageSource` seam + the `auto` branch in `resolveTaskAccount` / `resolveDefaultBackendName`, logging the choice and scores under `[account]`
- [ ] 4.4 Tests first: `resolveOrchestratorAccount` returns `auto` for an auto-routed coordinator and the verbatim pin otherwise; fork keeps account + flag
- [ ] 4.5 Implement the inheritance change in `hera_spawn.go` and fork
- [ ] 4.6 Tests first + implement: MCP `task_create` caller inheritance passes `auto` for an auto-routed caller; explicit `"auto"` accepted

## 5. Daemon wiring

- [ ] 5.1 Tests first: a bridge measure with `rate_limits` records both windows into the ledger under the task's account (default normalized)
- [ ] 5.2 Feed the ledger from the API server's bridge-measure path; daemon owns the ledger and sets `agent.AccountUsageSource`
- [ ] 5.3 Tests first: TUI-socket `AccountUsage` RPC returns the snapshot; client error → empty map + `[account]` log
- [ ] 5.4 Implement the RPC, the daemon client method, and set `agent.AccountUsageSource` in the local TUI at startup

## 6. Account move

- [ ] 6.1 Tests first (`t.TempDir()` HOME, fake runner): `PrepareAccountMove` rejections (unknown task, non-Claude, same account, unsupported/unknown target, `auto` with no candidate, busy session without force) leave no side effects; busy session with force proceeds; no model turn or agent tool call is involved
- [ ] 6.2 Tests first: a successful move stops a live idle session, copies `<sid>.jsonl` + `<sid>/` into the target's `projects/<enc>/` (overwriting an older copy, source kept), persists account + clears the flag, and returns `{from, to, resumed:true}`; missing transcript → session ID cleared, `resumed:false`
- [ ] 6.3 Implement `internal/agent/accountmove.go` (`RefreshResumeSessionID` first, stop + bounded wait, `BootstrapClaudeConfigDir`, copy, persist) with `[account]` logs
- [ ] 6.4 Tests first + implement `POST /api/tasks/{id}/account` (404 / 409 unless `force` / 400 / 500 mapping, restart shares the `handleRestartTask` body via an extracted helper)
- [ ] 6.5 Tests first + implement MCP `task_move_account` (self-move rejection, `force`, error mapping, description text)
- [ ] 6.6 `apiclient` + `apistore` `MoveTaskAccount` for remote mode, with a test against an httptest server

## 7. REST accounts listing

- [ ] 7.1 Tests first: `GET /api/accounts` includes the `auto` entry after `default` only when a named account is valid, `is_default` marks `auto` when the default resolves to it, and per-account `usage` reflects the ledger (omitted when empty)
- [ ] 7.2 Implement the `auto` entry and `usage` field

## 8. TUI

- [ ] 8.1 Tests first: new-task form lists `auto` (after `default`) when a named account supports the backend, preselects it when the default is `auto`, submits `"auto"`, and reads skills from `~/.claude` while `auto` is selected
- [ ] 8.2 Implement the form change (local + remote account sources)
- [ ] 8.3 Add `tasklist.move_account` / `hera_rail.move_account` (`>`) to `keymap` (defaultSpecs, actionLabels, contextOrder), dispatch cases, palette registries, and `TestHelpModal_Draw` assertions
- [ ] 8.4 Tests first: picker modal lists `auto` + eligible accounts with usage labels ("—" unknown); notices for no selection / non-Claude / no other account; busy session opens the picker with an interrupt warning and sends force; confirm runs the move off the UI thread and then `startSession`; remote mode calls the REST endpoint; SimulationScreen smoke test for open → confirm → close
- [ ] 8.5 Implement the picker modal and the move flow with `[account]` uxlog lines for success / failure

## 9. Docs and verification

- [ ] 9.1 Gotchas: ledger is in-memory and valid-until-reset; `auto` is resolved at creation, never stored; move copies (never deletes) transcripts; self-move rejection; old-daemon fail-open. Update `context/knowledge/index.md`
- [ ] 9.2 README Reference: `default_account = "auto"`, `[account_routing]`, `>` key rows, `task_move_account` MCP tool, `POST /api/tasks/{id}/account`, `/api/accounts` `usage`
- [ ] 9.3 `make pre-pr` clean
- [ ] 9.4 Dogfood: with `default_account = "auto"`, a new task lands on the account with headroom; `>` moves a capped task and the conversation resumes (check for any folder-trust prompt on the target account); a coordinator moves a worker via `task_move_account`
- [ ] 9.5 Archive the change (`openspec archive add-account-auto-routing`) on this branch before merge
