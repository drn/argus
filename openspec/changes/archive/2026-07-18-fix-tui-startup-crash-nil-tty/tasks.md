## 1. Root-cause verification

- [x] 1.1 Mine `ux.log` for the 2026-07-13 23:16:15/27/28 crash-loop and capture the full panic stack
- [x] 1.2 Trace the stack through `tcell`/`tview` vendor source: confirm `tview.Application.SetScreen` calls `screen.Init()` but discards its error, and `tcell.Screen.Init()` lazily opens `/dev/tty` via `tcell.NewDevTty()`

## 2. Core fix

- [x] 2.1 Add `probeTerminal` (overridable var) to `internal/tui/app.go`: opens and immediately closes `tcell.NewDevTty()`
- [x] 2.2 Call `probeTerminal()` at the top of `App.Run()`, before `tcell.NewScreen()`; return a wrapped error on failure
- [x] 2.3 Regression test `TestApp_Run_NoControllingTerminal_ReturnsCleanError` (`internal/tui/run_test.go`) overriding `probeTerminal` — no real tty required, runnable in CI
- [x] 2.4 Confirm existing SimulationScreen smoke tests (`TestEnableMouseAfterSetScreen`, `TestEnablePasteAfterSetScreen`) are unaffected (ordering of `SetScreen`/`EnableMouse` unchanged)

## 3. Documentation

- [x] 3.1 Document the confirmed root cause in `context/knowledge/gotchas/ui-threading.md`
- [x] 3.2 Bump `context/knowledge/index.md`'s ui-threading.md bullet count

## 4. Verification

- [x] 4.1 `go build ./...`
- [x] 4.2 `go test ./internal/tui/...`
- [x] 4.3 `make pre-pr` clean (build/vet/fmt-check/lint-pr/test-cover-gate all clean; `vuln` flags 3 pre-existing stdlib CVEs, advisory-only/continue-on-error in CI per Makefile)
- [x] 4.4 OpenSpec archive: merge deltas into `openspec/specs/tui-shell/spec.md`, move this folder to `openspec/changes/archive/`
- [ ] 4.5 Open PR via `iris_gh_pr_create`, `hera_send` the coordinator the PR URL + root-cause summary
