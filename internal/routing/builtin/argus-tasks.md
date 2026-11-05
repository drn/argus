---
tags: [argus]
audience: [shared]
---
## Argus task self-management (argus sandboxes)

If `ARGUS_TASK_ID` is unset and `$PWD` is not under `~/.argus/worktrees/`, ignore this section.

Inside an argus sandbox, an agent can finalize and schedule its OWN argus task via cwd resolution. Reach for the matching skill instead of hand-rolling raw MCP calls:

- Consult the `argus-complete` skill to mark the current task complete.
- Consult the `argus-archive` skill to move the current task into the Archive section.
- Consult the `argus-schedule` skill to create, list, or update local cron or one-shot argus tasks — anything needing local filesystem access (`~/.argus`, local DBs, dotfiles).

Completing and archiving are independent axes — one does not imply the other.

If a write fails with `Operation not permitted` on a directory the task legitimately needs, do not ask the user to edit config by hand: call `mcp__argus__sandbox_propose_write_paths` once with every needed path plus a short reason. It only records a pending request; the user must approve it in Argus, and the grant applies to NEW sessions only (the user restarts the task). You cannot approve it yourself; check status with `argus_api_get` on `/api/sandbox/proposals`.
