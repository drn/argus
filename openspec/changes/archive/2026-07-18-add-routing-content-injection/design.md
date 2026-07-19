# Design

## Context

Argus already ships two independent "get orientation content into a Claude session" mechanisms:

1. **Skill bodies** (5 skills: `archive`, `argus-complete`, `argus-schedule`, `hera`, `hera-plan`) — landed via PR #866. `internal/skills/builtin` embeds `.claude/skills/<name>/SKILL.md` (as manually-synced copies under `internal/skills/builtin/<name>/`) via `go:embed`, materializes them at spawn time to `~/.argus/skills/.claude/skills/<name>/`, and `BuildCmd` unconditionally appends `--add-dir <workspace-root>` for Claude backends. Claude Code auto-discovers `.claude/skills/` under any `--add-dir` root as a documented exception to `--add-dir` otherwise only granting file access.
2. **Routing content** (`claude/snippets/hera.md`, `claude/snippets/argus-tasks.md`) — the "when you're in an argus sandbox, coordinate via hera/iris" imperatives. Today these only reach a session if the user manually runs `install-claude-skills.sh`, which `awk`-appends them into `~/.claude/CLAUDE.md` between managed markers. This breaks silently for a user whose `CLAUDE.md` is itself compiled from a separate personal snippet pipeline (a symlink or generated file) — a recompile of *their* pipeline can silently drop argus's appended block, and the install script's own warning about this is easy to miss since it's only shown at manual-run time.

This change gives routing content the same spawn-time, no-user-action treatment skill bodies already have, using the CLI mechanism suited to *prose injected into the system prompt* rather than *a discoverable skills directory*: Claude Code's `--append-system-prompt-file <path>` flag (confirmed present in `claude --help`).

## Decision

### Package shape: `internal/routing`, mirroring `internal/skills/builtin.go`

A new package `internal/routing`, sibling to `internal/skills`:

