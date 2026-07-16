# Tasks

- [x] 1.1 Failing tests in `internal/doctor`: a pure `StopHookRegistered(cmds []string) bool` (or equivalent) reports true when a command references `argus coord-hook`, false for an empty/unrelated list — table tests covering a bare `argus coord-hook`, a full-path invocation (`/Users/x/.local/bin/argus coord-hook`), an unrelated Stop hook only, and no hooks at all.
- [x] 1.2 Implement the classifier to make 1.1 green.
- [x] 2.1 I/O glue in `cmd/argus/doctor.go`: read `~/.claude/settings.json`, parse `hooks.Stop[].hooks[].command`, classify via 1.2. Distinguish "file missing/unreadable" (unknown) from "readable but no match" (not registered).
- [x] 2.2 Wire the rendered section into `runDoctor()`, printed after the existing binary-coherence table/verdict. No change to the exit-code contract.
- [x] 3.1 Update the README "Diagnosing binary skew (`argus doctor`)" section (and the "Context-budget Stop hook" section's forward-reference) to describe the new check.
- [x] 3.2 Add a gotcha note to `context/knowledge/gotchas/daemon-rpc.md` (alongside the existing go-install-skew/doctor bullet).
- [x] 4.1 Run the full `make pre-pr` gate and fix any gaps.
- [x] 5.1 Archive: fold the delta into `openspec/specs/binary-coherence/spec.md`, move the change folder to `openspec/changes/archive/<date>-detect-missing-coord-hook/`, in the same branch before merge.
- [x] 5.2 Re-run `make pre-pr` after archiving to confirm no drift.
