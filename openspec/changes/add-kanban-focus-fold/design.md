## Context

`internal/tui/hera/rail.go`'s `buildRows` partitions top-level (root) orchestrators into four kanban groups — `active` (headerless today), `backlog`, `blocked`, `done` — each backlog/blocked/done group rendered as a `"Label (N)"` header (unconditionally, whenever non-empty) followed by its member orchestrator subtrees. There is currently no fold on these groups at all: if non-empty, their members always render. This is a different axis from the EXISTING fold mechanisms already in the file (per-orchestrator `collapsed` map, `freelanceCollap`, `archiveCollapsed`, `coordArchiveOpen`) — all of which are manually toggled via `ToggleCollapse` (Space key) and persisted via `RailStateStore`.

Aaron wants the kanban groups to auto-fold: only the group holding the current selection is expanded; the rest collapse to their header line. Confirmed decisions (quick-confirm round):

- Active also gets a header and participates in the same fold (not exempted) — this is a bigger simplification than a special case, since it removes the current "headerless active + Pinned-divider special case" branch entirely and folds Active into the same uniform per-group loop Backlog/Blocked/Done already use.
- Kanban headers stay non-selectable (unlike the Freelance header, which is a manual toggle target). Stepping past a group boundary silently expands the next/previous group and lands the cursor inside it directly — the header is never a cursor stop.

## Goals / Non-Goals

**Goals:**
- Exactly one kanban group (Active/Backlog/Blocked/Done) renders its member rows at a time; the other three show only their header + count.
- `j`/`k`/arrow stepping across a group boundary transparently expands the newly-entered group and collapses the one just left, landing the cursor on the new group's first (stepping down) or last (stepping up) row in the same keystroke.
- Any programmatic selection change that targets a row in a non-focused group (kanban status change via `m`/`M`, plan-view jump via `SelectByTaskID`, ancestor-expand via `EnsureAncestorsExpanded`) re-focuses that row's kanban group first, so the target row exists in the rebuilt rows to select.
- Active gains a header/count row, uniform with Backlog/Blocked/Done.

**Non-Goals:**
- No manual override/pin of a kanban group's fold (e.g. a Space-toggle to force a group open regardless of focus). Pure focus-driven for v1; can revisit if it proves annoying in practice.
- No change to Pinned, Freelance, or Archive fold behavior — those keep their existing manual, persisted fold.
- No persistence of the focused kanban group across restarts — it's fully derived from the restored selection ref each time (which IS persisted), so no new persisted field is needed.
- No REST/web/macOS surface change — kanban view is native-TUI-only already.

## Decisions

**1. New `Rail` field: `focusedKanban db.HeraKanbanStatus`.** Tracks which of the four groups is currently expanded. Not persisted. Derived, not free-standing: every code path that changes the selection ref (`SetModel`'s restore, `SelectByTaskID`, `EnsureAncestorsExpanded`, and the new step-crossing logic) is responsible for keeping it in sync with wherever the cursor is about to land, `before` `buildRows` runs — never the reverse. Rationale: a naive "recompute after building" approach can't work, because `buildRows` itself needs to know which group to expand in order to lay out the target row at all (chicken-and-egg). Resolving the group from the model (walking `canonicalParents()` from the target orchestrator to its root, then reading that root's `KanbanStatus`) is independent of the row array, so it can run first.

**2. `buildRows`'s kanban loop renders a group's members only when `g.status == r.focusedKanban`; the header always renders when the group is non-empty (unchanged from today for Backlog/Blocked/Done; new for Active).** This drops the current `g.label == ""` special case (headerless Active + conditional Pinned-divider) entirely — Active becomes just another entry in the same loop, with the same unconditioned per-group leading divider Backlog/Blocked/Done already use. Net simplification: removes a branch rather than adding one.

**3. `step(dir)` gains boundary-crossing detection.** The existing scan (advance `i` by `dir`, stop at the first `selectable()` row) is extended: if the next non-selectable row is a kanban-group header whose group differs from `r.focusedKanban`, that's a crossing — set `r.focusedKanban` to the header's group, `buildRows()` (which now expands that group), then locate the new group's first (`dir>0`) or last (`dir<0`) member row directly (no `currentRef()`/`restoreCursor` involved, since we're moving to a brand-new row, not preserving an old one) and stop there. A header belonging to the group we're *already* focused on (e.g. stepping up into your own group's header from its first child) is not a crossing — just a non-selectable row to skip past, exactly as today. An empty group contributes no header row at all (existing behavior), so the scan naturally passes over it without any special case.

