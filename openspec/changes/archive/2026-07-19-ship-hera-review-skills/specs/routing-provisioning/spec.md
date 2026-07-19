## MODIFIED Requirements

### Requirement: Builtin routing content bundle

The system SHALL embed argus's hera-coordination, argus-task-management, and code-review-methodology orientation content directly into the argus binary via `go:embed`, as the sole, authoritative source of that content. The embedded set SHALL be derived generically by concatenating every file found in the embedded directory tree, sorted by filename for determinism, so adding a new orientation section requires only adding its file, not a code change. The manual distribution path this superseded — `install-claude-skills.sh`/`uninstall-claude-skills.sh` and the `claude/snippets/*.md` files it distributed — is retired; there is no external copy for the embedded content to drift from or be verified against.

#### Scenario: Embedded content contains all three orientation sections

- **WHEN** the embedded routing content is read
- **THEN** it contains the hera-coordination, argus-task-management, and code-review-methodology orientation sections

#### Scenario: Code-review orientation section is self-gated

- **WHEN** the code-review-methodology orientation section is read
- **THEN** it opens with a gate on `ARGUS_TASK_ID`/sandbox residency, directing the reader to ignore the section entirely outside an argus sandbox
