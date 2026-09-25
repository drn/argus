# Design

## Decision

For a backend recognized as OpenCode, `BuildCmd` will not append a top-level `--model` flag. When model resolution produces a value and the configured command does not already contain a model flag, Argus will add the value to the child-only `OPENCODE_CONFIG_CONTENT` JSON object under the `model` key. The same merge pass adds Argus's `skills` path, so an OpenCode task receives both overrides in one environment value.

The existing `ResolveModel` precedence is unchanged:

```text
valid task override → valid profile model → backend default → no override
```

The delivery mechanism is backend-specific only. Claude, Codex, and Pi continue to receive `--model` on the command line; OpenCode receives the equivalent runtime configuration.

## Why inline configuration

OpenCode v2's full TUI rejects the top-level `--model` flag, while its configuration schema accepts `model` and the CLI documents `OPENCODE_CONFIG_CONTENT` as a runtime override. The environment value is child-scoped, so concurrent tasks with different models do not race on a shared global `opencode.json` or project file. It also composes with the existing OpenCode skills delivery instead of adding a second global mutation.

Verified live against opencode 2.0.16: a run launched with
`OPENCODE_CONFIG_CONTENT='{"model":"opencode/space-bunny-free"}'` and a deliberately conflicting `model` in a temporary global `opencode.json` recorded an assistant message on `space-bunny-free` — the inline value wins over the global file, and no `--model` appears in the launch. A second probe put a conflicting `model` in all three file-based sources at once (global `opencode.json`, project `opencode.json`, and `.opencode/opencode.json`, all confirmed as active sources via `opencode debug config`) and the inline value still won, so a repository's own OpenCode config does not silently outrank an Argus-launched task's selected model.

## Parsing tolerance

OpenCode parses the inline document as JSONC with trailing commas allowed, while Go's `encoding/json` rejects them. Argus therefore strips trailing commas outside string literals before unmarshalling, so a document OpenCode accepts is never mistaken for a malformed one (which would silently drop both the model and the skills path). Nothing else about the JSONC superset is supported: comments and other extensions still take the fail-open path.

## Model-shape honesty

OpenCode's own normalizer drops any `model` that is not `provider/model`, and it does so silently. A bare alias such as `sonnet` therefore produces a session on the CLI default with nothing on screen explaining why. Argus does not rewrite the operator's value — the launch is not failed over a string it cannot fully validate, and a future OpenCode may accept a form this check cannot predict — but it logs a warning naming the task and the value, so the mismatch is visible in `uxlog` instead of silent.

## Merge and precedence rules

- Parse inherited `OPENCODE_CONFIG_CONTENT` only when valid JSON representing an object, after tolerating the trailing commas OpenCode's JSONC parser accepts.
- Preserve every unrelated key and every existing `skills` entry.
- Add the resolved `model` only when the backend command has no model flag; the resolved value replaces an inherited inline `model` value for this child.
- Leave an existing command-level `--model` untouched and do not add a second model override.
- If inherited inline JSON is malformed, log the failure, do not replace the inherited value, and continue launching; this preserves the current fail-open skills behavior.
- If the JSON is valid but its `skills` value is not an array, preserve that user-owned value, log that Argus skills were skipped, and still deliver the model rather than losing the model to an unrelated skills-shape conflict.
- If no model and no skills override are needed, do not synthesize an environment entry.
- An explicit backend `env_vars` mapping targeting `OPENCODE_CONFIG_CONTENT` is operator intent and wins over the merged value; log the override so the loss of Argus's model/skills delivery is visible.

The environment is appended after the inherited process environment, so the child receives the merged value under normal `exec.Cmd.Env` last-value-wins semantics.

## Resume semantics

The merged model is present on both fresh and resumed launches, alongside the existing `--session` form. OpenCode owns the precedence between a configuration default and a model already stored on an existing session; this change makes the selected value available to the CLI without attempting to rewrite OpenCode's session database. Tests pin the launch contract (env present, no top-level flag) without claiming a database-level model rewrite.

## Alternatives rejected

- **Use `opencode mini`:** it accepts `--model` but ignores `--auto` in v2, breaking unattended task execution.
- **Probe the installed version and conditionally use a flag:** adds a subprocess/version cache and still leaves custom commands and future versions unresolved.
- **Write a temporary project/global config file:** introduces cleanup, precedence, and concurrent-task race hazards; the documented inline environment override already provides the required isolation.
- **Drive `/model` through the PTY after launch:** asynchronous, UI-dependent, and impossible to make deterministic for a task that is already running.
