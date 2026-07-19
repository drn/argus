## Why

A scoped `/spec-audit` of `internal/tui/hera/` found that four `hera-view`
requirements still describe a task-status-gated (`in_progress`) model for
needs-input surfacing, status-icon precedence, and spinner animation, while
PR #824 ("Bug bash 3" — BUG-A/BUG-C/BUG-F/#707) already shipped a
liveness/session/content-based model and never updated the spec. One of the
stale claims (the needs-input CLEARS requirement's derived-from) actively
misdirects a future reader to the wrong gate. This change brings the spec
back in line with already-shipped, already-correct code — no behavior is
changing.

## What Changes

- Rewrite "Status-icon precedence on role rows" to the actual classifier
  order (`NeedsInput > Active > ReadyToClose > Failed > Done > Idle > Live`)
  and add the currently-uncovered `Failed` (red ✕) case.
- Correct "Needs-input (?) propagates up" to drop the stale worker-only
  `in_progress` carve-out and fix its precedence sentence (needs-input
  outranks `ready_to_close`, not the reverse).
- Rewrite "Needs-input (?) CLEARS and propagates up" to describe the real
  mechanism (content-aware `needsInputIDs` set + liveness ending on session
  exit) instead of a `task.Status == in_progress` gate.
- Rewrite "Active agents animate a spinner glyph" to define "genuinely
  active" as `Live && SessionRunning && !SessionIdle`, and replace the
  now-wrong "Live-but-not-in_progress role is static" scenario with one
  confirming a live, content-active `in_review` role DOES animate (BUG-C).

No **BREAKING** changes — this is a documentation/spec correction with zero
code changes.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: four requirements corrected — status-icon precedence,
  needs-input propagation's worker gating, needs-input clearing mechanism,
  and spinner-animation gating — to match already-shipped liveness-based
  behavior.

## Impact

- `openspec/specs/hera-view/spec.md` only. No code, test, or API changes.
- No migration, no rollback concerns — pure documentation correction.
