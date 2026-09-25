## 1. Command behavior

- [x] 1.1 Add failing tests for fresh and resumed OpenCode commands, existing `--auto`, existing stored/custom OpenCode commands, and non-OpenCode commands.
- [x] 1.2 Inject `--auto` for recognized OpenCode commands in `BuildCmd`, once and before session or prompt arguments.
- [x] 1.3 Use the full OpenCode UI for the built-in backend and migrate the exact prior `mini` default, because `mini` does not honor `--auto`.

## 2. Documentation and verification

- [x] 2.1 Update the OpenCode gotcha to record launch-time auto approval and its interaction with explicit deny rules.
- [x] 2.2 Run the focused agent tests, `make test`, and `make test-cover`; check touched-package coverage.
- [x] 2.3 Archive the change in the same branch before merge, merging its requirement into the base agent-execution spec.
