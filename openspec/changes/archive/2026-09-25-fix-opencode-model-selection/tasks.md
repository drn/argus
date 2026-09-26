# Tasks

## 1. Implementation

- [x] 1.1 Generalize the OpenCode inline-config merge helper to accept a model and skills path while preserving valid inherited keys and existing skill entries.
- [x] 1.2 Change `BuildCmd` to deliver resolved OpenCode models through `OPENCODE_CONFIG_CONTENT`, leave explicit command-level model flags alone, and keep Claude/Codex/Pi flag injection unchanged.
- [x] 1.3 Add unit coverage for fresh, resume, backend-default, profile, inline-merge, explicit-command, malformed-config, and non-OpenCode cases.

## 2. Documentation and validation

- [x] 2.1 Update the README Reference appendix and OpenCode integration gotcha to describe the v2 model-delivery mechanism.
- [x] 2.2 Run `openspec validate fix-opencode-model-selection --strict --no-interactive`.
- [x] 2.3 Run focused Go tests, then `make pre-pr`.
- [x] 2.4 Perform a real OpenCode v2 smoke check that confirms the child inline config carries the selected model without an unsupported command-line flag.

## 3. Review follow-ups

- [x] 3.1 Accept OpenCode's trailing-comma JSONC superset before unmarshalling the inherited inline config.
- [x] 3.2 Log a warning when the resolved OpenCode model is not a `provider/model` identifier instead of dropping it silently.
- [x] 3.3 Cover the non-object guard, the idempotent no-change return, and the JSONC edge cases with tests; tighten the two tests that could pass for the wrong reason.
- [x] 3.4 Reword the MCP `task_create` / `schedule_create` / `schedule_update` / `hera_spawn_worker` model descriptions and the stale gotcha bullet, which still claimed only claude/codex/pi receive a model.
- [x] 3.5 Verify against a live CLI that the inline value outranks conflicting global, project, and `.opencode/` file configs (it does, on 2.0.16), and record the finding.
