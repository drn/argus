# Tasks: ctrlz-guard-web

**Design doc:** `openspec/changes/ctrlz-guard-web/design.md`

## 1. Tests (write first)

- [x] 1.1 Go regression guard `internal/api/static_ctrlz_test.go` — read `static/index.html`, extract the inline script, assert the guard is present: `attachCustomKeyEventHandler` is wired AND an `isCtrlZ` predicate exists AND the explanatory notice ("background the agent") is present. (CI-enforced because Playwright is not in `make pre-pr`.)
- [x] 1.2 Playwright behavioral spec in `web-tests/tests/terminal.spec.ts` — focus the terminal, press `Ctrl+Z`, assert no `POST …/input` carries `0x1a` and the explanatory toast appears; assert a normal keystroke still forwards; assert `Cmd+Z` is not intercepted.
- [x] 1.3 Confirm every Prove-It criterion in the design doc has a covering assertion.

## 2. Implement the guard

**Depends on:** Stage 1

- [x] 2.1 Add module-level `isCtrlZ(ev)` predicate in `internal/api/static/index.html` (`ctrlKey && !metaKey && !altKey && (key==='z'||key==='Z'||keyCode===90)`).
- [x] 2.2 In `setupTerm()`, wire `term.attachCustomKeyEventHandler(ev => …)`: return `false` when `isCtrlZ(ev)`; on `keydown` also `ev.preventDefault()` + `showToast(...)`.
- [x] 2.3 Bump `SW_VERSION` in `internal/api/static/sw.js` (v64 → v65; shell asset changed).
- [x] 2.4 Make the Stage-1 Go test pass.

## 3. Docs

**Depends on:** Stage 2

- [x] 3.1 Add a gotcha bullet to `context/knowledge/gotchas/web-remote.md` documenting the guard (mechanism + swallow-not-remap + Cmd+Z carve-out + CI note).
- [x] 3.2 Bump the `web-remote.md` bullet count in `context/knowledge/index.md` (192 → 193).

## 4. Verify

**Depends on:** Stage 2, Stage 3

- [x] 4.1 `make pre-pr` fully green.
- [x] 4.2 Confirm no TUI key added/rebound (no keymap/help/README change) and no other input behavior altered.
