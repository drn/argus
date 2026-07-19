## Why

BUG-061: the hera rail shows a spinner instead of `(?)` for a row genuinely parked at a live permission prompt, until the row is selected — at which point it flips to `(?)` instantly. This is the third reported occurrence of this symptom class (after BUG-029 and BUG-060), both of which tuned the escalation-counter state machine without fully resolving it. Live repro (three throwaway hera workers driven to a real Bash permission prompt) found the actual, DETERMINISTIC root cause: Claude Code renders a blinking cursor/status glyph that never stops, even while genuinely parked with the "working" affordance correctly absent. The needs-input detector's fixed 16 KB tail window can be entirely consumed by this redraw given enough real time (observed: 37 KB+ gap after ~4 minutes), permanently pushing the actual prompt text out of the scanned window — not intermittently (BUG-060's "torn read" theory), but permanently, until something forces a fresh repaint (which is why selecting a row "fixes" it — `ForceResyncPTY` forces exactly that). No escalation-counter retuning can fix a signal that is structurally absent from the window rather than merely flickering.

## What Changes

- Add `agent.SubstantiveTail` (and its `degenerateSuffixStart`/`TrimToSubstantiveTail` helpers) to `internal/agent/needsinput.go`: recognizes a trailing run of a short byte sequence repeating many times (Claude's blink redraw) and expands the read backward (bounded by `NeedsInputMaxExpandBytes`) until real content is found, instead of a flat last-N-bytes cut.
- Wire `SubstantiveTail` into both existing consumers of the shared detection heuristic: the TUI's on-disk log tail read (`internal/tui/app.go`'s `readSessionLogTailBytes`) and the daemon's Web Push watcher's ring-buffer read (`internal/api/push.go`'s `tailOf`) — both shared the same flat-window assumption and the same bug.
- Make the "sticky carry-forward" pass in both `detectNeedsInputSticky` (TUI) and `computeNeedsInput` (daemon/push) genuinely sticky: a previously-flagged, still-running task stays flagged unconditionally instead of re-requiring a fresh tail match every tick. `agent.NeedsInputClear` (user input past baseline, or archive) becomes the sole clearing mechanism — closing an unintended second clearing path that fired on a tail-content quirk rather than a genuine answer.
- No changes to `EscalateParkedSelection`'s state machine (BUG-029/060) — this is a distinct root cause, not another tuning of that counter.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `idle-detection`: "Detection scans only a bounded recent tail" now describes an expand-on-degenerate-tail read instead of a flat fixed-size window; "Needs-input flag clears on user input or archive, never on signal decay" is tightened so the sticky carry-forward pass can no longer silently drop a flag on a tail-content miss.

## Impact

- `internal/agent/needsinput.go` (new exported helpers + constant), `internal/tui/app.go` (`readSessionLogTailBytes` wraps the new helper via a renamed `readSessionLogRawTail`; `detectNeedsInputSticky`'s sticky pass simplified), `internal/api/push.go` (`tailOf` wraps the new helper; `computeNeedsInput`'s sticky pass simplified).
- No API/schema changes, no new config. No behavior change for actively-producing sessions (the degenerate-run check fails fast and never expands). Bounded worst-case added disk/ring read per tick, only for a session parked long enough to flood the base window (rare).
