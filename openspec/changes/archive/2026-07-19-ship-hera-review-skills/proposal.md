## Why

Three review skills (`hera-review`, `hera-spawn-review`, `hera-review-test-adversary`) were authored 2026-07-05 as markdown-only `.claude/skills/*/SKILL.md` bodies, but the commit adding them (`f6ac45b5`) sat on an orphaned, never-PR'd branch and never reached master. Meanwhile PR #866/#871 shipped a spawn-time delivery mechanism — `internal/skills/builtin` (`--add-dir`) and `internal/routing/builtin` (`--append-system-prompt-file`) — for exactly this class of argus-coupled skill, retiring the old manual install-script path (#872). None of the three were ever wired into either mechanism, and two don't exist on master yet.

`hera-review`/`hera-review-test-adversary` are self-contained prose, no code dependency. `hera-spawn-review` is not: it hard-depends on `mcp__argus__profile_resolve`, an `internal/review` package, and diligence-profile config — none of which exist on master (only appears live because this session's dogfood daemon runs a composed build ahead of master). Shipping it now would ship a first-invocation failure, so it stays deferred pending that infrastructure landing separately.

## What Changes

- Cherry-pick `hera-review/SKILL.md` and `hera-review-test-adversary/SKILL.md` (markdown-only, verified no hidden Go/MCP dependency) from `f6ac45b5` onto current master, shipping them into `.claude/skills/` for the first time.
- Add a new embedded routing/orientation snippet (`internal/routing/builtin/hera-review.md`) mirroring `hera.md`/`argus-tasks.md`'s shape: a short, self-gated (`ARGUS_TASK_ID`/sandbox residency) directive telling a spawned session when to prefer `hera-review` (and its `hera-review-test-adversary` lens) over an ad hoc review pass. This closes the "discretionary skill-description matching only, never an unconditional directive" parity gap these two skills shipped with.
- Extend `internal/skills/builtin` with `hera-review/` and `hera-review-test-adversary/` (synced copies of the `.claude/skills/*/SKILL.md` bodies), so both skills are embedded into the binary and guaranteed materialized via `--add-dir` for every spawned Claude session — the same guarantee `archive`/`argus-complete`/`argus-schedule`/`hera`/`hera-plan` already have. `BuiltinItems()`/`EnsureBuiltinSkills()` already iterate the embedded directory generically, so no code change is needed there beyond adding the two directories.
- **Non-goal, explicitly deferred:** `hera-spawn-review`. Not cherry-picked, not embedded, not routed. Tracked as a named follow-up once `profile_resolve` / `internal/review` / diligence-profiles land on master.

## Capabilities

### New Capabilities

- `skill-provisioning`: first-time OpenSpec capture of the builtin skill-body embedding mechanism (`internal/skills/builtin`, shipped without spec coverage in PR #866) — scoped narrowly to the invariant this change actually depends on (the embedded set is derived generically from the embedded directory tree, so adding a skill there is sufficient for materialization), not a full retroactive spec of #866's entire behavior.

### Modified Capabilities

- `routing-provisioning`: gains a third embedded orientation section (code-review guidance) alongside the existing hera-coordination and argus-task-management sections.

## Impact

- **New code:** `.claude/skills/hera-review/SKILL.md`, `.claude/skills/hera-review-test-adversary/SKILL.md` (cherry-picked), `internal/routing/builtin/hera-review.md`, `internal/skills/builtin/hera-review/SKILL.md`, `internal/skills/builtin/hera-review-test-adversary/SKILL.md` (synced copies).
- **Modified code:** none beyond the new embedded files — `BuiltinContent()` (routing) and `BuiltinItems()`/`EnsureBuiltinSkills()` (skills) both already iterate their embedded directory trees generically.
- **Tests:** `internal/routing/routing_test.go` gains coverage asserting the new section is present in `BuiltinContent()`; a new `internal/skills/builtin_test.go` (previously absent) gains coverage asserting `BuiltinItems()` includes the two new skills with their frontmatter descriptions.
- **Docs:** `context/knowledge/gotchas/` documents the parity fix and the `hera-spawn-review` deferral.
- **Data:** none. No schema change.
- **Backwards compatibility:** fully additive.

## Risks

- **`hera-spawn-review` stays unshipped.** Anyone reading `.claude/skills/` in isolation (or the orphaned `argus/2a-skills` branch) may expect all three review skills to be a set. Mitigated by the explicit deferral note in this proposal, the gotchas doc, and by simply not cherry-picking that file.
- **Retroactive `skill-provisioning` spec is intentionally partial.** It documents the mechanism's current shape (generic directory-embed iteration) as needed to justify "no code change required" for the two new skills — it does not attempt to backfill full spec coverage for PR #866's original five skills.

## Spec-as-local-docs

- **Specs are LOCAL DOCS only** (`openspec/project.md`). Do NOT wire `openspec validate` into Go CI or `make`; the quality gate stays `make pre-pr`.
