## Why

Codex, opencode, Antigravity and Cursor sessions got Argus's builtin routing prose prepended to their first prompt. It showed up as a large block of injected context at the top of every new task. Each of these CLIs (except Cursor) has a native way to carry instructions, so the prefix is unnecessary.

## What Changes

- Remove the prompt prefix for every backend; the delivered prompt is exactly the task's prompt.
- Deliver the same routing content natively: Pi via `--append-system-prompt`, opencode via an `instructions` entry in its inline config, Codex via a generated `AGENTS.md` in the Argus-only `CODEX_HOME` overlay, Antigravity via an always-on rule file under `~/.gemini/config/rules/`.
- Codex overlay: the user's own `AGENTS.override.md`/`AGENTS.md` is folded into the generated file and is no longer linked into the overlay.
- Supervisor spawn surface v30.

## Non-Goals

- Cursor (`cursor-agent`) receives no routing orientation: it has no per-process or file-based global instruction hook, and writing rule files into each worktree would dirty `git status`. Named follow-up if a hook appears.
- No change to Claude's `--append-system-prompt-file` delivery.

## Impact

Spec: `agent-execution`. Code: `internal/agent`, `internal/skills`, `internal/inject/antigravity`, `internal/inject/atomicfile`, `internal/daemon/surface.go`.
