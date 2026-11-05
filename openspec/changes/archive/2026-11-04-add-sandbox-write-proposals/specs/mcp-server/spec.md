## ADDED Requirements

### Requirement: sandbox_propose_write_paths records a pending proposal and nothing else

The MCP server SHALL expose `sandbox_propose_write_paths` taking `paths` (array of `{path, reason}`) and `id` or `cwd` to resolve the requesting task. It SHALL validate per the sandbox-execution requirement, create a pending proposal, and return its id and status. It SHALL NOT modify any configuration. The MCP server SHALL NOT expose any tool that approves, rejects, edits or applies a proposal.

#### Scenario: Batch proposal via cwd

- **WHEN** an agent calls the tool with `cwd` inside its worktree and three valid paths
- **THEN** one pending proposal with three entries is created for that task's project

#### Scenario: No decision tool exists

- **WHEN** `tools/list` is requested
- **THEN** no tool name permits approving or rejecting a sandbox proposal

#### Scenario: Per-task cap

- **WHEN** a task already has 10 pending proposals
- **THEN** a new distinct proposal is rejected with an explanatory error