- `internal/routing/builtin/hera.md`, `internal/routing/builtin/argus-tasks.md` — manually-synced copies of `claude/snippets/hera.md` / `claude/snippets/argus-tasks.md`, embedded via `//go:embed builtin`. This mirrors `internal/skills/builtin/<name>/SKILL.md` being a synced copy of `.claude/skills/<name>/SKILL.md` rather than a live symlink — `go:embed` cannot embed files outside its own package directory subtree (no `..` patterns), so some copy step is unavoidable given the source snippets live at repo-root `claude/snippets/`.
- **Drift guard, not silent duplication.** Unlike the skills precedent — where `internal/skills/builtin/hera/SKILL.md` has already drifted from `.claude/skills/hera/SKILL.md` (verified during this change's self-guard step, e.g. PR #863's "gate hera imperatives on role-evidence" update reached `.claude/skills/hera/SKILL.md` but not the embedded copy) with no test catching it — `internal/routing/routing_test.go` includes a test that reads `claude/snippets/hera.md` / `claude/snippets/argus-tasks.md` directly off disk (relative path from the test's package directory, plain `os.ReadFile`, no `go:embed` involved) and asserts byte-for-byte equality against the embedded copies. A snippet edit that isn't mirrored into `internal/routing/builtin/` now fails `make test`/`make pre-pr` instead of silently drifting.
- `EnsureBuiltinRouting() (string, error)` concatenates the embedded files (sorted by filename for determinism) and materializes them to a single file, `~/.argus/routing/system-prompt.md`, writing only when content differs (same `atomicWriteIfDifferent` temp-file-then-rename pattern as `skills.EnsureBuiltinSkills`). Returns the file path, suitable directly as the `--append-system-prompt-file` argument.
- Frontmatter (`---\ntags: [...]\naudience: [...]\n---`) is embedded **as-is**, unstripped — `install-claude-skills.sh`'s `append_snippet` also `cat`s the raw file including frontmatter into `CLAUDE.md` today, so this preserves exact byte-for-byte behavioral parity with the existing manual mechanism rather than introducing a new, subtly-different rendering of the same content.

### `BuildCmd` wiring: unconditional, same shape as the skills `--add-dir` block

```go
if IsClaudeBackend(backend.Command) {
    if path, err := ensureBuiltinRoutingFn(); err != nil {
        uxlog.Log("[routing] builtin routing content materialize failed (continuing without it): %v", err)
    } else if path != "" {
        cmdStr += " --append-system-prompt-file " + shellQuote(path)
    }
}
```

Placed immediately after the existing skills `--add-dir` block, same non-fatal error handling, same "additive, never gated" treatment. Not gated on `cfg.Hera.Enabled`: the injected content already self-gates on `ARGUS_TASK_ID`/`$PWD` at read time, so injecting it into a plain non-argus Claude spawn is inert prose, not a behavior change.

### The `ensureBuiltinRoutingFn` test seam

`internal/skills.EnsureBuiltinSkills()` short-circuits to `("", nil)` whenever `isTestBinary()` is true — necessary because dozens of existing `TestBuildCmd_*` cases assert an *exact* `cmdStr` string with no `HOME` override, so a real (environment-dependent) materialized path would break every one of them. `internal/routing.EnsureBuiltinRouting()` needs the identical guard for the identical reason. But that guard also means the mission's required test — "BuildCmd includes the new flag for claude backends" — can never observe the flag by calling the real, guarded function from inside `go test`.

Resolution: add a package-level function variable in `internal/agent`, following the existing `autoRenameFn` (`internal/agent/autorename.go`) / `ensurePrelaunchFn` (`internal/agent/prelaunch.go`) pattern already used in this exact package for the same class of problem (real I/O/network side effects that must be stubbable in tests):

```go
var ensureBuiltinRoutingFn = routing.EnsureBuiltinRouting

func SetEnsureBuiltinRoutingForTest(fn func() (string, error)) func() {
    old := ensureBuiltinRoutingFn
    ensureBuiltinRoutingFn = fn
    return func() { ensureBuiltinRoutingFn = old }
}
```

`BuildCmd` calls `ensureBuiltinRoutingFn()` instead of `routing.EnsureBuiltinRouting()` directly. Unstubbed (the default for all pre-existing tests), it resolves to the real, `isTestBinary()`-guarded function — so none of the ~40 existing `TestBuildCmd_*` assertions change. A new test stubs it to return a fixed fake path and asserts the flag is appended (Claude backends) or withheld (codex/pi/opencode/bare), and a further test stubs an error and asserts `BuildCmd` still succeeds without the flag.

This is a deliberate, minimal improvement over the `internal/skills.EnsureBuiltinSkills` precedent, which has no equivalent seam and consequently ships with zero test coverage for its own `--add-dir` injection in `BuildCmd`. `internal/skills` itself is untouched by this change — the new seam lives only in `internal/agent`, scoped to the routing call.

## Non-Goals

- **Retiring `install-claude-skills.sh` / `uninstall-claude-skills.sh` or the README symlink-install instructions.** Explicitly out of scope per this change's mission — a separate, dependent follow-up once this mechanism is merged and dogfooded.
- **Fixing the pre-existing drift between `internal/skills/builtin/hera/SKILL.md` and `.claude/skills/hera/SKILL.md`.** Noted as a finding (see Context above) but belongs to `internal/skills`, not this change; flagged to the coordinator separately.
- **Deduplicating routing content for a user who has both the compiled `CLAUDE.md` block and the injected system-prompt file.** Harmless duplication until the retirement follow-up lands (see proposal.md Impact).

## Risks

- **Double-delivery during the transition window.** A user who already ran `install-claude-skills.sh` gets the routing content twice (compiled into `CLAUDE.md` AND injected via `--append-system-prompt-file`) until the follow-up retirement stage removes the manual path. Content is idempotent guidance (not stateful instructions), so duplication is redundant, not contradictory or harmful.
- **`--append-system-prompt-file` flag support.** Confirmed present in `claude --help` on the deploying machine per the mission brief; not independently re-verified across all supported Claude Code CLI versions. If an older CLI lacks the flag, the spawned command would fail to start for Claude backends — no fallback is implemented, matching how `--add-dir` also assumes flag support without a version check.
