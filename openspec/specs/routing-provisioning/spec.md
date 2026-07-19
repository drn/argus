# routing-provisioning Specification

## Purpose

Routing Provisioning embeds argus's builtin hera-coordination and argus-task-management orientation content directly into the binary and materializes it to a stable path under `~/.argus`, so every spawned Claude session receives that guidance without any manual install step. It is the injection-side counterpart to `skill-provisioning`: where that capability embeds discoverable skill bodies for `--add-dir`, this one embeds orientation prose for `--append-system-prompt-file`.
## Requirements
### Requirement: Builtin routing content bundle

The system SHALL embed argus's hera-coordination, argus-task-management, and code-review-methodology orientation content directly into the argus binary via `go:embed`, as the sole, authoritative source of that content. The embedded set SHALL be derived generically by concatenating every file found in the embedded directory tree, sorted by filename for determinism, so adding a new orientation section requires only adding its file, not a code change. The manual distribution path this superseded — `install-claude-skills.sh`/`uninstall-claude-skills.sh` and the `claude/snippets/*.md` files it distributed — is retired; there is no external copy for the embedded content to drift from or be verified against.

#### Scenario: Embedded content contains all three orientation sections

- **WHEN** the embedded routing content is read
- **THEN** it contains the hera-coordination, argus-task-management, and code-review-methodology orientation sections

#### Scenario: Code-review orientation section is self-gated

- **WHEN** the code-review-methodology orientation section is read
- **THEN** it opens with a gate on `ARGUS_TASK_ID`/sandbox residency, directing the reader to ignore the section entirely outside an argus sandbox

### Requirement: Idempotent materialization to a stable path

The system SHALL materialize the embedded routing content, concatenated in a deterministic (name-sorted) order, to a single file under `~/.argus/routing/`, rewriting the file only when its content differs from what is already on disk. The materializing function SHALL return the file's path on success, suitable for direct use as a CLI flag argument.

#### Scenario: Idempotent materialization

- **WHEN** the routing content is materialized twice with no change to the embedded source
- **THEN** the on-disk file is not rewritten the second time

#### Scenario: Materialization is inert during automated tests

- **WHEN** the materializing function runs inside a Go test binary
- **THEN** it returns an empty path and no error, performing no filesystem writes under `~/.argus/`

