## Context

Argus ships two independent "get orientation content into a Claude session" mechanisms, both landed 2026-07-18:

1. **Skill bodies** (`internal/skills/builtin`, PR #866) — embeds 5 skills (`archive`, `argus-complete`, `argus-schedule`, `hera`, `hera-plan`) via `go:embed`, materializes them at spawn time to `~/.argus/skills/.claude/skills/<name>/`, and `BuildCmd` unconditionally appends `--add-dir <workspace-root>` for Claude backends.
2. **Routing content** (`internal/routing/builtin`, PR #871) — embeds `hera.md`/`argus-tasks.md` orientation prose via `go:embed`, materializes their concatenation to `~/.argus/routing/system-prompt.md`, and `BuildCmd` unconditionally appends `--append-system-prompt-file <path>`.

Separately, three review skills (`hera-review`, `hera-spawn-review`, `hera-review-test-adversary`) were authored 2026-07-05 on commit `f6ac45b5`, on branch `argus/2a-skills` — a large (20-commit), never-PR'd branch that also carries a much bigger, in-progress model-tiering/diligence-profiles/cross-vendor-review workstream. That branch predates both #866 and #871, so none of the three skills were ever wired into either mechanism, and the branch itself never merged — meaning two of the three don't exist on master at all.

Self-guard before touching any code (this change's own task 1) confirmed:
- `hera-review` and `hera-review-test-adversary`: pure prose, no code/tool dependency. Safe to cherry-pick standalone.
- `hera-spawn-review`: depends on `mcp__argus__profile_resolve` (backed by `internal/mcp/profiles.go`), an `internal/review` package (`panel.go`, `knownInSessionModels`), and `openspec/changes/add-cross-vendor-review/design.md` — none of which exist on master. It only appears callable in this session because the attached dogfood daemon runs a composed build ahead of master (a known pattern — see `context/knowledge/gotchas/misc.md`'s "Shared dogfood contention" note). Confirmed with the coordinator (hera message #3216) to defer it rather than ship a skill with a first-invocation failure.

## Goals / Non-Goals

**Goals:**
- Ship `hera-review` and `hera-review-test-adversary` for the first time, with full parity to the existing `archive`/`hera`/`hera-plan` treatment: an embedded skill body (guaranteed `--add-dir` delivery) AND an embedded routing directive (guaranteed `--append-system-prompt-file` delivery), not just discretionary description-matching.
- Extend the two existing embed mechanisms with the minimum change needed — no new mechanism, no whitelist array to edit (both `BuiltinItems()` and `BuiltinContent()` already iterate their embedded directory trees generically).

**Non-Goals:**
- Shipping `hera-spawn-review`. Deferred until `profile_resolve`/`internal/review`/diligence-profiles land on master via the separate model-tiering workstream. Tracked here as a named follow-up, not silently dropped.
- Building a generic glob-based routing/skill embed mechanism to replace the current hardcoded-directory-tree approach. Two more embedded files fit the existing shape; a broader mechanism change is out of scope for a parity fix.
- Retroactively producing full OpenSpec coverage for PR #866's original five skills. The `skill-provisioning` capability added here is scoped to the invariant this change depends on.
- Fixing the pre-existing drift between `internal/skills/builtin/hera/SKILL.md` and `.claude/skills/hera/SKILL.md` (noted during `add-routing-content-injection`, still unfixed). Not touched by this change.

## Decisions

### Cherry-pick markdown-only, not the whole `f6ac45b5` commit

`git show f6ac45b5:.claude/skills/hera-review/SKILL.md` (and the test-adversary equivalent) copied directly onto current master, rather than cherry-picking the commit (which would drag in the Go core / OpenSpec change folder for the deferred cross-vendor-review work, and conflict heavily given how far master has moved since the branch's base). Confirmed both files are self-contained prose with no dependency on anything else in that commit.

### `internal/routing/builtin/hera-review.md`: one new file, no wiring change

`BuiltinContent()` (`internal/routing/routing.go`) already does `fs.ReadDir(builtinFS, builtinRoot)` and concatenates every non-directory file found, sorted by name — adding `hera-review.md` alongside `argus-tasks.md`/`hera.md` is sufficient; no change to `routing.go` itself. Gated the same way as the existing two sections: `If ARGUS_TASK_ID is unset and $PWD is not under ~/.argus/worktrees/, ignore this section` — even though `hera-review` itself is a general-purpose review methodology usable in any repo (not argus-specific), the *directive* ("prefer this over ad hoc review") is an opinionated workflow nudge scoped to Aaron's own argus-sandbox usage, matching why `hera.md`/`argus-tasks.md` self-gate: not because the underlying skill is inert elsewhere, but because an unconditional opinion shouldn't leak into an unrelated user's Claude Code session in an unrelated repo.

### `internal/skills/builtin/{hera-review,hera-review-test-adversary}/`: two new directories, no wiring change

Same reasoning: `BuiltinItems()`/`EnsureBuiltinSkills()` (`internal/skills/builtin.go`) iterate `fs.ReadDir(builtinFS, builtinRoot)` generically over whatever directories exist — no hardcoded name list to extend. Adding the two directories (synced copies of the `.claude/skills/*/SKILL.md` bodies, matching how the original 5 were seeded) is the entire change.

### New `skill-provisioning` capability, scoped narrowly

No OpenSpec capability was ever created for PR #866's mechanism (it shipped without spec coverage). Rather than leave this change's `internal/skills/builtin` extension spec-less too, add a minimal `skill-provisioning` capability describing only the invariant this change relies on (generic directory-tree embedding, no per-skill whitelist) — not a full retroactive spec of #866's materialization/idempotency/`--add-dir` behavior, which belongs to a separate effort if ever undertaken.

## Risks / Trade-offs

- [Risk] A reader of the orphaned `argus/2a-skills` branch expects all three skills to ship together → Mitigated: explicit deferral noted in proposal.md, design.md, and the gotchas doc; `hera-spawn-review`'s file is simply never copied.
- [Risk] `internal/skills/builtin` still has zero test coverage for its `EnsureBuiltinSkills()` materialization path (a pre-existing gap admitted in the `add-routing-content-injection` design doc) → Not fixed here (no test seam exists, and adding one is a refactor beyond this change's scope); new tests instead target `BuiltinItems()`, which is directly testable without a seam.

## Migration Plan

Fully additive — no migration. `make pre-pr` gates the merge; no schema or data changes.

## Open Questions

None — resolved via coordinator sign-off (hera messages #3212, #3216) before implementation started.
