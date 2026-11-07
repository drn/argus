## 1. Dependency

- [x] 1.1 Bump `github.com/charmbracelet/x/vt` to `v0.0.0-20260828171018-3c30eef5e73e` (x/vt#868 OSC-8 parser fix); `go mod tidy`
- [x] 1.2 Confirm the emulator records `Link.URL`/`Link.Params` correctly for both `id=`-param and empty-param hyperlinks

## 2. OSC filter hyperlink carve-out (terminal-rendering)

- [x] 2.1 Tests: OSC-8 passthrough with BEL and ST terminators, percent-encoding of bytes outside printable ASCII, split-across-feeds, oversized-payload strip, cancel by CAN/SUB and fresh ESC, non-OSC-8 OSC (incl. `OSC 80…`) still stripped
- [x] 2.2 Implement the hyperlink buffering state in `internal/oscfilter.Filter`
- [x] 2.3 Add `oscfilter.HyperlinkID(params)` with tests
- [x] 2.4 Update the filter's doc comment (OSC 8 is no longer dropped)

## 3. Style mapping

- [x] 3.1 `terminal.UvCellToTcellStyle` sets `UrlId` from the link's `id=` param; replace the assertion-free hyperlink test with one that checks URL and id
- [x] 3.2 `terminalpane.uvCellToTcellStyle` (plugin views) maps URL and id, with tests
- [x] 3.3 End-to-end test: real Claude Code OSC-8 bytes → `FilterOSC` → emulator → `UvCellToTcellStyle` yields the URL and id

## 4. Docs and archive

- [x] 4.1 Gotcha note in `context/knowledge/gotchas/pty-terminal.md` (+ index row): the filter must keep OSC 8, and hyperlink tests must go through the filter
- [x] 4.2 Archive the change into base specs in this PR
- [x] 4.3 `make pre-pr` green
