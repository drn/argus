## ADDED Requirements

### Requirement: Cursor backend family

The system SHALL classify a backend whose first-word basename is `cursor-agent` as the `cursor` family. Cursor sessions SHALL NOT pin a session ID; the ID SHALL be captured after exit from the newest chat directory under `~/.cursor/chats/<md5 of worktree path>/` and resumed with `--resume <id>`. The prompt SHALL be passed after `--`, `--trust` SHALL be added to every launch unless present, and the resolved model SHALL be passed as `--model`.

#### Scenario: Cursor detected by binary name
- **WHEN** a backend command's first word is `cursor-agent` or an absolute path ending in it
- **THEN** it is classified as the cursor family

#### Scenario: New session does not pin an ID
- **WHEN** a new session is built for a cursor backend that has a session ID
- **THEN** the command omits any start-time session-id flag and passes the prompt after `--`

#### Scenario: Resume reconnects and drops the prompt
- **WHEN** a resume command is built for a cursor backend with a known session ID
- **THEN** the command appends `--resume` followed by the quoted ID and omits the prompt

#### Scenario: Resume with no session ID starts fresh
- **WHEN** resume is requested for a cursor backend with no session ID
- **THEN** the command has no resume flag

#### Scenario: Capture picks the newest chat
- **WHEN** post-exit capture runs and the worktree's chat directory holds several `<uuid>` chat dirs
- **THEN** the newest by modification time is returned, and an error is returned when none exist

### Requirement: Cursor permission modes

The system SHALL map `defaults.permission_mode` for cursor backends: bypass-active to `--force`, plan and bypass-allow to `--plan`, others to nothing, and SHALL inject nothing when the command already contains `--force`, `--yolo`, `--plan`, or `--mode`.

#### Scenario: Bypass-active forces
- **WHEN** the permission mode is bypass-active and the command has no permission flag
- **THEN** `--force` is added

#### Scenario: Hand-edited flag wins
- **WHEN** the backend command already contains `--yolo`
- **THEN** no permission flag is injected
