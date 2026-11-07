## Context

`cap-output-repaint-rate` already wires `TerminalPane.OnUserInput` to the repaint pump's `noteKey` for the Tasks pane and both Hera panes. This change only adds two callers of that hook.

## Decisions

- **Press and release both report.** The agent usually acts on the press, but some affordances act on release; each report just renews the 150ms window, so reporting both is cheap and covers either.
- **Report after the write, only on the forwarding path.** The call sits at the end of `forwardClickPress` (after the liveness check, so an unforwarded click reports nothing) and `forwardClickRelease`. Order relative to the write does not matter for correctness: the pump reads the stamp when the echo wakes it.
- **Wheel excluded.** A wheel tick scrolls the agent's surface continuously; treating each tick as an echo would keep the fast cap open for the whole scroll, defeating the cap. It also was not requested.
- **Shared helper.** `reportUserInput` is nil-safe and also replaces the inline nil check in the paste path.

## Non-goals

No change to the intervals, the pump, or the wheel path.
