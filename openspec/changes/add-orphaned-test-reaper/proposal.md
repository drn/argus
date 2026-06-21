## Why

A leaked `internal/tui` test binary (`tui.test`) ran orphaned at ~95% CPU for
**2+ days** before it was noticed and killed. It was an ordinary per-package
test binary from a `make test-cover` run (`-test.gocoverdir`,
`-test.coverprofile`, `-test.timeout=10m0s`) — NOT the re-exec fork bomb already
backstopped in `gotchas/daemon-rpc.md`.

Two independent factors combined:

1. **A `internal/tui` test leaks a busy-spinning goroutine and the test binary's
   `main` never returns.** TUI tests start the real tview event loop + tick
   goroutines (`simApp`/`runApp`/`wireApp` in `internal/tui/smoke_test.go`); one
   is not being torn down, so the binary keeps spinning a CPU after the test
   "finishes".

2. **The `-test.timeout` watchdog never fired.** Go's test timeout uses the
   runtime monotonic timer, which macOS suspends during sleep. The laptop slept
   overnight, so the 10-minute timer never accumulated 10 *awake* minutes. When
   `go test` was later interrupted, the per-package binary was orphaned to PID 1
   and kept spinning unbounded.

No single fix covers the whole class, so this change layers three defenses,
strongest backstop first.

## What Changes

- **Orphaned-test reaper (primary backstop).** A repo-versioned shell script
  plus a macOS LaunchAgent timer that periodically kills any Go `*.test` binary
  reparented to PID 1 (orphaned) that has been alive past a configurable age
  threshold. PID 1 parentage is the discriminator — a test binary running under
  a live `go test` is never reparented to PID 1 — so a legit in-flight test run
  is never touched. This catches the entire class regardless of which test leaks
  or why the watchdog failed. `--dry-run` lists candidates without killing; all
  actions and no-ops are logged. An install/uninstall helper renders and
  bootstraps the LaunchAgent (macOS only; a no-op with a clear message
  elsewhere).

- **Root-cause hardening: `internal/tui` test event-loop teardown.** The test
  harness's `runApp` now returns an idempotent `stop` and ALSO registers it via
  `t.Cleanup`, so the real tview event loop is torn down even when a test forgets
  `defer stop()`. A leaked, still-running `app.Run()` loop is the one goroutine
  that can busy-spin (tview spins on a nil `PollEvent` once its screen is
  finalized) — the actual CPU-peg vector. NOTE: investigation found no single
  "spinning test"; `TestMain` does `os.Exit(m.Run())`, which reaps all goroutines
  unless a test HANGS. The dominant residual leak (~100 `x/vt` emulator-drain
  goroutines) is a deliberately-accepted per-pane leak — `terminal/terminalpane.go`
  documents that closing the emulator to stop the drain reintroduces a `-race`
  data race — so this change does NOT touch it (doing so would break the gate).

- **Tighten the test timeout (weak, last line).** Add an explicit `-timeout`
  (2m) to the `test`, `test-pkg`, `test-cover`, and `test-cover-gate` Makefile
  recipes so a hung suite is bounded while the machine is awake. This does not
  survive sleep on its own — the reaper is the real backstop — but it trims the
  common awake-hang case fast.

## Impact

- **Affected specs:** `os-integration` — ADDED requirement for the orphaned-test
  reaper LaunchAgent (a second, maintenance-only LaunchAgent distinct from the
  daemon auto-start agent, installed from a repo script rather than argus Go
  code). The Makefile timeout and the tui test-teardown fix are tooling / test
  -only and carry no spec delta.
- **Affected files:** new `script/reap-orphaned-tests.sh` (the reaper) and
  `script/install-reaper.sh` (LaunchAgent install/uninstall); `Makefile`
  (`-timeout 120s` on the `test`/`test-pkg`/`test-cover`/`test-cover-gate`
  recipes); `internal/tui/smoke_test.go` (`runApp` idempotent stop + `t.Cleanup`,
  plus two guard tests); `context/knowledge/gotchas/*.md` (new gotchas).
- **No argus runtime/product behavior changes.** The daemon, TUI, DB, and every
  shipped capability are untouched; the reaper is external host tooling and the
  other two are build/test hygiene.
- **Coverage:** the reaper and install helper are shell tooling (outside the Go
  coverage universe, like `scripts/spec-check-hook.sh`), so the 88% Go floor is
  unaffected; the tui teardown fix touches test files only. `make pre-pr` stays
  green.
