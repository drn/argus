# Sandbox Execution

## Purpose

Argus runs agent processes inside a macOS `sandbox-exec` confinement so an autonomous agent can do real work (edit its worktree, push over SSH, store auth tokens, drive a browser) without being able to write outside its task's worktree or read sensitive credential stores. This capability generates a deny-by-default SBPL profile per task, grants the minimal set of write and read paths the supported agent backends actually need, and wraps the agent command in the `sandbox-exec` invocation.
## Requirements
### Requirement: Sandbox availability detection

The system SHALL report whether `sandbox-exec` is available on the host, preferring the canonical macOS path and falling back to `PATH` lookup. The result SHALL be cached after the first probe and SHALL be safe to query concurrently.

#### Scenario: sandbox-exec present at canonical path

- **WHEN** the host has an executable at the canonical `/usr/bin/sandbox-exec` path
- **THEN** the availability check reports that the sandbox is available

#### Scenario: cache reset re-probes

- **WHEN** the cached availability is reset and the availability check is queried again
- **THEN** the probe runs again and the cached value reflects the fresh result

#### Scenario: concurrent queries are safe

- **WHEN** many callers query availability simultaneously
- **THEN** the probe runs once and all callers observe a consistent result without a data race

### Requirement: Deny-by-default profile with worktree write access

The generated SBPL profile SHALL deny all operations by default and SHALL grant write access only to the task's worktree and a fixed set of system temp locations. Writes inside the worktree SHALL succeed and writes to arbitrary paths outside the allowed set SHALL be denied.

#### Scenario: write inside the worktree is allowed

- **WHEN** a sandboxed command writes a file under the task's worktree path
- **THEN** the write succeeds and the file exists

#### Scenario: write outside the allowed set is denied

- **WHEN** a sandboxed command writes a file to a path not covered by any allow rule
- **THEN** the write fails and the file is not created

#### Scenario: write to system temp is allowed

- **WHEN** a sandboxed command creates a temp file under `/tmp`
- **THEN** the write succeeds

### Requirement: Generated profile parameterized by HOME and WORKTREE

The system SHALL emit the profile with `HOME` and `WORKTREE` referenced as SBPL params, and SHALL return a params list binding those names to the resolved home directory and the resolved worktree path. All emitted paths SHALL be resolved through symlink resolution because the macOS kernel resolves symlinks before matching sandbox rules, so unresolved paths silently fail to match.

#### Scenario: profile references the params

- **WHEN** a profile is generated for a worktree
- **THEN** the profile text references both the `HOME` and `WORKTREE` params, and the returned params list contains a `HOME=` entry and a `WORKTREE=` entry bound to the worktree path

#### Scenario: symlink resolution falls back to the original path

- **WHEN** a path cannot be symlink-resolved (for example it does not yet exist)
- **THEN** the original path is used unchanged rather than failing generation

### Requirement: Credential read protection

The generated profile SHALL deny read access to known credential directories (`~/.gnupg`, `~/.aws`, `~/.kube`, `~/.config/gcloud`) while leaving `~/.ssh` readable so that git push and fetch over SSH continue to work.

#### Scenario: credential dir reads are denied

- **WHEN** a sandboxed command tries to read a protected credential directory such as `~/.aws`, `~/.gnupg`, or `~/.kube`
- **THEN** the read is blocked

#### Scenario: SSH directory remains readable

- **WHEN** a sandboxed command reads `~/.ssh`
- **THEN** the read is allowed, since SSH keys are required for git over SSH

#### Scenario: custom deny-read path is honored

- **WHEN** the sandbox config specifies an additional deny-read path and a sandboxed command tries to read a file under it
- **THEN** the read is blocked

### Requirement: Backend auth and session persistence writes

The generated profile SHALL grant the narrow write paths each supported agent backend needs to persist auth tokens and session state, without broadening to all of `$HOME`. This covers `~/.claude.json` (including atomic-write sibling files), `~/.claude/`, `~/.codex/`, and `~/.pi/`. Unrelated `$HOME`-rooted paths SHALL remain denied.

