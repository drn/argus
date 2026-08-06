## MODIFIED Requirements

### Requirement: Hera orchestration tab

The PWA SHALL provide a read-only "Hera" tab (the second tab, reachable by the `g` hotkey and documented in the in-app help modal) that renders the orchestration roster from `GET /api/hera`. Orchestrators SHALL be grouped into Pinned, Active, and Archived sections, with a separate Freelance section for hoisted freelance roles. Each orchestrator SHALL show its `subtree_cost_usd` figure when present. Each role row SHALL show a status dot keyed to the hera status, the role kind and name, its `cost_usd` figure when present, and — when the role holds a live binding — the bound task's name and workflow badge plus a ready-to-close indicator when flagged. Tapping a role that has a live binding SHALL open that task's existing detail/terminal overlay; the roster itself SHALL remain read-only (no mutation controls, including for cost/token fields). All orchestrator, role, and task names rendered into the DOM MUST be HTML-escaped.

#### Scenario: Switching to the Hera tab renders the roster

- **WHEN** the user presses `g` or taps the Hera tab
- **THEN** the Hera view is shown and the roster is fetched and rendered, with a status line summarizing the orchestrator and role counts

#### Scenario: Empty roster placeholder

- **WHEN** `/api/hera` returns no orchestrators and no freelance roles
- **THEN** the Hera view shows an empty-state placeholder rather than an error

#### Scenario: Drill into a live role

- **WHEN** the user taps a role row that has a live binding
- **THEN** the task detail overlay opens for that role's bound task

#### Scenario: Roster excluded from mutation

- **WHEN** the Hera tab is displayed
- **THEN** it exposes no controls that create, modify, or delete orchestrators or roles, and no controls that edit a rate, a token total, or any other cost field

#### Scenario: An orchestrator row shows its subtree cost when present

- **WHEN** an orchestrator's roster entry carries a non-null `subtree_cost_usd`
- **THEN** that figure is rendered on the orchestrator's row

#### Scenario: A role row omits its cost figure when unmeasured

- **WHEN** a role's roster entry carries no `cost_usd` (omitted or null)
- **THEN** the role row renders with no cost figure, not a `$0.00` placeholder
