# routing-provisioning

Embedding and materializing argus's builtin hera/argus-task routing content — the orientation prose that tells a Claude session when to coordinate via hera, when to reach for iris, and how to self-manage its own argus task. This is the injection-side counterpart to `skill-provisioning` (PR #866): where `skill-provisioning` embeds discoverable skill bodies for `--add-dir`, `routing-provisioning` embeds prose for `--append-system-prompt-file`.

## ADDED Requirements

### Requirement: Builtin routing content bundle

The system SHALL embed argus's hera-coordination and argus-task-management orientation content — the same content distributed for manual installation as `claude/snippets/hera.md` and `claude/snippets/argus-tasks.md` — directly into the argus binary via `go:embed`. The embedded copies SHALL be kept byte-identical to those source snippets, verified by a test that reads the source files directly (not through the embed) and compares them against the embedded content.

#### Scenario: Embedded content matches the source snippets

- **WHEN** the embedded routing content is compared against `claude/snippets/hera.md` and `claude/snippets/argus-tasks.md` read directly from disk
- **THEN** the two are byte-identical

### Requirement: Idempotent materialization to a stable path

The system SHALL materialize the embedded routing content, concatenated in a deterministic (name-sorted) order, to a single file under `~/.argus/routing/`, rewriting the file only when its content differs from what is already on disk. The materializing function SHALL return the file's path on success, suitable for direct use as a CLI flag argument.

#### Scenario: Idempotent materialization

- **WHEN** the routing content is materialized twice with no change to the embedded source
- **THEN** the on-disk file is not rewritten the second time

#### Scenario: Materialization is inert during automated tests

- **WHEN** the materializing function runs inside a Go test binary
- **THEN** it returns an empty path and no error, performing no filesystem writes under `~/.argus/`
