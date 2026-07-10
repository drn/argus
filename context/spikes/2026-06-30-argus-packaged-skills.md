# Spike: Packaging argus-specific skills inside the argus codebase

**Date:** 2026-06-30
**Question:** How can argus package/distribute its own argus-coupled skills (currently shipped via `~/.dots`) so that every task argus boots gets them automatically — without a separate repo?
**Status:** Complete

## Summary

Argus already owns three of the four hooks needed to inject Claude config into every booted task: a daemon-startup injector (`internal/inject`), a per-worktree creation hook (`OnWorktreeCreated`), and per-process env/flag control in `BuildCmd`. The cleanest fit is to **embed the argus-coupled skills in the binary via `go:embed`, materialize them on daemon startup into a managed `~/.argus/skills/.claude/skills/` tree, and append `--add-dir <that-tree>` to the `claude` invocation in `BuildCmd`.** This scopes the skills to exactly argus-booted tasks (not every global `claude` session), avoids fighting the existing `~/.claude/skills → ~/.dots` symlink, and keeps the target repo's working tree clean. The plugin route is closed (no supported programmatic install API). One behavioral claim — that `--add-dir` actually loads `.claude/skills/` from the added directory — must be verified empirically before committing (a 5-minute test); the per-worktree-inject fallback needs no such verification.

## Background

**Current state.** Argus-coupled skills (`archive`, `complete`, `argus-schedule`, `orchestrate-stack`, and the `hera`/`hera-plan` family — anything referencing `mcp__argus__*`, `ARGUS_TASK_ID`, `~/.argus`, or the hera tools) live in `~/.dots/agents/skills/<name>/SKILL.md`. `dots install agents` (`~/.dots/cli/commands/install/agents.go:23`) does `link.Soft(agents/skills, ~/.claude/skills)` — it symlinks the *entire* skills directory to `~/.claude/skills`, making every skill globally available to **all** Claude Code sessions, argus or not. So argus-specific skills are distributed by a sibling repo and are global by side effect.

