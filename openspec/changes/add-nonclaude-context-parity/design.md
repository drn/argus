## Context

Today, `internal/agent.BuildCmd` (`internal/agent/agent.go` ~L662-723) gives Claude-backend sessions two things no other backend gets, both gated strictly on `IsClaudeBackend(backend.Command)`:

1. `--add-dir <workspace>` pointing at `internal/skills.EnsureBuiltinSkills()`'s materialized output, so Claude Code auto-loads argus's builtin skills (`.claude/skills/` under a `--add-dir` root is a documented Claude Code exception to `--add-dir` otherwise granting file access only).
2. `--append-system-prompt-file <path>` pointing at `internal/routing.EnsureBuiltinRouting()`'s materialized output — the hera/argus-task orientation prose.

On top of that, Claude Code itself natively auto-discovers `CLAUDE.md` (global `~/.claude/CLAUDE.md` and repo-local `CLAUDE.md`) with zero Argus involvement — confirmed by `internal/llm/namegen.go` deliberately running Claude from a neutral cwd specifically to *suppress* this auto-pickup for its own narrow summarization subprocess.

Codex and opencode have no CLI flags equivalent to `--add-dir`/`--append-system-prompt-file`, and no native `CLAUDE.md`-equivalent auto-discovery Argus can piggyback on (Codex reads `AGENTS.md`, but only if one exists in the worktree — see the rejected alternative below). MCP wiring is already in place and out of scope: `internal/inject/codex` and `internal/inject/opencode` already register the argus MCP server (`:7742`, native `hera_*`/`iris_*`/etc.) into both CLIs' configs at daemon startup, so a Codex or opencode worker can already call argus's MCP tools — it just starts the session with no orientation telling it those tools (or argus's conventions) exist.

The operator (Aaron) has already ruled out writing any file into the target repo's worktree to close this gap, even a `.git/info/exclude`-scoped one — see Decision 1.

## Goals / Non-Goals

**Goals:**

- Give Codex and opencode hera workers the same *content* Claude workers get today (global + repo CLAUDE.md text, hera/routing orientation, and skill discoverability), delivered without writing any file into the project's worktree.
- Let a non-Claude worker read a skill's full body on demand once it knows (from a lightweight up-front catalog) which one it needs, instead of paying the token cost of every skill body on every spawn.
- Keep the Claude-backend path completely unchanged — this is additive for non-Claude backends only.

**Non-Goals:**

- The `pi` backend. There is no `internal/inject/pi` package, so `pi` sessions cannot reach argus's MCP server at all today; adding that wiring is out of scope here. `pi` gets neither the new prompt-prefix block nor the new MCP tool.
- Writing `AGENTS.md`, `.git/info/exclude`, or any other file into the worktree (see Decision 1) — considered and explicitly rejected, not merely deferred.
- Changing the MCP config-injection mechanism itself (`internal/inject/codex`, `internal/inject/opencode`) — it already works and needs no change.
- Achieving byte-for-byte parity with Claude Code's own `CLAUDE.md` discovery semantics (e.g. Claude's directory-walk-upward behavior for nested `CLAUDE.md` files). This change reads the same two sources Argus already knows how to find (global `~/.claude/CLAUDE.md`, worktree-root `CLAUDE.md`) — it does not reimplement Claude Code's full discovery algorithm.

## Decisions

### Decision 1: Prepend to the spawn prompt, not write a file into the worktree

