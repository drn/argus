# Tasks: Fix BUG-008 — snap terminal scroll to bottom on user input

## 1. Tests (RED)

- [x] 1.1 Hera `forwardKey`: with the pane scrolled up, a printable key snaps `scrollOffset` to 0; PgUp/PgDn do not.
- [x] 1.2 Terminal `PasteHandler`: with the pane scrolled up, a paste to a live session snaps `scrollOffset` to 0; a dead session does not snap.

## 2. Implementation (GREEN)

- [x] 2.1 `forwardKey` calls `tp.ResetScroll()` before writing encoded input to the live session (after the PgUp/PgDn early-return).
- [x] 2.2 `PasteHandler` calls `tp.ResetScroll()` when writing paste to a live session.

## 3. Verify

- [x] 3.1 `go test ./internal/tui/terminal/... ./internal/tui/hera/...`
- [x] 3.2 `golangci-lint run --new-from-rev=33d16fc8 ./internal/tui/terminal/ ./internal/tui/hera/` → 0 issues
- [x] 3.3 `go build ./...`; gofmt clean
- [x] 3.4 Add gotcha bullet to `pty-terminal.md`
