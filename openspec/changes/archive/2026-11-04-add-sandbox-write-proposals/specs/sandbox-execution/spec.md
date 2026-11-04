## ADDED Requirements

### Requirement: Agents can propose sandbox write paths that take effect only on human approval

The system SHALL persist agent-submitted write-path proposals as pending records and SHALL NOT alter any sandbox configuration (project or global `extra_write`) when a proposal is created, listed, or rejected. A proposal SHALL change configuration only through the human approval action, which SHALL append the approved paths (deduplicated) to the chosen scope's existing `extra_write` list.

#### Scenario: Proposing does not change config

- **WHEN** an agent submits a proposal for `~/.gem`
- **THEN** a pending proposal exists and neither the project nor the global `extra_write` list has changed

#### Scenario: Approval applies to the chosen scope

- **WHEN** a human approves a pending proposal with scope `project`
- **THEN** the paths are appended to that project's `extra_write` and the global list is unchanged

#### Scenario: Rejection changes nothing

- **WHEN** a human rejects a pending proposal
- **THEN** its status is `rejected` and no `extra_write` list changes

### Requirement: Malformed paths are rejected and sensitive paths are accepted with a warning

The system SHALL reject, on propose and on approve, any path failing `ValidateWritePath`, rejecting the whole call and naming every offending path. A path that equals, contains, or lies within a sensitive location (`~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.config/gcloud`, `~/.netrc`, `~/Library/Keychains`, `~/.argus`, `~/.claude`, `~/.codex`) or is a broad root (`~`, `~/Library`, `/Users`) SHALL be accepted but carry a warning that is returned to the proposer and displayed by every approval surface.

#### Scenario: Malformed path rejected whole

- **WHEN** a proposal lists `~/.gem` and `relative/dir`
- **THEN** the call fails naming `relative/dir` and no proposal is stored

#### Scenario: Sensitive path accepted with warning

- **WHEN** a proposal lists `~/.aws/cache`
- **THEN** a pending proposal is stored whose entry carries a warning

#### Scenario: Edited approval is revalidated

- **WHEN** a human edits a pending proposal's path to a malformed value during approval
- **THEN** the approval fails and the proposal stays pending

### Requirement: Manual settings edits need no approval

Changing `extra_write` directly in TUI Settings, web Settings, or the master-token settings API SHALL apply immediately without creating a proposal. Only requests originating from the MCP proposal tool are held for approval.

#### Scenario: Settings edit is immediate

- **WHEN** a human adds a path in TUI Settings
- **THEN** it is saved with no proposal created

### Requirement: Approval states when it takes effect

The system SHALL report on approval that granted paths apply to newly started sessions only, and the TUI and web approval surfaces SHALL display that running tasks must be restarted.

#### Scenario: Approval response

- **WHEN** a proposal is approved
- **THEN** the response contains `takes_effect: "new_sessions"`

### Requirement: Approval surfaces in TUI and web

The web SPA (Settings panel) and the macOS app (Settings > Sandbox tab) SHALL list pending proposals with paths, warnings, reasons and requesting task, with approve (project or global, with editable paths) and reject actions. The TUI (Settings > Sandbox rows) SHALL list them with approve, a project/global scope toggle, and reject; free-form path editing in the TUI is done through the existing extra-write rows, which need no approval.

#### Scenario: Pending count visible

- **WHEN** one proposal is pending
- **THEN** the TUI Settings shows a System warning and a Sandbox proposal row, and the web/macOS Settings show a pending count of 1
