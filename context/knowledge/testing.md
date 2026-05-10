# Testing Rules — Argus

Stdlib-only testing (no testify, no ginkgo). All assertions go through `internal/testutil`.

> **Read this before writing any new test.** The patterns here are non-negotiable; deviations should have a comment justifying them.

## Coverage Targets

- **Project floor: 88%** of statements today, ratcheting toward **95%** (per the filtered profile — see "Coverage Exclusions" below). The floor is enforced in CI; PRs that drop the filtered total below the floor are rejected.
- **Per-package aspiration: 95%**, except UI smoke-only paths which may sit at 90%. Any package below 95% is a candidate for follow-up coverage work.
- **Ratcheting policy**: when the rolling number rises ≥ 2 percentage points above the floor, raise the floor by the same amount in `Makefile` (`test-cover-gate`) and `.github/workflows/ci.yml` (`Coverage Gate` step). Document the bump in the PR description.

## Coverage Exclusions

Go has no native ignore comment. Argus excludes files via a **post-hoc filter** (`scripts/cover-filter`) that strips paths matching `coverage-ignore.txt` patterns from `coverage.out` before reporting. The list is intentionally short — every entry needs a written justification.

Currently excluded:
- `cmd/argus/main.go`, `cmd/argus/kb.go` — CLI entry / interactive subcommands. Tested via end-to-end `go build -cover` runs (out of scope here).
- `cmd/argus-test-server/` — Test harness for Playwright; covered by web-tests/.
- `internal/launchagent/` — macOS launchd plist generation; tested by Playwright/E2E only.
- `*/sysproc_unix.go`, `*/attach.go` — `syscall.SysProcAttr` / raw-mode ioctl glue. CLAUDE.md exempts "real terminal functions (raw mode, ioctl)".
- `internal/agent/sandbox.go:89` (`runSandboxExec`) — exec-only wrapper, requires real `sandbox-exec`.

**Add to the exclusion list ONLY when**: (1) the code is platform glue with no logic to test, OR (2) it's a CLI entry point covered by integration tests, OR (3) testing requires a privileged/external resource. Add a one-line comment above the entry explaining which.

## Test File Naming

Follow Go's stdlib convention: **one test file per source file**, named `<source>_test.go`. Example: tests for `runner.go` go in `runner_test.go`.

When tests for a single source grow large, split by aspect with a meaningful suffix: `<source>_<aspect>_test.go`. Examples that match the codebase:

- `runner_test.go` — primary tests for `runner.go`.
- `runner_extra_test.go` — additional branch-coverage tests (error paths, edge inputs) the primary file doesn't fit.
- `app_keyhandlers_test.go` — focused tests for keypress handling code in `app.go`.
- `branches_test.go` — package-level tests for cross-cutting branches that don't have a single dominant source file.

**Forbidden patterns** (rejected in code review):

- Numbered suffixes: `coverage_test.go`, `coverage2_test.go`, `tests3_test.go`. Numbers carry no information.
- Generic vague prefixes: `more_test.go`, `extra_test.go` (with no source noun). The reader must guess what's tested.
- `coverage_*_test.go` — coverage is universal; every test file contributes to it. Naming on that axis is meaningless.
- Filler words: `_misc_test.go`, `_other_test.go` (unless the source file itself is `*_other.go` — see `launchagent_other.go`).

**Test files for `package main` binaries** in `cmd/`: same convention. `cmd/argus/main.go` → `cmd/argus/main_test.go`.

**Black-box test files** (using `package foo_test` not `package foo`): use the same naming; the package suffix in the file is the only signal needed.

When in doubt, ask: "If a future reader greps for failing tests in this package, will they find them by source filename?" The answer must be yes.

## Test Idioms

### Table-driven + `t.Run`

Every test that varies inputs uses subtests. Required for `-run TestFoo/bar` filtering and clean failure localization.

```go
for _, tc := range []struct {
    name, in, want string
}{
    {"empty", "", ""},
    {"trim", "  x  ", "x"},
} {
    t.Run(tc.name, func(t *testing.T) {
        testutil.Equal(t, Trim(tc.in), tc.want)
    })
}
```

Loop variable capture is fixed in Go 1.22+. Do **not** write `tc := tc` shadowing in new code.

### Assertions

Always use `internal/testutil` — never raw `if got != want`:

```go
testutil.Equal(t, got, want)        // comparable
testutil.DeepEqual(t, got, want)    // structs/slices via go-cmp
testutil.NoError(t, err)            // err == nil
testutil.ErrorIs(t, err, target)    // errors.Is
testutil.Nil(t, val)                // handles nil-interface trap
testutil.Contains(t, s, substr)
```

### Helpers

Mark every helper with `t.Helper()`. Helpers that need to work for benchmarks too take `testing.TB` (not `*testing.T`).

### Cleanup

Prefer `t.Cleanup` over `defer` inside helpers — runs LIFO at end of test, survives `t.FailNow` from another goroutine. `t.TempDir()` and `t.Setenv()` register their own cleanup automatically.

