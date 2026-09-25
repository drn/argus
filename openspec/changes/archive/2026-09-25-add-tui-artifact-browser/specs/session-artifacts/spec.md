## ADDED Requirements

### Requirement: Discover task artifacts in the TUI

The TUI SHALL show the registered artifact count for the selected task and SHALL provide a task-scoped browser listing each registered artifact's title, type, size, and registration time. An empty manifest SHALL show an explicit empty state. A task change SHALL not show the prior task's artifact data while an asynchronous request is pending.

#### Scenario: Selected task has artifacts

- **WHEN** a task with registered artifacts is selected in the Tasks list
- **THEN** its artifact count is shown in the detail panel, and its browser lists the registered metadata without blocking input

#### Scenario: Task has no artifacts

- **WHEN** the browser opens for a task with an empty manifest
- **THEN** it shows an empty state and offers refresh

#### Scenario: Selection changes during fetch

- **WHEN** the user selects another task before the previous manifest request finishes
- **THEN** the previous result does not replace the newly selected task's count or browser contents

### Requirement: Preview or open a registered artifact from the TUI

The TUI SHALL preview bounded text and Markdown within the browser. For HTML, PDF, image, audio, and video artifacts, it SHALL open the registered bytes with the system viewer only after an explicit user action. Text and Markdown SHALL also offer an explicit external-open action. A missing backing file or failed open SHALL produce a visible error and keep the browser usable.

#### Scenario: Preview text

- **WHEN** the user selects a registered text or Markdown artifact and presses Enter
- **THEN** the TUI shows a scrollable text preview with a stated truncation limit and a way back to the list

#### Scenario: Open a non-text artifact

- **WHEN** the user selects a registered HTML, PDF, image, audio, or video artifact and presses Enter
- **THEN** the TUI opens those bytes in the system viewer and remains in the artifact browser

#### Scenario: Missing backing bytes

- **WHEN** a registered artifact has no readable backing file
- **THEN** the TUI reports the failure without exiting or showing an unrelated file

### Requirement: Artifact browser works in local and remote TUI modes

The local TUI SHALL use the per-task manifest and the same path-escape protections as REST serving. The remote TUI SHALL list and retrieve artifacts through the existing authenticated REST endpoints, using the manifest filename as the selector. Remote external opening SHALL stream bytes to a temporary file without buffering the whole artifact in memory and SHALL clean up temporary files when their ownership ends. Neither mode SHALL expose unregistered files.

#### Scenario: Remote artifact browser

- **WHEN** a remote TUI opens the artifact browser for a task
- **THEN** it shows the same registered metadata and can preview or externally open the same artifact types as local mode

#### Scenario: Unregistered or escaping file

- **WHEN** a file is absent from the manifest or resolves outside the task artifact directory
- **THEN** the TUI refuses to open it

#### Scenario: Large remote media

- **WHEN** a user explicitly opens a registered audio or video artifact over the remote TUI
- **THEN** the transfer is streamed to disk, can be canceled, and does not require memory proportional to file size

### Requirement: Refresh artifact discovery

The browser SHALL offer manual refresh, and reopening it SHALL fetch the current manifest so registration or replacement performed during a live session is discoverable. Refresh errors SHALL be visible while retaining the last successful list.

#### Scenario: Agent registers a new artifact

- **WHEN** an agent registers an artifact after the browser was last loaded and the user refreshes or reopens it
- **THEN** the new manifest entry and task count appear
