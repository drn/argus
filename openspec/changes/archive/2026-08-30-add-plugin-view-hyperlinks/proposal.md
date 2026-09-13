## Why

**The plugin-view terminal pane silently drops OSC 8 hyperlinks that the
default agent terminal pane already makes clickable.** The default pane's
cell-style mapper (`internal/tui/terminal/UvCellToTcellStyle`) maps the x/vt
emulator's parsed `cell.Link.URL` onto tcell's per-cell `Style.Url(...)`,
which tcell re-emits as a real hyperlink escape sequence to the user's outer
terminal as a normal part of its draw cycle. A plugin view runs the same
class of PTY output through its own, separate mapper
(`internal/tui/terminalpane.uvCellToTcellStyle`), which mirrors the default
mapper but deliberately omits the hyperlink path — the existing code comment
says so directly. Any plugin that prints a clickable link (a PR reference, a
"view artifact" URL) loses that affordance purely because it renders inside a
plugin view instead of the default pane, with no user-visible reason why.

## What Changes

- **`terminalpane.uvCellToTcellStyle` gains the same `cell.Link.URL` →
  `style.Url(...)` mapping** the default pane's `UvCellToTcellStyle` already
  has, so a plugin view's OSC 8 hyperlinks reach tcell (and therefore the
  user's real terminal) the same way the default pane's do.
- No caching or desaturation layer exists in this package to account for —
  `terminalpane.paint` calls `screen.SetContent` directly from the mapped
  style every frame, so there is nothing else to update for the URL to
  survive redraws.
- Mouse-click passthrough (making a rendered link actually clickable via a
  plugin-forwarded click event) is explicitly **out of scope** — this change
  only closes the rendering-parity gap so the outer terminal emits the OSC 8
  sequence; click handling is a separate, larger UX question.

## Capabilities

### Modified Capabilities

- `plugin-views`: plugin-view terminal panes now render OSC 8 hyperlinks
  (`cell.Link.URL`) as real terminal hyperlinks, at parity with the default
  agent terminal pane — previously the plugin-view cell-style mapper
  discarded link data entirely.

## Impact

- **Modified code:** `internal/tui/terminalpane/terminalpane.go` —
  `uvCellToTcellStyle`.
- **No new key, no new dependency, no schema change, no daemon RPC surface
  change, no mouse-click behavior change.**
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make /
  Go-build wiring is added or changed. The quality gate stays `make pre-pr`.
