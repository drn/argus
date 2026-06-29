## MODIFIED Requirements

### Requirement: Session pinning and resume by backend family

The system SHALL pin a new session's ID for backends that accept a start-time session identifier (Claude-style), and SHALL omit it for backends that do not (codex, pi, opencode). On resume, the system SHALL reconnect to an existing conversation using the backend's own resume mechanism, and SHALL drop the prompt because the conversation is reloaded. The opencode resume form appends `--session <id>` (the same shape as pi). When resume is requested for a Claude-style, pi, or opencode backend but no session ID is known, the system SHALL start a fresh session rather than emit an empty resume flag.

#### Scenario: New Claude-style session pins the ID
- **WHEN** a new (non-resume) session is built for a Claude-style backend that has a session ID
- **THEN** the command includes a start-time session-id flag carrying that ID

#### Scenario: New codex/pi/opencode session never pins the ID
- **WHEN** a new session is built for a codex, pi, or opencode backend even though a session ID is present
- **THEN** the command omits any start-time session-id flag

#### Scenario: Resume reconnects and drops the prompt
- **WHEN** a resume command is built for a backend with a known session ID
- **THEN** the command uses that backend's resume form carrying the ID and does not append the prompt

#### Scenario: opencode resumes with its session flag
- **WHEN** a resume command is built for an opencode backend with a known session ID
- **THEN** the command appends `--session` followed by the safely quoted session ID and omits the prompt

#### Scenario: Resume with no session ID starts fresh
- **WHEN** resume is requested for a Claude-style, pi, or opencode backend but no session ID is set
- **THEN** the command is the plain base command with no resume flag

### Requirement: Post-exit session-ID capture for capture-style backends

The system SHALL recover a session identifier after exit for backends that mint their ID externally (codex, pi, opencode) by reading the backend's own state for the task's worktree, returning the most recently updated matching session. For backends that pin their ID at start (Claude-style) or are unrecognized, the system SHALL report no captured ID without error. A captured codex ID SHALL be validated as a UUID before being returned, and a captured opencode ID SHALL be validated against the `ses_` identifier format before being returned.

For opencode the system SHALL resolve the data directory from `XDG_DATA_HOME` (falling back to `~/.local/share`) under `opencode`, and SHALL locate the session whose recorded working directory equals the task's worktree, choosing the most recently updated one. It SHALL read the current SQLite store (`opencode.db`, table `session`) first and fall back to the legacy JSON session files when the SQLite store is absent or yields no match. When no matching session is found in either store, the system SHALL report no captured ID (fail open) so the conversation simply starts fresh rather than failing the launch.

#### Scenario: Codex ID recovered from its state for the worktree
- **WHEN** capture runs for a codex-backed task whose worktree has a recorded session
- **THEN** the most-recently-updated session ID for that worktree is returned

#### Scenario: Pi ID recovered from the newest session file
- **WHEN** capture runs for a pi-backed task whose worktree has session files
- **THEN** the UUID from the newest matching session file is returned

#### Scenario: opencode ID recovered from the SQLite store for the worktree
- **WHEN** capture runs for an opencode-backed task whose worktree has a row in the opencode SQLite session store
- **THEN** the id of the most-recently-updated session whose directory equals the worktree is returned

#### Scenario: opencode ID recovered from legacy JSON when no SQLite store
- **WHEN** capture runs for an opencode-backed task and only the legacy JSON session files exist
- **THEN** the id of the most-recently-updated JSON session whose directory equals the worktree is returned

#### Scenario: opencode capture fails open when nothing matches
- **WHEN** capture runs for an opencode-backed task whose worktree has no recorded session in either store
- **THEN** no captured ID is produced and the launch is unaffected (the next start is a fresh session)

#### Scenario: Claude-style and unknown backends capture nothing
- **WHEN** capture runs for a Claude-style or unrecognized backend
- **THEN** an empty ID is returned with no error

#### Scenario: Malformed captured codex ID is rejected
- **WHEN** the recorded codex session ID is not a valid UUID
- **THEN** capture returns an error rather than the malformed value
