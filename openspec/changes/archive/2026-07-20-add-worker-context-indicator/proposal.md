## Why

`coordinator-context-management` gave coordinators a live `context_size` signal, a budget nudge,
and a recycle primitive — because a coordinator is long-lived and personally accumulates every
token it reads, delegates, or relays. Workers and freelance roles are shorter-lived and have no
such guard, which was the right call at the time — but it also means a worker quietly running long
enough to approach its own context ceiling gives zero warning in the rail today. The rail itself is
narrow (roughly 30 columns), so this isn't "add a progress bar" — it's a single reserved character
at the row's trailing edge, in the same place a coordinator's live-child count already lives.

## What Changes

- `argus coord-hook`'s Stop hook widens its stamping gate from "coordinator-kind role" to "any
  hera-bound role" (coordinator, worker, or freelance) — `task_meta` (`hera`, `context_size`) is
  now overwritten on every Stop event for a worker or freelance session too. The
  budget/nudge/hard-stop/recycle machinery stays coordinator-only; workers and freelance roles are
  stamped and nothing more.
- The rail drops the parens on a coordinator's live-child-count badge — `(17)` becomes a bare grey
  `17` — freeing up the visual language around that column for the new indicator on worker rows,
  which have no count badge of their own to reuse.
- Every live worker/freelance rail row reserves a trailing 2-character slot: blank under 40% of the
  configured `coordinator_context_budget`, a dot that ramps pale yellow (40–65%) to hot orange
  (65–90%), then a red `!` past 90%. The slot is reserved for every worker/freelance row regardless
  of its current percentage (not only once it goes warm), so a name never reflows the instant a row
  crosses a threshold. Coordinators are excluded — they already have the coord-hook guard.

## Capabilities

### Modified Capabilities

- `coordinator-context-management`: the Stop hook's stamping gate widens from coordinator-only to
  any hera-bound role; budget/nudge/hard-stop/recycle enforcement is unchanged and stays
  coordinator-only.
- `hera-view`: the orchestrator row's live-count badge drops its parens; worker/freelance rail rows
  gain a reserved trailing context-pressure indicator.

## Impact

- **Modified code:** `cmd/argus/coord_hook.go` (gate), `internal/tui/hera/model.go` (`RoleView`
  gains `ContextSize`/`ContextPercent`), `internal/tui/hera_tiering.go` (`resolveHeraTier` computes
  `ContextPercent` from `cfg.Hera.CoordinatorContextBudget` — local mode only, the existing seam for
  cfg-dependent `RoleView` annotation), `internal/tui/hera/rail.go` (bare count, indicator
  rendering), `internal/tui/theme/theme.go` (three new colors/styles).
- **No schema change:** reuses the existing `task_meta` (`hera`, `context_size`) key.
- **No new config:** reuses `coordinator_context_budget` as the shared reference denominator for
  the rail's percentage display, coordinator or worker — it's "how many tokens is a lot for a
  session on this project," not something inherently coordinator-specific.
- **Named non-goal:** `ContextPercent` resolution is local-mode only (mirrors the existing
  diligence-tiering seam it reuses). Remote (web/macOS) rail views do not show the indicator —
  `ContextSize` reaches them via the same `ListMetaByNamespace` path either way, but the
  budget-dependent percentage does not, since remote mode has no `cfg` to compute it from. Filed
  as a follow-up, not silently dropped, per this repo's frontend-parity rule.
- **No breaking changes.** Additive; a session that never crosses 40% renders identically to today.
