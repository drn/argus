## 1. Shared helper (internal/agent)

- [x] 1.1 Write failing tests for `RefreshResumeSessionID` in `internal/agent/resume_test.go`: Claude task with newer transcript → refreshed; non-Claude backend → unchanged; zero-transcript worktree → existing ID preserved (not blanked); empty session ID → not fabricated; unchanged newest ID → no-op. Seed transcripts via `claudesession.EncodeProjectDir` + `t.Setenv("HOME", t.TempDir())`.
- [x] 1.2 Add `internal/agent/resume.go` with `RefreshResumeSessionID(database *db.DB, task *model.Task)`: Claude-only gate, worktree/session-ID guards, `CaptureClaudeSessionID`, in-place mutate + read-modify-write persist, `uxlog` on refresh and skip/no-op.

## 2. Wire the resume chokepoints

- [x] 2.1 `internal/daemon/bounce.go` `reattachSupervised`: after reconcile, refresh each orphan's Claude session ID. Test in `internal/daemon/resume_recapture_test.go`: orphaned Claude worker with a newer transcript is refreshed; non-Claude orphan unchanged.
- [x] 2.2 `internal/tui/app.go` `startSession`: refresh before the resume `Start`, guarded on local `*db.DB` (extracted as `refreshResumeSessionID`). Unit test in `internal/tui/resume_recapture_test.go` (resume refreshes; fresh start is a no-op).
- [x] 2.3 `internal/api/handlers.go` `handleResumeTask` / `handleRestartTask`: refresh before `StartOrReattach`. Handler test in `internal/api/resume_recapture_test.go` asserting the refreshed ID is persisted.

## 3. Docs, gotcha, and gate

- [x] 3.1 Add the non-obvious gotcha to `context/knowledge/gotchas/daemon-rpc.md` (session-resume section): resume-time recapture mirrors the exit hook because hera workers idle/StreamLost and never reach `captureSessionIDPostExit`; Claude-only.
- [x] 3.2 Run the `make pre-pr` gates (build → vet → fmt-check → lint-pr → vuln → test-cover-gate); all green except the documented stdlib-only `vuln` advisory (CI continue-on-error). Coverage 89.3% ≥ 88 floor.
- [x] 3.3 Archive this change (openspec archive) within the PR before it is ready.
