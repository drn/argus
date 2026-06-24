# Tasks

## 1. Shared classification
- [x] 1.1 Add `IsPR(url string) bool` to `internal/links` (GitHub `/pull/<n>`, GitLab `/merge_requests/<n>`).
- [x] 1.2 Add `IsPR bool` (`json:"isPR"`) to `links.Link`; populate it in `Extract`.
- [x] 1.3 Unit-test `IsPR` (PR vs non-PR vs compare-range vs malformed) and that `Extract` stamps the field.

## 2. TUI rendering
- [x] 2.1 Add an `IconPRLink` glyph + `StylePRLink` style to `theme`.
- [x] 2.2 Render the glyph before PR rows in `LinkPickerModal.Draw`.
- [x] 2.3 Render the glyph before PR rows in `FuzzyLinkPickerModal.Draw`.
- [x] 2.4 Tests assert the marker is drawn for PR rows and absent for non-PR rows.

## 3. Webapp rendering
- [x] 3.1 Prepend a "PR" badge before PR rows in `renderLinksList` (DOM API, no innerHTML).
- [x] 3.2 Add `.links-pr-badge` CSS.
- [x] 3.3 Bump `SW_VERSION` in `sw.js`.

## 4. Docs + gate
- [x] 4.1 Add gotcha note(s) where the invariant lives.
- [x] 4.2 `make pre-pr` green.
- [x] 4.3 Archive this change folder into `openspec/changes/archive/` in the same PR.
