## Why

PR #874 shipped embed/routing parity for `hera-review` and `hera-review-test-adversary`, but explicitly deferred a third skill from the same 2026-07-05 batch, `hera-spawn-review`: it hard-depends on `mcp__argus__profile_resolve`, an `internal/review` panel-grammar package, and diligence-profile config that did not exist on master at the time. That infrastructure has since landed via PR #873 (the model-tiering merge), which also added a fourth skill, `resolve-archetype-model` (the native-sub-agent-dispatch counterpart to `hera-spawn-review`'s pattern) — committed at `.claude/skills/resolve-archetype-model/SKILL.md` but likewise never wired into either embed mechanism.

Both blockers are now confirmed gone: `internal/mcp/profiles.go` has `toolProfileResolve` wired to the `profile_resolve` MCP tool, and `internal/review/` (`panel.go`, `knownInSessionModels`) exists on master. Neither skill is markdown-only-blocked anymore, so both can ship the same embed/routing treatment `hera-review`/`hera-review-test-adversary` already have.

## What Changes

- Copy `.claude/skills/hera-spawn-review/SKILL.md` and `.claude/skills/resolve-archetype-model/SKILL.md` (byte-identical) into `internal/skills/builtin/hera-spawn-review/SKILL.md` and `internal/skills/builtin/resolve-archetype-model/SKILL.md`, so both are embedded into the binary and guaranteed materialized via `--add-dir` for every spawned Claude session — the same guarantee `archive`/`hera`/`hera-plan`/`hera-review`/`hera-review-test-adversary` already have. `BuiltinItems()`/`EnsureBuiltinSkills()` already iterate the embedded directory generically; no code change needed beyond adding the two directories.
- Add two new embedded routing/orientation snippets, `internal/routing/builtin/hera-spawn-review.md` and `internal/routing/builtin/resolve-archetype-model.md`, mirroring the existing sections' shape (self-gated on `ARGUS_TASK_ID`/sandbox residency, short directive naming when to prefer the skill). `BuiltinContent()` already concatenates every file under the embedded root generically; no code change needed there either.
- Fix a now-stale claim in the existing `internal/routing/builtin/hera-review.md` ("`hera-spawn-review` … has not shipped yet — don't expect multi-finder behavior until it lands") that this change would otherwise leave self-contradicting alongside the new `hera-spawn-review.md` section shipping in the same PR.
- Extend `internal/skills/builtin_test.go` and `internal/routing/routing_test.go` with coverage for both new skills/sections.

## Capabilities

### Modified Capabilities

- `routing-provisioning`: gains two more embedded orientation sections (panel-review-orchestration guidance, archetype→model-resolution guidance) alongside the existing three.
- `skill-provisioning`: the embedded skill set grows from 7 to 9, adding `hera-spawn-review` and `resolve-archetype-model`.

## Impact

- **New code:** `internal/skills/builtin/hera-spawn-review/SKILL.md`, `internal/skills/builtin/resolve-archetype-model/SKILL.md` (synced copies), `internal/routing/builtin/hera-spawn-review.md`, `internal/routing/builtin/resolve-archetype-model.md`.
- **Modified code:** `internal/routing/builtin/hera-review.md` (stale-claim fix only). Neither `BuiltinContent()` nor `BuiltinItems()`/`EnsureBuiltinSkills()` require a code change — both already iterate their embedded directory trees generically.
- **Tests:** `internal/skills/builtin_test.go` gains the two new names to the expected-skills list plus description-coverage assertions; `internal/routing/routing_test.go` gains section-presence assertions for both new snippets.
- **Docs:** `context/knowledge/gotchas/misc.md` and `context/knowledge/index.md` document the parity fix.
- **Data:** none. No schema change.
- **Backwards compatibility:** fully additive.

## Risks

- **None beyond the existing pattern's own known gaps** (embed-drift between `.claude/skills/*` and `internal/skills/builtin/*` on future edits, zero test coverage for the actual `--add-dir`/materialization happy path under `isTestBinary()`) — both pre-existing, not introduced or worsened by this change.

## Spec-as-local-docs

- **Specs are LOCAL DOCS only** (`openspec/project.md`). Do NOT wire `openspec validate` into Go CI or `make`; the quality gate stays `make pre-pr`.
