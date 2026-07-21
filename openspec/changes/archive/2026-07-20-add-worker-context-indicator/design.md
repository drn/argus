## Context

`coordinator-context-management` (archived 2026-07-05) shipped `argus coord-hook`, a global Claude
Code `Stop` hook that stamps `task_meta` (`hera`, `context_size`) and nudges a coordinator to
recycle once it crosses a configured budget. It deliberately gated on the bound role being
`coordinator`-kind — workers are disposable, so the budget/nudge/recycle machinery has no reason to
apply to them. That reasoning still holds. What it left unexamined is that the *stamp itself* is
generically useful — it's just "how many tokens has this session read" — and a worker or freelance
role has no visibility into that at all today, anywhere, not even for a human glancing at the rail.

This change was scoped interactively against a live screenshot of the actual rail (roughly 30
columns wide, icon + fold caret + name + a coordinator's live-count badge) rather than designed in
the abstract — the design that follows is the result of several corrections against that real
constraint, recorded here so the next person doesn't re-litigate them.

## Goals

- Give a live worker/freelance rail row a glance-able signal that its underlying Claude Code
  session is approaching its context ceiling.
- Fit inside the rail's existing width with no new column and no reflow surprises.
- Reuse the coord-hook's existing transcript-tailing and `task_meta`-stamping mechanism rather than
  building a second one.

## Non-Goals

- No budget enforcement, nudging, or recycling for workers/freelance — that stays coordinator-only.
  A worker that runs hot just runs hot; the indicator is informational only, not a stop signal.
- No indicator on coordinator rows. A coordinator already has the coord-hook guard; the rail
  reserves its trailing slot for its live-count badge, unchanged (aside from dropping the parens).
- No web/macOS surfacing of the derived percentage (see D4).

## Decisions

### D1 — Widen the stamp gate, not a second hook

Reuse `argus coord-hook` itself rather than adding a second Stop-hook subcommand. The transcript
read, `ARGUS_TASK_ID` gate, and REST-based `task_meta` write are 100% identical regardless of role
kind — only the budget/nudge/hard-stop/recycle path is coordinator-specific. The gate moves from
`kind != coordinator → no-op` to `kind == "" → no-op` (an unbound task, same as any non-Argus
session), and the coordinator-only budget logic runs after the now-unconditional stamp instead of
gating it.

#### Acceptance criteria

- it should stamp `context_size` for a coordinator, worker, or freelance role identically
- it should still no-op with zero side effects for a task with no hera role bound at all
- it should never emit a Stop-hook block decision for a worker or freelance role, regardless of
  `context_size`

### D2 — Reuse `coordinator_context_budget` as the shared denominator

No new config field. `coordinator_context_budget` is not conceptually "a coordinator's budget" so
much as "how many tokens counts as a lot on this project" — reusing it for the worker/freelance
percentage avoids a second knob that would need to be kept in sync with the first for the numbers
to mean the same thing across the rail.

### D3 — Thresholds, ramp, and glyph

Settled interactively, in this order, each a correction of the previous:

1. A single dot, appearing past 40%, escalating to a distinct mark past 90% — chosen over a
   percentage suffix, a block gauge, or recoloring an existing glyph, because it's the only option
   that costs one reserved column rather than four-plus, and because recoloring an existing glyph
   (the spinner, or the needs-input `?`) risks landing on a color the rail has already committed to
   a different meaning.
2. The indicator lives in the row's trailing space, mirroring where a coordinator's live-count
   badge already sits, rather than the leading icon cluster — leading space is contested by the
   fold caret + status glyph; trailing space is genuinely free on a worker row (no count badge).
3. Scope is workers/freelance, not coordinators — a coordinator already has the coord-hook's
   guard; the workers are the ones with nothing watching them.
4. Ramp: pale yellow (40–65%) → hot orange (65–90%) → a plain `!` in red past 90%, not a Unicode
   warning-triangle glyph (⚠ / △ / ▲ were considered; checked against real terminal fonts and
   dropped — plain `!` is the one glyph guaranteed to render identically everywhere, which matters
   more here than expressiveness).
5. The 2-character slot (a blank separator column + the glyph column) is reserved on every eligible
   row's whole lifetime, not only once a row first goes warm — so a name never visibly reflows the
   instant it crosses 40%. The cost (every worker/freelance row permanently gives up 2 characters
   of name width) was raised explicitly and accepted in favor of that stability.

#### Acceptance criteria

- it should render nothing in the reserved slot under 40%
- it should render a pale-yellow dot from 40% up to (not including) 65%
- it should render a hot-orange dot from 65% up to (not including) 90%
- it should render a red `!` at 90% and above
- it should never render anything on a coordinator row
- it should compose with the existing PR-tag reservation on the same row without either
  overwriting the other

### D4 — `ContextPercent` resolution stays local-mode only

`RoleView.ContextSize` (the raw stamped value) is populated in `buildRoleView` straight from the
already-fetched `heraMeta` map — pure, no I/O, works in both local and remote mode, same as
`ReadyToClose`. The derived `ContextPercent` needs `cfg.Hera.CoordinatorContextBudget`, which is
only available via `a.db.Config()` in local mode. Rather than plumb `cfg` into `BuildModel` (which
would ripple into every caller, including the remote/test paths that don't have a budget concept
today), `ContextPercent` is computed by the existing `resolveHeraTier` closure — the same
already-established, already-local-mode-only seam that stamps `AppliedModel`/`AppliedEffort` for
the diligence-tiering readout. Remote mode simply never calls it, so `ContextPercent` stays its
zero value there and the indicator never renders — the same shape of gap the tiering readout
already has, not a new one.

## Risks / Trade-offs

- Every worker/freelance row permanently loses 2 characters of name width, whether or not it's ever
  going to use them (D3.5) — accepted in favor of never reflowing.
- The rollup is per-row, not per-subtree: a worker bridging a nested sub-coordinator shows its OWN
  context, not a rollup of what's inside that child orchestrator. Unlike the needs-input rollup,
  this isn't extended here — context pressure is a property of the one session actually running,
  not something meaningful to aggregate across a subtree of otherwise-independent sessions.
- Remote (web/macOS) rail views never show the percentage-derived indicator (D4) — named, not
  silent, per this repo's frontend-parity rule.

## Migration Plan

None — additive. A session that never crosses 40% context renders identically to today (both the
row layout and the coordinator's bare-count badge look the same either way once the parens are
dropped, unconditionally, for every coordinator regardless of context state).

## Open Questions

- None outstanding at time of writing.
