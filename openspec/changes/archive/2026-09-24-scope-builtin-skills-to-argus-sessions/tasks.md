## Tasks

- [x] Materialize embedded Codex skills in an Argus-only Codex home and link ordinary Codex state and skills.
- [x] Export the isolated `CODEX_HOME` only to Argus-launched Codex processes; keep SQLite state shared.
- [x] Extend the macOS sandbox profile for the isolated home and bump the supervisor spawn surface.
- [x] Add tests for isolation, preserved user skills, child environment, and sandbox writes.
- [x] Verify with Codex prompt inspection that ordinary sessions omit Argus skills and isolated sessions list them.
- [x] Update the base OpenSpec requirements and archive this change with the implementation.
