## ADDED Requirements

### Requirement: Plan-DAG node description shows the mission's first lines (area 6)

The plan view SHALL, in the coordinator Details region's embedded `" Plan "`
graph, render a selected node's description as the first N (N ≈ 3) NON-EMPTY lines
of the role's stored prompt, wrapped/truncated to the detail-pane width, rather
than only the single first line. The header MAY grow to accommodate the additional
description rows, and the coordinator Details region SHALL keep laying out the
roster-over-graph split without overflow.

The render SHALL be POLICY-AGNOSTIC: it SHALL NOT strip, skip, or pattern-match
any organization/security policy text, and SHALL NOT assume any particular line
is boilerplate. When the role's stored prompt is empty the header SHALL render the
existing `"(no description)"` placeholder.

Derived from: `internal/tui/hera/plan.go` (`heraPlanNodesWithBridge`,
`Node.Description`), `internal/tui/planview/planview.go` (`nodeHeaderLines`).

#### Scenario: A multi-line mission shows several lines

- **WHEN** a plan node's stored prompt has multiple non-empty lines and the node is selected
- **THEN** the detail header renders the first few (≈3) lines of the prompt, wrapped to the pane, not just the first line

#### Scenario: The description is rendered verbatim, no policy stripping

- **WHEN** a plan node's stored prompt begins with organization/security policy text
- **THEN** the detail header renders those opening lines as-is, without stripping or skipping them (the fix for polluted prompts is upstream prompt hygiene, not view-side stripping)

#### Scenario: Empty prompt still shows the placeholder

- **WHEN** a plan node's stored prompt is empty
- **THEN** the detail header renders `"(no description)"`
