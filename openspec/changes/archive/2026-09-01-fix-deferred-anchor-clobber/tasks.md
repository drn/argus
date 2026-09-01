# Tasks

## 1. Reproduce

- [x] 1.1 Recover the live `ux.log` trace showing a deferred bind reading `committed=142
      panel=90` followed by repeated `skipping kick … unchanged since last attach (90)`.
- [x] 1.2 Replay the reporting task's real session log through the live full-replay path at
      its authored width and at the Hera split-pane width; confirm the reported artifact
      (mid-word splits, single characters piled in the last column) appears only at the
      narrow width.
- [x] 1.3 Add a red test in `internal/tui/app_test.go` reproducing the trace verbatim: a
      deferred wide bind, then a deferred narrow bind at the same width, then an idle
      rebind at that width that must still kick.

## 2. Fix

- [x] 2.1 Add `App.recordUnreconciledCommittedCols` and route the three "kick owed but not
      taken" branches through it.
- [x] 2.2 Refuse the overwrite when the existing anchor still exceeds the rerender margin
      against the evaluated panel width.

## 3. Verify and document

- [x] 3.1 Red test green; PR #937's own regression test (`…LaterNarrowRebindStillKicks…`)
      still green.
- [x] 3.2 Document the invariant in `context/knowledge/gotchas/pty-terminal.md` and
      `context/knowledge/gotchas/hera-view.md`; update `context/knowledge/index.md`.
- [x] 3.3 `make pre-pr` green.
- [x] 3.4 Archive this change into `openspec/specs/idle-detection/spec.md` before merge.
