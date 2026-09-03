# binary-coherence Specification

## Purpose
TBD - created by archiving change detect-binary-skew. Update Purpose after archive.
## Requirements
### Requirement: Cross-process binary identity reporting

The daemon SHALL report its own boot-time binary identity AND, when a session-supervisor is connected, the supervisor's binary identity to the TUI in a single `BootInfo` response. Identity comprises the resolved binary path, the SHA-256 content hash, and the VCS revision plus dirty flag read from the binary's build info. The supervisor's `Hello` handshake SHALL carry its content hash and VCS identity, and the R/S `ProtocolVersion` SHALL be bumped additively so a newer daemon can feature-detect an older supervisor.

#### Scenario: Supervisor identity relayed

- **WHEN** the TUI requests `BootInfo` from a daemon that is driving a connected supervisor
- **THEN** the response SHALL include the supervisor's resolved path, content hash, and VCS identity

#### Scenario: Older supervisor reports unknown, never stale

- **WHEN** the connected supervisor speaks a protocol version that predates the binary-hash field
- **THEN** the daemon SHALL report the supervisor identity as unknown and SHALL NOT report the supervisor as stale

#### Scenario: No supervisor present

- **WHEN** the daemon runs the in-process runner with no supervisor connected
- **THEN** the `BootInfo` response SHALL indicate that no supervisor is present

### Requirement: Binary identity display and staleness signal

The system SHALL display a binary's identity as its commit SHA and a dirty flag when VCS build
info is present, and SHALL fall back to the short content hash when VCS build info is absent. The
stale-versus-current decision SHALL be based on the SHA-256 content hash for the daemon, and on the
declared executed-surface version for the session-supervisor; it SHALL NEVER be based on VCS
information for any process. Where the supervisor's surface version is judged, its content hash
SHALL still be reported and displayed.

#### Scenario: Rich identity shown when VCS info present

- **WHEN** a binary was built from inside a git tree and carries VCS build info
- **THEN** its identity is displayed as the commit SHA plus a dirty/clean flag plus its resolved path

#### Scenario: Hash fallback when VCS info absent

- **WHEN** a binary lacks VCS build info
- **THEN** its identity is displayed using the short content hash

#### Scenario: Decision is hash-based

- **WHEN** two binaries have differing VCS info but identical content hashes
- **THEN** they SHALL be judged current (not stale)

#### Scenario: The supervisor's decision is surface-based, not hash-based

- **WHEN** the running supervisor's content hash differs from the current build's but its reported
  surface version matches
- **THEN** it SHALL be judged current (not stale), and both its content hash and its surface
  version SHALL still be displayed

### Requirement: Binary-coherence diagnostic command

The CLI SHALL provide an `argus doctor` command that runs read-only, enumerates the argus binaries resolvable on disk (the `PATH` entry, the `~/.argus/argusd` symlink target, and the `go install` target) and the identity each live process is running, and prints a verdict with an exact remediation command. The verdict SHALL distinguish a coherent install, a restart-needed install (same resolved path, differing hash), and a path-divergence install (the daemon symlink target and the `PATH` `argus` resolve to different files). A row that cannot be resolved SHALL degrade to "unknown" rather than aborting the command.

#### Scenario: Healthy install

- **WHEN** the TUI, daemon, and supervisor resolve to the same binary file with matching hashes
- **THEN** `argus doctor` SHALL print a healthy verdict and exit zero

#### Scenario: Non-healthy verdict exits non-zero

- **WHEN** `argus doctor` renders any non-healthy verdict (restart-needed or path-divergence)
- **THEN** it SHALL exit with a non-zero status after printing the table and remediation

#### Scenario: Restart needed

- **WHEN** a process is running a different hash from a binary at the same resolved path
- **THEN** `argus doctor` SHALL report "restart needed" and print the restart command

#### Scenario: Path divergence

- **WHEN** the daemon symlink target and the `PATH` `argus` resolve to different files
- **THEN** `argus doctor` SHALL report "path divergence" and print the re-point/reinstall fix rather than a plain restart

#### Scenario: Read-only

- **WHEN** `argus doctor` runs
- **THEN** it SHALL NOT modify any symlink, binary, `PATH`, or process

#### Scenario: Unresolvable row degrades

- **WHEN** one binary location cannot be resolved or hashed
- **THEN** that row SHALL be reported as "unknown" and the command SHALL still complete

### Requirement: Startup binary-skew detection and prompt

