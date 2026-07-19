## Why

Argus's hera/iris coordination model only works if a spawned Claude session actually knows the rules: when to coordinate via hera instead of hand-rolling it, when to reach for iris for host-side git/gh, and how to self-manage its own argus task. Today that orientation content (`claude/snippets/hera.md`, `claude/snippets/argus-tasks.md`) only reaches a session if the user has run `./install-claude-skills.sh` to compile it into their `~/.claude/CLAUDE.md`. This is fragile in two ways: it is a manual step a user must remember (and re-run after any snippet update), and it actively breaks for anyone whose own global `CLAUDE.md` is itself a compiled/symlinked artifact from a separate personal snippet pipeline — the install script already detects and warns about exactly this collision.

PR #866 solved the parallel problem for skill *bodies*: `internal/skills/builtin` embeds 5 skills into the binary via `go:embed`, materializes them to `~/.argus/skills/.claude/skills/<name>/` at spawn time, and `BuildCmd` unconditionally appends `--add-dir <that root>` for Claude backends so Claude Code auto-discovers them — no install step, no user action, no symlink collision. This change builds the equivalent mechanism for the *routing* content, using Claude Code's `--append-system-prompt-file <path>` flag instead of `--add-dir`.

## What Changes

- **New `internal/routing` package** embeds the current content of `claude/snippets/hera.md` and `claude/snippets/argus-tasks.md` via `go:embed`, and materializes their concatenation idempotently to `~/.argus/routing/system-prompt.md`.
- **`BuildCmd` (`internal/agent/agent.go`) unconditionally appends `--append-system-prompt-file <materialized-path>`** for Claude backends, on every spawned session (coordinator, worker, freelance) — mirroring the unconditional `--add-dir` treatment the 5 builtin skills already get. Not gated on `cfg.Hera.Enabled` or any per-project/per-task config: the content is already self-gating (each snippet opens with "if `ARGUS_TASK_ID` is unset ... ignore this section"), so appending it to a non-argus-context spawn is harmless.
- Materialization failure is logged and non-blocking, exactly like the skills path — a session still launches without the flag rather than failing to start.

Non-goals (explicitly deferred, tracked as a follow-up): retiring `install-claude-skills.sh` / `uninstall-claude-skills.sh` or the README's symlink-install instructions. Those stay as-is until this mechanism is proven merged and dogfooded — removing the manual path is a separate, dependent stage.

## Capabilities

### Added Capabilities

- `routing-provisioning`: embeds and materializes argus's builtin hera/argus-task routing content, the injection-side counterpart to the existing `skill-provisioning` mechanism.

### Modified Capabilities

- `agent-execution`: `BuildCmd` gains an unconditional routing-content injection step for Claude backends, alongside its existing unconditional skills `--add-dir` injection.

## Impact

- **New code:** `internal/routing/routing.go` (+ embedded `internal/routing/builtin/{hera,argus-tasks}.md`), `internal/routing/routing_test.go`.
- **Modified code:** `internal/agent/agent.go` (`BuildCmd`), a small new `internal/agent/routing_prompt.go` holding a test-injectable `ensureBuiltinRoutingFn` seam (mirrors the existing `ensurePrelaunchFn` / `autoRenameFn` package-var pattern already used in this package for the same reason: real materialization must stay off in `go test` — see Risks below).
- **Tests:** new package tests for embed/materialization correctness (including a drift guard comparing the embedded copies against `claude/snippets/*.md` byte-for-byte) and `internal/agent/agent_test.go` cases proving the flag is appended for Claude backends and withheld for others.
- **Docs:** `context/knowledge/gotchas/` gets the new unconditional-injection invariant documented alongside the existing `--add-dir` one.
- **Data:** none. No schema change.
- **Backwards compatibility:** fully additive. `install-claude-skills.sh` keeps working exactly as today; a user who has already compiled the snippets into their `CLAUDE.md` will simply see the same guidance delivered twice (once via `CLAUDE.md`, once via the injected system-prompt file) until the follow-up retirement stage lands — harmless duplication, not a conflict.

## Risks

- **Test-mode gating is required, not optional.** `EnsureBuiltinRouting()` must skip real materialization when running under `go test` (mirroring `skills.EnsureBuiltinSkills`'s `isTestBinary()` guard), because dozens of existing `TestBuildCmd_*` cases assert exact `cmdStr` equality without any HOME override — a real, environment-dependent path would break them all. This makes the "happy path" (`BuildCmd` really appending the flag) untestable through the guarded production function alone, hence the `ensureBuiltinRoutingFn` test seam in `internal/agent`.

## Spec-as-local-docs

- **Specs are LOCAL DOCS only** (`openspec/project.md`). Do NOT wire `openspec validate` into Go CI or `make`; the quality gate stays `make pre-pr`.
