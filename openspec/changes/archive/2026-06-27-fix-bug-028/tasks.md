# Tasks: Fix BUG-028

## 1. Reproduce

- [x] 1.1 Write a RED test mirroring a permission-blocked worker (in_progress + needs-input) and confirm the per-role and with-coordinator paths already render "(?)" (no-op candidate fix verified)
- [x] 1.2 Reproduce the genuine gap: a collapsed, coordinator-less orchestrator header renders no needs-input cue (RED integration test through the real render path)

## 2. Implement

- [x] 2.1 `buildRoleView`: apply the in_progress gate to WORKER roles only; live non-worker (coordinator/freelance) roles surface needs-input regardless of task status (BUG-028)
- [x] 2.2 `needsInputForHeraRail` App feed: admit coordinators regardless of status (managed → no BUG-005 regression); keep `needsInputInProgress` for the agent-view attention bar
- [x] 2.3 Stamp the per-orchestrator rollup on `OrchView.SubtreeNeedsInput`; surface it on a coordinator-less header in `drawOrchRow`
- [x] 2.4 Preserve BUG-023 (finished worker clears) — verified by a dedicated test

## 3. Verify

- [x] 3.1 `make test-pkg PKG=./internal/tui/hera/` green
- [x] 3.2 `make test-pkg PKG=./internal/tui/` green
- [x] 3.3 Confirm the coordinator-less regression test fails without the fix
- [x] 3.4 Document the gotcha in `context/knowledge/gotchas/hera-view.md`
