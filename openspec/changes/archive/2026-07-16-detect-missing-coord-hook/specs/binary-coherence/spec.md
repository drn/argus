## ADDED Requirements

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
