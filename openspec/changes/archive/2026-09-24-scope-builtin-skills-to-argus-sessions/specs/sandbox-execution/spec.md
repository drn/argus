## MODIFIED Requirements

### Requirement: Backend auth and session persistence writes

The generated profile SHALL permit writes under `~/.local/share/argus/codex-home/` for Argus-launched Codex sessions, in addition to the existing narrow backend write paths, without permitting arbitrary writes under `$HOME` or `~/.local/share`.

#### Scenario: Isolated Codex home is writable

- **WHEN** a sandboxed Codex command writes to its Argus-only home
- **THEN** the write succeeds while unrelated home paths remain denied
