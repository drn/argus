## 1. Root-cause verification

- [x] 1.1 Live-repro three throwaway hera workers to a real Bash permission prompt and confirm the flat 16 KB tail window misses the signal deterministically (not intermittently) by running the production detectors directly against the on-disk log
- [x] 1.2 Capture the real repeating byte pattern (blinking cursor/status-glyph redraw) and confirm it, not a torn read, is what floods the window

## 2. Core fix: expand-on-degenerate-tail read

- [x] 2.1 Add `degenerateSuffixStart`/`TrimToSubstantiveTail`/`SubstantiveTail` + `NeedsInputMaxExpandBytes` to `internal/agent/needsinput.go`
- [x] 2.2 Unit tests: synthetic blink-cycle fixture (boundary detection, short-repeat-below-minimum non-trigger, ordinary content never trimmed, entirely-degenerate buffer), `SubstantiveTail` expansion/no-expansion/give-up-at-cap cases
- [x] 2.3 Verify against the REAL captured on-disk log from a live repro session (not just synthetic fixtures)

## 3. Wire the fix into both consumers

- [x] 3.1 `internal/tui/app.go`: `readSessionLogTailBytes` wraps the new `SubstantiveTail` helper over a renamed `readSessionLogRawTail`
- [x] 3.2 `internal/api/push.go`: `tailOf` wraps `SubstantiveTail` over `sess.RecentOutputTail`, bounded by the ring's own capacity

## 4. Make the sticky carry-forward pass genuinely sticky

- [x] 4.1 `internal/tui/app.go`'s `detectNeedsInputSticky`: drop the re-match requirement, rely solely on `NeedsInputClear`
- [x] 4.2 `internal/api/push.go`'s `computeNeedsInput`: same change
- [x] 4.3 Update the affected existing tests (`TestComputeNeedsInput`'s "sticky clears when marker scrolled out of tail" case, `TestDetectNeedsInputTick`'s tick-2 expectation, `TestRefreshTasks_NeedsInputSticky`'s tick-3 expectation) to the new intended semantics

## 5. Documentation and verification

- [x] 5.1 Update `context/knowledge/gotchas/events.md` with the real BUG-061 mechanism, correcting/extending the BUG-029/060 write-ups
- [x] 5.2 Update `context/knowledge/index.md`'s events.md summary row
- [x] 5.3 OpenSpec change folder (this one) with delta spec against `idle-detection`
- [x] 5.4 Full test suite green (`go test -race -count=1 ./...`)
- [ ] 5.5 `make pre-pr` clean
- [ ] 5.6 Archive this change (merge deltas into `openspec/specs/idle-detection/spec.md`, move folder to `openspec/changes/archive/`) before opening the PR
- [ ] 5.7 Clean up the throwaway `bug061-live-repro` orchestrator's repro-a/b/c tasks
- [ ] 5.8 Open PR via `iris_gh_pr_create`
