## Why

OSC-8 terminal hyperlinks (e.g. clickable `#960` PR references in Claude Code's
status line) are inert everywhere in Argus, including panes that already have
the hyperlink-style mapping wired up (`UvCellToTcellStyle` → `tcell.Style.Url`).

Root cause: Argus's vendored `github.com/charmbracelet/x/vt` (pinned to a
pseudo-version at commit `f2fb44ab3145`) has a parser bug in
`vt/osc.go`'s `handleHyperlink` — for an OSC-8 sequence that includes an `id=`
param (`ESC ] 8 ; id=1vaggxp ; https://... ST`, the exact form Claude Code
emits), the two OSC data segments are assigned to the wrong struct fields:
`cell.Link.URL` receives the `id=...` params segment and `cell.Link.Params`
receives the real URL. Every downstream consumer (including Argus's terminal
panes) faithfully renders a syntactically valid but useless hyperlink whose
href is the literal string `"id=1vaggxp"` — text displays styled
(orange/underlined) but Option/Cmd-click does nothing.

This was already fixed upstream: `github.com/charmbracelet/x/vt` commit
`3c30eef5e73e8ad8e1765913b0ca2aeb43d32e4f` ("fix(vt): store osc 8 hyperlink
params and uri in correct fields (#868)", merged 2026-08-28).

## What Changes

- **Bump `github.com/charmbracelet/x/vt`** to the exact upstream fix commit
  (pinned rather than `@latest`, to avoid pulling in unrelated changes from
  the two days between the fix and today). `github.com/charmbracelet/x/ansi`
  moves 0.11.6 → 0.11.7 as a coordinated transitive requirement of the new
  `x/vt` version (confirmed via `go mod tidy`; the fix itself lives entirely
  in `x/vt`, not `x/ansi`).
- **No Argus code changes** — the bug lived entirely in the vendored
  dependency. `UvCellToTcellStyle`'s existing `cell.Link.URL` → `Style.Url(...)`
  mapping was already correct; it was just being fed corrupted data.
- **New regression test** (`internal/tui/terminal`) drives the real x/vt
  emulator with the actual OSC-8 wire-format bytes Claude Code emits
  (`id=` param + URL), rather than a hand-built `uv.Cell` — the gap that let
  this ship with zero test coverage in the first place.

## Capabilities

### Added Capabilities

- `terminal-rendering`: OSC-8 hyperlink cells resolve their click target to
  the actual URI, independent of any `id=`/params segment in the same
  sequence.

## Impact

- **Modified code:** `go.mod`, `go.sum` (dependency bump only).
- **New code:** `internal/tui/terminal/terminalpane_test.go` (regression test).
- **No behavior change to Argus's own code** — this restores previously
  broken behavior (hyperlinks were supposed to be clickable; the wiring for
  that was already shipped, only the upstream data was wrong).
- Out of scope: `internal/tui/terminalpane/terminalpane.go` (the plugin-view
  pane) — only exists on an unmerged sibling branch (PR #960); it inherits
  this fix automatically once merged, since it depends on the same corrected
  `x/vt` cell data.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make /
  Go-build wiring is added or changed. The quality gate stays `make pre-pr`.
