# Design — package-argus-skills

## Confirmed mechanism

Official Claude Code docs (`code.claude.com/docs/en/skills`, "Skills from
additional directories"):

> The `--add-dir` flag and `/add-dir` command grant file access rather than
> configuration discovery, **but skills are an exception: `.claude/skills/`
> within an added directory is loaded automatically.** This exception applies
> only to `--add-dir` and `/add-dir`. The `permissions.additionalDirectories`
> setting in `settings.json` grants file access only and does not load skills.

Also confirmed: live-change detection watches `.claude/skills/` inside `--add-dir`
directories, and the CLI "validates each path exists as a directory" — so the
added path **must exist before the flag is passed**.

Precedence (highest→lowest): enterprise → personal (`~/.claude/skills`) → project
(`<cwd>/.claude/skills`, walking to repo root) → **`--add-dir` dirs** → plugin →
bundled. So argus skills load at the `--add-dir` tier: a same-named personal or
project skill shadows them. Acceptable and intentional (lets a user override).

## Layout

- **Embed root:** `internal/skills/builtin/<name>/SKILL.md` (+ any supporting
  files). `go:embed` paths cannot escape the package dir with `..`, which is why
  the sources move here rather than being embedded from the repo's `.claude/`.
- **Added dir (the `--add-dir` target):** `~/.argus/skills`. Skills live one
  level down at `~/.argus/skills/.claude/skills/<name>/` — the flag points at the
  *workspace* dir that *contains* `.claude/skills/`, not at `.claude/skills`
  itself.

## Idempotent materialization

Mirror `inject.InjectGlobal`'s discipline: compare embedded content against the
on-disk copy and rewrite only on drift; write atomically (temp + rename). Two
extra rules beyond inject:

- **Overwrite local edits.** The managed dir is argus-owned; version skew is the
  problem being solved, so the embedded copy always wins. (A user who wants a
  variant puts it in `~/.claude/skills/<name>` — higher precedence — instead.)
- **Mirror exactly.** Skill directories present on disk but absent from the
  embedded set are removed, so renaming/dropping a builtin skill doesn't leave a
  ghost. Removal is scoped to the argus-managed `.claude/skills` subtree only.

## Where materialization runs — closing the "dir must exist" race

`--add-dir` requires the path to exist at exec time, but the daemon's
inject goroutine is async and non-daemon paths (in-process TUI fallback,
headless create, supervisor) don't hit it. To cover every path with one rule:

- `BuildCmd` calls `skills.EnsureBuiltinSkills()` (idempotent, cheap — a hash
  compare over a handful of small files) immediately before deciding the flag,
  and appends `--add-dir <root>` only when it returns a valid root with no error.
  This makes existence a precondition of the flag by construction — no race, no
  reliance on startup ordering.
- The daemon MAY still call it once at startup next to `inject.InjectGlobal` as
  an eager warmup, but correctness does not depend on that call.

`BuildCmd` already has filesystem side effects (sandbox profile temp-file
generation, worktree stat), so an idempotent ensure-call there is in keeping with
the function's existing shape.

## Flag scoping

Gate on `IsClaudeBackend(backend.Command)`, exactly like the existing
permission-mode injection. `--add-dir` is repeatable, so ours is appended
additively and does not conflict with a user-supplied `--add-dir` in the backend
command template. Injected before the resume/session-id/prompt suffixes so it
precedes any `--` separator (same ordering rule the existing flags follow).

## Migration / dual-source removal

`diff -rq` shows the argus repo's `.claude/skills/archive` is byte-identical to
`~/.dots/agents/skills/archive` today. After relocation to `internal/skills/
builtin/`, the repo's `.claude/skills/` copies are removed in this PR. The
`~/.dots` copies are removed out-of-band (separate repo). During the overlap,
runtime precedence means a lingering `~/.dots`→`~/.claude/skills` symlinked copy
(personal tier) *shadows* the argus `--add-dir` copy — harmless while identical,
and the correct direction (user/global wins) if they diverge.

## Open decision (not blocking)

Keep vs. drop the repo's project-local `.claude/skills/` for people running
`claude` **manually** in the argus repo (outside argus). Recommendation: **drop
it** — `--add-dir` already covers argus-booted tasks whose worktree is the argus
repo, and a single embedded source avoids reintroducing skew. Developers who want
the skills in a manual session get them from the global set. Revisit only if
manual-in-repo ergonomics prove painful.
