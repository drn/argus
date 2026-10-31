## 1. Usage and persistence

- [x] 1.1 Tests first, then `usagebudget.CachedReadingForAccount` (bridge reading else probe cache, both windows)
- [x] 1.2 `tasks.account_routed` column (ALTER migration, scan/insert/update), `model.Task.AccountRouted`, targeted `db.SetTaskAccount(id, account, routed)`

## 2. Routed inheritance

- [x] 2.1 Tests first: `CreateAndStart` marks a task routed only when its account came from the tier choice
- [x] 2.2 Routed parent → blank account in hera `resolveOrchestratorAccount` and MCP `taskCreateAccount`; other inheritance unchanged

## 3. Move operation

- [x] 3.1 Port `agent.MoveTaskAccount` (guard, validation, transcript copy, persist-then-kick, rollback) with tests
- [x] 3.2 `auto` target: lowest current usage among other Claude-capable accounts (D3)
- [x] 3.3 Per-account usage source: daemon in-process, local TUI over a new `AccountUsage` RPC

## 4. Surfaces

- [x] 4.1 REST `POST /api/tasks/{id}/account` + `usage` on `GET /api/accounts`, with tests
- [x] 4.2 MCP `task_move_account` (self-move rejection), with tests
- [x] 4.3 `apiclient` / `apistore` move call
- [ ] 4.4 TUI: keymap actions + help, account picker modal, task list and Hera rail wiring, remote path, smoke tests

## 5. Docs and gates

- [ ] 5.1 README Reference (keybindings, MCP tool, REST endpoint, account usage), gotchas, knowledge index
- [ ] 5.2 Supervisor spawn digest re-record if spawn paths changed
- [ ] 5.3 `make pre-pr`
- [ ] 5.4 Dogfood on master: `>` moves a task and resumes it; a coordinator moves a worker; a routed coordinator's worker is routed
- [ ] 5.5 Archive the change on this branch before merge
