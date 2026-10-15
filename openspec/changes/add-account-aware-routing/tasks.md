**Design doc:** `openspec/changes/add-account-aware-routing/design.md`

## 1. Failing tests

- [ ] 1.1 Write failing tests for every scenario in `specs/backend-tier-routing/spec.md`: account on tiers (omitted = default, unknown/unsupported skipped), headroom across two Claude accounts + Codex, identity collapse preferring `default`, not-signed-in skipped, lookup error fails open, re-signed folder changes routing, archetype model unaffected, decision log line via a captured slog handler. Stub identity lookup through a seam over `claudeaccount.CachedAuthStatus`.
- [ ] 1.2 Write failing tests for `specs/usage-budget-routing/spec.md`: one probe per distinct identity with the right `CLAUDE_CONFIG_DIR` (unset for `default`), collapsed identities probed once, independent fail-open per identity, single-account config unchanged. Use the existing fake probe-session seam.
- [ ] 1.3 Write failing tests for `specs/agent-execution/spec.md`: routed account replaces inheritance, routed Codex worker under a Claude-only coordinator succeeds, explicit account restricts routing to that account's tiers, project backend suppresses routing, explicit-backend inherited-account rejection (corrected scenario). All tests `t.Setenv("HOME", t.TempDir())`, `agent.NewRunner(nil)`.
- [ ] 1.4 Write failing tests for `specs/hera-coordination/spec.md`: ordered coordinator walk, fallback past `coordinator_threshold_pct`, workers-stop-before-coordinators, all-over-limit uses first usable, unknown = available, same-login collapse, sub-coordinator materialization parity, explicit wins, unset list unchanged.
- [ ] 1.5 Write failing tests for `specs/tui-shell/spec.md`: multi-identity segments, collapsed single segment, stale `—` per segment, single-account form unchanged.
- [ ] 1.6 Confirm each new test fails for the intended reason (Prove-It)

## 2. Config and identity-aware resolver

**Depends on:** Stage 1

- [ ] 2.1 Add `BackendTier.Account` (`toml:"account"`) and `BackendRoutingConfig.CoordinatorAccounts` / `CoordinatorThresholdPct` in `internal/config`; document them in the README Reference `[backend_routing]` table
- [ ] 2.2 Add an identity resolver in `internal/backendtier`: per Claude tier, map account → config folder → signed-in email via a `claudeaccount.CachedAuthStatus` seam; skip not-signed-in, fail open on error; collapse same-email tiers preferring `default`
- [ ] 2.3 Change the resolver to return `Resolution{Backend, Account}` for both `ordered` and `headroom`, over identity-keyed candidates; skip unknown/unsupported accounts with a log line; accept an optional account restriction
- [ ] 2.4 Emit one `slog` decision line per resolution (candidates, readings, thresholds, headroom, winner, reason)

## 3. Per-identity Claude probe

**Depends on:** Stage 2

- [ ] 3.1 Make the Claude probe take a config folder: set `CLAUDE_CONFIG_DIR` for named accounts, keep stripping it for `default`
- [ ] 3.2 Key the probe cache by identity; run one probe per distinct identity per tick and at daemon startup, each failing open independently
- [ ] 3.3 Re-resolve identities every tick so a re-signed `~/.claude` takes effect within one interval

## 4. Account precedence at task creation (workers)

**Depends on:** Stage 2

- [ ] 4.1 Thread the routed account out of `resolveDefaultBackendName` / `CreateAndStart` and insert it above inherited/project/global accounts in `resolveTaskAccount`, only when routing chose the backend
- [ ] 4.2 Pass an explicit account as a routing restriction when no explicit backend is given
- [ ] 4.3 Verify hera worker/freelance spawn, MCP `task_create` and TUI new-task all route through the shared path (no bespoke resolution)

## 5. Coordinator account selection

**Depends on:** Stage 2

- [ ] 5.1 Add coordinator account selection beside `resolveCoordinatorBackend`: ordered walk of `coordinator_accounts` against `coordinator_threshold_pct`, identity-collapsed, skip unusable accounts, first usable when all over, explicit wins, unset = unchanged
- [ ] 5.2 Wire it into `SpawnHeraCoordinator` and `MaterializeHeraSubCoordinator` via the same function; log the decision via `slog`

## 6. Status bar per-identity readout

**Depends on:** Stage 3

- [ ] 6.1 Add an additive per-identity readings list (account label, percentage, known) to the daemon `BootInfo` RPC
- [ ] 6.2 Render one segment per distinct identity in the status bar (`cla` for `default`, account name otherwise); keep the single-account form when only one Claude identity exists

## 7. Docs, verification, archive

**Depends on:** Stages 4, 5, 6

- [ ] 7.1 Add gotchas to `context/knowledge/gotchas/usage-budget-routing.md` (identity collapse prefers `default`; not-signed-in skipped vs lookup-error fail-open; routed account overrides inheritance only when routing chose the backend; per-identity probe sets `CLAUDE_CONFIG_DIR`) and update the index row
- [ ] 7.2 Record the named follow-ups in the change: REST exposure of per-identity readings for web + macOS, and a Settings surface for the new keys
- [ ] 7.3 `make pre-pr` green
- [ ] 7.4 Live verification: configure `[accounts.personal]` + three tiers + coordinator keys; confirm per-identity probe lines and decision lines in `daemon.log`, the status bar segments, a routed worker landing on the expected account, and the collapse when `~/.claude` and `personal` share a login
- [ ] 7.5 Archive the change (`openspec archive add-account-aware-routing`) on the branch before merge