#### Scenario: Claude auth file and atomic-write siblings are writable

- **WHEN** a sandboxed command writes `~/.claude.json` or an atomic-write sibling such as `~/.claude.json.tmp.NNN`, `~/.claude.json.backup`, or `~/.claude.json.lock`
- **THEN** each write succeeds, preserving OAuth token persistence

#### Scenario: Codex and Pi session state are writable

- **WHEN** a sandboxed command writes Codex state under `~/.codex/` (auth, session DB, sessions) or Pi session files under `~/.pi/`
- **THEN** the writes succeed so the backends can persist auth and resume sessions

#### Scenario: unrelated HOME path stays denied

- **WHEN** a sandboxed command writes an unrelated `$HOME`-rooted path such as `~/.not-claude-related`
- **THEN** the write is denied, confirming the backend rules are not over-broad

### Requirement: Narrow SSH known_hosts write access

The generated profile SHALL allow writes to `~/.ssh/known_hosts` (prefix-scoped to cover OpenSSH's atomic mkstemp-then-rename update) while keeping private keys, `authorized_keys`, and the SSH `config` file write-protected.

#### Scenario: known_hosts append and atomic tempfile are allowed

- **WHEN** a sandboxed command appends to `~/.ssh/known_hosts` or writes an OpenSSH atomic-update tempfile like `~/.ssh/known_hosts.XXXXXX`
- **THEN** the writes succeed so new host keys can be accepted without an interactive prompt

#### Scenario: sensitive SSH files remain write-protected

- **WHEN** a sandboxed command tries to write `~/.ssh/id_ed25519`, `~/.ssh/authorized_keys`, or `~/.ssh/config`
- **THEN** each write is denied

### Requirement: Scoped tool, cache, and browser write access

The generated profile SHALL grant scoped write access to the GitHub CLI config (`~/.config/gh`), build-tool caches, the macOS Keychains directory, and the Google Chrome support directory, without broadening to their parent directories. In particular the gh allow rule SHALL NOT undo the gcloud deny-read, and the Chrome allow rule SHALL NOT broaden to all of Application Support.

#### Scenario: gh config is writable but gcloud read stays denied

- **WHEN** a sandboxed command writes a file under `~/.config/gh` and separately tries to read `~/.config/gcloud`
- **THEN** the gh write succeeds and the gcloud read remains blocked

#### Scenario: Chrome crashpad support file is writable

- **WHEN** a sandboxed command writes the Chrome crashpad `settings.dat` under `~/Library/Application Support/Google/Chrome`
- **THEN** the write succeeds, allowing Chrome to launch for browser automation

#### Scenario: sibling Application Support directories stay denied

- **WHEN** a sandboxed command writes to an unrelated `~/Library/Application Support/OtherApp` path
- **THEN** the write is denied, confirming the Chrome rule is not over-broad

#### Scenario: Keychains directory is writable

- **WHEN** a sandboxed command writes a file under `~/Library/Keychains`
- **THEN** the write succeeds so the agent can store API keys via the macOS Keychain

### Requirement: Worktree git-dir write access

When the worktree is a git worktree (its `.git` is a file pointing at a main repo's `.git/worktrees/<name>` directory), the generated profile SHALL grant write access to that main repo's `.git` directory so git metadata operations succeed. A plain repository or a non-git directory SHALL NOT add such a rule.

#### Scenario: worktree git metadata is writable

- **WHEN** the worktree's `.git` file points at a main repo's `.git/worktrees/<name>` and a sandboxed command writes a lock file under that git dir
- **THEN** the write succeeds and the profile contains the corresponding git-dir write rule

#### Scenario: non-worktree directory adds no git-dir rule

- **WHEN** the path has a real `.git` directory or no `.git` at all
- **THEN** no main-repo git-dir write rule is added to the profile

### Requirement: Custom extra write paths

The generated profile SHALL append a write-allow rule for each configured extra write path, expanding a leading `~/` to the home directory and trimming surrounding whitespace before resolving symlinks.

#### Scenario: configured extra write path is granted

- **WHEN** the sandbox config lists an extra write path and a sandboxed command writes a file under it
- **THEN** the write succeeds

### Requirement: AppleEvent destination allow rules with bundle-ID validation

The system SHALL emit one `appleevent-send` allow rule per configured AppleEvent destination, but only for entries that pass syntactic CFBundleIdentifier validation; entries that are empty, whitespace-only, or fail validation SHALL be silently skipped so a single bad entry cannot make `sandbox-exec` reject the whole profile.

#### Scenario: valid bundle IDs emit rules

- **WHEN** the config lists valid bundle identifiers
- **THEN** the profile contains one `appleevent-send` allow rule per identifier

#### Scenario: invalid or injection entries are skipped

- **WHEN** the config mixes a valid identifier with blank, whitespace-only, and SBPL injection-attempt entries
- **THEN** only the valid identifier produces a rule and no injected content appears in the profile

#### Scenario: bundle-ID validation accepts and rejects per charset

- **WHEN** validating a candidate bundle identifier
- **THEN** identifiers matching the allowed letters/digits/dot/hyphen charset (not starting with dot or hyphen) are accepted, and empty strings or those containing spaces, quotes, parentheses, semicolons, or slashes are rejected

### Requirement: Profile lifecycle and command wrapping

The system SHALL write the generated profile to a temporary file and return a cleanup function that removes it; after cleanup the file SHALL no longer exist. The system SHALL wrap an agent command in a `sandbox-exec` invocation that passes each param via `-D`, the profile via `-f`, and runs the command through `sh -c`, with all arguments shell-quoted. When the sandbox is disabled, the command SHALL NOT be wrapped and no cleanup function SHALL be returned.

#### Scenario: cleanup removes the profile file

- **WHEN** a profile is generated and its cleanup function is then called
- **THEN** the temporary profile file exists before cleanup and is removed afterward

#### Scenario: wrapped command carries params, profile, and shell invocation

- **WHEN** a command is wrapped with the sandbox using a profile path and params
- **THEN** the result begins with the sandbox-exec path, contains a `-D` flag for each param, a `-f` flag with the profile path, and runs the original command via `sh -c`

#### Scenario: sandbox disabled skips wrapping

- **WHEN** an agent command is built with the sandbox disabled
- **THEN** the resulting command is not wrapped in `sandbox-exec` and no cleanup function is returned

### Requirement: Per-task sandbox override resolution

The system SHALL support an optional per-task sandbox override with three
states: unset (inherit), force-enabled, and force-disabled. When resolving the
effective sandbox config for a task, the system SHALL apply, in order: the
global setting, then the task's project override (if set), then the task's own
override (if set) — a set task override SHALL win over both the project and
global settings. An unset task override SHALL leave resolution exactly as it
was before this requirement existed (global, then project).

#### Scenario: Task override forces sandboxing on

- **WHEN** a task has a force-enabled sandbox override and its project has sandboxing disabled
- **THEN** the resolved sandbox config is enabled for that task

#### Scenario: Task override forces sandboxing off

- **WHEN** a task has a force-disabled sandbox override and both its project and the global setting have sandboxing enabled
- **THEN** the resolved sandbox config is disabled for that task

#### Scenario: Unset task override inherits the existing precedence

- **WHEN** a task has no sandbox override
- **THEN** the resolved sandbox config is exactly the project override if set, else the global setting, unchanged from prior behavior

#### Scenario: Override is baked in at creation time, not re-derived

- **WHEN** a task is created with a sandbox override
- **THEN** the task's persisted `Sandboxed` state reflects that override as resolved at creation time, and does not change if the global or project setting later changes

