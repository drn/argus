## Why

Claude Code's CLI runs its OWN per-user background-session supervisor, separate
from argus. A literal Ctrl+Z byte (`0x1A`, ASCII SUB) reaching that CLI — via
`/bg`, arrow-key detach, or simply pressing Ctrl+Z in the terminal — makes
Claude Code reparent the session out of argus's process tree *permanently and
invisibly*. Argus's stop path can never signal it again: the process has
structurally left the tree it was spawned into. This is the confirmed root cause
of orphaned worker sessions.

Argus already guards this footgun in its TUI (twice: the classic agent view's
zoom-toggle keybinding and the native Hera view's explicit trap — both remap
Ctrl+Z so the raw byte never reaches the PTY). **But the guard exists only in the
TUI.** The native macOS app (`macos/`) forwards keystrokes from SwiftTerm
straight to the daemon with zero Ctrl+Z handling, so a human attached to a
worker's terminal in the ArgusMac app who presses Ctrl+Z out of ordinary shell
muscle memory orphans the session today, with nothing to intercept it.

This is one of two parallel prevention fixes closing the same root cause across
the terminal-bearing frontends (the web/PWA client is fixed separately).

## What Changes

- **The macOS app strips Ctrl+Z (`0x1A`) from all outbound terminal keyboard
  input**, at the single chokepoint where SwiftTerm hands keystrokes to the
  daemon (`TerminalCoordinator.send`). All other bytes — including other control
  characters (Ctrl+C `0x03`, Ctrl+Y `0x19`, ESC `0x1B`) — are forwarded
  unchanged, in order.
- **Behavior chosen for this surface: swallow, not remap.** The TUI remaps
  Ctrl+Z to a pane-zoom / fullscreen toggle; the SwiftUI macOS surface has no
  analogous terminal-zoom affordance, and Ctrl+Z conventionally means "undo" in
  macOS apps — inventing a remap would be surprising. Dropping the byte mirrors
  the TUI's *intent* (Ctrl+Z never reaches the session) without inventing an
  unmapped action. A lone Ctrl+Z keypress therefore forwards nothing.
- **The decision logic is a pure `ArgusKit` helper (`TerminalInput.sanitize`)**
  so it is unit-testable from the `ArgusKitTests` executable target without
  SwiftTerm/AppKit; `ArgusMac`'s delegate calls it and logs when a byte is
  dropped.

## Capabilities

### Modified Capabilities

- `macos-app`: adds a terminal-input Ctrl+Z guard — the app strips `0x1A` from
  keyboard input before forwarding to `POST /input`, preventing session
  orphaning via Claude Code's background-session supervisor.

## Impact

- **New code:** `macos/Sources/ArgusKit/TerminalInput.swift` (pure sanitizer),
  `macos/Tests/ArgusKitTests/TerminalInputTests.swift` (byte-level contract).
- **Modified code:** `macos/Sources/ArgusMac/TerminalController.swift`
  (`TerminalCoordinator.send` filters via `TerminalInput.sanitize` + logs a
  drop).
- **No daemon change, no new REST endpoint, no schema change, no new
  dependency.** The guard is entirely client-side in the macOS app.
- **Frontend parity:** this closes the macOS app's slice of a cross-frontend
  root cause; the TUI already guards it, the web/PWA client is fixed by a
  parallel change. No REST-exposed surface changes.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring changes. The Go quality gate stays `make pre-pr`; the Swift gate is
  `make mac-test` / `make mac-build`.
