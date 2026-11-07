## Why

Links Claude Code prints in an agent pane (Notion pages, PRs, artifacts) are styled as links but can't be clicked in the TUI. Claude Code does emit real OSC-8 hyperlinks (confirmed in a live session log: `ESC]8;id=ub9bei;https://app.notion.com/p/…`), but three stacked defects lose the URL before it reaches the host terminal:

1. The pane's OSC filter (added in #648 to stop a leaked window-title character in AskUserQuestion menus) drops **every** OSC sequence, including OSC 8, so the emulator never records a link.
2. The pinned `charmbracelet/x/vt` swaps `Link.URL` and `Link.Params` when the sequence carries an `id=` param (which Claude always sends), and records no URL at all when params are empty.
3. The plugin-view pane's style mapper never maps links.

Fixes for (2) and (3) were written in August (#962, #960) and passed CI, but both PRs were closed unmerged, and their tests fed the emulator directly, bypassing the filter. That's why the problem looked fixed and wasn't.

## What Changes

- The OSC filter passes OSC-8 hyperlink sequences through to the emulator and keeps stripping every other OSC (window titles etc.). Bytes outside printable ASCII in a passed-through hyperlink payload are percent-encoded and the terminator is normalized to 7-bit ST, so a `0x9C` byte can never reach the upstream parser inside an OSC (the #648 bug stays fixed).
- Bump `charmbracelet/x/vt` to the upstream commit carrying the OSC-8 parser fix (x/vt#868). `x/ansi` 0.11.6 → 0.11.7 comes along.
- Both the agent pane and the plugin-view pane map a cell's hyperlink to the tcell style, including the link's `id=` param, so a link wrapped across rows is one link to the host terminal.
- An end-to-end regression test drives real Claude Code OSC-8 bytes through filter → emulator → style mapping.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `terminal-rendering`: the "OSC sequence filtering" requirement now preserves OSC-8 hyperlinks (sanitized); new requirement that hyperlinks reach the host terminal from the agent pane.
- `plugin-views`: new requirement that the plugin-view pane forwards hyperlinks.

## Impact

- **Code:** `internal/oscfilter` (filter state machine + `HyperlinkID` helper), `internal/tui/terminal` (`UvCellToTcellStyle`), `internal/tui/terminalpane` (`uvCellToTcellStyle`). `agent.ScreenRenderer` also uses the filter; links don't affect its rendered text.
- **Dependencies:** `github.com/charmbracelet/x/vt`, `github.com/charmbracelet/x/ansi`, plus transitive `displaywidth`, `go-colorful`, `go-runewidth` bumps.
- **Frontends:** TUI only. The web SPA feeds raw PTY bytes to xterm.js, which handles OSC 8 natively. The macOS app's SwiftTerm already opens http/https links via `requestOpenLink`. Neither uses this filter.
- **UX:** links open with the host terminal's own gesture (Cmd+click in iTerm2). Argus keeps capturing plain clicks for focus and click passthrough.
