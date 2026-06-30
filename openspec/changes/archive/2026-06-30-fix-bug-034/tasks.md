# Tasks

## 1. Shared clear helper (internal/agent)

- [x] 1.1 Add `agent.NeedsInputClear(candidates, prevBaseline, lastInputOf, archivedOf)` — pure, deterministic, no wall-clock dependency (compares last-input timestamps against a captured baseline).
- [x] 1.2 Unit tests: entry baseline capture, persist with no input, clear on input-after-baseline (incl. stale-tail still matching), no cross-session clear, archive clear + baseline drop, re-arm after signal disappears.

## 2. Daemon detector (internal/api/push.go)

- [x] 2.1 Add `needsInputSince map[string]time.Time` to `idleWatcherState`.
- [x] 2.2 Thread `prevSince`, `lastInputOf`, `archivedOf` into `computeNeedsInput`; return `newSince`; call the shared helper after the candidate passes.
- [x] 2.3 In `detectNeedsInputTick`, build `lastInputOf` from `runner.Get(id).LastInput()` and `archivedOf` from the DB; carry `state.needsInputSince` across ticks.
- [x] 2.4 Update existing `computeNeedsInput` tests for the new signature; add clear-on-input and archive cases.

## 3. TUI detector (internal/tui/app.go)

- [x] 3.1 Add `needsInputSince map[string]time.Time` to `App`.
- [x] 3.2 In `detectNeedsInputSticky`, after the candidate passes, call the shared helper with `lastInputOf` (nil-guarded `a.runner.Get`) and `archivedOf` (`a.tasks` archived set); store `newSince`.
- [x] 3.3 Tests: clear-on-input via a fake runner / injected last-input, archive clear, persistence with no input.

## 4. Spec + docs

- [x] 4.1 Delta added under `specs/idle-detection/spec.md`; `openspec validate fix-bug-034 --strict` passes.
- [x] 4.2 Document the gotcha in `context/knowledge/gotchas/events.md`; bump `index.md` count.

## 5. Verify

- [x] 5.1 `make test-pkg` green for `./internal/agent/`, `./internal/api/`, `./internal/tui/`.
- [x] 5.2 `make pre-pr` clean (fmt via local goimports, lint via pinned golangci-lint).
