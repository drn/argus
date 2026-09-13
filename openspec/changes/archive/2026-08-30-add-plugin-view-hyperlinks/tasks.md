## 1. Add OSC-8 hyperlink mapping to the plugin-view cell-style mapper

- [x] 1.1 In `internal/tui/terminalpane/terminalpane.go`, add
  `if cell.Link.URL != "" { st = st.Url(cell.Link.URL) }` to
  `uvCellToTcellStyle`, mirroring
  `internal/tui/terminal.UvCellToTcellStyle`.
- [x] 1.2 Update the function's doc comment — it currently states the
  mapper deliberately omits the OSC-8 path; correct it now that parity is
  restored.
- [x] 1.3 Confirm there is no caching (`cachedCell`-equivalent) or
  desaturation layer in this package that would need to preserve/pass
  through the mapped `Url` separately — `paint` calls `screen.SetContent`
  directly from the mapped style every frame, so no other call site needs
  updating.

## 2. Tests

- [x] 2.1 Add a test proving a cell with `Link.URL` set produces a
  `tcell.Style` with the URL attached, mirroring
  `TestUvCellToTcellStyle_Hyperlink` in
  `internal/tui/terminal/terminalpane_test.go` but asserting equality
  (tcell.Style has no public URL getter, but the struct is comparable) since
  the existing default-pane test never actually asserts the mapped result.
- [x] 2.2 Add a companion test proving a cell with no link leaves the style
  unaffected (regression guard against the mapping firing unconditionally).

## 3. Docs

- [x] 3.1 Add a gotcha bullet to `context/knowledge/gotchas/pty-terminal.md`
  noting plugin-view panes now also support OSC-8 hyperlink passthrough,
  mirroring the default pane.
- [x] 3.2 Update `context/knowledge/index.md`'s bullet count for
  `pty-terminal.md`.

## 4. Archive

- [x] 4.1 Run `openspec archive add-plugin-view-hyperlinks` (or the manual
  merge-and-move fallback) in this same PR before opening it for merge.
