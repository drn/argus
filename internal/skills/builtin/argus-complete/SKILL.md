---
name: argus-complete
description: Mark the current Argus task as complete only when the user explicitly asks to change its Argus status to Complete.
allowed-tools: mcp__argus__task_complete
---

# Mark Current Task Complete

Use this skill only when the user explicitly asks to mark the Argus task Complete. Finishing the work, merging a PR, or ending a response does not authorize a status change. This sets the task's status to `complete` and stamps `EndedAt`. It does **not** stop a running agent session — if an agent is still attached, the user should stop it separately first.

This skill is **not** the same as `/argus-archive`. `/argus-archive` moves the task into the Archive section (a visibility flag, independent of status). `/argus-complete` transitions the workflow status to `complete` only on the user's explicit request.

## Context

- Current directory: !`pwd`

## Your task

Call the `mcp__argus__task_complete` MCP tool with the working directory from the Context block above as the `cwd` argument:

```
mcp__argus__task_complete(cwd: "<pwd from context>")
```

Argus resolves the task from `cwd` by matching it against task worktree paths — the agent process does not know its own task ID, so `cwd` is the required hand-off.

Do **not** pass `id` — the agent has no reliable way to know it.

After the call, report the tool's response verbatim in one line. If the tool is unavailable or errors (e.g. "no task matches cwd"), report that and stop; do not use a CLI, HTTP endpoint, or another transport as a fallback. If the response says the task is already complete, surface that as-is and stop.
