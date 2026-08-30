## Why

Claude-backend hera workers get project context and skill access for free: Claude Code natively auto-discovers `CLAUDE.md` files, and `internal/agent.BuildCmd` additionally injects argus's builtin skills (`--add-dir`) and hera/routing orientation prose (`--append-system-prompt-file`) — but strictly gated on `IsClaudeBackend`. Codex and opencode workers get neither: their CLIs have no equivalent flags, and Argus writes nothing into their worktree to fill the gap. A Codex or opencode hera worker today starts with no repo context beyond its raw prompt and no way to discover or read an argus builtin skill, which makes it a materially weaker worker than a Claude-backed one for the identical task.

## What Changes

- For non-Claude backends only (Codex, opencode), prepend a context block to the worker's initial spawn prompt containing: the global `~/.claude/CLAUDE.md` content, the repo-local `CLAUDE.md` content (if present), the same hera/routing builtin orientation prose Claude receives via `--append-system-prompt-file`, and a skill catalog (name + one-line description per skill, not full bodies).
- Claude backend is **unchanged** — it keeps native `CLAUDE.md` discovery plus the existing `--add-dir`/`--append-system-prompt-file` injection. This change adds a parallel, prompt-based delivery path for backends that have no file-discovery or flag-injection equivalent; it does not touch the Claude path.
- Add a new MCP tool to the native argus MCP server (`internal/mcp/`) letting a worker fetch a given skill's full `SKILL.md` body on demand, since only the name+description catalog is prepended up front. Modeled loosely on the "Skills Over MCP" convention (draft MCP spec extension SEP-2640, reference implementation at skillsovermcp.com, which already supports Codex as a client): a lightweight catalog for discovery plus an on-demand full-body read, using MCP's existing tool-call primitive rather than pushing every skill body into every prompt.
- **Rejected alternative**: writing an `AGENTS.md` file into the worktree (Codex natively reads `AGENTS.md` the way Claude reads `CLAUDE.md`). Rejected on principle — the operator does not want Argus writing any file into a project's worktree that could contaminate the repo or affect any other user of the system, including via `.git/info/exclude` (still a worktree-local write, still rejected).
- **Non-goal**: the `pi` backend has no MCP injection at all (no `internal/inject/pi` package) and is out of scope for this change — pi gets neither the new prompt-prefix block nor the new MCP tool's benefit, since it can't reach argus's MCP server to begin with.

## Capabilities

### New Capabilities

(none — this change extends existing capabilities rather than introducing a new one)

### Modified Capabilities

- `agent-execution`: adds a requirement that non-Claude backend spawn prompts (Codex, opencode) are prefixed with a context block (global + repo CLAUDE.md, routing orientation, skill catalog), parallel to and independent of the existing Claude-only `--append-system-prompt-file`/`--add-dir` injection requirements, which are unchanged.
- `skill-provisioning`: adds a requirement for building a plain-text skill catalog (name + one-line description, reusing the existing `SkillItem` frontmatter data) suitable for embedding directly into a prompt, as opposed to the existing `--add-dir` materialization path which only Claude backends can consume.
- `mcp-server`: adds a new always-registered tool for reading a single skill's full `SKILL.md` body on demand by name, independent of hera wiring (mirrors the always-registered `kb_*` tools), so a non-Claude worker can pull in a skill body it decided (from the catalog) it needs.

## Impact

- `internal/agent/agent.go` (`BuildCmd`) — new non-Claude branch building the prefix block; existing Claude branch untouched.
- `internal/skills/`, `internal/routing/` — reused as-is for their existing embedded content (routing prose, skill frontmatter); a new function to render the catalog as prompt-embeddable plain text.
- `internal/mcp/server.go` (or a new `internal/mcp/skills.go`) — new tool registration + handler for on-demand skill body reads.
- No change to `internal/inject/codex` or `internal/inject/opencode` (MCP wiring already works) or to `internal/inject/pi` (does not exist; out of scope).
- No files written into any project's worktree — the entire delivery mechanism is prompt content passed in-memory to the spawned process, and an MCP tool call at runtime.