**Chosen:** prepend the context block directly to the plain prompt string already passed uniformly to every backend (`task.Prompt`, flowing into `BuildCmd`'s prompt-flag/positional-argument construction at `internal/agent/agent.go` ~L753-768).

**Rejected:** write an `AGENTS.md` file into the worktree. Codex natively reads `AGENTS.md` the same way Claude reads `CLAUDE.md` (confirmed via Codex's own docs), which would have been the more "native-feeling" integration and would require no prompt-size budgeting. Rejected because the operator does not want Argus writing any file into a project's worktree that could contaminate the repo or affect any other user of the system. A `.git/info/exclude`-scoped (local, untracked, git-invisible) variant was also considered and rejected on the same principle, not on implementation-risk grounds — the objection is to the write itself, not to whether it would leak into a commit.

**Consequence:** the delivery mechanism is pure in-memory prompt content (and, for skill bodies, a runtime MCP tool call) — nothing new touches disk inside the worktree. This is consistent with the existing codebase pattern: hera already prepends orientation content directly into a worker's spawn prompt today (`HeraCheckInOrientation`, `HeraSubCoordinatorOrientation` in `internal/agent/hera_spawn.go`) rather than writing it to a file, so prompt-prefixing for non-Claude context is an extension of an established Argus mechanism, not a novel one.

### Decision 2: Content sources reused verbatim, no new authoring surface

The prefix block draws from content that already exists and is already read/generated by Argus for the Claude path, so there is no new content to author or keep in sync:

- Global `~/.claude/CLAUDE.md` — read directly (new: this file is not currently read by Argus for any backend; only Claude Code itself reads it natively).
- Repo-local `CLAUDE.md` at the worktree root — read directly (same: new read, currently only Claude Code reads it natively).
- Hera/routing orientation prose — `internal/routing.BuiltinContent()` (or the already-materialized file `internal/routing.EnsureBuiltinRouting()` produces), the exact same content Claude receives via `--append-system-prompt-file`.
- Skill catalog — reuses `internal/skills.SkillItem` (`Name`, `Description`, already the parsed `SKILL.md` frontmatter) rather than inventing a second metadata format; a new rendering function turns the existing `[]SkillItem` slice into plain prompt text (name + one-line description per line), sourced from `internal/skills.BuiltinItems()` (argus's own builtin skills) at minimum, and optionally `internal/skills.LoadSkills()`'s broader project/user/plugin skill set — see Open Question 4 for what "no skills" degrades to.

### Decision 3: New MCP tool for on-demand full skill-body reads

Only the catalog (name + one-line description) goes into the prompt; a skill's full `SKILL.md` body is fetched on demand via a new MCP tool once the worker decides, from the catalog, that it needs one. This mirrors the emerging "Skills Over MCP" community convention — a draft MCP spec extension (SEP-2640, "Skills Extension") built on MCP's Resources primitive, with a live reference implementation at skillsovermcp.com that already supports Codex as an MCP client. Argus does not need to adopt that draft's exact `skill://` URI scheme or its Resources-primitive plumbing to get the validated shape: **catalog-up-front, full-body-on-demand, via a tool call** — which is a small addition on top of Argus's existing MCP tool-call primitive (no new MCP primitive needed). See Open Question 3 for where this tool should live in the tool family.

**Rejected:** inlining every skill's full body into the prefix block up front. Rejected because the total content (both CLAUDE.md files, routing prose, and N full skill bodies) would make every non-Claude spawn's first-turn context cost scale with the size of the entire skill library rather than with what the task actually needs — the exact anti-pattern the Skills-Over-MCP convention exists to avoid.

## Risks / Trade-offs

- **[Risk] Prefix block size grows unbounded per spawn** (both CLAUDE.md files, plus routing prose, plus the skill catalog, could be substantial) → **Mitigation:** this is the same total content volume Claude already loads natively per session (CLAUDE.md via its own discovery, routing prose via `--append-system-prompt-file`, skills discoverable via `--add-dir`) — so there is no *new* cost relative to Claude parity. The difference is *where* the cost is paid: Claude's Claude-Code-native CLAUDE.md read presumably doesn't count against the visible prompt/context budget the same way an explicitly-prepended block does for Codex/opencode, so the true cost may be worse than "the same," and it is paid on every non-Claude spawn (not once, the way `--add-dir` materialization is idempotent to disk). Flagged explicitly rather than resolved — see Open Question 2.
- **[Risk] Multiple spawn paths could drift** (`agent.CreateAndStart`, `startSession`, hera's `SpawnHeraWorker`, headless task creation) if the prefixing logic is duplicated per callsite → **Mitigation:** not resolved here; see Open Question 1. Whichever insertion point is chosen, it should be a single shared function, not copy-pasted per callsite, to avoid exactly this drift.
- **[Risk] A project with no `CLAUDE.md` or no skills produces an oddly-empty or malformed prefix block** → **Mitigation:** each section of the prefix block must be independently omittable — see Open Question 4. This mirrors the existing self-gating pattern the routing prose already uses (each section opens with its own applicability check) rather than emitting an empty-but-present heading.
- **[Trade-off] Reading global `~/.claude/CLAUDE.md` and repo `CLAUDE.md` from Go for the first time** (previously read only by Claude Code itself) means Argus now has an implicit content-format dependency on whatever Claude Code's own CLAUDE.md conventions are, even though nothing here parses that content — it is treated as opaque text and concatenated, not interpreted, so this dependency is shallow (read-and-paste, not parse-and-act).

## Migration Plan

No data migration. This is new command-construction and new MCP-tool-registration behavior with no schema changes and no persisted new state. Rollout is a normal code change: land the `BuildCmd` non-Claude branch and the new MCP tool together (the tool is only useful once the catalog is being prompted for), verify against a live Codex and/or opencode worker spawn, then ship. Rollback is deleting the new branch/tool — no stored state to reverse.

## Open Questions

1. **Insertion point.** Given the multiple spawn paths that can originate a fresh task (`agent.CreateAndStart`, `startSession`, hera's `SpawnHeraWorker`, headless task creation via `internal/daemon/headless.go`), where should prompt-prefixing actually happen? Candidates include a single shared helper called from deep inside `BuildCmd` itself (the one function every path funnels through to build the actual command — see `internal/agent/agent.go` ~L654 `BuildCmd`), versus wiring it per-callsite at prompt-construction time (mirroring how hera's own orientation-prefixing already happens per-callsite in `internal/agent/hera_spawn.go` rather than inside `BuildCmd`). Not resolved here — pick before implementation.
2. **Token/prompt-size cost.** The combined block (both CLAUDE.md files + routing prose + skill catalog) could be large, and unlike `--add-dir`/`--append-system-prompt-file` materialization (written to disk once, referenced by path thereafter), a prepended prompt block is paid in full on every non-Claude spawn — including every hera-gater-materialized re-spawn, if the insertion point from Open Question 1 lands somewhere that re-fires on resume. Worth measuring against a real Codex/opencode context window before shipping broadly, even though the content itself is not new relative to what Claude already loads.
3. **Where the new MCP tool lives.** Should the on-demand skill-body-read tool join the native `internal/mcp/hera_*` tool family (gated the way hera tools are, `s.heraEnabled()`), or be registered unconditionally alongside the always-on `kb_*` tools (`internal/mcp/server.go`, independent of hera wiring)? The latter seems more correct since skill access is not inherently a hera concept — a non-hera Codex/opencode task should presumably also be able to call it — but this wasn't decided by the operator and should be confirmed before implementation.
4. **Graceful omission.** What should the prefix block look like for a project with no `CLAUDE.md` at all, or no `.claude/skills/` directory (beyond argus's own always-present builtins)? Each source (global CLAUDE.md, repo CLAUDE.md, skill catalog) needs an independent "cleanly absent" behavior — likely: omit the section's heading entirely rather than emit an empty or "none found" placeholder — but the exact per-section behavior needs to be pinned down as part of implementation, and is deliberately left open here rather than assumed.
