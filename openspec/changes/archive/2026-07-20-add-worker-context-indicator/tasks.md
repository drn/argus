**Design doc:** `openspec/changes/add-worker-context-indicator/design.md`

Note on structure: mostly a linear chain (data plumbing before rendering), but Stage 1 (coord-hook
gate) has already landed ahead of the rest of this tasks.md, since it was the first piece
implemented and verified independently before the openspec artifacts were written up in full.

## 1. Coord-hook gate widened (already landed)

- [x] 1.1 Widen `runCoordHook`'s gate from `kind != coordinator` to `kind == ""`; move
      budget/nudge/hard-stop/recycle logic behind a coordinator-only check placed after the
      now-unconditional stamp
- [x] 1.2 Update/replace the old "worker session is a no-op" test with worker + freelance
      stamp-but-no-enforcement tests, plus a new unbound-task no-op test
- [x] 1.3 Update doc comments (top-of-file + `runCoordHook`) and the `HeraMetaKeyContextSize`
      comment in `internal/db/hera.go`

## 2. RoleView data plumbing

**Depends on:** Stage 1

- [x] 2.1 Write failing tests for `RoleView.ContextSize` population in `buildRoleView` (from
      `heraMeta[taskID][db.HeraMetaKeyContextSize]`, pure, no I/O) — mirrors the `ReadyToClose`
      test shape
- [x] 2.2 Add `ContextSize int` to `RoleView`; populate it in `buildRoleView`
- [x] 2.3 Write failing tests for `resolveHeraTier` computing `ContextPercent` from `ContextSize`
      and `cfg.Hera.CoordinatorContextBudget` (including a zero-budget / missing-config guard so a
      project with no configured budget doesn't divide by zero or show a bogus 100%)
- [x] 2.4 Add `ContextPercent int` to `RoleView`; compute it in `resolveHeraTier`
      (`internal/tui/hera_tiering.go`)
- [x] 2.5 Make the Stage 2.1/2.3 tests green

## 3. Theme tokens

**Depends on:** Stage 2

- [x] 3.1 Add `ColorContextWarm`/`ColorContextHot`/`ColorContextCritical` and
      `StyleContextWarm`/`StyleContextHot`/`StyleContextCritical` to `internal/tui/theme/theme.go`

## 4. Rail rendering

**Depends on:** Stage 3

- [x] 4.1 Write failing tests pinning: bare coordinator count format, no-indicator-under-40%,
      pale-yellow/hot-orange/red-bang tiers, coordinator rows never show it, dead/archived rows
      never show it, PR-tag + indicator composition — from the `hera-view` delta scenarios
- [x] 4.2 `drawOrchRow`: change the count format from `" (%d)"` to `" %d"`
- [x] 4.3 Add a `contextIndicator(role *RoleView) (reserve bool, glyph rune, style tcell.Style)`
      helper in `rail.go`
- [x] 4.4 Wire the reserved slot into `drawRoleRow`, composing with the existing PR-tag reservation
- [x] 4.5 Make the Stage 4.1 tests green

## 5. Docs and archive

**Depends on:** Stage 4

- [x] 5.1 Add a gotcha bullet to `context/knowledge/gotchas/hera-view.md` (or `orchestration.md` for
      the coord-hook half) covering the widened stamp gate + the rail indicator's design
      constraints (always-reserved slot, coordinator exclusion, local-mode-only percent)
- [x] 5.2 Run `make pre-pr` clean
- [x] 5.3 `openspec archive add-worker-context-indicator` (or the manual merge-and-move fallback),
      on the same branch, before merge