At startup the TUI SHALL detect binary skew against the daemon and the supervisor and present a
blocking modal when the detected skew affects work in flight — a stale daemon, or a supervisor
mismatch that reaches live sessions. A supervisor mismatch that can affect only sessions started
from that point on SHALL be surfaced through the transient status-bar notice instead of a blocking
modal, since it cannot affect any already-running agent. Daemon-staleness detection SHALL be
performed only when the TUI connected to a pre-existing daemon (a daemon the TUI itself just
auto-started cannot be stale). Supervisor-staleness detection SHALL be performed whenever a
supervisor is present, regardless of how the daemon was started. The modal SHALL display the rich
identity of whichever process is stale and offer the relevant restart action(s); where the
supervisor is among them, the modal SHALL also state what its mismatch costs, so an agent-
interrupting restart is never offered without saying what it buys. Restarting the supervisor SHALL
require a second explicit confirmation that names the number of running agents the restart will
interrupt; declining the second confirmation SHALL leave the supervisor running. The modal SHALL
auto-dismiss only when a single restart action (or none) was offered; when both the daemon and the
supervisor are stale, resolving one restart action SHALL leave the modal open with the remaining
action(s) until the user addresses or skips it.

#### Scenario: Supervisor checked on the auto-start path

- **WHEN** the TUI auto-started the daemon and the connected supervisor is stale in a way that
  affects live sessions
- **THEN** the skew modal SHALL be presented for the supervisor

#### Scenario: A spawn-only supervisor mismatch at startup does not block

- **WHEN** the only skew present at launch is a supervisor spawn-surface mismatch
- **THEN** no blocking modal SHALL be presented, and the mismatch SHALL be surfaced through the
  status-bar notice

#### Scenario: Daemon check gated on pre-existing connection

- **WHEN** the TUI auto-started the daemon
- **THEN** daemon-staleness SHALL NOT be evaluated for that daemon

#### Scenario: Rich identity in the modal

- **WHEN** the skew modal is presented
- **THEN** it SHALL display the commit SHA, dirty flag, and path (or hash fallback) of the stale process

#### Scenario: The modal states what a supervisor restart buys

- **WHEN** the skew modal offers a supervisor restart
- **THEN** it SHALL also state the consequence of the supervisor's mismatch — whether running
  agents are affected or only newly started sessions

#### Scenario: Supervisor restart double-confirm

- **WHEN** the user selects "restart supervisor" in the skew modal
- **THEN** a second confirmation SHALL be shown naming the count of agents that will be interrupted, and the supervisor SHALL be restarted only on an explicit second yes

#### Scenario: Declining the supervisor restart

- **WHEN** the user declines the second supervisor-restart confirmation
- **THEN** the supervisor SHALL be left running and no agents interrupted

#### Scenario: Restarting the daemon while the supervisor is also stale leaves the modal open

- **WHEN** both the daemon and the supervisor are stale and the user chooses "Restart daemon"
- **THEN** the daemon restart SHALL fire and the skew modal SHALL remain open, now offering only "Restart supervisor" and "Skip"

#### Scenario: Restarting the supervisor resolves a pending daemon restart too

- **WHEN** both the daemon and the supervisor are stale and the user chooses "Restart supervisor"
- **THEN** the skew modal SHALL dismiss in one step (the supervisor restart also bounces the daemon), proceeding straight to the supervisor's double-confirm

#### Scenario: Single restart action still auto-dismisses

- **WHEN** only the daemon or only the supervisor is stale and the user chooses the offered restart action
- **THEN** the skew modal SHALL dismiss immediately, unchanged from prior behavior

### Requirement: Stop-hook registration diagnostic

`argus doctor` SHALL additionally report whether `~/.claude/settings.json` registers a Claude Code `Stop` hook whose command references `argus coord-hook` (the context-budget Stop hook backing `coordinator-context-management`). This check SHALL be independent of the binary-coherence table and verdict: it is printed as its own section and SHALL NOT alter `argus doctor`'s exit-code contract, which remains governed solely by the binary-coherence verdict.

The check SHALL report exactly one of three states:

- **Registered** — at least one `hooks.Stop[].hooks[].command` entry references `argus coord-hook`.
- **Not registered** — the settings file was read successfully and parsed, but no entry references `argus coord-hook`; the output SHALL include the exact JSON snippet to add.
- **Unknown** — the settings file is missing or could not be read/parsed; this SHALL be reported distinctly from "not registered" rather than assumed to mean the hook is absent.

#### Scenario: Hook registered

- **WHEN** `~/.claude/settings.json` contains a `Stop` hook entry whose command references `argus coord-hook`
- **THEN** `argus doctor` reports the hook as registered

#### Scenario: Hook not registered

- **WHEN** `~/.claude/settings.json` is readable and has no `Stop` hook entry referencing `argus coord-hook`
- **THEN** `argus doctor` reports the hook as not registered and prints the exact registration snippet

