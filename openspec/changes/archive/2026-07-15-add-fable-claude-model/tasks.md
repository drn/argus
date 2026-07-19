## 1. Implementation

- [x] 1.1 Add `"fable"` to the Claude case of `agent.KnownModels` in `internal/agent/agent.go`; update the doc comment's alias list.
- [x] 1.2 Update expected model lists in `internal/agent/models_test.go`, `internal/tui/newtaskform_test.go` (incl. entry-count comment), and `internal/api/backends_crud_test.go`.
- [x] 1.3 Update prose mentions of the Claude alias set: README `[backends.<name>]` table, `context/knowledge/gotchas/misc.md`, `context/knowledge/gotchas/tasklist-ui.md`, `internal/mcp/hera.go` `hera_spawn_worker` tool description.
- [x] 1.4 Run `make pre-pr` (build/vet/fmt-check/lint-pr/test-cover-gate green; `vuln` fails only on pre-existing stdlib CVEs, confirmed toolchain-only per `gotchas/ci-gates.md`).

## 2. Archive

- [x] 2.1 Archive this change into the base specs in the same PR.
