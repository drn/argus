## Why

The native Hera view dropped the focus-aware hotkey hints the old plugin showed in the bottom bar. The bar currently shows only static rail-nav hints regardless of which region holds keyboard focus — a pane-focused operator sees irrelevant rail keys, and mutation keys like `w spawn` / `R retire` / `/ filter` are never surfaced.

## What Changes

- The bottom status bar now renders DIFFERENT hint sets depending on which Hera region holds keyboard focus:
  - **Rail focused** – rail nav + mutation hints (nav, fold, filter, spawn, coord, status, retire, prune, delete, focus-pane)
  - **Coord/Agent pane focused** – pane hints (rail, pane, fullscreen, rail-nav, scroll)
- Hints update on the same frame as a focus change (keyboard or mouse).
- Other tabs are unaffected; prior statusbar behavior is preserved when Hera is not active.
- No new keybindings are added; displayed keys match `modal/help.go` "Hera View (rail)" exactly.

## Capabilities

### New Capabilities

None – this is a display enhancement to an existing surface.

### Modified Capabilities

- `hera-view`: the bottom bar rendering behaviour changes when the Hera tab is active.
