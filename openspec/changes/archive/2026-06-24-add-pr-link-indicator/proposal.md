# Add a PR indicator to the Open Link picker (TUI + webapp)

## Why

The Open Link picker lists every http/https URL an agent emitted, flat and
undifferentiated. Pull-request URLs are usually the link a user is hunting for,
but they look identical to CI links, docs, and random references. Marking PR
links makes the most actionable link scannable at a glance.

## What Changes

- Add a shared `links.IsPR(url)` classifier and an `IsPR` field on
  `links.Link`, populated by `links.Extract`, so both the TUI and the REST API
  expose the same PR judgement (single source of truth — JS never re-derives it).
- TUI: both link pickers (the simple `LinkPickerModal` and the agent-view
  `FuzzyLinkPickerModal`) prepend a git-pull-request glyph before PR rows.
- Webapp: the Open Link modal prepends a small "PR" badge before PR rows.

No keybindings change. The picker's selection/filter behavior is unchanged.

## Impact

- Specs: `forms-and-modals` (TUI pickers + shared classification),
  `mobile-pwa` (webapp Open Link list).
- Code: `internal/links`, `internal/tui/links.go`,
  `internal/tui/fuzzylinkpicker.go`, `internal/tui/theme/theme.go`,
  `internal/api/static/index.html` (+ `SW_VERSION` bump).
