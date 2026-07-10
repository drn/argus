# Package argus-specific skills in the binary

## Why

Argus ships a set of skills that only make sense inside argus — `archive`,
`argus-complete`, `argus-schedule`, `hera`, `hera-plan` (they drive `mcp__argus__*`
tools, read `ARGUS_TASK_ID`/`~/.argus`, or encode the hera coordination model).
Today those skills reach a booted task through **two accidental paths**, neither
of which the argus binary controls:

- **The argus repo's own `.claude/skills/`** (git-tracked). Claude Code discovers
  project-local skills rooted at the session cwd, so these reach a task **only
  when that task's worktree is the argus repo itself**. A task booted in any
  other repo's worktree — the common case — never sees them.
- **A `~/.dots/agents/skills` symlink** into `~/.claude/skills`. `dots install
  agents` symlinks the whole directory, making the skills global to *every*
  `claude` session on the machine. This requires the separate `~/.dots` repo to
  be installed, and the argus-coupled skills there are **byte-identical copies**
  of the ones in the argus repo — kept in sync by hand, already drifting
  (`dots` names it `complete`, argus names it `argus-complete`; `dots` carries an
  `orchestrate-stack` the argus repo doesn't).

So a fresh argus install on a machine without `~/.dots` has no argus skills for
tasks in non-argus repos, and the dual source guarantees version skew between
what the installed binary implements and what the skill text claims.

The goal: **skills packaged by argus are compiled into the binary and made
available to every task argus boots**, regardless of which repo the worktree
belongs to, with no dependency on `~/.dots` and a single source of truth.

## What Changes

A single behavioral change built on Claude Code's documented discovery rules.
Per the official docs, `--add-dir <path>` grants file access rather than config
discovery, **"but skills are an exception: `.claude/skills/` within an added
directory is loaded automatically"** (code.claude.com/docs/en/skills). Argus
already appends flags to the `claude` command in `BuildCmd` and already injects
config on daemon startup (`internal/inject`), so this fits existing patterns.

- **Embedded builtin skills (new `skill-provisioning` capability).** The canonical
  argus-coupled skill sources move to `internal/skills/builtin/<name>/` and are
  compiled into the binary via `go:embed`. This becomes the single source of
  truth; the repo's hand-synced `.claude/skills/` copies and the `~/.dots`
  copies are retired.
- **Idempotent materialization.** Argus materializes the embedded set to a
  managed directory `~/.argus/skills/.claude/skills/<name>/` on startup. The
  managed tree mirrors the embedded set exactly — content-compare-gated (rewrite
  only on drift), overwriting local edits (argus-owned, no skew) and removing
  stale skill directories no longer embedded.
- **`--add-dir` injection (modifies `agent-execution`).** For Claude backends,
  `BuildCmd` ensures the builtin skills are materialized and appends
  `--add-dir <skills-root>`, so every argus-booted Claude session loads the argus
  skills as ordinary `/name` slash commands. Scoped to Claude backends (the flag
  is Claude-only) and skipped gracefully if materialization fails, so a
  materialization error never blocks task start.
- **Picker parity.** The new-task skill autocomplete (`internal/skills.LoadSkills`)
  includes the embedded argus skills so the picker lists what the booted task can
  actually run. They merge at the lowest precedence, matching Claude Code's
  runtime precedence (personal `~/.claude/skills` > project > added-dir), so a
  same-named personal skill still shadows the builtin one in both the picker and
  at runtime.

Non-goals:

- **No plugin/marketplace install.** Claude Code has no supported programmatic
  plugin-install API; the `--add-dir` skills exception is the supported path.
- **No non-Claude coverage.** `--add-dir` is a Claude-only flag; codex/pi/opencode
  tasks do not receive the argus skills (these are Claude-Code slash-command
  constructs, so this is the intended scope).
- **No sandbox change.** The SBPL profile already allows reads globally
  (`allow file-read*`), so the managed skills dir is readable under sandbox with
  no profile edit. (The now-obsolete `~/.dots` *write*-allow in `sandbox.go`
  becomes removable once dots no longer hosts the skills — deferred as an
  independent cleanup, not part of this change.)

## Impact

- **Affected specs:** new `skill-provisioning` capability; `agent-execution`
  (command construction gains the skills `--add-dir` flag for Claude backends).
- **Affected code:** `internal/skills/` (new `builtin/` embed + `Materialize`/
  `EnsureBuiltinSkills` + `BuiltinItems`, `LoadSkills` merges them);
  `internal/agent/agent.go` (`BuildCmd` appends `--add-dir`); `internal/daemon/
  daemon.go` (optional eager warmup call next to `inject.InjectGlobal`).
- **Retired in this PR:** the repo's git-tracked `.claude/skills/{archive,
  argus-complete,argus-schedule,hera,hera-plan}` (relocated under
  `internal/skills/builtin/`).
- **Out-of-repo follow-up (coordinated, not in this PR):** remove the migrated
  skills from `~/.dots/agents/skills/`, and later drop the `~/.dots` sandbox
  write-allow in `internal/agent/sandbox.go`.
