## Why

The Hera rail's end-of-life (EOL) surface (shipped by `hera-rail-eol-keys` + `hera-coord-cascade-delete`) grew five keys with overlapping, hard-to-reason-about semantics: `a` (archive), `R` (retire), `C` (prune descendants), `Ctrl+R` (rail-wide prune), and `Ctrl+D` (cascade delete). Three of them reclaimed worktrees, two archived, the ladder (`retire → prune`) was implicit, and `Ctrl+R` swept the WHOLE rail with one keystroke. The operator could not cleanly answer "what does it mean to be done with this agent?" and risked nuking unrelated work.

BUG-022 (user-approved) replaces this with a **two-resting-state** model and three keys, anchored by a bedrock rule: **a DB row is NEVER deleted**. "Done with" always means: gone from the rail + worktree gone from disk + role/orchestrator/inbox/task all retained and retrievable.

## What Changes

**Two resting states** (added to roles AND orchestrators):

- **HIDDEN (Tier 1, `a`)** — the row moves into its PARENT coordinator's nested "Archive (N)" expando; the worktree + session stay ALIVE (no detach); fully reversible (un-hide = exact restore). A sub-coordinator drags its whole subtree into the parent's archive, structure retained.
- **NUKED (Tier 2, `Ctrl+D` / `C`)** — the row (and its whole subtree) is REMOVED from the rail entirely (not in any visible archive); the worktree + local/remote branch are RECLAIMED from disk and the session stopped; the DB rows (role + orchestrator + inbox + argus task via `db.SetArchived`) are RETAINED. Recover only via the DB (re-spin a fresh worktree). A nuked role's inbox stays readable.

**The three keys kept (reworked):**

- **`a` — HIDE.** Worker or sub-coordinator ONLY (a top-level coordinator has no parent to nest under → feedback no-op). Moves the row into its parent coordinator's nested archive; does NOT detach (session + worktree stay alive). Reversible toggle, NO confirmation.
- **`Ctrl+D` — NUKE.** Any worker or coordinator (incl. top-level). Removes it (and its whole subtree) from the rail; reclaims worktree + local/remote branch; stops the session; ARCHIVES + marks NUKED the DB rows (role + orchestrator + inbox + argus task — never hard delete). Confirm with scope/count. Multi-binding preserved: a task bound live OUTSIDE the subtree is left fully alone.
- **`C` — CLEAR THIS COORD'S ARCHIVE.** On a selected coordinator: NUKE every item the user `a`-hid into that coordinator's archive (= `Ctrl+D` on each). Confirm with count. Scoped to the selected coordinator's archive, NEVER global.

**Dropped:** the `R` (retire) key + handler, and the rail-wide `Ctrl+R` prune key + handler.

## Capabilities

### Modified Capabilities

- `hera-view`: the "Rail keybindings (area 4)" requirement drops `R` and `Ctrl+R` and redefines `a` (hide, worker/sub-coord only), `Ctrl+D` (nuke), and `C` (clear this coordinator's archive). The cascade-delete requirement is recast from "archive into the visible Archive" to "NUKE: remove from the rail entirely + reclaim worktrees". New requirements capture the two-state hide/nuke model and the per-coordinator nested-archive rendering.
- `hera-coordination`: a new requirement captures the two-state (HIDDEN vs NUKED) representation in the store — a second `nuked_at` marker on `hera_orchestrators` and `hera_roles`, the rail-visibility rule (nuked = invisible), and the zero-hard-deletes invariant for every EOL store op.

## Impact

- **Schema (additive, no migration code beyond idempotent ALTERs):** a nullable `nuked_at TEXT` column on `hera_orchestrators` and `hera_roles`. HIDDEN = `archived_at` set, `nuked_at` NULL. NUKED = `nuked_at` set (and `archived_at` set, so the row leaves the active-name index). This is the author's only DB; a breaking schema change is fine (no migration code — the column is added to the `CREATE TABLE` DDL and via an idempotent `ALTER TABLE … ADD COLUMN`).
- **New store methods:** `NukeHeraRole` / `NukeHeraOrchestrator` (stamp `nuked_at` + `archived_at`), `RoleView.Nuked`/`OrchView.Nuked` projection, and BuildModel filtering nuked rows out of the rail entirely.
- **Modified code:** `internal/db/schema.go` (column), `internal/db/hera.go` (Nuke verbs + nuked-aware lists/scans), `internal/tui/hera/ops.go` (`MutateStore` + `Ops` nuke verbs), `internal/tui/hera/model.go` (`Nuked` projection + filter), `internal/tui/hera/page.go` (drop `OnRetire`/`OnPruneDone`; redefine `a`/`Ctrl+D`/`C`), `internal/tui/heraactions.go` (rework hide/nuke/clear handlers; drop retire + rail-wide prune), `internal/tui/app.go` (callback wiring), `internal/tui/modal/help.go` (+ `help_test.go`), README keybinding table, `context/knowledge/gotchas/hera-view.md`.
- **Multi-binding isolation** preserved throughout: every EOL op acts on the Selection's role/orchestrator and only reclaims a task/worktree when no OTHER live binding (outside the nuked subtree) points at it.
- **Zero hard deletes:** no EOL path calls `DeleteHeraRole` / `DeleteHeraOrchestrator` / `db.Delete` / message-row deletes. (The pre-existing `db.SetArchived`-drops-queued-`task_messages` behavior — a different table — is established and out of scope.)
- **Specs are LOCAL DOCS only** (`openspec/project.md`). Do NOT wire `openspec validate` into Go CI or `make`; the quality gate stays `make pre-pr`. Run `openspec validate --strict` locally only.
