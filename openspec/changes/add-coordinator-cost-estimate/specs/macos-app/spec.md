## MODIFIED Requirements

### Requirement: Hera roster (read-only)

The system SHALL display the hera role roster for a task or orchestrator by
reading `GET /api/hera`, with no mutation actions (spawn worker, send
message, plan mutation, or editing any cost/token field) exposed in this app,
matching the web app's current scope. When present in the response, the
system SHALL render each orchestrator's `subtree_cost_usd` and each role's
`cost_usd` figure; when either is absent (omitted or null), the app SHALL
render no cost figure for that row rather than a `$0.00` placeholder.

#### Scenario: Roster renders without mutation controls

- **WHEN** the user opens the Hera tab for a task bound to an orchestrator
- **THEN** the app renders the role tree from `GET /api/hera` and presents no
  buttons or menu items that would spawn a worker, send a hera message,
  mutate a plan node, or edit a cost/token field

#### Scenario: Cost figures render when present

- **WHEN** the fetched roster includes a non-null `subtree_cost_usd` for an
  orchestrator or a non-null `cost_usd` for a role
- **THEN** the app renders that figure on the corresponding row

#### Scenario: Unmeasured rows show no cost figure

- **WHEN** an orchestrator's `subtree_cost_usd` or a role's `cost_usd` is
  omitted or null in the fetched roster
- **THEN** the app renders that row with no cost figure, not a `$0.00`
  placeholder
