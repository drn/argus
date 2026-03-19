# Terminal Runtime Research Notes

**Date:** 2026-03-18
**Context:** Evaluating libraries for native terminal passthrough in the agent view
**Status:** Migration complete — tcell/tview is the sole runtime, x/vt is the emulator

## Problem (Resolved)

Argus originally re-rendered agent PTY output through a vt10x replay pipeline inside Bubble Tea's `View() string` model. This was not true terminal passthrough — cursor behavior, prompt styling, alt-screen, and wide-character handling were all reconstructed rather than native. The migration to tcell/tview + x/vt resolved this.

## Libraries In Use

### tcell/v2 (`github.com/gdamore/tcell/v2`)
- Low-level screen/event library with direct cell-level control, cursor management, mouse handling, and event polling.
- Provides screen ownership that enables direct cell painting from the emulator.

### tview (`github.com/rivo/tview`)
- Higher-level widget library built on tcell. Layouts (Flex, Grid), lists, tables, forms, modals, pages.
- Used for application chrome (task list, settings, forms, modals, status bar). The agent terminal pane is a custom `tview.Box` that writes directly to the tcell screen.

### x/vt (`github.com/charmbracelet/x/vt`)
- Modern terminal emulator from Charm. `Write`, `Draw`, cursor tracking, damage tracking via `Touched()`, native scrollback buffer, alt-screen support.
- Used for emulation in both live rendering and replay/preview paths.

### creack/pty (`github.com/creack/pty`)
- PTY allocation and resize. Works well, independent of the renderer.

## Architecture

The rendering pipeline is: PTY bytes → x/vt emulator → `CellAt()` cells → `tcell.SetContent()` directly. No ANSI string intermediary, no lossy round-trip. Damage tracking via `Touched()` enables incremental repainting of only changed lines.

Scrollback uses x/vt's native `Scrollback()` buffer — no replaying the entire ring buffer through a throwaway emulator.

## Key Design Decisions

- The agent pane reads PTY bytes, feeds them to x/vt, and paints the emulator's cells directly to tcell. No prompt-row highlighting or cursor synthesis needed.
- Replay/preview (finished sessions, task list preview) also use x/vt with its native scrollback.
- The `TerminalAdapter` interface in `internal/app/agentview/` abstracts what the rendering pane needs from a session.
