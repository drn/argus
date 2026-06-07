# Fork / Create Task

## Purpose

This capability lets a user duplicate an existing task into a fresh worktree, carrying forward the source agent's recent output and code changes as on-disk context files so a new agent session can pick up where the previous one left off. The goal is continuity: a fork starts with the original prompt plus a curated summary of what the previous attempt did, optionally retargeted to a different project.

## Requirements

### Requirement: Fork confirmation and project selection

The fork modal SHALL confirm intent before forking and SHALL let the user choose the destination project, defaulting to the source task's project. Pressing Enter (outside autocomplete) SHALL confirm; Escape or Ctrl+Q SHALL cancel. A typeahead SHALL match project names case-insensitively, and the resolved project SHALL be empty when the typed text matches no known project.

#### Scenario: Confirm with Enter

- **WHEN** the user presses Enter while the project autocomplete is closed
- **THEN** the modal reports confirmed and not canceled

#### Scenario: Cancel with Escape or Ctrl+Q

- **WHEN** the user presses Escape or Ctrl+Q
- **THEN** the modal reports canceled and not confirmed

#### Scenario: Project defaults to source

- **WHEN** the fork modal opens for a task whose project is "alpha"
- **THEN** the resolved selected project is "alpha" without any typing

#### Scenario: Change destination project via typeahead

- **WHEN** the user clears the input, types a substring matching another project, and accepts the autocomplete match
- **THEN** the resolved selected project becomes that matched project and the Enter that accepted the match does NOT also confirm the fork

#### Scenario: Unknown project resolves empty

- **WHEN** the typed project text matches no known project name (case-insensitively)
- **THEN** the resolved selected project is the empty string

### Requirement: Fork context extraction

Forking SHALL extract context from the source task: its name, original prompt, status, and branch, plus the tail of its session output (ANSI/PTY chrome stripped) and the git diff of its worktree. The session-output tail SHALL be capped at 32 KiB and the git diff SHALL be capped at 64 KiB with a truncation marker appended when exceeded. Missing or unreadable sources SHALL yield empty context for that part rather than an error.

#### Scenario: Metadata captured from source

- **WHEN** context is extracted from a source task
- **THEN** the extracted context carries the source task's name, original prompt, status string, and branch

#### Scenario: Missing session log yields empty output

- **WHEN** the source task has no readable session log
- **THEN** the extracted recent output is the empty string

#### Scenario: Oversized git diff is truncated

- **WHEN** the source worktree's git diff exceeds the 64 KiB cap
- **THEN** the captured diff is truncated to the cap and a truncation marker is appended

#### Scenario: Diff only read from a valid worktree

- **WHEN** the source worktree path is not within a known worktree root
- **THEN** no git diff is captured and the captured diff is the empty string

### Requirement: Context-file injection into the destination worktree

When a fork is created, context files SHALL be written under a `.context/` directory in the destination worktree before the new agent starts. A `fork-source.md` file SHALL always be written containing the source name, status, branch (when present), and the original prompt. A `fork-output.md` file SHALL be written only when recent output was captured, and a `fork-diff.patch` file SHALL be written only when a git diff was captured.

#### Scenario: All context files written

- **WHEN** the extracted context includes recent output and a git diff
- **THEN** `.context/fork-source.md`, `.context/fork-output.md`, and `.context/fork-diff.patch` are all written into the destination worktree

#### Scenario: Empty parts are skipped

- **WHEN** the extracted context has no recent output and no git diff
- **THEN** `.context/fork-source.md` is written but `.context/fork-output.md` and `.context/fork-diff.patch` are NOT created

### Requirement: Forked task prompt construction

The forked task's prompt SHALL instruct the new agent to continue prior work, reference only the `.context/` files that were actually written, and include the source task's original prompt. When the destination project differs from the source project, the prompt SHALL include a note naming both the source and destination projects.

#### Scenario: Prompt references only written files

- **WHEN** the prompt is built with both recent output and a git diff present
- **THEN** the prompt references `.context/fork-source.md`, `.context/fork-output.md`, and `.context/fork-diff.patch` and includes the original prompt

#### Scenario: Prompt omits absent files

- **WHEN** the prompt is built with no recent output and no git diff
- **THEN** the prompt references `.context/fork-source.md` but does NOT reference `fork-output.md` or `fork-diff.patch`

#### Scenario: Cross-project fork note

- **WHEN** the destination project differs from the source project
- **THEN** the prompt includes a note naming the source project and the destination project

#### Scenario: Same-project fork has no note

- **WHEN** the destination project equals the source project
- **THEN** the prompt includes no project-change note

### Requirement: Fork creation via REST API

The REST API SHALL fork a task by source id, defaulting the new task's name to "<source>-fork", its prompt to the source prompt, and its project to the source project when those fields are omitted. The fork SHALL inherit the source task's backend. A fork request whose resolved project is empty SHALL be rejected, and a request for an unknown source id SHALL return not-found. On success the API SHALL create the task and emit a task-forked event carrying the parent-to-child linkage.

#### Scenario: Defaults applied for omitted fields

- **WHEN** a fork request omits name, prompt, and project for an existing source task
- **THEN** the new task's name is the source name suffixed with "-fork", its prompt is the source prompt, its project is the source project, and it inherits the source backend

#### Scenario: Empty project rejected

- **WHEN** the fork request's project is empty and the source task has no project
- **THEN** the API responds with a client error indicating the project is required

#### Scenario: Unknown source not found

- **WHEN** the fork request targets a task id that does not exist
- **THEN** the API responds with not-found and creates no task

#### Scenario: Fork event emitted on success

- **WHEN** a fork is created successfully
- **THEN** a task-forked event is emitted carrying the source task id and the new task id
