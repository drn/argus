# Proposal: Ctrl+Z guard for the web/PWA terminal input

## Why

Claude Code's CLI hosts its own per-user background-session supervisor, separate from argus. A bare `Ctrl+Z` (byte `0x1a` / SUB, which the OS turns into `SIGTSTP`) reaching Claude Code's stdin makes the CLI background the session into that supervisor, reparenting it out of argus's process tree permanently and invisibly — argus can never signal or stop it again. This orphans the worker.

Argus already fixed this footgun in the TUI, twice: the classic agent view remaps `Ctrl+Z` to zoom and swallows the raw byte, and the native Hera view traps `Ctrl+Z` → fullscreen and always consumes it. Both guarantee the literal `0x1a` byte never reaches the PTY. But that guard exists only in the TUI. The web/PWA terminal client (`internal/api/static/index.html`, xterm.js) has zero `Ctrl+Z` handling: a human attached to a worker's terminal pane via the PWA who presses `Ctrl+Z` out of ordinary terminal muscle memory orphans the session today, with nothing intercepting it.

## What Changes

- The PWA terminal SHALL intercept `Ctrl+Z` at the xterm.js key layer (via `attachCustomKeyEventHandler`) BEFORE any byte is emitted, so the `0x1a` byte can never reach the agent's PTY through the keyboard input path.
- Because the web surface has no split-pane / zoom analog to remap `Ctrl+Z` onto (the detail view is already full-screen), the key is SWALLOWED rather than remapped. This mirrors the TUI's underlying safety intent (byte never reaches the PTY) without inventing a surface-specific action.
- A brief, self-deduping toast explains the swallow so the key does not read as a silent dead key ("Ctrl+Z is disabled here — it would background the agent").
- `Cmd+Z` (metaKey — the browser/textarea undo) is intentionally NOT matched; only a bare `Ctrl+Z` (the SIGTSTP vector) is guarded. Shift is tolerated because `Ctrl+Shift+Z` produces the same `0x1a` byte.

Scope boundary: this guards the KEYBOARD path (the reported vector), matching the TUI which also guards at the key level, not the raw byte stream. Pasting a literal `0x1a` byte is out of scope (extraordinarily unlikely, and the TUI has the same boundary).

No breaking changes; no other input behavior is altered.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- **mobile-pwa** — the SPA terminal gains a `Ctrl+Z` interception guard on the keyboard input path.

## Impact

- Code: `internal/api/static/index.html` — a small `isCtrlZ(ev)` predicate plus a `term.attachCustomKeyEventHandler` guard wired in `setupTerm()`; `SW_VERSION` bump in `internal/api/static/sw.js` (shell asset changed).
- Tests: a Go regression guard (`internal/api/static_ctrlz_test.go`) asserting the guard is present in the served shell — because Playwright is NOT part of `make pre-pr`/CI, this is the CI-enforced protection; plus a Playwright behavioral spec (`web-tests/tests/terminal.spec.ts`) asserting `Ctrl+Z` produces no `/input` write of `0x1a` and surfaces the toast.
- Docs: a gotcha bullet in `context/knowledge/gotchas/web-remote.md` and an index count bump in `context/knowledge/index.md`.
- Help modal / README: no TUI key added or rebound, so no keymap/help/README change. The PWA has no key-reference surface to update.
