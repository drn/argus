## Why

The Hera rail today gives a top-level coordinator exactly two states an operator can set: pinned or not, and archived or not. There is no way to signal *where a whole orchestration effort stands* — "haven't started this yet," "actively driving it," "stuck," "shipped, just tidying up" — independent of whether the coordinator's underlying argus task is `in_progress`/`in_review`/`complete` (that lifecycle is owned by the session, not the operator) and independent of the existing `s`/`S` rail keys (which step the bound *role's* hera status, a completely different axis scoped to whichever role is selected, not the top-level coordinator as a unit). With more than a handful of concurrent orchestrators running, Aaron has no lightweight way to triage "what's actually live right now" from the rail without opening each one.

## What Changes

- `HeraOrchestrator` gains a `kanban_status` column (`active` | `backlog` | `blocked` | `done`, default `active`), independent of `pinned_at`/`archived_at`, scoped to top-level (root, no canonical parent) coordinators only.
- The rail's Active section is partitioned into ordered sub-groups by kanban status: **active** (headerless, identical to today's rendering when nothing has been re-categorized), then **Backlog (N)**, **Blocked (N)**, **Done (N)** — each preceded by a divider, each entirely suppressed when empty. Pinned still renders first regardless of kanban status; Archive still renders last regardless of kanban status. Nested/non-root coordinators are unaffected — they keep rendering nested under their parent and never get their own kanban grouping.
- A new rail hotkey pair on `CtxHeraRail`: `m` steps the selected TOP-LEVEL coordinator's kanban status forward through the rail-order sequence (active → backlog → blocked → done, wrapping to active); `M` (shift+m) steps it backward. Both are no-ops when the cursor is not on a top-level coordinator header (a role, a nested sub-coordinator, Freelance, or an empty rail).
- `GET /api/hera`'s orchestrator envelope gains a `kanban_status` field so the read-only web/macOS Hera tabs can display it (no mutation support there — that gap is already the standing, named TUI-only exception for Hera mutations).

## Capabilities

### Modified Capabilities

- `hera-view`: new `kanban_status` data axis on `HeraOrchestrator`; rail section grouping/dividers for the Active list; new `m`/`M` rail keybindings (help overlay, selection gating).
- `rest-api`: `GET /api/hera` orchestrator envelope gains `kanban_status`.

## Impact

- **New code:** `db.HeraKanbanStatus` type + constants, `HeraOrchestrator.KanbanStatus` field, `SetHeraOrchestratorKanbanStatus` store method (`internal/db/hera.go`, `internal/db/schema.go`); `OrchView.KanbanStatus` (`internal/tui/hera/model.go`); `Ops.KanbanStep` (`internal/tui/hera/ops.go`); kanban-group partitioning in `Rail.buildRows` (`internal/tui/hera/rail.go`); `ActHeraKanbanAdv`/`ActHeraKanbanRev` (`internal/tui/keymap/actions.go`) + dispatch in `internal/tui/hera/page.go`.
- **Modified data:** new `hera_orchestrators.kanban_status` column, `ALTER TABLE ... ADD COLUMN` idempotent migration (no CHECK constraint — SQLite cannot widen a CHECK via `ALTER TABLE ADD COLUMN`, and the existing `nuked_at`/`base_branch` columns already establish the plain-`TEXT`-column-with-Go-level-validation precedent for this table).
- **Modified code:** `internal/api/hera.go` (`heraOrchJSON` gains `kanban_status`).
- **No breaking changes.** Existing orchestrators default to `active` on the ALTER migration, so the rail renders byte-identical to today until an operator explicitly re-categorizes a coordinator.
