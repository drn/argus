# Tasks — BUG-007: expand collapsed ancestor coordinators when joining a DAG node

TDD throughout (Red → Green → Refactor). Use `internal/testutil` assertions. Targeted gate: `go test ./internal/tui/hera/...` + the CI linter `new-from-rev`.

## 1. Model resolver (fold-independent)

- [x] 1.1 RED: `TestModel_OrchIDsForTask` — resolve a task's containing orchestrator(s) from the full model, including the multi-binding fan-out and empty/missing guards.
- [x] 1.2 Add `Model.OrchIDsForTask(taskID)` scanning Pinned/Active/Archived role rows.

## 2. Rail expand helper

- [x] 2.1 RED: `TestRail_EnsureAncestorsExpandedRevealsNestedLeaf` — collapse a top grandparent, prove `SelectByTaskID` fails, then expand the chain and prove the leaf row builds + selects (multi-level). `TestRail_EnsureAncestorsExpandedNoOpWhenVisible` — no spurious rebuild + zero-id guard.
- [x] 2.2 Add `Rail.EnsureAncestorsExpanded(orchID)` — walk `canonicalParents()` to root, uncollapse each ancestor (cycle-guarded), rebuild + persist when changed.

## 3. Wire into the leaf-Enter join

- [x] 3.1 RED: `TestPlanLeafEnter_ExpandsCollapsedAncestorBeforeJoin` — collapse a coordinator, Enter on its plan leaf expands the rail AND joins (selection lands, focus → agent, reattach fires).
- [x] 3.2 In `jumpToLeaf`, before `SelectByTaskID`, expand the ancestor chain of every orchestrator returned by `OrchIDsForTask(id)`. Keep the no-match log as the genuine fallback.

## 4. Verify

- [x] 4.1 `go test ./internal/tui/hera/...` and `./internal/tui/` green.
- [x] 4.2 CI linter `golangci-lint run --new-from-rev=<master> ./internal/tui/hera/ ./internal/tui/` → 0 issues; `go build ./...`; gofmt clean.
- [x] 4.3 Gotcha bullet added to `context/knowledge/gotchas/hera-view.md`.
