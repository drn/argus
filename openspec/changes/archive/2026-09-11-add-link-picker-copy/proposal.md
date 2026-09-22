## Why

Both TUI "Open Link" modals (`LinkPickerModal` and `FuzzyLinkPickerModal`) only ever open the highlighted link in the default browser. There is no way to get a link's URL onto the clipboard without opening it first — a real gap when the operator wants to paste the URL somewhere (a Slack message, a Linear ticket) rather than visit it.

## What Changes

- Both link picker modals gain a copy affordance: **ctrl+y** copies the highlighted link's URL to the OS clipboard via the app's existing `copyToClipboard` machinery (same "Copied"-style header notice pattern already used for staged-clipboard and name/prompt copies), and leaves the modal open so the operator can keep browsing or copy another link.
- ctrl+y is chosen over a plain rune (e.g. `c`) because `FuzzyLinkPickerModal`'s unmodified runes are live query-filter input; ctrl+y is a distinct `tcell.Key` that never reaches the filter, so one binding works identically in both pickers.
- Copying an empty picker (no links) flashes "Nothing to copy" instead of copying an empty string.
- No keymap-system change: the modals' internal navigation keys (↑/↓, j/k, Enter, Esc) are already structural/literal (not entries in `internal/tui/keymap`), and this follows the same pattern.

## Capabilities

### Modified Capabilities

- `forms-and-modals`: the link picker requirements gain a copy-to-clipboard affordance alongside the existing open/select behavior.

## Impact

- **Modified code:**
  - `internal/tui/links.go` — `LinkPickerModal` gains `copyRequested`/`TakeCopyRequested()`, a ctrl+y case in `InputHandler`, and updated help text.
  - `internal/tui/fuzzylinkpicker.go` — same shape for `FuzzyLinkPickerModal`.
  - `internal/tui/clipboard.go` — new `copyLinkToClipboard(link Link)` helper shared by both handlers.
  - `internal/tui/app.go` — `handleLinkPickerKey`/`handleFuzzyLinkPickerKey` check `TakeCopyRequested()` before the existing `Selected()` branch.
- **New tests:** modal-level ctrl+y coverage in `links_test.go` / `fuzzylinkpicker_test.go` (copy requested + flag clears + empty-list no-op), and app-level coverage in `app_test.go` asserting the clipboard writer is invoked and the modal stays open.
- **No REST/wire-contract change** — this is TUI-modal-internal, so the web/macOS parity rule (triggered by REST-exposed surface changes) doesn't apply here.
- **Specs are LOCAL DOCS only**: no CI/Make/Go-build wiring added or changed.
