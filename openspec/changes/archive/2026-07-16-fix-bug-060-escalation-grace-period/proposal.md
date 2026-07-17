## Why

**BUG-060 — the BUG-029 escalation counter's all-or-nothing reset makes it fragile against an isolated single-tick detection miss, so a genuinely, continuously-parked hera worker can never reach the escalation threshold.** Live bug-bash repro (real dogfood session, 4 screenshots, not staged): under one coordinator with 3 sibling hera workers, the FIRST sibling to hit a permission prompt correctly surfaced the needs-input "(?)" indicator (on itself and rolled up to both ancestors); LATER siblings that hit their OWN permission prompt — identically-shaped prompt, same code path — never did, even though the Agent pane clearly showed the live, unanswered prompt. The asymmetry was not about prompt shape or ordinal position (all 4 prompts were identically shaped, and the first was detected fine) — it was luck: `agent.EscalateParkedSelection`'s consecutive-tick counter resets to zero on ANY non-qualifying tick, and a genuinely parked session can still produce an isolated single-tick miss (Claude's own fullscreen redraw can blink the selection-cursor glyph off for one frame, and `readSessionLogTailBytes` has no synchronization with the daemon's concurrent log-file writer, so an occasional read can land on a torn/partial frame). Whichever sibling's stream happened to avoid any miss for a full `NeedsInputEscalationTicks`-tick window converged; any sibling that hit even one isolated miss within that window had its counter reset to zero and had to restart — and if misses recurred often enough, the counter could never reach the threshold at all.

## What Changes

- A non-qualifying tick that follows an ONGOING escalation streak is now held in a one-tick GRACE period instead of being discarded outright. The very next tick either resumes the streak in full (the miss was a transient blip) or, if it ALSO misses, confirms a genuine break (two CONSECUTIVE non-qualifying ticks reset the streak for real — unchanged from today).
- A grace tick's `escalated` result stays `true` if the streak had already reached the threshold before the miss, so an already-flagged worker does not visibly flicker its needs-input indicator off for the one tick a blip is being confirmed or forgiven.
- The anti-false-positive guarantee is unchanged: a busy/streaming agent showing only sparse, isolated coincidental matches (not a genuine parked streak) still never accumulates escalation credit, because each such match remains surrounded by 2+ misses that confirm the non-parked state.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `idle-detection`: the "Bounded consecutive-tick escalation when the fingerprint never converges" requirement's reset behavior changes from an all-or-nothing reset on any single non-qualifying tick to a one-tick grace period that only confirms a break after two CONSECUTIVE non-qualifying ticks.

## Impact

- Affected code: `internal/agent/needsinput.go` (`EscalateParkedSelection`), `internal/tui/app.go` (`detectNeedsInputSticky`'s escalation-counter persistence guard), `internal/agent/needsinput.go` (`ContentIdle`'s equivalent persistence guard) — both callers had a pre-existing `if newTicks > 0 { store }` guard that would have silently discarded the new negative grace-sentinel value if left unchanged.
- Affected docs: `context/knowledge/gotchas/events.md`, `context/knowledge/index.md`.
- Affected tests: `internal/agent/needsinput_test.go` (`TestEscalateParkedSelection` state-machine coverage), `internal/tui/bug060_integration_test.go` (new end-to-end rail-render repro + regression guard).