### Parallelism

Pure-logic tests should call `t.Parallel()`. Tests that use `t.Setenv`, modify global state, or share files **must not** parallelize — `t.Setenv` panics under parallel. UI smoke tests are sequential.

## Concurrency Testing

- **`-race -count=1`** on every CI run (already wired).
- **`testing/synctest`** (stable in Go 1.25) for any test that previously used `time.Sleep` for synchronization. `synctest.Test(t, func(t *testing.T) {...})` runs in a bubble where `time.Sleep`, tickers, and deadlines advance instantly when all goroutines block. Use `synctest.Wait()` to flush pending goroutines instead of polling.
- **Never `time.Sleep` to wait for state.** Use channels, `sync.Cond`, or `synctest.Wait`. Sleep-based sync is the single largest source of flakes.

```go
import "testing/synctest"

func TestThrottle(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        m := newManager()
        m.Send("a")
        time.Sleep(time.Hour) // instant under synctest
        synctest.Wait()
        testutil.Equal(t, m.Sent(), 1)
    })
}
```

## Dependency Injection

Prefer **function-field injection** over interfaces for single-method seams:

```go
type Service struct{ now func() time.Time }
func New() *Service { return &Service{now: time.Now} }
// in test: s.now = func() time.Time { return fixed }
```

Use **interfaces** only when there are 2+ methods or 2+ implementations (e.g. `agent.SessionProvider`). Define them at the consumer.

## Filesystem

Always `t.TempDir()` for paths and `t.Setenv("HOME", t.TempDir())` for code that resolves through `$HOME` (anything calling `db.DataDir()` or `agent.WorktreeDir()`). The `testGuard` in `internal/agent/cleanup.go` blocks deletions on real `~/.argus/` during `go test`, but tests should be designed correctly in the first place.

## HTTP Tests

- **`httptest.NewServer`** for end-to-end (real socket + client). Use this for SSE, auth middleware, integration paths.
- **`httptest.NewRecorder` + `handler.ServeHTTP`** for handler unit tests — faster, no port.
- **SSE**: assert against `bufio.Scanner` over `resp.Body`; cancel via `context.WithCancel` to exercise cleanup.
- **Auth**: table-drive `(token, wantStatus)` against `httptest.NewRecorder`.

## SQLite

`db.OpenInMemory(t)` already returns a fresh DB and registers cleanup. Each `:memory:` is a distinct database — for shared in-memory across connections in one test, use `file::memory:?cache=shared`.

## Process / PTY

Cheap subprocesses for PTY tests: `exec.Command("bash", "-c", "echo hello; sleep 1")`. The harness in `cmd/argus-test-server` shows the pattern. **Never** spawn the real `claude` or `codex` binary in tests.

## TUI (tcell SimulationScreen)

The reference helpers live in `internal/tui/smoke_test.go`:

- `simApp(t)` — `SimulationScreen` wrapped in `lazyScreen`, mouse + paste enabled in the correct order.
- `wireApp(t, app)` — full `App` wired to a SimulationScreen.
- `runApp(t, app)` — runs the event loop in a goroutine, returns a stop func.
- `syncUI(t, app)` — flush pending events via an empty `QueueUpdateDraw`.

**New page wrappers, layout containers, and any `SetScreen`/`EnableMouse`/`EnablePaste` change require a smoke test.** See CLAUDE.md "Testing Requirements".

For mouse-focus regressions: inject a click on a non-interactive panel and assert `app.GetFocus()` stayed on the interactive widget. See `TestSmoke_Click*` for the pattern.

## Mocking the Daemon

The TUI's `Client` and `RemoteSession` (`internal/daemon/client/`) talk to the daemon via a Unix socket. Tests start a tiny in-process daemon-like listener that:

1. Binds a Unix socket under `t.TempDir()` (path length matters on macOS — keep names short, ≤104 bytes total).
2. Accepts connections, dispatches on the first byte ('R' = JSON-RPC, 'S' = stream).
3. Returns canned responses for the methods the test exercises.

See `internal/daemon/client/client_test.go` for the canonical fixture. Tests that exercise reconnect/timeout paths use `synctest`.

## What Not to Test

- `cmd/argus/main.go` flag parsing — covered by integration via the test harness.
- `os.Exit`, `signal.Notify` glue — refactor to extract logic, leave the syscall.
- Real terminal raw-mode (`golang.org/x/term`) — exempted by CLAUDE.md.
- Generated files — exclude via `coverage-ignore.txt`.

## Discovering Coverage Gaps

```bash
make test-cover                          # writes coverage.out + filtered total
go tool cover -html=coverage.out         # visual heatmap
scripts/cover-filter coverage.out | go tool cover -func=/dev/stdin | sort -k3 -n  # lowest first
```

The CI summary prints the filtered total per the gate; the unfiltered total is the rolling baseline.
