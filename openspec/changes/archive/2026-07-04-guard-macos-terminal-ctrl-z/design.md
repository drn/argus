## Context

The macOS app embeds a SwiftTerm `TerminalView` per task. Keystrokes flow
outbound through exactly one path:

```
SwiftTerm keyDown → TerminalCoordinator.send(source:data:)
  → TerminalController.enqueueInput(_:)  (FIFO pump)
  → ArgusClient.sendInput → POST /api/tasks/{id}/input
```

`TerminalCoordinator.send` is the SwiftTerm `TerminalViewDelegate` callback and
the single boundary where raw keyboard bytes (and paste content) leave the app.
It is the exact analog of the TUI's "before forwarding to the PTY" interception
point.

## Goals / Non-Goals

- **Goal:** a literal Ctrl+Z byte (`0x1A`) can never be forwarded to the daemon
  from macOS-app keyboard input.
- **Goal:** the decision is unit-testable without SwiftTerm/AppKit.
- **Non-Goal:** remapping Ctrl+Z to some macOS-app action. There is no terminal
  zoom/fullscreen affordance to mirror the TUI's remap, so the byte is simply
  dropped.
- **Non-Goal:** touching argus's core stop-path or the web/PWA client (separate
  parallel workers own those).

## Decisions

### Where the guard lives: `TerminalCoordinator.send`

`send` is the sole outbound chokepoint — every keystroke and paste passes
through it before reaching the FIFO input pump. Guarding here catches the
muscle-memory Ctrl+Z keypress (which SwiftTerm delivers as a single
`send([0x1A])`) as well as any Ctrl+Z that could ride along in a larger payload.

### Decision logic in `ArgusKit`, not `ArgusMac`

The `ArgusKitTests` executable target depends only on `ArgusKit` (pure
Foundation) — `ArgusMac` (SwiftTerm/AppKit) is not importable into it. To keep
the guard **testable** under `make mac-test`, the byte-filtering decision is a
pure `ArgusKit` helper (`TerminalInput.sanitize(_:)`); `ArgusMac`'s delegate is a
thin caller. This mirrors how other pure ArgusKit logic (`ByteLineSplitter`,
`SSEParser`) is tested.

### Strip the byte, don't just drop lone-Ctrl+Z payloads

`sanitize` removes every `0x1A` and forwards the remainder (possibly empty),
rather than only dropping a payload that is *exactly* `[0x1A]`. This satisfies
the acceptance criterion — "a literal Ctrl+Z byte must never be forwarded" —
maximally and robustly regardless of how SwiftTerm batches the keypress. The
cost (a `0x1A` embedded in pasted content is also removed) is negligible: a SUB
control byte has no legitimate use in interactive agent input, and forwarding
one would risk the same suspend/orphan behavior. A fast path returns clean input
untouched with no allocation.

### Swallow, not remap

The TUI remaps Ctrl+Z to a pane-zoom (agent view) / fullscreen (Hera view)
toggle so the byte is *consumed* rather than forwarded. The macOS app's SwiftUI
detail pane has no equivalent terminal-zoom surface, and Ctrl+Z conventionally
means "undo" on macOS. Rather than invent a surprising binding, the app drops
the byte — the *intent* (Ctrl+Z never reaches the session) is preserved, the
mechanism is not. A one-line `os.Logger` info entry records each drop for
observability.

## Risks / Trade-offs

- **A `0x1A` inside pasted binary content is stripped.** Accepted: the terminal
  is not a binary-transfer channel and a SUB byte is never desirable input for
  an interactive agent CLI. The TUI's paste path forwards bracketed content
  verbatim, so this is a minor, deliberate divergence in service of the stronger
  guarantee.
- **No end-to-end delegate test.** `TerminalCoordinator.send` needs a live
  SwiftTerm `TerminalView` and is in the un-testable `ArgusMac` target; the pure
  `TerminalInput.sanitize` contract is fully covered instead, and the wiring is a
  one-liner at the single chokepoint.