**4. `SetModel`, `SelectByTaskID`, and `EnsureAncestorsExpanded` each resolve-and-set `r.focusedKanban` from the target ref's top-level orchestrator before calling `buildRows`.** A small shared helper (e.g. `r.focusGroupOf(orchID)`) walks `canonicalParents()` to the root and reads its `KanbanStatus`, used by all three call sites plus the step-crossing path. This is what keeps the "exactly one group expanded, and it's always the one containing the selection" invariant intact even when the selection moves via a path other than plain arrow stepping (a coordinator's own kanban status changing out from under the current selection via `m`/`M` is the sharpest case: the row would otherwise vanish from the rebuilt rows because the wrong group is expanded).

**5. Default `focusedKanban` (no resolvable prior selection, e.g. very first build on an empty-then-populated rail) is `active`.** Matches "Active is the default working set" mental model; falls out of leaving the zero-value default (`db.HeraKanbanActive` is the documented zero-value default for the column itself, per the base spec's "no explicit value reads as active").

## Risks / Trade-offs

- **[Risk]** The crossing logic in `step()` adds real branching complexity to a function that's currently a simple linear scan. → **Mitigation**: keep the crossing check as an isolated, well-named helper (`groupOf(row) (db.HeraKanbanStatus, bool)`) so `step()`'s main loop stays readable; cover it with dedicated table tests for every boundary (Active↔Backlog, Backlog↔Blocked, Blocked↔Done, and the empty-group-skip case in both directions).
- **[Risk]** Losing the cursor visually when a coordinator's kanban status changes via `m`/`M` while it's selected, if the re-focus step is missed at that call site. → **Mitigation**: this is exactly Decision 4's shared helper; the `m`/`M` handler already goes through `Selection`/`buildRows` on every status change, so wiring the helper there is a small, testable change — add a regression test asserting the selection survives an `m` press across a group boundary.
- **[Risk]** The Active-header change is a visible behavior change beyond pure fold mechanics (a new `"Active (N)"` line appears where none existed before). → **Mitigation**: explicitly called out as **BREAKING** (visual) in the proposal; Aaron confirmed this tradeoff directly via the preview during quick-confirm.

## Migration Plan

Pure rail-rendering change, no data migration. Ships as a normal PR; no feature flag needed (matches the project's no-legacy-migration-code policy for a single-user tool). Rollback is a plain revert if needed.

## Open Questions

None outstanding — the quick-confirm round resolved the two ambiguous points (Active's participation, and header-selectability during crossing). No implementation-driven design questions were raised; the worker's one blocker (below) was a CI-infrastructure problem, not a design ambiguity.

**Status (as of this writing, 2026-07-19 ~23:15 UTC):** Implementation complete and merged into the branch by hera worker role `kanban-focus-fold` (argus task `1784479174020396000`) — all `tasks.md` stages done, `make pre-pr` green, OpenSpec change archived into `openspec/specs/hera-view/spec.md` in the same commit. The worker opened PR #880, then #881, against `master`.

**Blocker hit and resolved (not a design issue):** `drn/argus` GitHub Actions stopped triggering ANY workflow run repo-wide for ~4 hours (last run 17:20 UTC, resumed ~22:31 UTC) — root cause never determined (no admin/billing access to confirm; ruled out workflow-file/repo-disabled/individually-disabled causes). The worker's own session was heavy-context by the time CI came back, so per Aaron's direction the coordinator (not the worker) took over: rebased `argus/kanban-focus-fold` onto the now-current `master` (which had advanced 3 commits, including one touching the same rail file), resolving one real conflict in `context/knowledge/index.md` (both sides had appended a bullet to the same gotcha-file table row — combined both additions), verified `go build`/`go vet`/`internal/tui/...` tests green, and force-pushed. This was done in an independent scratch clone (`git clone` into the session scratchpad, not a `git worktree add`) because the worker's own worktree (`~/.argus/worktrees/ARGUS/kanban-focus-fold`) is write-blocked from the coordinator's sandbox, and the branch can't be checked out twice via `git worktree` — a plain clone sidesteps that constraint.

**Remaining steps:** confirm the fresh CI run on PR #881 (triggered by the rebase push) goes green, squash-merge, redeploy dogfood, verify, tell Aaron it's safe to test. If this recycles before that finishes, the next coordinator session should pick up exactly there — check `iris_gh_pr_view(task_id=1784479174020396000, pr_number=881)` first.
