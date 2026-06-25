# Hera View

## MODIFIED Requirements

### Requirement: Rail sections — Pinned, Active, Freelance, Archive (area 2)

The system SHALL render the rail in a fixed section order: a "Pinned" section (rendered when pinned orchestrators OR pinned non-root roles exist), the active orchestrators (no header, like the task list's active section), a "Freelance (N)" fold section (only when freelance roles exist), and an "Archive (N)" expando section at the bottom (only when archived orchestrators exist). Within the Pinned section, pinned orchestrators (with their subtrees) render first, followed by pinned non-root roles (each as a two-line breadcrumb entry — see the pinned-non-root-role requirement). A non-selectable horizontal-rule divider (the same `─` `StyleBorder` rule drawn above the Freelance and Archive sections) SHALL separate the Pinned section from the Active list, rendered ONLY when the Pinned section is present AND at least one Active entry follows it (no stray rule when nothing is pinned, and none when the Pinned section is the only content). The Freelance section defaults expanded; the Archive section defaults collapsed. When the model is entirely empty the rail renders a single "No hera orchestrators" placeholder row.

Derived from: `internal/tui/hera/rail.go` (`buildRows`), `internal/tui/hera/rail.go` (`NewRail` archive default), `internal/tui/hera/model.go` (Model sections).

#### Scenario: Pinned section appears when an orchestrator is pinned

- **WHEN** at least one orchestrator is pinned
- **THEN** a "Pinned" header renders above the active list with the pinned orchestrators (and their subtrees) under it

#### Scenario: Pinned section appears when only a non-root role is pinned

- **WHEN** no orchestrator is pinned but at least one non-root role is pinned
- **THEN** a "Pinned" header still renders, with the pinned role(s) shown as breadcrumb entries under it

#### Scenario: Divider separates the Pinned section from the Active list

- **WHEN** the rail has a Pinned section AND at least one Active orchestrator below it
- **THEN** a single non-selectable horizontal-rule divider renders between the last Pinned row and the first Active row, and the cursor skips it during `j`/`k` navigation

#### Scenario: No Pinned divider when nothing is pinned or no Active follows

- **WHEN** there is no Pinned section, OR the Pinned section is present but no Active entry follows it
- **THEN** no Pinned→Active divider renders

#### Scenario: Archive section collapsed by default

- **WHEN** archived orchestrators exist
- **THEN** an "Archive (N)" expando renders at the bottom, collapsed by default, expanding only when toggled

#### Scenario: Empty model shows a placeholder

- **WHEN** there are no orchestrators or freelance roles at all
- **THEN** the rail renders a single non-selectable "No hera orchestrators" row
