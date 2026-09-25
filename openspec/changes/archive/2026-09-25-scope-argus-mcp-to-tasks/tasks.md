## Implementation

- [x] Pass the actual MCP port to in-process and supervisor session launches, including restart paths.
- [x] Add Claude and Codex launch arguments for fresh and resumed sessions; preserve other MCP configurations.
- [x] Replace global Claude and Codex registration with selective cleanup of Argus-owned entries; stop writing Claude project trust.
- [x] Update README and base MCP injection specification.

## Verification

- [x] Test launch arguments for Claude and Codex, fresh and resumed, with and without an MCP listener.
- [x] Test global cleanup preserves unrelated config and is idempotent.
- [x] Run focused Go tests and the repository's required pre-PR gate.
