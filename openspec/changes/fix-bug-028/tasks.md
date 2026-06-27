# Tasks: Fix BUG-028

## 1. Reproduce

- [x] 1.1 Write a RED test mirroring a permission-blocked worker (in_progress + needs-input) and confirm the per-role and with-coordinator paths already render "(?)" (no-op candidate fix verified)
- [x] 1.2 Reproduce the genuine gap: a collapsed, coordinator-less orchestrator header renders no needs-input cue (RED integration test through the real render path)

## 2. Implement

- [x] 2.1 Stamp the per-orchestrator rollup on `OrchView.SubtreeNeedsInput` in `rollupNeedsInput`
- [x] 2.2 Surface the needs-input glyph on the header in `drawOrchRow` when no coordinator role carries it
- [x] 2.3 Preserve the BUG-023 in_progress gate (finished worker clears the header rollup)

## 3. Verify

- [x] 3.1 `make test-pkg PKG=./internal/tui/hera/` green
- [x] 3.2 `make test-pkg PKG=./internal/tui/` green
- [x] 3.3 Confirm the coordinator-less regression test fails without the fix
- [x] 3.4 Document the gotcha in `context/knowledge/gotchas/hera-view.md`
