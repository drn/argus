## 1. Escalation counter: one-tick grace period

- [x] 1.1 `agent.EscalateParkedSelection`: a non-qualifying tick following an
  ongoing streak (`prevTicks > 0`) returns `-prevTicks` (a negative sentinel
  preserving the streak) instead of resetting to zero; `escalated` stays
  `true` on that grace tick if `prevTicks >= NeedsInputEscalationTicks`.
- [x] 1.2 A qualifying tick following a grace state (`prevTicks < 0`) resumes
  the preserved streak (`-prevTicks + 1`), not a restart from 1.
- [x] 1.3 A SECOND consecutive non-qualifying tick (`prevTicks <= 0` on a
  miss) confirms a genuine break and resets to zero for real — unchanged
  anti-false-positive guarantee for a busy/streaming session with only sparse
  isolated matches.
- [x] 1.4 Fix the two call-site persistence guards (`internal/tui/app.go`'s
  `detectNeedsInputSticky`, `internal/agent/needsinput.go`'s `ContentIdle`)
  from `if newTicks > 0 { store }` to `if newTicks != 0 { store }` — the old
  guard silently dropped the new negative grace sentinel.

## 2. Tests

- [x] 2.1 Rewrite `TestEscalateParkedSelection` (`internal/agent/needsinput_test.go`):
  isolated single miss holds streak in grace and fully recovers; an
  already-escalated streak stays escalated through a grace tick; two
  consecutive misses confirm a genuine break and reset for real; sparse
  isolated matches amid otherwise-missing ticks never accumulate
  (anti-false-positive, preserved from the original BUG-029 test).
- [x] 2.2 New integration coverage (`internal/tui/bug060_integration_test.go`):
  a continuously-parked hera worker whose captured frame intermittently
  (every 3rd tick) misses the selection-cursor glyph still reaches
  needs-input and renders the rail glyph, and does not flicker off on a
  single grace-held miss after already escalating; two hera siblings sharing
  the single App-level `agent.ScreenRenderer` within one tick converge
  independently (first sibling fast via the fingerprint path, second via
  escalation).

## 3. Docs

- [x] 3.1 `context/knowledge/gotchas/events.md`: new BUG-060 section
  documenting the root cause and fix; update the existing BUG-029 section's
  "reset-not-pause" language to note the supersession.
- [x] 3.2 `context/knowledge/index.md`: update the `events.md` row summary.
