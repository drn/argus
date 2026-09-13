## 1. Dependency bump

- [x] 1.1 Bump `github.com/charmbracelet/x/vt` to commit
  `3c30eef5e73e8ad8e1765913b0ca2aeb43d32e4f` (pinned exact commit, not
  `@latest`) via `go get`.
- [x] 1.2 Run `go mod tidy`; confirm the only other direct/indirect changes
  are the coordinated `x/ansi` 0.11.6 → 0.11.7 bump and small transitive
  bumps (`displaywidth`, `go-colorful`, `go-runewidth`) required by the new
  `x/vt` version — no unrelated dependency churn.

## 2. Regression test

- [x] 2.1 Add `TestOSC8HyperlinkWithIDParam_RealWireFormat` in
  `internal/tui/terminal/terminalpane_test.go`, alongside the existing
  `UvCellToTcellStyle` hyperlink tests — feeds the real OSC-8 wire bytes
  (`\x1b]8;id=1vaggxp;<url>\x07...\x1b]8;;\x07`) through `xvt.NewSafeEmulator`
  and asserts `cell.Link.URL` is the real URL (not the `id=...` segment) and
  `cell.Link.Params` is the `id=...` segment.
- [x] 2.2 Verify the test actually catches the regression: temporarily
  reverted to the old pinned `x/vt` commit and confirmed the new test fails
  with the predicted swapped values, then restored the fix.

## 3. Docs

- [x] 3.1 Add a gotcha bullet to `context/knowledge/gotchas/pty-terminal.md`
  documenting the swapped-fields bug, its silent-until-tested nature, and the
  fix.
- [x] 3.2 This change folder (documents the dependency bump as a behavioral
  fix per repo convention).

## 4. Verification

- [x] 4.1 `make pre-pr` passes clean.
