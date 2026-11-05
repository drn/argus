## ADDED Requirements

### Requirement: Oh My Pi backend family

The system SHALL classify a backend whose first-word basename is `omp` as the `omp` family. omp sessions SHALL NOT pin a session ID; the ID SHALL be captured after every exit from the newest top-level `<timestamp>_<uuid>.jsonl` file in the worktree's omp session directory under `~/.omp/agent/sessions/` (encoded as omp does: `-tmp-<rel>` under the temp dir, `-<rel>` under `$HOME`, else `--<abs>--`, with `/`, `\`, `:` replaced by `-`) and resumed with `--resume <id>`. The prompt SHALL be passed after `--`, and the resolved model SHALL be passed as `--model`.

#### Scenario: omp detected by binary name
- **WHEN** a backend command's first word is `omp` or an absolute path ending in it
- **THEN** it is classified as the omp family

#### Scenario: New session does not pin an ID
- **WHEN** a new session is built for an omp backend that has a session ID
- **THEN** the command omits any start-time session-id flag and passes the prompt after `--`

#### Scenario: Resume reconnects and drops the prompt
- **WHEN** a resume command is built for an omp backend with a known session ID
- **THEN** the command appends `--resume` followed by the quoted ID and omits the prompt

#### Scenario: Resume with no session ID starts fresh
- **WHEN** resume is requested for an omp backend with no session ID
- **THEN** the command has no resume flag

#### Scenario: Capture picks the newest session under the home-relative encoding
- **WHEN** post-exit capture runs for a worktree under `$HOME` whose session directory holds several session files, a subagent subdirectory, and a lock file
- **THEN** the UUID of the newest top-level session file is returned, and an error is returned when none exist

### Requirement: Oh My Pi permission modes

The system SHALL map `defaults.permission_mode` for omp backends onto `--approval-mode`: bypass-active to `yolo`, accept-edits to `write`, default, plan and bypass-allow to `always-ask` (omp's own default is `yolo`), and any other value to nothing; it SHALL inject nothing when the command already contains `--approval-mode`, `--auto-approve`, or `--yolo`.

#### Scenario: Accept-edits maps to write
- **WHEN** the permission mode is accept-edits and the command has no permission flag
- **THEN** `--approval-mode write` is added

#### Scenario: Hand-edited flag wins
- **WHEN** the backend command already contains `--auto-approve`
- **THEN** no permission flag is injected
