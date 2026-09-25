## Why

`add-nonclaude-context-parity` (archived 2026-08-31) has Argus manually read the user's global
`~/.claude/CLAUDE.md` and the repo-local `CLAUDE.md`, then prepend both to every Codex-backend
task's spawn prompt. The stated reason (design.md Decision 3 / 4) was that Codex has no native
mechanism for either: its own empirical check at the time (`codex-cli 0.139.0`, `codex debug
prompt-input`) confirmed Codex does not read `CLAUDE.md`, and separately declined to rely on a
global `~/.codex/AGENTS.md` because it was "third-party-sourced... absent from OpenAI's own
documentation... reported unreliable."

A fresh empirical check on the currently-installed `codex-cli 0.157.0` (same `codex debug
prompt-input` technique, marker-string files, run and cleaned up during this change's review)
disproves that second premise:

- Repo-local `AGENTS.md` discovery, walking from cwd up to the worktree root and concatenating
  every level found — already known, unchanged.
- **A global `~/.codex/AGENTS.md` is read unconditionally and prepended ahead of repo content**,
  separated by `--- project-doc ---`. This is not third-party speculation; it was directly observed
  in the rendered model-visible prompt.

Codex therefore already has its own native, first-party instruction-file channel at both scopes —
the same shape Claude Code's `CLAUDE.md` discovery has, just a different filename and a Codex-owned
location. Manually prepending the *Claude*-specific `~/.claude/CLAUDE.md` file into Codex's prompt
was always a workaround for a gap that, per this new evidence, doesn't exist: it duplicates content
the user would instead place in their own `~/.codex/AGENTS.md` / repo `AGENTS.md`, on every single
spawn, at real token cost, for no discovery benefit Codex doesn't already provide itself. This is
exactly the reasoning `add-nonclaude-context-parity` already applied to exclude **opencode** from
the same injection (Non-Goals: "opencode already natively reads... prepending that same content
would be pure duplication").

## What Changes

- **Remove Codex from the CLAUDE.md portion of `nonClaudeContextPrefix`.** Codex becomes symmetric
  with opencode: both backends receive only the builtin hera/routing orientation prefix; neither
  receives global or repo `CLAUDE.md` content prepended into the prompt.
- Delete the now-dead global/repo `CLAUDE.md` reading machinery (`readGlobalClaudeMD`,
  `readGlobalClaudeMDReal`, `readRepoClaudeMD`, `readClaudeMDFile`, `maxClaudeMDBytes`, the
  `readGlobalClaudeMDFn` test seam) — nothing else in the codebase calls it.
- No change to Codex's skill discovery (`$CODEX_HOME/skills/`, isolated `CODEX_HOME`) or to
  opencode/pi/Claude behavior — those are unaffected by this change.

## Impact

- Affected capability: `agent-execution` (`Non-Claude backend context prefix` requirement).
- Affected code: `internal/agent/nonclaude_context.go`, `internal/agent/nonclaude_context_test.go`,
  a comment in `internal/agent/agent.go`.
- Behavioral change: a Codex-backend task's spawn prompt no longer contains a `# Global CLAUDE.md`
  or `# Repository CLAUDE.md` section. Anyone relying on Argus force-feeding their Claude memory
  file into Codex sessions should instead maintain the equivalent content in `~/.codex/AGENTS.md`
  (global) or the repo's own `AGENTS.md` (repo-local) — files Codex already reads on its own.
