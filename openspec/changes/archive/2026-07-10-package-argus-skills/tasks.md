# Tasks — package-argus-skills

**Design doc:** `openspec/changes/package-argus-skills/design.md`

Single PR. TDD: write failing tests from the deltas first, then implement. Use
`internal/testutil` assertions and `t.Run` subtests. Tests that resolve through
`$HOME` (materialization, `EnsureBuiltinSkills`, `BuildCmd`) MUST
`t.Setenv("HOME", t.TempDir())` first. Verify with `make test` per package during
dev and `make pre-pr` before the PR.

## 0. Relocate canonical sources

- [x] 0.1 Move the git-tracked skill dirs `archive`, `argus-complete`, `argus-schedule`, `hera`, `hera-plan` from the repo's `.claude/skills/` into `internal/skills/builtin/<name>/` (keep `SKILL.md` + any supporting files intact). This is the single source of truth going forward.
- [x] 0.2 Delete the now-empty repo `.claude/skills/` copies (per the design's open-decision recommendation). Confirm `git status` shows the move, not a duplicate.

## 1. Tests first (failing, from the deltas)

- [x] 1.1 `internal/skills` — `TestBuiltinItems`: returns one entry per embedded dir with name + description parsed from `SKILL.md` frontmatter; no filesystem/network dependency (runs against the embedded FS only).
- [x] 1.2 `internal/skills` — `TestEnsureBuiltinSkills` over `t.Setenv("HOME", t.TempDir())`: first run creates `~/.argus/skills/.claude/skills/<name>/SKILL.md` for every embedded skill and returns root `~/.argus/skills`; second run rewrites nothing (mtimes unchanged); a drifted/edited file is restored to embedded content; a stale skill dir not in the embedded set is pruned while embedded ones survive; writes are atomic (no partial file on a simulated failure if feasible).
- [x] 1.3 `internal/skills` — `TestLoadSkills_BuiltinsMerged`: builtin skills appear when absent elsewhere; a same-named entry in an `extraDir`/`~/.claude/skills` shadows the builtin (name appears once, sourced from the higher-precedence location).
- [x] 1.4 `internal/agent` — `BuildCmd` cases (`t.Setenv("HOME", t.TempDir())`): Claude backend includes `--add-dir <root>` positioned before resume/session-id/prompt suffix; codex/pi/opencode backends include no argus `--add-dir`; when materialization fails (e.g. unwritable HOME), the flag is omitted and `BuildCmd` still succeeds; the flag is additive to a backend command that already contains its own `--add-dir`.

## 2. Embed + provisioning (`internal/skills`)

**Depends on:** Stage 0, Stage 1

- [x] 2.1 Add `//go:embed builtin/*` (recursive as needed) and expose the embedded `fs.FS`.
- [x] 2.2 `BuiltinItems() []SkillItem` — enumerate embedded dirs, reuse the existing frontmatter reader for `description`.
- [x] 2.3 `EnsureBuiltinSkills() (root string, err error)` — materialize to `~/.argus/skills/.claude/skills/`, content-gated + atomic write (mirror `inject.writeJSON`'s temp+rename), overwrite-on-drift, mirror-exactly (prune stale). Resolve `~/.argus` via the same `db.DataDir()` helper the rest of the code uses.
- [x] 2.4 `LoadSkills` — merge `BuiltinItems()` at lowest precedence using the existing `seen` dedup so higher-precedence sources win.

## 3. Command construction (`internal/agent`)

**Depends on:** Stage 2

- [x] 3.1 In `BuildCmd`, for `IsClaudeBackend` commands, call `skills.EnsureBuiltinSkills()` and append `--add-dir <root>` on success; skip silently on error. Place it alongside the existing permission-mode/model injection (before the resume/session-id/prompt suffixes). Add a `uxlog.Log("[skills] ...")` line on both the materialize-success (root + skill count) and the skip-on-error paths.

## 4. Daemon warmup (optional, non-load-bearing)

**Depends on:** Stage 2

- [x] 4.1 In `internal/daemon/daemon.go`, call `skills.EnsureBuiltinSkills()` once in the startup injection goroutine next to `inject.InjectGlobal`, logging success/failure. Correctness does not depend on this (BuildCmd ensures on demand) — it just warms the cache and surfaces errors early.

## 5. Docs + knowledge

**Depends on:** Stages 2–3

- [x] 5.1 Add a `context/knowledge/gotchas/` bullet (likely `misc.md` or a new `skills.md`): the `--add-dir`-loads-`.claude/skills` exception, the `~/.argus/skills/.claude/skills/` layout (flag points at the *workspace* dir, not `.claude/skills`), overwrite-on-drift + mirror-exactly semantics, Claude-only scope, and the runtime precedence that lets personal/project skills shadow builtins.
- [x] 5.2 README Reference appendix: skipped — no existing `~/.argus/` paths table to update and not a pillar-class capability, per the docs policy.

## 6. Gate + archive (same PR, before merge)

- [x] 6.1 `make pre-pr` passes clean (build → vet → fmt-check → lint-pr → vuln → test-cover-gate); target ≥95% on `internal/skills` and touched `internal/agent` paths.
- [x] 6.2 Archive this change in the same PR: merge the `skill-provisioning` requirements into a new `openspec/specs/skill-provisioning/spec.md`, fold the `agent-execution` MODIFIED requirement into `openspec/specs/agent-execution/spec.md`, and move this folder to `openspec/changes/archive/<YYYY-MM-DD>-package-argus-skills/`.

## 7. Out-of-repo follow-up (NOT this PR — tracked here for handoff)

- [ ] 7.1 Remove `archive`, `complete`/`argus-complete`, `argus-schedule`, `hera`, `hera-plan`, `orchestrate-stack` from `~/.dots/agents/skills/` and reconcile the `complete` vs `argus-complete` naming drift.
- [ ] 7.2 After 7.1 lands and propagates, drop the `~/.dots` `(allow file-write* …/.dots)` line in `internal/agent/sandbox.go` (its own tiny change + test).
