# Tasks: add-pr-poll-terminal-skip

**Design doc:** openspec/changes/add-pr-poll-terminal-skip/design.md

## 1. Tests (Red)

- [x] 1.1 Add `internal/model/prstate_test.go` table for `IsTerminal()`: `merged-closed`→true; `none`/`draft`/`awaiting-review`/`changes-requested`/`approved`/`unknown`→false.
- [x] 1.2 Extend `internal/daemon/pr_poll_test.go`: a non-archived branch task with cached `pr` state `merged-closed` is NOT fetched and its meta is left untouched.
- [x] 1.3 Extend `pr_poll_test.go`: tasks with cached non-terminal state (e.g. `approved`), with no cached state, and with `none` are still fetched.
- [x] 1.4 Extend `pr_poll_test.go`: simulate "restart" by seeding `task_meta` `pr.state=merged-closed` directly (no prior poll), then run the poll and assert the task is excluded — proves the skip is cache-backed, not in-memory.

## 2. Model helper (Green)

**Depends on:** Stage 1

- [x] 2.1 Add `func (s PRState) IsTerminal() bool { return s == PRMergedClosed }` to `internal/model/prstate.go` with a doc comment.

## 3. Daemon eligibility filter (Green)

**Depends on:** Stage 1, Stage 2

- [x] 3.1 In `pollPRStatesOnce`, read `d.db.ListMetaByNamespace("pr")` once before the eligibility loop; on error, log and fall back to polling all eligible (fail-open).
- [x] 3.2 In the eligibility loop, after the archived/branchless skip, parse `prMeta[t.ID]["state"]` via `model.ParsePRState`; if it parses and `IsTerminal()`, skip the task (continue), increment a `skipped` counter, and `uxlog.Log("[pr] poll: skip terminal ...")`.
- [x] 3.3 Include `skipped` in the existing summary uxlog line.

## 4. Verify

**Depends on:** Stage 3

- [x] 4.1 `make pre-pr` clean (build → vet → fmt-check → lint-pr → vuln → test-cover-gate).
- [x] 4.2 `openspec validate --all --strict` clean.
- [x] 4.3 Document the cache-backed terminal-skip invariant in `context/knowledge/gotchas/daemon-rpc.md` and bump the index bullet count.
