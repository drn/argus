## ADDED Requirements

### Requirement: Pull-request links are visually marked in the link pickers

Both TUI link pickers (the simple link picker and the fuzzy link picker) SHALL render a distinct pull-request indicator glyph before any link row whose URL points at a pull request or merge request, and SHALL render no such glyph for any other link. Whether a URL is a pull request SHALL be decided by the
shared `links` classifier (a GitHub `/<owner>/<repo>/pull/<n>` path or a GitLab
`/merge_requests/<n>` path), so the TUI and the web client agree. The indicator
SHALL be purely decorative: it SHALL NOT change which links match the filter,
SHALL NOT be part of the value returned on selection, and SHALL NOT consume the
URL/label text that the row already displays.

#### Scenario: PR link shows the indicator

- **WHEN** the link list contains a URL whose path is a GitHub pull request or GitLab merge request
- **THEN** that row SHALL be drawn with the pull-request indicator glyph preceding its text

#### Scenario: Non-PR link shows no indicator

- **WHEN** the link list contains a URL that is not a pull request (e.g. a docs page, a CI build, or a GitHub compare range)
- **THEN** that row SHALL be drawn without the pull-request indicator glyph

#### Scenario: Indicator does not affect selection or filtering

- **WHEN** the user filters or selects a PR-marked link
- **THEN** filtering SHALL match against the URL and label only, and the selected value SHALL be the link's URL and label, unaffected by the indicator
