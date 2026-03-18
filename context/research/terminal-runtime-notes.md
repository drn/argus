# Terminal Runtime Notes

Date: 2026-03-18
Status: Phase 1 groundwork

## Goal

Document why terminal passthrough requires a runtime split instead of more work inside Bubble Tea's string-rendered `View()` path.

## Current Constraint

Argus renders the live agent pane by replaying PTY bytes into `vt10x` and then converting the virtual screen back into ANSI strings. That keeps the rest of the UI simple, but it means Bubble Tea still owns the final screen surface. As long as `View() string` is the paint boundary, the live pane is reconstructed output rather than a native terminal surface.

## Library Fit

- `github.com/charmbracelet/bubbletea`
  - Mature and already integrated.
  - Good fit for list/detail shells and modal state.
  - Poor fit for true embedded terminal passthrough because the screen is still emitted as strings.
- `github.com/gdamore/tcell/v2`
  - Best candidate for direct screen ownership, cursor control, mouse, and resize events.
  - Appropriate target runtime for the live-pane migration.
- `github.com/rivo/tview`
  - Useful shell/widget layer on top of `tcell` for panels, lists, pages, and forms.
  - Reduces the amount of chrome Argus would need to rebuild by hand.
- `github.com/charmbracelet/x/vt`
  - Promising emulator/damage-tracking option for preview or replay paths.
  - Not a complete application runtime on its own.
- `github.com/hinshun/vt10x`
  - Acceptable for deterministic replay today.
  - Better treated as preview/offline infrastructure than the long-term live-pane foundation.

## Phase 1 Decisions Implemented

- Added `ARGUS_UI_RUNTIME` parsing with `bubbletea` default and `tcell` reserved as the migration target.
- Extracted panel-to-PTY sizing and Bubble Tea key forwarding into reusable terminal helpers so future runtimes do not need to rediscover those rules inside `AgentView`.

## What This Does Not Do Yet

- No `tcell` entrypoint exists yet.
- The Bubble Tea renderer remains the only active runtime.
- The live pane still uses replay plus incremental `vt10x` feeding.

## Next Useful Cut

Build a feature-gated `tcell` shell, then move the live PTY pane to a widget that owns drawing, cursor visibility, and input routing directly.