#### Scenario: Settings file unreadable degrades to unknown, not a false negative

- **WHEN** `~/.claude/settings.json` does not exist or cannot be parsed
- **THEN** `argus doctor` reports the hook status as unknown rather than "not registered"

#### Scenario: Check does not change the exit-code contract

- **WHEN** the Stop hook is not registered but the binary-coherence verdict is healthy
- **THEN** `argus doctor` still exits zero

### Requirement: Diligence-profile library diagnostic

`argus doctor` SHALL additionally report whether the per-user diligence-profile library (`~/.argus/profiles/`) contains at least one valid profile file. This check SHALL be independent of the binary-coherence table and verdict and of the Stop-hook registration diagnostic: it is printed as its own section and SHALL NOT alter `argus doctor`'s exit-code contract, which remains governed solely by the binary-coherence verdict. The check SHALL report library existence only, not any project's profile binding.

The check SHALL report exactly one of three states:

- **Found** — at least one `*.toml` file under `~/.argus/profiles/` passes profile validation.
- **None found** — the directory does not exist, is empty, or every file in it fails validation; the output SHALL include the exact remediation command.
- **Unknown** — the directory could not be listed for a reason other than nonexistence (e.g. a permission error); this SHALL be reported distinctly from "none found" rather than assumed to mean no profiles exist.

#### Scenario: Profile found

- **WHEN** `~/.argus/profiles/` contains at least one file that passes profile validation
- **THEN** `argus doctor` reports the diligence-profile library as found

#### Scenario: No profiles installed

- **WHEN** `~/.argus/profiles/` does not exist or contains no file that passes profile validation
- **THEN** `argus doctor` reports the diligence-profile library as none found and prints the `argus profiles install-defaults` remediation

#### Scenario: Library unreadable degrades to unknown, not a false negative

- **WHEN** `~/.argus/profiles/` exists but cannot be listed (e.g. permission denied)
- **THEN** `argus doctor` reports the diligence-profile-library status as unknown rather than "none found"

#### Scenario: Per-project binding is out of scope

- **WHEN** a project has no profile bound and resolves to a missing `default` profile
- **THEN** this check SHALL NOT report that project as a warning — only the library's own existence is evaluated

#### Scenario: Check does not change the exit-code contract

- **WHEN** no diligence profiles are found but the binary-coherence verdict is healthy
- **THEN** `argus doctor` still exits zero

### Requirement: Secrets bootstrap diagnostic