**Why change.** The skills are conceptually owned by argus (they drive argus MCP tools and the hera coordination model), version-skew between argus and dots is a real hazard (a hera skill can document tool semantics the installed argus binary doesn't implement yet, or vice-versa), and a fresh argus install on a machine without dots has no argus skills at all.

**Two discovery mechanisms — don't conflate them.**
- `internal/skills/skills.go` (`LoadSkills`) is **argus's autocomplete picker only** — it scans `~/.claude/skills`, the project's `.claude/skills`, and `~/.claude/plugins/installed_plugins.json` to populate the new-task slash-command list (`internal/tui/newtaskform.go:490`, `internal/api/handlers.go:1750`). It does **not** install anything or affect what the booted agent can actually run.
- **Claude Code's own runtime discovery** is what determines whether a booted task can invoke `/archive`. Per the CLI's documented behavior, it scans (highest→lowest priority): enterprise managed dir → personal `~/.claude/skills` → project `<cwd>/.claude/skills` (walking up to repo root) → **`.claude/skills/` inside any `--add-dir` directory** → plugin skills → bundled skills. Skills are re-read fresh on each `claude` launch. **No env var or settings.json key adds extra skill roots** beyond these; `permissions.additionalDirectories` grants file access only, not skill loading.

## Findings

### Finding 1 — Argus already injects Claude config at three layers

- **Daemon startup:** `internal/daemon/daemon.go:1071` calls `inject.InjectGlobal(port)` (writes the `argus` MCP server into `~/.claude.json`) and `inject.SetClaudeProjectMcpTrust()` (writes `enableAllProjectMcpServers: true` into `~/.claude/settings.json`). Both are idempotent, atomic (temp-file + rename), and only touch their own keys. This is the natural place to also **materialize embedded skills**.
- **Per-worktree:** `agent.CreateInput.OnWorktreeCreated(wtPath)` (`internal/agent/create.go:60`) runs after the worktree exists but before the task starts. The fork path already uses it to write `.context/` files into the worktree (`internal/tui/app.go:4682`). This is the hook for the **project-local** alternative.
- **Per-process:** `BuildCmd` (`internal/agent/agent.go:556`) assembles the `claude` command string and is the single place flags are appended (permission mode, `--model`, `--resume`/`--session-id`). It is Claude-backend-scoped via `IsClaudeBackend`. `cmd.Dir = task.Worktree` (line 685) and `cmd.Env` is set (line 696), so the agent's cwd is the worktree and env is controllable. This is where `--add-dir` would be appended.

### Finding 2 — `--add-dir` is the one lever that hits "argus tasks only" without collateral

The CLI's only extra-skill-root mechanism beyond the fixed personal/project locations is `--add-dir <path>`, which (per documented behavior) grants file access **and** loads `.claude/skills/` from the added directory. Appending `--add-dir ~/.argus/skills` to the `claude` command in `BuildCmd` makes argus skills available to **exactly** the sessions argus launches — global `claude` sessions outside argus are unaffected. This is strictly better-scoped than the current dots behavior.

⚠️ **Verify first:** this is a specific behavioral claim. Quick empirical test before building:
```bash
mkdir -p /tmp/skilltest/.claude/skills/spike-probe
printf -- '---\nname: spike-probe\ndescription: probe\n---\nsay hi\n' > /tmp/skilltest/.claude/skills/spike-probe/SKILL.md
claude --add-dir /tmp/skilltest   # confirm /spike-probe is listed/usable
```
If `--add-dir` does **not** load skills, fall back to Finding 4 (per-worktree inject), which relies only on the well-established project-local `.claude/skills` discovery.

### Finding 3 — The plugin route is effectively closed for automatic install

Claude Code plugins can bundle `skills/`, and argus's picker already reads `~/.claude/plugins/installed_plugins.json` (`internal/skills/skills.go:88`). **But there is no supported API to install a plugin programmatically** — installation goes through `/plugin install <name>@<marketplace>` or `claude plugin install`, managed by the CLI's internal plugin manager. Writing `installed_plugins.json` / the plugin cache by hand is unsupported and fragile. Conclusion: argus could *author* a plugin/marketplace for users who want to opt in manually, but it cannot make plugin skills "automatic for every booted task." Reject for the automatic-distribution goal.

### Finding 4 — Per-worktree inject is the robust fallback

Writing the embedded skills into `<worktree>/.claude/skills/<name>/` inside `OnWorktreeCreated` relies only on standard project-local discovery (no flag dependency, works regardless of the `--add-dir` question). Trade-offs: (a) it pollutes the **target repo's** working tree → `git status` noise and accidental-commit risk, mitigated by appending the paths to `.git/info/exclude`; (b) N copies (one per worktree); (c) worktrees created outside the hook (or pre-existing) miss it until recreated. Acceptable as a fallback, messier than `--add-dir`.

### Finding 5 — Embedding mechanics and sandbox are non-blocking

- `go:embed` is already used in-repo (`internal/api/routes.go:10`), so the pattern is established. Natural layout: `internal/skills/builtin/<name>/SKILL.md` with `//go:embed builtin/*`, plus a `Materialize(destRoot string) error` (content-hash-gated like `InjectGlobal`, rewrite only on drift) and a `BuiltinItems() []SkillItem` that also feeds `LoadSkills` so the picker shows them.
- **Sandbox is not a constraint:** the SBPL profile is `(allow file-read*)` globally with only specific secret-dir denies (`internal/agent/sandbox.go:63`), so a skills tree anywhere readable works under sandbox. Note `~/.dots` currently has an explicit `(allow file-write* …/.dots)` (line 127) — a direct artifact of today's coupling that this change would let us **remove**.

### Finding 6 — Skill selection criterion

Migrate skills that are genuinely argus-coupled — those invoking `mcp__argus__*`, reading `ARGUS_TASK_ID`/`~/.argus`, or depending on the hera model: `archive`, `complete`, `argus-schedule`, `orchestrate-stack`, `hera`, `hera-plan`. Leave general-purpose skills (`pr`, `explore`, `debug`, etc.) in dots — they aren't argus-specific and shouldn't ship in the argus binary. A grep on `~/.claude/skills` also incidentally matches `dream`/`handoff`/`improve`/`logo` (they mention `~/.argus` or argus in passing); these are judgment calls, not clear-cut, and can stay in dots.

## Recommendation

**Proceed** with **Option A: embed + materialize-on-startup + `--add-dir`**, contingent on the Finding-2 empirical check. Concretely:
1. Move the argus-coupled SKILL.md trees into `internal/skills/builtin/` and `go:embed` them.
2. Add `skills.Materialize(root)` and call it from the daemon startup block next to `inject.InjectGlobal` (write to `~/.argus/skills/.claude/skills/`).
3. In `BuildCmd`, append `--add-dir <skillsRoot>` for Claude backends (same `IsClaudeBackend` scoping as the existing flag injections).
4. Feed `BuiltinItems()` into `LoadSkills` so the picker reflects them.
5. Update `dots install agents` to stop shipping the migrated skills (delete from `~/.dots/agents/skills/`), and drop the `~/.dots` sandbox write-allow.

If the `--add-dir` check fails, **fall back to Option B** (materialize into each `<worktree>/.claude/skills/` in `OnWorktreeCreated`, plus `.git/info/exclude`).

This is a behavioral change to argus (new flag, new startup side effect, new discovery surface) → it **must route through an `openspec/changes/<name>/` proposal** before implementation, per the repo's spec-driven workflow.

**Confidence:** High (architecture + hooks confirmed in-tree) / Medium on the `--add-dir`-loads-skills specific behavior until the probe runs.
**Effort estimate:** M (embed plumbing + one BuildCmd flag + materialize-on-startup + tests + a dots-side removal; the openspec change and `--add-dir` verification are the gating items).

## Open Questions

- ~~Does `--add-dir` actually load `.claude/skills/` from the added directory?~~ **RESOLVED (docs-confirmed):** official docs state `--add-dir`/`/add-dir` load `.claude/skills/` from the added dir as a documented exception to the file-access-only rule; live-change detection watches it too; the added path must exist before the flag is passed. Option A confirmed.
- ~~Do `hera`/`hera-plan` ship from dots or elsewhere?~~ **RESOLVED:** the argus repo already git-tracks `.claude/skills/{archive,argus-complete,argus-schedule,hera,hera-plan}` — byte-identical to the dots copies (`diff -rq` clean), i.e. hand-synced. That in-repo set is the canonical source to embed; `~/.claude/skills → ~/.dots/agents/skills` is the global symlink providing the machine-wide copies.
- Should materialized skills win over user-edited copies on every startup (always overwrite) or only seed-if-absent? **Decided in the openspec: overwrite-on-drift + mirror-exactly** (argus-owned; skew is the problem being solved). Users override via higher-precedence `~/.claude/skills`.
- Codex/pi/opencode don't honor `--add-dir` (Claude-only). Argus-coupled skills are Claude-Code slash-command constructs, so non-Claude backends getting nothing is the intended scope (documented as a non-goal in the change).
- Keep vs. drop the repo's project-local `.claude/skills/` for manual (non-argus) `claude` sessions in the argus repo — recommendation is drop-it (see design.md open decision).

## Next Steps

- [ ] Run the Finding-2 `--add-dir` probe; record the result.
- [ ] Pin the exact migrate-set (Finding 6) and confirm hera/hera-plan provenance (Open Q).
- [ ] Write an `openspec/changes/<name>/` proposal (proposal.md + delta spec + tasks.md) for the chosen option; get approval.
- [ ] Implement: `internal/skills/builtin/` + `go:embed`, `Materialize`, daemon-startup call, `BuildCmd` `--add-dir`, picker wiring, tests.
- [ ] Remove migrated skills from `~/.dots/agents/skills/` and drop the `~/.dots` sandbox write-allow; archive the openspec change in the same PR.
