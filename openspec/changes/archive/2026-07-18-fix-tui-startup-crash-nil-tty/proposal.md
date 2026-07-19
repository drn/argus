## Why

`ux.log` shows three identical startup crash-loops within 13 seconds on 2026-07-13 (23:16:15, 23:16:27, 23:16:28): `runtime error: invalid memory address or nil pointer dereference` inside `github.com/gdamore/tcell/v2/terminfo.(*Terminfo).TPuts` → `tScreen.enableMouse` → `tview.Application.EnableMouse` → `internal/tui.(*App).Run` (`app.go:1091`). This is NOT the historical EnableMouse-ordering bug (fixed 2026-03-22, documented in `gotchas/ui-threading.md`) — `EnableMouse` is already correctly called after `SetScreen`. It is a new failure mode: `tcell.NewScreen()` returns an un-initialized screen; `tview.Application.SetScreen` calls `screen.Init()` internally but discards its returned error entirely (`rivo/tview@v0.42.0` `application.go` `SetScreen`, unconditional `screen.Init()` with no error check). `tcell.Screen.Init()` lazily opens the controlling terminal (`tcell.NewDevTty`, `/dev/tty` on Unix) the first time it runs; when no controlling terminal is available (the process was launched with no ctty — a detached/headless process, a non-interactive script or tool sandbox, etc.), `Init()` fails, the screen's internal tty writer is left nil, and `SetScreen` returns as if nothing went wrong. The very next `EnableMouse(true)` call then panics several frames deep inside tcell attempting to write terminfo escape sequences through the nil writer.

## What Changes

- `internal/tui.(*App).Run()` now probes for a real controlling terminal (open-then-immediately-close `tcell.NewDevTty()`, which has no raw-mode side effects since `Start()` is never called) BEFORE constructing the tcell screen at all, via an overridable `probeTerminal` var. A tty-less launch now returns a clean, wrapped error from `Run()` instead of reaching `tview.Application.SetScreen`/`EnableMouse` with a screen whose initialization silently failed.
- No change to the existing `SetScreen`-then-`EnableMouse`/`EnablePaste` ordering, which remains correct for the case where a real terminal IS available.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `tui-shell`: adds a "Startup requires a controlling terminal" requirement — `Run()` fails fast with a clear error when no controlling terminal is available, rather than proceeding into screen setup that can silently half-fail.

## Impact

- `internal/tui/app.go` (`Run()` gains a `probeTerminal()` preflight check + the `probeTerminal` var), `internal/tui/run_test.go` (new regression test), `context/knowledge/gotchas/ui-threading.md` + `context/knowledge/index.md` (documented root cause).
- No API/schema changes, no new config. No behavior change for the normal case (a real controlling terminal present) — the probe succeeds and `Run()` proceeds exactly as before. A tty-less launch now exits with a one-line error instead of a nil-pointer panic + stack dump.
