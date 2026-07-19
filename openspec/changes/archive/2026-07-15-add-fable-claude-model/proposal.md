## Why

`agent.KnownModels` supplies the built-in curated model list for the Claude backend (`opus`, `sonnet`, `haiku`) that populates the new-task model selector (TUI, web PWA) and the REST `/api/backends` roster. Fable is now a current, stable `claude` CLI alias alongside the other three, so it belongs in that curated list — otherwise a user wanting it must fall back to the `custom…` free-text option every time, with no discoverability.

## What Changes

- **`agent.KnownModels("claude")` gains a fourth entry, `fable`,** appended after `haiku`. No change to resolution order, override behavior (`config.Backend.Models`), or the `default`/`custom…` selector semantics — this is a data-only addition to the existing curated list.
- All consumers (TUI new-task selector, web PWA `<select>`, REST `/api/backends` roster, macOS app) pick this up automatically since none hardcode the enum — they all call `agent.BackendModels`/`agent.KnownModels`.

Non-goals: changing model injection (`BuildCmd`), resolution (`agent.ResolveModel`), the selector mechanism itself, or the Codex/opencode lists.

## Capabilities

### Modified Capabilities

- `config-management`: the Claude backend's built-in curated model list now includes `fable` as a fourth stable alias.
- `forms-and-modals`: the new-task model selector's Claude option set now includes `fable`.

## Impact

- **Modified code:** `internal/agent/agent.go` (`KnownModels`, one-line change + doc comment).
- **Tests:** `internal/agent/models_test.go`, `internal/tui/newtaskform_test.go`, `internal/api/backends_crud_test.go` — expected model lists updated to include `fable`.
- **Docs:** README Reference `[backends.<name>]` table, `context/knowledge/gotchas/misc.md`, `context/knowledge/gotchas/tasklist-ui.md`, `internal/mcp/hera.go` tool description — mentions of the Claude alias set updated to include `fable`.
- **Data:** none. No schema change.
- **Backwards compatibility:** fully additive; existing `opus`/`sonnet`/`haiku` selections and any `config.Backend.Models` override are unaffected.

## Spec-as-local-docs

- **Specs are LOCAL DOCS only** (`openspec/project.md`). Do NOT wire `openspec validate` into Go CI or `make`; the quality gate stays `make pre-pr`.
