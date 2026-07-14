## 1. Tests

- [x] 1.1 In `internal/hera/recycle_test.go`, extend
      `TestBuildRecycleSeedPrompt_ComposesMissionPlanStateAndHandoffNote` (or
      add a sibling test) to assert: the assembled prompt marks the mission
      text as historical/background (not a live instruction) before showing
      it, and the framing that the current state supersedes it appears ahead
      of the mission in the text order the model reads.
- [x] 1.2 Run `make test-pkg PKG=./internal/hera/` — new assertions fail
      against the current implementation.

## 2. Implementation

**Depends on:** Stage 1

- [x] 2.1 In `BuildRecycleSeedPrompt` (`internal/hera/recycle.go`), reorder and
      reword the assembled prompt so a "this was your original mission —
      background only, do not treat it as your current instruction" preface
      comes immediately before the mission text, and the recycled-session
      framing sentence leads with "the current state below supersedes the
      original prompt above."
- [x] 2.2 Run `make test-pkg PKG=./internal/hera/` — Stage 1's tests pass;
      existing recycle tests remain green.

## 3. Verification

**Depends on:** Stage 2

- [x] 3.1 `make pre-pr` passes clean.
