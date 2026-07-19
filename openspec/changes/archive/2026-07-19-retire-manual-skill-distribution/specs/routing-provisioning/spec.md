## MODIFIED Requirements

### Requirement: Builtin routing content bundle

The system SHALL embed argus's hera-coordination and argus-task-management orientation content directly into the argus binary via `go:embed`, as the sole, authoritative source of that content. The manual distribution path this superseded — `install-claude-skills.sh`/`uninstall-claude-skills.sh` and the `claude/snippets/*.md` files it distributed — is retired; there is no external copy for the embedded content to drift from or be verified against.

#### Scenario: Embedded content contains both orientation sections

- **WHEN** the embedded routing content is read
- **THEN** it contains both the hera-coordination and argus-task-management orientation sections
