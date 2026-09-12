## ADDED Requirements

### Requirement: Link pickers copy the highlighted link to the clipboard

Both TUI link pickers (the simple link picker and the fuzzy link picker) SHALL support copying the currently highlighted link's URL to the OS clipboard via ctrl+y, in addition to opening it via Enter. Copying SHALL NOT close the picker or change which link is highlighted, so the operator can copy another link or still open one afterward. Ctrl+y SHALL be bound identically in both pickers rather than a plain rune, since the fuzzy picker's unmodified runes are live query-filter input. Confirming a copy with an empty link list SHALL NOT copy anything.

#### Scenario: Ctrl+y copies the highlighted link without closing the picker

- **WHEN** the user highlights a link and presses ctrl+y
- **THEN** that link's URL is written to the OS clipboard
- **AND** the picker remains open with the same link highlighted

#### Scenario: Ctrl+y on an empty link list is a no-op

- **WHEN** the picker has no links and the user presses ctrl+y
- **THEN** nothing is copied to the clipboard
