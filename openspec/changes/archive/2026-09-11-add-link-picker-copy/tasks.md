## 1. Implementation

- [x] 1.1 Add `copyRequested`/`TakeCopyRequested()` and a ctrl+y case to `LinkPickerModal.InputHandler` (`internal/tui/links.go`); update help text.
- [x] 1.2 Add the same shape to `FuzzyLinkPickerModal` (`internal/tui/fuzzylinkpicker.go`), keyed off ctrl+y (not a rune) since runes are live filter input.
- [x] 1.3 Add `copyLinkToClipboard(link Link)` to `internal/tui/clipboard.go`, flashing "Nothing to copy" for an empty link.
- [x] 1.4 Wire `TakeCopyRequested()` into `handleLinkPickerKey` / `handleFuzzyLinkPickerKey` in `internal/tui/app.go`, ahead of the existing `Selected()` branch, without closing the modal.

## 2. Tests

- [x] 2.1 Modal-level tests: ctrl+y sets/clears the copy flag, doesn't select/cancel, no-ops on an empty list (both modals).
- [x] 2.2 App-level tests: ctrl+y reaches `clipboardWriter` and the modal stays open (both modals); empty-list ctrl+y doesn't crash and stays open.

## 3. Docs

- [x] 3.1 Archive this change into `openspec/specs/forms-and-modals/spec.md` in the same PR.
