## 1. Delta spec

- [x] 1.1 Add the "Bounded consecutive-tick escalation when the fingerprint
      never converges" requirement to
      `openspec/changes/fix-bug-029-needsinput-escalation/specs/idle-detection/spec.md`.

## 2. Tests (TDD — write first)

- [x] 2.1 `internal/agent/needsinput_test.go`: escalation counter/helper fires
      needs-input-equivalent after N consecutive qualifying ticks despite a
      non-converging fingerprint.
- [x] 2.2 `internal/agent/needsinput_test.go`: fewer than N consecutive
      qualifying ticks (match drops before the window elapses) does not
      escalate.
- [x] 2.3 `internal/agent/needsinput_test.go`: working-affordance present on
      any tick within the window resets the counter and suppresses escalation.
- [x] 2.4 `internal/agent/needsinput_test.go` `TestContentIdle`: a
      never-converging-fingerprint session showing the selection shape with no
      working affordance becomes content-idle after N ticks (spinner stops).
- [x] 2.5 `internal/tui` sticky-detection test: `detectNeedsInputSticky`
      surfaces needs-input after N ticks for a session whose fingerprint never
      converges but shows the qualifying combination.
- [x] 2.6 Re-verify existing BUG-032/033/035/036 fixtures
      (`TestContentFingerprint`, `TestAwaitingInputFingerprint`,
      `TestContentIdle*`, `TestShouldKickRerender`,
      `TestMaybeKickRerender_DefersWhenBlockedOnPrompt`) still pass unmodified.

## 3. Implementation

- [x] 3.1 Add a named constant for N (5-10 consecutive ticks / seconds) with a
      comment explaining the tradeoff, in `internal/agent/needsinput.go`.
- [x] 3.2 Add the consecutive-tick escalation tracking (selection-shape
      present + working-affordance absent), factored as a small shared helper
      if that doesn't force an awkward `internal/tui` <-> `internal/agent`
      coupling; otherwise duplicate the small counter logic at each call site.
- [x] 3.3 Wire the escalation into `agent.ContentIdle`
      (`internal/agent/needsinput.go`).
- [x] 3.4 Wire the escalation into `detectNeedsInputSticky`
      (`internal/tui/app.go`).
- [x] 3.5 Do not touch `fingerprintVolatileLine`/`decorationLine` or
      `ForceResyncPTY`/resize behavior.

## 4. Docs

- [x] 4.1 Add a bullet to `context/knowledge/gotchas/events.md` (same section
      as BUG-032/033/035/036) documenting the escalation fallback, the chosen
      N, and why it's a separate path from fingerprint convergence.

## 5. Archive + gate

- [x] 5.1 `openspec archive fix-bug-029-needsinput-escalation` (merge the delta
      into `openspec/specs/idle-detection/spec.md`, move the change folder to
      `openspec/changes/archive/<date>-fix-bug-029-needsinput-escalation/`)
      before opening the PR.
- [x] 5.2 `go test ./internal/tui/... ./internal/agent/...` clean.
