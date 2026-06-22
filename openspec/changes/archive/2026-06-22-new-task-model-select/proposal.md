## Why

The new-task modal's **Model** field is a free-text input. The original code comment justified this ("model names are free text; the meaningful set differs per backend and even per CLI version"), but in practice it means the user must remember and hand-type exact model identifiers (`opus`, `gpt-5-codex`, …) with no discoverability, no per-backend guidance, and easy typos that silently fall through to the CLI as an unknown `--model`. The backend selector right above it is already a clean cycling selector; the model field should match — a per-backend, dynamically-populated select, while still allowing a typed value for models the built-in list does not yet know.

This applies to both surfaces that create tasks: the TUI new-task modal (`internal/tui/newtaskform.go`) and the web PWA new-task form (`internal/api/static/index.html`).

## What Changes

- **TUI: convert the Model field from free-text to a per-backend select.** The field becomes a cycling selector (same `◀ value ▶` affordance as Backend) whose options are: `default` (empty → resolves to the backend's configured default model, or the CLI's own default) → the backend's known models → `custom…`. Selecting `custom…` reveals the existing single-line text input so a model not in the list can still be typed. Left/right cycles the selector; up/down navigates fields (unchanged).
- **Repopulate on backend change.** When the Backend selector changes, the Model option list is rebuilt for the new backend and the selection resets to `default` (the previously-typed custom value is cleared). This is the "dynamically populated" behavior — the list tracks the selected backend.
- **Model option source: built-in defaults + config override.** A new `agent.KnownModels(command)` returns a curated list per backend type — `claude` → `opus`, `sonnet`, `haiku` (the stable `claude` CLI aliases that always map to the current models); `codex` → `gpt-5-codex`, `gpt-5`; unknown/`pi`/custom backends → empty (only `default` + `custom…`). A new optional `models` field on `config.Backend` (`backends.<name>.models = [...]`) overrides the built-in list per backend, so power users extend or replace it without code changes. Resolution: configured `models` if non-empty, else `KnownModels`.
- **Web PWA: convert the `#create-model` text input to a `<select>`** populated from the same per-backend list, with the backend's default surfaced as the `default` option label and a `custom…` option that reveals a text input. The list rebuilds when the backend selector changes. The backend roster the PWA already fetches gains a `models` array per backend so the select can populate without a new endpoint.
- **Submitted value semantics are unchanged.** `default` → empty `model` (BuildCmd injects no `--model`, or the backend default); a chosen model or a typed custom value → that string, exactly as the free-text field produced today. `agent.ResolveModel` / `BuildCmd` are untouched.

Non-goals: querying a live model catalog from each CLI (no such interface exists; the list is curated + config-driven), changing model injection or resolution, or touching the Hera spawn-worker path (it has no model picker today).

## Capabilities

### Modified Capabilities

- `forms-and-modals`: The new-task form's model field becomes a per-backend select with a custom free-text fallback that repopulates when the backend changes; the submitted model value semantics are unchanged.
- `config-management`: A backend entry MAY carry an optional `models` list that supplies the new-task model select's per-backend options, overriding the built-in defaults.
- `mobile-pwa`: The web new-task form's model field becomes a per-backend `<select>` (populated from the backend roster's `models`) with a custom free-text fallback.

## Impact

- **New code:** `internal/agent` (`KnownModels(command string) []string` — curated per-backend lists keyed on `IsClaudeBackend`/`IsCodexBackend`); `config.Backend.Models []string` field.
- **Modified code:** `internal/tui/newtaskform.go` (model field state → selector + custom fallback, repopulate on backend change, draw, key handling, `Task()` resolution); `internal/api/static/index.html` (`#create-model` text → select + custom fallback, repopulate on backend change); the REST/PWA backend roster payload (add `models` per backend); `internal/api/static/sw.js` `SW_VERSION` bump (shell asset changed).
- **Docs:** `context/knowledge/gotchas/*` (model-select repopulation invariant); README Reference appendix only if a keybinding/field description is now wrong (no new keybinding is added — the field reuses the existing selector left/right + field nav, so the help modal is unaffected).
- **Data:** none. No schema change; `config.toml` `[backends.<name>].models` is additive and optional.
- **Remote mode:** the select is populated from the backend roster (config-derived), which `--remote` already receives, so it works in both local and remote TUI; the web form is served by the same API.
- **Backwards compatibility:** a backend with no `models` config and an unknown command shows only `default` + `custom…`, so any custom model is still reachable exactly as today.

## Spec-as-local-docs

- **Specs are LOCAL DOCS only** (`openspec/project.md`). Do NOT wire `openspec validate` into Go CI or `make`; the quality gate stays `make pre-pr`. Run `openspec validate new-task-model-select --strict` locally only.
