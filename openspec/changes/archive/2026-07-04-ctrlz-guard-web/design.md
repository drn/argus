# Design: Ctrl+Z guard for the web/PWA terminal input

## Context

- The PWA terminal is xterm.js (`internal/api/static/index.html`). Keystrokes flow `xterm keydown → term.onData(data) → sendInputBytes(data) → POST /api/tasks/{id}/input → PTY`.
- xterm's default handler converts `Ctrl+Z` to the byte `0x1a` and emits it via `onData`. Delivered to Claude Code's stdin, the OS raises `SIGTSTP` and the CLI backgrounds the session into its own supervisor — permanently orphaning the argus worker (root cause confirmed by a sibling investigating worker against Claude Code's docs + a live test).
- The TUI already guards this on every surface. Classic agent view: `handleAgentKey`'s `KeyCtrlZ` case remaps to `toggleAgentZen()` and returns `nil` (byte never forwarded); plus an unconditional `if event.Key()==tcell.KeyCtrlZ { return nil }` net independent of the rebindable zoom binding. Native Hera view: `HeraPage.InputHandler` traps `Ctrl+Z` → `FocusMachine.ToggleFullscreen` and ALWAYS consumes it (even on the rail, where it's a no-op). The invariant in both: **a literal `Ctrl+Z` byte must NEVER reach the PTY.** (See `context/knowledge/gotchas/keybindings.md` and `hera-view.md`.)

## Goals / Non-Goals

- **Goal:** a bare `Ctrl+Z` pressed while the PWA terminal has keyboard focus never emits `0x1a` to the PTY input path.
- **Goal:** the user gets a hint (not a silent dead key) explaining why nothing happened.
- **Non-Goal:** remapping `Ctrl+Z` to a surface action. The PWA detail view is already full-screen; there is no split-pane/zoom to toggle, so a swallow is the correct behavior (the task brief explicitly permits swallow when no analog exists).
- **Non-Goal:** stripping `0x1a` from the raw byte stream (paste, `sendInputBytes` callers). The reported vector and the TUI parity are both KEY-level. Guarding the byte stream would risk mangling a legitimate paste and expands scope with no matching precedent.

## Decisions

### Intercept at the xterm key layer, not `onData`

`term.attachCustomKeyEventHandler(handler)` runs before xterm processes a key: in the vendored xterm, `_keyDown` does `if (this._customKeyEventHandler && false === this._customKeyEventHandler(e)) return false;` as its FIRST step — returning `false` short-circuits xterm before it emits any byte to `onData`. This is the direct analog of the TUI intercepting the key in `handleAgentKey` before `tcellKeyToBytes`, and it's strictly better than filtering in `onData` (which only sees the already-translated byte and would have to string-match `0x1a`).

### Swallow, with a toast

- `isCtrlZ(ev)` matches `ev.ctrlKey && !ev.metaKey && !ev.altKey && (ev.key === 'z' || ev.key === 'Z' || ev.keyCode === 90)`.
  - `!metaKey` leaves `Cmd+Z` (browser/textarea undo) alone.
  - `!altKey` leaves `Ctrl+Alt+Z` alone.
  - Shift is tolerated: `Ctrl+Shift+Z` also produces `0x1a` (both `'z'&0x1f` and `'Z'&0x1f` == `0x1a`), so it is the same footgun and is caught too.
  - `keyCode === 90` is a layout-robust fallback alongside the `ev.key` character check.
- The handler returns `false` for every event phase where `isCtrlZ` holds (keydown/keyup), so xterm never processes it. On `keydown` only it also calls `ev.preventDefault()` (suppresses the browser's native textarea undo) and `showToast(...)`. Gating the side effects to `keydown` avoids a double toast from the paired keyup. `showToast` already removes any existing toast before showing a new one, so mashing `Ctrl+Z` keeps exactly one (re-timed) toast rather than stacking.

### Placement and mount lifecycle

- The guard is wired inside `setupTerm()` (which creates a fresh `term` on every mount and already wires `term.onData`), so every terminal instance carries it with no teardown needed. `isCtrlZ` is a module-level function (testable, greppable).

## Testing strategy

- **CI-enforced (Go):** `make pre-pr` is Go-only — Playwright is NOT wired into the Makefile or CI. So the primary regression guard is a Go test that reads the served `static/index.html`, extracts the inline script, and asserts the guard is present (`attachCustomKeyEventHandler` wired + the `isCtrlZ` predicate + a `0x1a`/SIGTSTP rationale token). Modeled on the existing `TestSPAJSReferencesResolve` file-read approach. Without this, removing the guard would be invisible to CI.
- **Behavioral (Playwright, repo convention, not in CI):** a `terminal.spec.ts` test focuses the terminal, presses `Ctrl+Z`, and asserts (a) no `POST /input` carrying `0x1a` fired and (b) the explanatory toast appears — while a control keystroke still forwards normally.

## Prove-It criteria

- it should not emit `0x1a` to `/input` when `Ctrl+Z` is pressed with the terminal focused
- it should still forward a normal printable keystroke to `/input`
- it should leave `Cmd+Z` (metaKey) unguarded (no interception)
- it should surface an explanatory toast on `Ctrl+Z`
- it should keep the guard present in the served app shell (CI regression guard)
