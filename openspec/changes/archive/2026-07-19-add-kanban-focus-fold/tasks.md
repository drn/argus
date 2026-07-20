**Design doc:** openspec/changes/add-kanban-focus-fold/design.md

## 1. Tests

- [x] 1.1 Failing test: only the focused kanban group's members render; the other three render header+count only
- [x] 1.2 Failing test: Active renders its own `"Active (N)"` header/divider uniformly with Backlog/Blocked/Done (no headerless special case, no Pinned-conditional divider)
- [x] 1.3 Failing test: stepping down past the focused group's last row expands the next non-empty group, collapses the one just left, lands on the new group's first row
- [x] 1.4 Failing test: stepping up past the focused group's first row expands the previous non-empty group, collapses the one just left, lands on the new group's last row
- [x] 1.5 Failing test: crossing skips a genuinely empty intermediate group (e.g. Blocked empty — stepping down from Backlog's last row lands directly in Done's first row)
- [x] 1.6 Failing test: pressing `m`/`M` on the selected top-level coordinator, moving it to a different kanban group, keeps it selected and re-focuses its new group
- [x] 1.7 Failing test: `SelectByTaskID` targeting a role in a non-focused kanban group re-focuses that group before locating the row, so the jump succeeds
- [x] 1.8 Failing test: `EnsureAncestorsExpanded` likewise re-focuses the target's kanban group when needed
- [x] 1.9 Failing test: default focused group is `active` on the first non-empty build with no resolvable prior selection
- [x] 1.10 Failing test: kanban fold is not persisted — a `RailStateStore` round-trip carries per-orchestrator/Freelance/Archive fold but no kanban-fold field
- [x] 1.11 Confirm existing Pinned/Freelance/Archive fold tests still pass unmodified (regression guard, not new tests)

## 2. Rail data model: focus field + resolver helpers

**Depends on:** Stage 1

- [x] 2.1 Add `focusedKanban db.HeraKanbanStatus` field to `Rail`, defaulting to `db.HeraKanbanActive`
- [x] 2.2 Add a `focusGroupOf(orchID int64) (db.HeraKanbanStatus, bool)` helper: walks `canonicalParents()` to the root orchestrator and returns its `KanbanStatus`
- [x] 2.3 Add a helper to identify a kanban-group header row and its group (for `step()`'s crossing check)

## 3. buildRows: uniform kanban loop + focus-gated child rendering

**Depends on:** Stage 2

- [x] 3.1 Fold the headerless `active` case into the same loop as backlog/blocked/done — drop the `g.label == ""` branch
- [x] 3.2 Render each group's header unconditionally when non-empty; render its members only when `g.status == r.focusedKanban`
- [x] 3.3 Remove the now-obsolete Pinned→Active conditional-divider code path (replaced by the uniform per-group divider all four groups now share)

## 4. step(): boundary-crossing expand/collapse

**Depends on:** Stage 2

- [x] 4.1 Extend `step(dir)` to detect landing on a kanban-group header belonging to a group other than `r.focusedKanban`
- [x] 4.2 On crossing: set `r.focusedKanban` to that group, rebuild rows, then land on the new group's first (`dir>0`) or last (`dir<0`) member row
- [x] 4.3 Confirm a header belonging to the group already focused is still just skipped (not treated as a crossing)

## 5. Re-focus on programmatic selection jumps

**Depends on:** Stage 2

- [x] 5.1 `SetModel`: resolve+set `r.focusedKanban` from the pre-rebuild selection ref before calling `buildRows()`
- [x] 5.2 `SelectByTaskID`: resolve+set `r.focusedKanban` from the target role's top-level orchestrator before searching rows
- [x] 5.3 `EnsureAncestorsExpanded`: resolve+set `r.focusedKanban` alongside its existing per-orchestrator collapse expansion
- [x] 5.4 Verify the `m`/`M` kanban status-cycle mutation flows through `SetModel` (so 5.1 already covers it); add a dedicated call site only if it does not — confirmed: `heraKanbanStep` → `heraRefresh` → `doRefresh` → `rail.SetModel`, no dedicated call site needed

## 6. Cleanup + verification

**Depends on:** Stage 3, Stage 4, Stage 5

- [x] 6.1 Update existing kanban rail tests in `rail_test.go` for the new Active header text/count
- [x] 6.2 `make test-cover` on `internal/tui/hera` — confirm coverage floor maintained (91.6%)
- [x] 6.3 `make pre-pr` full gate green (build/vet/fmt-check/lint-pr/test-cover-gate all green; `make vuln` hard-fails on pre-existing stdlib CVEs unrelated to this change — CI runs govulncheck as continue-on-error/advisory, per `context/knowledge/gotchas/ci-gates.md`)
- [ ] 6.4 Manual dogfood smoke: verify `m`/`M` and arrow-key crossing behavior live in the TUI (both a pinned and non-pinned top-level coordinator) — NOT performed in this sandbox (no interactive TUI session available); the SimulationScreen smoke test (`TestSmoke_HeraRailCursorNavAndCollapse`) plus the dedicated unit tests in `rail_kanban_focus_test.go` cover the same crossing/refocus behavior at the widget level. Recommend a live dogfood pass before or shortly after merge.

## 7. Archive

**Depends on:** Stage 6

- [x] 7.1 Run `openspec archive add-kanban-focus-fold` (or the manual merge-and-move fallback) — merge deltas into `openspec/specs/hera-view/spec.md` and move the change folder to `openspec/changes/archive/<date>-add-kanban-focus-fold/`, committed on the same branch/PR before merge
