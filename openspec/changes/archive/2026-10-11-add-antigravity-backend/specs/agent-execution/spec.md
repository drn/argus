## MODIFIED Requirements

### Requirement: Session pinning and resume by backend family

The system SHALL pin a new session's ID for backends that accept a start-time session identifier (Claude-style and custom backends), and SHALL omit it for backends that do not (codex, pi, opencode, antigravity). On resume, the system SHALL reconnect to an existing conversation using the backend's own resume mechanism, and SHALL drop the prompt because the conversation is reloaded. The opencode resume form appends `--session <id>` (the same shape as pi); the antigravity resume form appends `--conversation <id>`. When resume is requested for a Claude-style, pi, opencode, or antigravity backend but no session ID is known, the system SHALL start a fresh session rather than emit an empty resume flag. Each backend's pinning and resume form SHALL be declared once in the backend family registry and read from there by every caller.

#### Scenario: New Claude-style session pins the ID
- **WHEN** a new (non-resume) session is built for a Claude-style backend that has a session ID
- **THEN** the command includes a start-time session-id flag carrying that ID

#### Scenario: New codex/pi/opencode/antigravity session never pins the ID
- **WHEN** a new session is built for a codex, pi, opencode, or antigravity backend even though a session ID is present
- **THEN** the command omits any start-time session-id flag

#### Scenario: Resume reconnects and drops the prompt
- **WHEN** a resume command is built for a backend with a known session ID
- **THEN** the command uses that backend's resume form carrying the ID and does not append the prompt

#### Scenario: opencode resumes with its session flag
- **WHEN** a resume command is built for an opencode backend with a known session ID
- **THEN** the command appends `--session` followed by the safely quoted session ID and omits the prompt

#### Scenario: antigravity resumes with its conversation flag
- **WHEN** a resume command is built for an antigravity backend with a known session ID
- **THEN** the command appends `--conversation` followed by the safely quoted ID and omits the prompt

#### Scenario: Resume with no session ID starts fresh
- **WHEN** resume is requested for a Claude-style, pi, opencode, or antigravity backend but no session ID is set
- **THEN** the command is the plain base command with no resume flag

## ADDED Requirements

### Requirement: Backend family registry

The system SHALL classify every backend command into a family by the basename of its first word: `claude`, `codex`, `pi`, `opencode`, `antigravity` (binaries `agy` or `antigravity`), or custom for anything else. Each family SHALL declare its per-backend behavior (session pinning, resume form, capture and recapture policy, model delivery, skill delivery, routing delivery, prompt delivery, per-launch MCP flag, permission-mode flags, prelaunch, start timeout, global configuration injection, and sandbox state directories) in one registry entry, and every component that varies by backend SHALL read the registry rather than test backend identity itself.

#### Scenario: Antigravity detected by binary name
- **WHEN** a backend command's first word is `agy`, `/opt/homebrew/bin/agy`, or `antigravity`
- **THEN** it is classified as the antigravity family

#### Scenario: Unknown command is custom
- **WHEN** a backend command's first word matches no family binary
- **THEN** it is classified as custom: Claude-style session pinning and `--resume`, no Claude-only flags, and no post-exit capture

#### Scenario: Existing backends unchanged
- **WHEN** a command is built for a claude, codex, pi, or opencode backend
- **THEN** the command string is identical to the pre-registry behavior

### Requirement: Antigravity command construction

The system SHALL deliver an antigravity task's prompt through the backend's configured prompt flag, defaulting to `--prompt-interactive` when none is configured, because agy rejects positional prompts. It SHALL inject `--model <id>` for a resolved model unless the command already names `--model`. It SHALL map `defaults.permission_mode` to agy flags — bypass-active to `--dangerously-skip-permissions`, accept-edits to `--mode accept-edits`, plan and bypass-allow to `--mode plan`, default to no flag — and SHALL inject nothing when the command already names `--dangerously-skip-permissions` or `--mode`. It SHALL prepend Argus's routing content to the first prompt, as for codex and opencode.

#### Scenario: Prompt via prompt-interactive
- **WHEN** a new antigravity session with a prompt is built from a backend with no configured prompt flag
- **THEN** the command carries `--prompt-interactive` followed by the quoted prompt

#### Scenario: Permission mode mapped
- **WHEN** the permission mode is accept-edits
- **THEN** the antigravity command carries `--mode accept-edits`

#### Scenario: Hand-edited permission flag wins
- **WHEN** the antigravity command already contains `--dangerously-skip-permissions`
- **THEN** no permission flag is injected

### Requirement: Antigravity session capture

The system SHALL capture an antigravity task's conversation ID after every exit by reading `~/.gemini/antigravity-cli/conversation_summaries.db` read-only and selecting the `conversation_id` with the newest `last_modified_time` whose `workspace_uris` names the task's worktree (comparing canonical paths after stripping `file://` and URL-decoding). When nothing matches, the store is missing, or the read fails, capture SHALL report no ID and the stored ID SHALL be kept. Resume-time refresh SHALL apply to antigravity as it does to Claude.

#### Scenario: Newest matching conversation captured
- **WHEN** two conversations reference the worktree
- **THEN** the one with the newer `last_modified_time` is returned

#### Scenario: Other workspaces ignored
- **WHEN** the only conversations reference other directories
- **THEN** capture returns an error and no ID

#### Scenario: Missing store fails open
- **WHEN** the conversation database does not exist
- **THEN** capture returns an error and the launch is unaffected

### Requirement: Resume-time session-ID refresh for recapture-every-exit backends

The system SHALL refresh a task's recorded session ID at resume for every backend family that recaptures on every exit (Claude and antigravity), using that family's capture, and SHALL leave codex, pi, opencode, and custom backends untouched.

#### Scenario: Antigravity refresh picks the newer conversation
- **WHEN** an antigravity task resumes and a newer conversation exists for its worktree
- **THEN** the task's session ID is updated before the resume command is built
