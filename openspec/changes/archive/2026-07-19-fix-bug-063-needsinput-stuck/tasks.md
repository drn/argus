## 1. Regression test (RED)

- [x] 1.1 Add `TestNeedsInputClear` cases to `internal/agent/needsinput_test.go`
  reproducing the exact race: a task clears, a gap tick with no candidacy
  (session still running), then a later tick re-presents the SAME
  `lastUserInput` timestamp as a candidate — assert it is NOT re-stuck, across
  multiple subsequent ticks, and that a genuinely newer input still re-arms.
- [x] 1.2 Update the existing `TestNeedsInputClear` cases to the new
  `NeedsInputClear` signature (`running` param, `prevCleared`/`newCleared`
  return); adjust the "re-arms after signal disappears" case to model the
  task leaving the running set (not merely a candidacy gap), matching the
  refined contract.

## 2. Implementation (GREEN)

- [x] 2.1 Add `running []string` and `prevCleared/newCleared
  map[string]time.Time` to `agent.NeedsInputClear`; carry the cleared marker
  forward for every ID in `running` regardless of candidacy; suppress a
  candidacy whose `lastInputOf` hasn't advanced past its cleared marker; drop
  the marker on archive or when the ID leaves `running`.
- [x] 2.2 Update `internal/tui/app.go`: add `App.needsInputCleared`, thread
  `runningIDs` + the new map through `detectNeedsInputSticky`'s
  `agent.NeedsInputClear` call.
- [x] 2.3 Update `internal/api/push.go`: add
  `idleWatcherState.needsInputCleared`, thread `runningIDs` + the new map
  through `computeNeedsInput`'s `agent.NeedsInputClear` call.
- [x] 2.4 Update existing callers' tests (`internal/tui/*_test.go`,
  `internal/api/needsinput_test.go`) for the new signatures/fields.

## 3. Docs

- [x] 3.1 Add a BUG-063 entry to `context/knowledge/gotchas/events.md`
  following the existing BUG-0XX style: the stuck-forever race, the fix, and
  the accepted content-blind trade-off.

## 4. Verification

- [x] 4.1 `make pre-pr` clean.
- [x] 4.2 Archive this change (`openspec archive fix-bug-063-needsinput-stuck`
  or the manual merge-and-move fallback) in the same PR before merge.
