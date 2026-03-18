# Terminal Runtime Research Notes

**Date:** 2026-03-18
**Context:** Evaluating libraries for native terminal passthrough in the agent view

## Problem

Argus re-renders agent PTY output through a `vt10x` replay pipeline inside Bubble Tea's `View() string` model. This works for normalized display but is not true terminal passthrough — cursor behavior, prompt styling, alt-screen, and wide-character handling are all reconstructed rather than native.

## Libraries Evaluated

### Bubble Tea (current)
- **Fit:** Mature, well-maintained, excellent for form/list/modal UIs
- **Limitation:** `View() string` is fundamentally incompatible with embedding a live terminal surface. Every render cycle produces a string; there is no way to own a screen region and write raw bytes to it. The entire agent view is a string reconstruction.
- **Verdict:** Keep for non-terminal views (task list, settings, forms). Not suitable for live terminal passthrough.

### tcell/v2 (`github.com/gdamore/tcell/v2`)
- **Fit:** Low-level screen/event library with direct cell-level control, cursor management, mouse handling, and event polling. The right primitive for owning a screen region.
- **Maturity:** Very mature, widely used (tview, lazygit, etc.)
- **Verdict:** Target screen layer for the new runtime. Provides the screen ownership that Bubble Tea lacks.

### tview (`github.com/rivo/tview`)
- **Fit:** Higher-level widget library built on tcell. Layouts (Flex, Grid), lists, tables, forms, modals, pages. Closest conservative replacement for the Bubble Tea shell primitives Argus uses.
- **Maturity:** Mature, actively maintained
- **Verdict:** Use for application chrome (task list, settings, forms, modals, status bar). The agent terminal pane will be a custom `tview.Primitive` that writes directly to the tcell screen.

### x/vt (`github.com/charmbracelet/x/vt`)
- **Fit:** Modern terminal emulator from Charm. `Write`, `Draw`, cursor tracking, damage tracking, alt-screen support.
- **Limitation:** Emulation library, not an application runtime. Useful for replay/preview but doesn't solve screen ownership.
- **Verdict:** Possible future replacement for `vt10x` in the replay/preview path. Not the live-pane solution.

### vt10x (`github.com/hinshun/vt10x`)
- **Fit:** Currently used for replay. Older, minimal maintenance.
- **Limitation:** Adequate for replay but missing modern features (damage tracking, proper alt-screen).
- **Verdict:** Keep for now. Consider replacing with `x/vt` in Phase 4 when replay is the only remaining use case.

### creack/pty (`github.com/creack/pty`)
- **Fit:** PTY allocation and resize. Already used, works well.
- **Verdict:** Keep. Independent of the renderer.

## Why Not Bubble Tea-Only Passthrough?

Three approaches were considered and rejected:

1. **Mixed ownership (Bubble Tea + raw terminal writes):** Bubble Tea assumes exclusive screen ownership. Writing raw bytes to a screen region while Bubble Tea renders the rest causes repaint conflicts, cursor jumps, and broken focus. Alternate-screen transitions from the agent would fight with Bubble Tea's own alt-screen. Dead end.

2. **tea.ExecCommand for full passthrough:** Works for full-screen takeover (attach mode) but not for a panel within a three-column layout. ExecCommand yields the entire terminal to the child process; there's no way to render side panels simultaneously.

3. **Richer View() encoding:** Even if View() returned structured data instead of strings, the fundamental issue remains: Bubble Tea redraws the entire screen on every update. A live terminal pane needs to update only its region. The re-render model is O(screen) per update versus O(damage) for a native surface.

## Chosen Approach

Runtime split with staged migration:

1. **Phase 1 (done):** Extract UI-agnostic state and interfaces into `internal/app/agentview/`. Define `TerminalAdapter` and `SessionLookup` interfaces. Add `ARGUS_UI_RUNTIME` env var.
2. **Phase 2:** Build a tcell/tview app shell with three-panel layout, focus ring, and event loop.
3. **Phase 3:** Implement a custom `tview.Primitive` for the live agent pane that reads PTY output and writes directly to tcell cells. Remove `activeInputBG`, `findInputRow`, and cursor synthesis from the live path.
4. **Phase 4:** Decide vt10x vs x/vt for replay. Scope vtrender.go to preview/log only.
5. **Phase 5:** Port remaining views (git status, file explorer, diff, modals) to tview. Cut over default runtime.

## Key Design Decisions

- The agent pane in the new runtime will NOT use vt10x for live rendering. It will read raw PTY bytes and write them to tcell cells directly, letting the terminal emulator (the user's actual terminal) handle escape sequences natively.
- Replay/preview (finished sessions, task list preview) will continue to use a virtual terminal (vt10x or x/vt) since there is no live PTY to read from.
- The `TerminalAdapter` interface abstracts what the rendering pane needs from a session, making both runtimes possible without forking the session layer.
- `ARGUS_UI_RUNTIME=bubbletea|tcell` keeps Bubble Tea as the default during migration.
