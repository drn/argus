## ADDED Requirements

### Requirement: Profile resolution tool

The MCP `profile_resolve` tool SHALL expose backend-keyed archetype model choices. When a caller supplies a backend, the tool SHALL identify the model selected for that backend while retaining the full backend-keyed choices for consumers that need to dispatch across backends. A missing, invalid, or unsupported selection SHALL use the existing fail-open response contract.

#### Scenario: Codex profile resolution

- **WHEN** a caller resolves a valid profile for backend `codex`
- **THEN** the result identifies each archetype's Codex model choice rather than a Claude model alias
