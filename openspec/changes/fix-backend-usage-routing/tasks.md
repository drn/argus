**Design doc:** `openspec/changes/fix-backend-usage-routing/design.md`

## 1. Fixtures and failing tests

- [x] 1.1 Capture a real Claude `/usage` screen (raw PTY bytes) from a trusted, empty directory and commit it as `internal/usagebudget/testdata/usage_*.raw`; also capture the folder-trust dialog screen. Strip anything account-identifying (email/org) before committing.
- [x] 1.2 Add real Codex rollout fixtures under `internal/backendtier/testdata/`: weekly-in-`primary`, mixed 300/10080 windows, and depleted (`rate_limit_reached_type` set, both windows null)
- [x] 1.3 Write failing tests for every scenario in `specs/usage-budget-routing/spec.md` (incremental parse + early stop via a fake streaming runner, header-then-next-line parse against the real fixture, trust-dialog accept/reject/other-dialog abort, startup probe, slog lines reach a captured handler)
- [x] 1.4 Write failing tests for every scenario in `specs/backend-tier-routing/spec.md` (strategy parsing + default + unknown value, all headroom scenarios, Codex window-by-duration, depleted=100%, valid-until-resets_at, past-reset=unknown)
- [x] 1.5 Confirm each new test fails for the intended reason

## 2. Codex probe correctness

**Depends on:** Stage 1

- [x] 2.1 Replace `worstCodexWindow` with weekly-window selection by `window_minutes == 10080`
- [x] 2.2 Parse `rate_limit_reached_type`; record 100% held 24h when no weekly window is present
- [x] 2.3 Drop the rollout-mtime freshness gate; validity = until weekly `resets_at` (depleted: 24h from the rollout file mtime; TestProbe_DepletedReadingHeld24hFromRolloutMtime)
- [x] 2.4 Route all codexprobe logging through slog (+ existing uxlog)

## 3. Claude probe correctness

**Depends on:** Stage 1

- [ ] 3.1 Create/ensure `~/.argus/usage-probe` (via `db.DataDir()`), run the probe with `cmd.Dir` set to it
- [ ] 3.2 Replace wait-for-exit with a streaming read loop: feed emulator per chunk, attempt parse, terminate process (SIGTERM → kill) on success; keep 45s timeout
- [ ] 3.3 Detect the folder-trust dialog (dialog shape + the probe dir path on screen) and select the trust option once; abort + log on any other dialog
- [ ] 3.4 Rewrite `parseUsageOutput` to accept the percentage on the header line or the following lines up to the next section header
- [ ] 3.5 Log every outcome via slog (+ uxlog)

## 4. Startup probes

**Depends on:** Stages 2, 3

- [ ] 4.1 `runUsageBudgetPoller` and `runCodexProbePoller` probe once immediately, then on the ticker; tests use the existing probe seams

## 5. Headroom strategy

**Depends on:** Stage 1

- [ ] 5.1 Add `Strategy string \`toml:"strategy"\`` to `config.BackendRoutingConfig` with constants `ordered`/`headroom`; ensure config.toml-wins semantics carry it (strategy is read from config.toml even when tiers come from the DB)
- [ ] 5.2 Split `backendtier.ResolveBackend` into ordered (existing) and headroom paths; headroom uses a reading accessor that distinguishes known vs unknown
- [ ] 5.3 Log an unrecognized strategy once per distinct value

## 6. Docs and wrap-up

**Depends on:** Stages 2–5

- [ ] 6.1 Add gotchas to `context/knowledge/gotchas/usage-budget-routing.md` (interactive `/usage` never exits; trust/imports dialogs; uxlog is a no-op in the daemon; Codex weekly window lives in `primary`; depleted record has null windows) and update the index row
- [ ] 6.2 Update README Reference config table with `strategy` and the probe-directory note
- [ ] 6.3 `make pre-pr` green
- [ ] 6.4 Live verification: deploy, confirm `[usagebudget] probe updated` and Codex reading lines in `daemon.log`, status bar shows real numbers, and a fresh worker with `strategy = "headroom"` resolves as expected
- [ ] 6.5 Archive the change (`openspec archive fix-backend-usage-routing`) on the branch before merge