`argus doctor` SHALL additionally report the RESOLVED / NOT RESOLVED / NOT
CONFIGURED tri-state for the `[secrets.op]` bootstrap source (see
`secrets-resolution`'s "op bootstrap resolution status tri-state"), doing
one resolve-and-discard of `bootstrap_source` at check time. This check
SHALL be independent of the binary-coherence table and verdict and of the
Stop-hook and diligence-profile-library diagnostics: it is printed as its
own section and SHALL NOT alter `argus doctor`'s exit-code contract, which
remains governed solely by the binary-coherence verdict.

#### Scenario: Bootstrap resolves

- **WHEN** `[secrets.op].bootstrap_source` is configured and resolves
  successfully
- **THEN** `argus doctor` reports the secrets bootstrap status as RESOLVED

#### Scenario: Bootstrap configured but failing

- **WHEN** `[secrets.op].bootstrap_source` is configured but fails to
  resolve (e.g. a renamed Keychain item, or 1Password signed out)
- **THEN** `argus doctor` reports the secrets bootstrap status as NOT
  RESOLVED

#### Scenario: Secrets not configured

- **WHEN** `[secrets]` or `[secrets.op].bootstrap_source` is absent from
  configuration
- **THEN** `argus doctor` reports the secrets bootstrap status as NOT
  CONFIGURED, distinctly from NOT RESOLVED

#### Scenario: Check does not change the exit-code contract

- **WHEN** the secrets bootstrap status is NOT RESOLVED but the
  binary-coherence verdict is healthy
- **THEN** `argus doctor` still exits zero

### Requirement: Dev-stack orphan diagnostic

`argus doctor` SHALL additionally report any currently-running devbox dev-stack process (`process-compose`, `mysqld`, `redis-server`, `postgres`, or `caddy`) whose command line embeds a worktree path that no longer exists on disk. This check SHALL be independent of the binary-coherence table and verdict: it is printed as its own section and SHALL NOT alter `argus doctor`'s exit-code contract, and it SHALL NOT terminate, signal, or otherwise mutate any process it reports — it is read-only, matching every other gather step `argus doctor` performs.

The check SHALL report exactly one of three states:

- **Found** — one or more dev-stack processes reference a worktree path that no longer exists; each is listed with its PID, process name, and the missing worktree path.
- **None found** — the scan ran successfully and found no such process.
- **Unknown** — the process scan itself could not run (e.g. the scanning mechanism is unavailable on the current platform); this SHALL be reported distinctly from "none found" rather than assumed clean.

#### Scenario: Orphaned dev-stack process found

- **WHEN** a `mysqld`, `redis-server`, `postgres`, `caddy`, or `process-compose` process is running with a worktree path in its command line that no longer exists on disk
- **THEN** `argus doctor` reports it as found, listing its PID, process name, and the missing path

#### Scenario: No orphans found

- **WHEN** every dev-stack process currently running references a worktree path that still exists on disk
- **THEN** `argus doctor` reports none found

#### Scenario: Scan unavailable degrades to unknown, not a false negative

- **WHEN** the process-scanning mechanism cannot run on the current platform
- **THEN** `argus doctor` reports the check as unknown rather than "none found"

#### Scenario: Check does not change the exit-code contract

- **WHEN** orphaned dev-stack processes are found but the binary-coherence verdict is healthy
- **THEN** `argus doctor` still exits zero

#### Scenario: Check never signals a process

- **WHEN** `argus doctor` reports one or more orphaned dev-stack processes
- **THEN** none of those processes are terminated, signaled, or otherwise modified by the check itself

### Requirement: Supervisor coherence is judged on an executed-surface version, not the whole-binary hash

Supervisor staleness SHALL be judged on a declared surface version describing the observable
behavior of the code the supervisor actually executes, reported alongside the binary hash over
the existing handshake and boot-info surfaces. When the running supervisor's surface version
matches the current build's, the supervisor SHALL be reported coherent even though the two
binary hashes differ — a build that changes nothing the supervisor runs is not skew. The
whole-binary hash SHALL continue to be reported and displayed, and SHALL be used as the
fallback signal only when the supervisor reports no surface version at all (an older
supervisor), which SHALL be treated as unknown rather than stale, as it is today.

The surface version SHALL be reported as two independently-compared components, so a mismatch
can be attributed to its consequence: a **spawn** component covering the code read only when a
session starts, and a **stream** component covering the code that serves live sessions. A
spawn-component mismatch SHALL be reported as affecting only sessions started from that point
on; a stream-component mismatch SHALL be reported as affecting live sessions.

A change to any source path declared as supervisor-resident SHALL fail continuous integration
unless the corresponding surface component is also changed, so that omission is caught
mechanically rather than relying on the author remembering.

Nothing in this requirement SHALL cause a live supervisor to be restarted automatically.

#### Scenario: A build that does not change the supervisor's surface is coherent

- **GIVEN** a running supervisor whose binary hash differs from the current build's
- **WHEN** its reported surface version matches the current build's
- **THEN** the supervisor is reported coherent and no restart is suggested

#### Scenario: A supervisor that reports no surface version is unknown, never stale

- **WHEN** the running supervisor reports no surface version
- **THEN** it is reported present-but-unknown and is never treated as stale on that basis alone

#### Scenario: A spawn-only mismatch is reported as affecting new sessions only

- **WHEN** only the spawn component of the surface version differs
- **THEN** the verdict states that already-running agents are unaffected and only newly started
  sessions will use the previous build's spawn configuration

#### Scenario: A stream mismatch is reported as affecting live sessions

- **WHEN** the stream component of the surface version differs
- **THEN** the verdict states that live sessions are affected

#### Scenario: Touching supervisor-resident code without bumping the surface version fails CI

- **WHEN** a change modifies a source path declared as supervisor-resident and leaves both
  surface components unchanged
- **THEN** continuous integration fails

### Requirement: Binary skew is re-evaluated while the TUI is running

Binary skew SHALL be re-evaluated periodically while the TUI is running and on reconnection to
the daemon, not only during startup, so that a build installed after launch is discovered
without requiring a relaunch. A skew discovered after startup SHALL be surfaced through the
transient status-bar notice rather than a blocking modal, so it never interrupts work in
flight; the blocking startup modal SHALL remain the surface for skew present at launch.

#### Scenario: Skew introduced after launch is discovered

- **GIVEN** a TUI that was coherent at startup
- **WHEN** a new build is installed and the supervisor's surface version no longer matches
- **THEN** the skew is discovered without relaunching the TUI

#### Scenario: Post-startup skew does not present a blocking modal

- **WHEN** skew is discovered after startup
- **THEN** it is surfaced as a status-bar notice and no blocking modal is presented

