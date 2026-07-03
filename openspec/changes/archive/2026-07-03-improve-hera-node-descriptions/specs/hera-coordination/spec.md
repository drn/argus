## ADDED Requirements

### Requirement: Worker and plan prompts carry the mission only, not injected policy

The system SHALL document, on the `prompt` parameter of `hera_spawn_worker` and
`hera_plan_node`, that the caller supplies the worker's MISSION/task only and does
NOT prepend organization or security policy into the prompt. The param
descriptions SHALL state the rationale: every spawned worker session receives its
organization instructions independently (harness-injected as an
`<organizationInstructions>` block), so a manually prepended policy is a redundant
copy that also pollutes the stored role prompt and the plan-DAG view.

argus SHALL continue to store the supplied `prompt` verbatim on the role
(unchanged) — this requirement governs the documented tool contract, not
enforcement: argus SHALL NOT parse, strip, or reject prompt content based on
"policy-looking" text.

Derived from: `internal/mcp/hera.go` (`hera_spawn_worker` tool registration +
`RolePrompt`), `internal/mcp/hera_plan.go` (`hera_plan_node` tool registration +
verbatim `Prompt`).

#### Scenario: spawn/plan prompt params document mission-only

- **WHEN** the `hera_spawn_worker` or `hera_plan_node` tool schema is inspected
- **THEN** the `prompt` param description directs supplying the worker's mission only
- **AND** it does not direct prepending organization/security policy, stating that the worker session receives org instructions independently

#### Scenario: The supplied prompt is still stored verbatim

- **WHEN** a coordinator calls `hera_spawn_worker` or `hera_plan_node` with any `prompt`
- **THEN** argus stores that prompt verbatim on the role, without stripping or transforming it
