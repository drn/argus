# Tasks — per-backend model select in the new-task modal

TDD throughout (Red → Green → Refactor). Use `internal/testutil` assertions. Gate: `make pre-pr`.

## 1. agent: per-backend known-model list

- [x] 1.1 Failing test in `internal/agent/agent_test.go` (or `models_test.go`): `KnownModels("claude")` returns `["opus","sonnet","haiku"]`; `KnownModels("/usr/local/bin/claude")` (basename detection) same; `KnownModels("codex --dangerously-bypass-approvals-and-sandbox")` returns the Codex list; `KnownModels("pi")` and `KnownModels("bash")` return empty.
- [x] 1.2 Implement `func KnownModels(command string) []string` in `internal/agent/agent.go`, keyed on the existing `IsClaudeBackend` / `IsCodexBackend` detectors (Claude → `opus`, `sonnet`, `haiku`; Codex → `gpt-5-codex`, `gpt-5`; else `nil`). Return a fresh slice each call (callers may mutate). Document that the Claude entries are the stable `claude` CLI aliases that always map to the current models, so the list does not churn per model release.
- [x] 1.3 Failing test for the override resolver: `BackendModels(config.Backend{Command:"claude"})` → built-ins; `BackendModels(config.Backend{Command:"claude", Models:["x","y"]})` → `["x","y"]`.
- [x] 1.4 Implement `func BackendModels(b config.Backend) []string` = `b.Models` when non-empty, else `KnownModels(b.Command)`.

## 2. config: optional per-backend models override

- [x] 2.1 Add `Models []string` with `toml:"models"` to `config.Backend` in `internal/config/config.go`; document it as the new-task model-select override (empty ⇒ built-in `KnownModels`). No DB column, no schema migration — it is a config.toml overlay field, parallel to the existing override layer.
- [x] 2.2 Failing test in `internal/config/file_test.go` (or wherever the overlay is tested): a `[backends.claude]` table with `models = ["opus","sonnet"]` in config.toml decodes into `Backend.Models`. Confirm `DefaultConfig()` backends leave `Models` nil (built-ins apply).

## 3. TUI form: model selector + custom fallback

- [x] 3.1 Failing tests in `internal/tui/newtaskform_test.go`:
  - `Task().Model` is `""` when the model selector is on `default`.
  - cycling the model selector to a listed model (`sonnet`) → `Task().Model == "sonnet"`.
  - selecting `custom…` then typing → `Task().Model` is the trimmed typed text.
  - changing the backend selector rebuilds the model options for the new backend and resets the selection to `default` (and clears any typed custom text).
  - a backend with empty `BackendModels` exposes only `default` + `custom…`.
- [x] 3.2 Replace the free-text model state with selector state in `NewTaskForm`: keep `modelInput`/`modelCursorPos` (now only used in custom mode); add `modelOptions []string` (resolved via `agent.BackendModels`), `modelIdx int` (0 = `default`, `1..len` = options, `len+1` = `custom…`), and a `modelCustom()` helper (`modelIdx == len(modelOptions)+1`).
- [x] 3.3 Populate `modelOptions` in `NewNewTaskForm` for the initial backend, and rebuild + reset (`modelIdx = 0`, clear `modelInput`) from `handleSelectorKey` whenever `backendIdx` changes (the existing `idx == &f.backendIdx` branch already fires there alongside `updateAutocomplete`).
- [x] 3.4 Rewrite `handleModelKey`: left/right cycle `modelIdx` over `len(modelOptions)+2` entries (wrap); up/down move focus (unchanged). When `modelCustom()`, route rune/backspace/Ctrl+W/Ctrl+U/Ctrl+K/Home/End into `modelInput` (drop the per-character left/right cursor moves — left/right are the selector cycle; Home/End still jump within the text). When not custom, runes are ignored (it is a selector).
- [x] 3.5 Rewrite `drawModelField`: when not custom, render the `◀ value ▶` selector (reuse `drawSelector` semantics, value = `default` / model / `custom…`, with the `default` row surfacing `backendDefaultModel()` as a hint when present). When custom, render the label + the existing single-line text input beneath/inline so the user can type.
- [x] 3.6 Update `Task()`: `Model` = `""` for `default`, `modelOptions[modelIdx-1]` for a listed model, `strings.TrimSpace(string(f.modelInput))` for custom.
- [x] 3.7 Update the modal-height math in `Draw` if custom mode adds a row (the field currently occupies one row; account for the optional custom-input row exactly as the autocomplete dropdowns extend `modalH`).
- [x] 3.8 `PasteHandler` `ntFieldModel` case: only accept paste when `modelCustom()` (otherwise a selector ignores paste, mirroring the backend selector's paste no-op).

## 4. Form render smoke coverage

- [x] 4.1 Extend `forms_render_test.go` / `forms_test.go`: render the form with a Claude backend and assert the model selector shows `default` initially and `◀ … ▶` affordance; flip to `custom…` and assert the text input renders.
- [x] 4.2 Confirm no new keybinding is introduced (the field reuses left/right + Tab/↑/↓), so `modal/help.go` and the README keybinding table need no edit. Note this explicitly in the PR.

## 5. Web PWA: model select

- [x] 5.1 Add `Models []string `json:"models,omitempty"`` to `backendJSON` in `internal/api/handlers.go`; populate it in `handleListBackends` via `agent.BackendModels(b)` (built-in `KnownModels` for DB-stored backends; document that config.toml `models` overrides are TUI-only since the API server reads the DB roster, not merged cfg).
- [x] 5.2 Failing API test (`internal/api/handlers_test.go`): `GET` the backends list with a seeded `claude` backend → its `models` array contains the built-in Claude aliases.
- [x] 5.3 In `internal/api/static/index.html`: replace `#create-model` text input with a `<select id="create-model-select">` + a hidden `#create-model` text input revealed only when `custom…` is chosen. Populate options from the selected backend's `models` (default / models / custom…), surfacing the backend default in the `default` option label (reuse the existing `b.model` placeholder logic at ~line 4660).
- [x] 5.4 Rebuild the model select when the backend selector changes (hook the existing backend-change handler near line 4650). On submit (~line 4777), read the value: empty for `default`, the option value for a model, the trimmed text input for `custom…`; send as `model` exactly as today.
- [x] 5.5 Bump `SW_VERSION` in `internal/api/static/sw.js` (shell asset changed).
- [x] 5.6 Add/extend a Playwright or `cmd/argus-test-server` assertion if the harness covers the create form; otherwise note manual verification.

## 6. Docs & gate

- [x] 6.1 Add a gotcha to `context/knowledge/gotchas/tasklist-ui.md` (or `web-remote.md` for the PWA half): model field is a per-backend selector that **rebuilds + resets to `default` on backend change**; `custom…` is the free-text escape; option source is `agent.BackendModels` (config `models` ⇒ built-in `KnownModels`).
- [x] 6.2 Update the README Reference appendix only if it documents the new-task model field as free-text (search; edit in place if present).
- [x] 6.3 `openspec validate new-task-model-select --strict` (local only — never wired into CI/Make).
- [x] 6.4 `make pre-pr` green; `openspec archive new-task-model-select` after merge.
