# Tasks: add-pr-review-indicator

**Design doc:** openspec/changes/add-pr-review-indicator/design.md

## 1. Tests

- [x] 1.1 Write failing tests for `PRState` enum (`internal/model/prstate_test.go`): Parse/String/MarshalText round-trip for every value, unknown-string error.
- [x] 1.2 Write failing tests for detection mapping (`internal/gitutil/pr_test.go`) by injecting a fake `prFetcher`/gh-output: table-cover awaiting-review, changes-requested, approved, draft, none (non-zero exit), merged, closed, gh-absent→unknown, malformed JSON.
- [x] 1.3 Write failing tests for keep-stale-on-error: timeout/network error returns error and the poller does not clobber an existing `task_meta` value.
- [x] 1.4 Write failing tests for `db.ListMetaByNamespace` (batch read shape) and that `DeleteMetaForTask` clears the `pr` namespace.
- [x] 1.5 Write failing daemon poller tests: skips archived/branchless tasks, writes `state`+`url` via `SetMetaBatch`, stops on `d.done`, respects concurrency cap.
- [x] 1.6 Write failing TUI render tests (`drawTaskRow` against a mock screen): actionable states draw glyph+color in the reserved cell, non-actionable draw blank, status glyph unchanged, name column does not shift.
- [ ] 1.7 Write failing API test: `handleListTasks` returns `pr_state` from seeded `task_meta` and does not call `prFetcher`.
- [x] 1.8 Write failing uxlog test: gh-absent logs exactly once across repeated polls.
- [x] 1.9 Confirm every `it should X` criterion in design.md has a failing test (Prove-It Pattern).

## 2. Model + detection core

**Depends on:** Stage 1

- [x] 2.1 Add `internal/model/prstate.go`: `PRState` enum mirroring `status.go` (Parse/String/MarshalText, glyph-agnostic).
- [x] 2.2 Add `internal/gitutil/pr.go`: `var prFetcher = func(ctx, worktreeDir, branch) (model.PRState, string, error)` shelling `gh pr view … --json …` with 5s timeout; JSON parse + state mapping; gh-absent→`PRUnknown` with once-only uxlog.

## 3. Persistence

**Depends on:** Stage 1

- [x] 3.1 Add `db.ListMetaByNamespace(namespace) (map[taskID]map[key]value, error)` next to `ListMeta` in `internal/db/task_meta.go` (single indexed query).

## 4. Daemon poller

**Depends on:** Stages 2, 3

- [x] 4.1 Add a 60s poller goroutine in `internal/daemon/daemon.go` beside the MCP idle sweep (~404), gated by `d.done`.
- [x] 4.2 Bounded worker pool (≤4 concurrent gh procs) over non-archived tasks with a branch; write `state`+`url` via `SetMetaBatch(taskID,"pr",…)`; keep-stale on error.

## 5. TUI rendering

**Depends on:** Stages 2, 3

- [x] 5.1 Add `IconPRAwaiting/Changes/Approved` + `ColorPR*`/styles + `PRGlyph(PRState) (rune, tcell.Style)` in `internal/tui/theme/theme.go` (render-test codepoints for distinctness).
- [x] 5.2 Add `prStates map[string]model.PRState` field + `SetPRStates` setter in `internal/tui/taskview/tasklist.go` (mirror `needsInput`/`SetNeedsInput`).
- [x] 5.3 Insert the reserved PR cell after the status glyph in `drawTaskRow` (after line 1149/1150); confirm `maxNameW` adapts.
- [x] 5.4 Wire `SetPRStates` from `ListMetaByNamespace("pr")` into the existing tick in `internal/tui/app.go` (~1547), inside the current `QueueUpdateDraw` flow.

## 6. Web PWA parity

**Depends on:** Stage 3

- [ ] 6.1 Add `PRState string \`json:"pr_state,omitempty"\`` to `taskJSON`; populate from a batch `task_meta` `pr` read in `handleListTasks`/`handleGetTask` (`internal/api/handlers.go`); handler must not shell out to gh.
- [ ] 6.2 Render PR badges per `pr_state` in `renderTaskList` + `.badge-pr-*` CSS in `internal/api/static/index.html`; keep `effectiveStatus` untouched.
- [ ] 6.3 Bump `SW_VERSION` in `internal/api/static/sw.js`.
- [ ] 6.4 Add a Playwright spec in `web-tests/` asserting the badge renders for a seeded `pr_state`.

## 7. Docs + gate

**Depends on:** Stages 4, 5, 6

- [ ] 7.1 Add gotchas: `tasklist-ui.md` (reserved-cell anti-jitter, orthogonality vs `in_review`) and `web-remote.md` (pr_state DTO, daemon-only-fetch / handlers-never-shell-out, 60s keep-stale contract); update `context/knowledge/index.md` bullet counts.
- [ ] 7.2 Run `make pre-pr` and get a clean pass (build → vet → fmt-check → lint-pr → vuln → test-cover-gate).
