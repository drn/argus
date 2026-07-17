## Why

**A merged (or closed) pull request still shows the orange "PR" tag in the
native Hera rail and "ready PR" in the coordinator roster (Details pane).**
Ground truth: PR #856 merged to master, but its role kept showing the `PR`
indicator in both surfaces indefinitely.

Root cause: both render sites key the indicator on PR **url presence**, not PR
**state**:

- `internal/tui/hera/rail.go` `rolePR()` returns true whenever
  `prMeta[taskID]["url"] != ""`.
- `internal/tui/hera/details.go` `roleMark()` appends `"PR"` under the same
  `url != ""` check.

The daemon's PR poller is correct — it writes both `state` and `url` every
cycle, and a merged/closed PR transitions `state` to `merged-closed` while
**retaining** `url` (the poller doesn't clear it). So the url-presence check
never turns false again once a PR ever existed. The TUI task list
(`theme.PRGlyph`) and the web PWA badge renderer already gate on state and
filter out `merged-closed` (and draft/unknown) — only the native Hera rail and
details surfaces have this bug.

## What Changes

- **Gate the Hera rail and details "PR" indicator on PR STATE, not url
  presence.** Parse the cached `state` string via `model.ParsePRState` and show
  `PR` only when the state is one of the three actionable review states
  (`awaiting-review`, `changes-requested`, `approved`) — the same set
  `theme.PRGlyph` already uses for the task list. Merged/closed, draft,
  unknown, and empty states show no mark, even though `url` is still cached.
- **Introduce a single shared predicate, `model.PRState.IsActionable()`,** and
  key `theme.PRGlyph`, `rail.go`'s `rolePR`, and `details.go`'s `roleMark` all
  off it, so the three surfaces cannot drift apart again on what counts as an
  "active" PR.
- **No poller change.** `internal/daemon`'s PR poller already writes the
  correct state; this is purely a render-predicate fix in the TUI layer.

## Capabilities

### Modified Capabilities

- `hera-view`: the roster "PR indicator" (Details pane) and the rail "PR
  indicator on rail role rows" requirements now gate on cached PR **state**
  (actionable review states only) instead of url presence, so a merged/closed
  PR's indicator clears once the poller marks it terminal.

## Impact

- **Modified code:**
  - `internal/model/prstate.go` — new `PRState.IsActionable()` method (single
    source of truth for "does this PR state deserve a live badge").
  - `internal/tui/theme/theme.go` — `PRGlyph` now delegates its actionable
    check to `IsActionable()` (behavior unchanged; it already filtered by the
    same three states).
  - `internal/tui/hera/rail.go` — `rolePR()` parses `state` and checks
    `IsActionable()` instead of checking `url != ""`.
  - `internal/tui/hera/details.go` — `roleMark()` same fix.
- **No new key, no new dependency, no schema change, no daemon RPC.** Pure
  read-only view-layer predicate fix.
- **Frontend parity:** the TUI task list (`theme.PRGlyph`) and the web SPA
  (badge renderer already excludes `merged-closed`) are already correct — this
  fix is Hera-view-only. The macOS Hera tab is a read-only `/api/hera` roster
  with no local PR-indicator logic of its own to fix. No parity follow-up
  needed.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.
