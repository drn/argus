---
name: argus-recycle
description: Reset this task's context window while continuing the same work — write a handoff, then have Argus kill this session and start a brand-new one on the identical task/worktree/branch, seeded with the handoff. Use when the conversation has grown large and a fresh context window would help, but the underlying task should continue unchanged (the manual, hand-off-aware alternative to a built-in /compact).
allowed-tools: mcp__argus__task_recycle
---

# Recycle Current Task

Reset this session's context window without losing the task's place. Unlike a built-in `/compact`, which shrinks the transcript but keeps every prior turn in scope, this ends the current session outright and starts a genuinely fresh one on the same task — same worktree, same branch, same in-progress changes — seeded with a handoff note you write now plus the task's original prompt as background.

This is a manually-triggered action. Only run it when the user asks for it, or when you yourself judge the context window has grown large enough to warrant a reset and you say so before proceeding.

**If you are a Hera-bound role** (coordinator, worker, or freelance — you'd know from your spawn orientation or a live `hera_status`/`hera_send` exchange), prefer `hera_status`'s `handoff_note` + `request_recycle: true` instead of this tool. That path builds a richer seed prompt (your role's plan-DAG/sibling state, not just your handoff note) and is self-service like this one. Reach for `task_recycle` only when you're not Hera-bound, or when Hera's recycle isn't available.

## Context

- Current directory: !`pwd`

## Your task

1. **Write a real handoff**, not a status update. Compose it as if briefing a new engineer who will pick up this exact task cold, with no memory of this conversation. Cover, concretely:
   - **Done**: what's actually finished and verified (tests passing, files changed, commits made).
   - **In progress**: what's partially done, and exactly where it was left off.
   - **Key decisions**: choices made and why, especially anything non-obvious a fresh session might otherwise second-guess or redo.
   - **Next step**: the single concrete next action — not a vague "continue the work."

   Be specific: file paths, function/variable names, command output, error messages. The fresh session will treat this note as its primary source of truth and the task's original prompt as background only — anything you don't include here is effectively lost.

2. Call the `mcp__argus__task_recycle` MCP tool with the working directory from the Context block above as `cwd`, and your handoff as `handoff_note`:

```
mcp__argus__task_recycle(cwd: "<pwd from context>", handoff_note: "<your handoff>")
```

Do **not** pass `id` — the agent has no reliable way to know it.

3. The restart does not happen until this session goes idle, so the tool call itself returns immediately. After the call, report the tool's response verbatim in one line, then stop — do not keep working. This conversation is about to end; anything done after this point will not reach the fresh session.

If the tool errors (e.g. "no task matches cwd" or a missing/oversized handoff note), show the error and stop; do not retry with a guessed or truncated argument.
