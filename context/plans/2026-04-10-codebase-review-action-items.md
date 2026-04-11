# Plan: Codebase Review Action Items

**Date:** 2026-04-10
**Source:** Deep codebase review and analysis
**Status:** Complete

## Summary

Comprehensive codebase review identified 8 actionable improvements across correctness,
reliability, tooling, layering, and consistency. Each item is implemented as a
separate commit on this branch.

## Action Items

### 1. Fix TOCTOU race in Runner.Start() [HIGH — correctness]

**File:** `internal/agent/runner.go:36-42`

The existence check and session insertion are separated by an unlocked window where
`BuildCmd` and `StartSession` run. Two concurrent `Start()` calls for the same
task ID can both pass the check, creating duplicate sessions and leaking PTY fds.

**Fix:** Use a placeholder entry in the sessions map while holding the lock, so
concurrent calls see the reservation and fail fast.

### 2. Add HTTP server timeouts [HIGH — resource safety]

**File:** `internal/api/server.go:67`

`&http.Server{Handler: handler}` has no `ReadTimeout`, `WriteTimeout`, or
`IdleTimeout`. Slow or abandoned connections can leak resources.

**Fix:** Set `ReadHeaderTimeout: 10s`, `WriteTimeout: 60s`, `IdleTimeout: 120s`.

### 3. Add golangci-lint to CI [MEDIUM — tooling]

**Files:** `.golangci.yml` (new), `.github/workflows/ci.yml`

No static analysis in CI. Adding `golangci-lint` with `staticcheck`, `errcheck`,
`govet`, `revive`, and `gosec` catches bugs the compiler misses.

### 4. Fix DB error swallowing [MEDIUM — reliability]

**Files:** `internal/db/tasks.go`, `internal/db/projects.go`,
`internal/db/backends.go`, `internal/db/config.go`, all callers

`Tasks()`, `Projects()`, `Backends()`, `TasksByTodoPath()`, and `TaskByPRURL()`
silently return empty results on query errors, masking database failures.

**Fix:** Change signatures to return `(data, error)`. Update all callers.

### 5. Move spinner logic out of model [MEDIUM — layering]

**Files:** `internal/model/status.go`, `internal/tui/spinnerstate.go` (new)

Spinner animation state (`activeSpinner`, `SetActiveSpinner`, `SpinnerFrame`,
`SpinnerFrameCount`, `SpinnerTickInterval`) lives in `model/status.go` — UI
rendering logic in the domain layer.

**Fix:** Move spinner state to `internal/tui/spinnerstate.go`. Remove
`DisplayForFrame` from model (only used in model tests). Update callers.

### 6. Standardize RPC error reporting [MEDIUM — consistency]

**Files:** `internal/daemon/rpc.go`, `internal/daemon/types.go`,
`internal/daemon/client/client.go`

`StartSession` returns errors directly via the RPC transport, while all other
RPC methods (StopSession, WriteInput, Resize, etc.) return `nil` with errors
in the response struct. This inconsistency makes error handling unpredictable.

**Fix:** Add `Error` field to `StartResp`. Return `nil` from the RPC method
and set `resp.Error` on failure, matching the pattern used everywhere else.
Update the client to check `resp.Error`.

### 7. Migrate daemon/agent logging to log/slog [MEDIUM — debuggability]

**Files:** `internal/daemon/daemon.go`, `internal/daemon/rpc.go`,
`internal/daemon/stream.go`, `internal/daemon/headless.go`,
`internal/agent/runner.go`

~60 `log.Printf` calls produce unstructured text logs. The daemon manages
concurrent agent sessions — structured logging with key-value pairs improves
debuggability and enables JSON log output.

**Fix:** Replace `log.Printf` with `log/slog` calls. Use `slog.Info`,
`slog.Error`, `slog.Warn` with key-value pairs.

## Deferred (Too Large for This Branch)

### Decompose tui into sub-packages

`internal/tui` is 17K LOC in a flat package with a 3,337-line god object
(`App`). Should decompose into `tui/taskview`, `tui/agentview`,
`tui/reviewview`, `tui/modals`, `tui/theme`. This is a multi-session effort.

### Split agent package by concern

`internal/agent` mixes session management, worktree creation, sandbox
orchestration, and command building. Should split into `agent/runner`,
`agent/worktree`, `agent/sandbox`. Moderate effort, requires careful
interface design.
