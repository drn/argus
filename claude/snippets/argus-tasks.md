---
tags: [argus]
audience: [shared]
---
## Argus task self-management (argus sandboxes)

If `ARGUS_TASK_ID` is unset and `$PWD` is not under `~/.argus/worktrees/`, ignore this section.

Inside an argus sandbox, an agent can finalize and schedule its OWN argus task via cwd resolution. Reach for the matching skill instead of hand-rolling raw MCP calls:

- Consult the `argus-complete` skill to mark the current task complete.
- Consult the `archive` skill to move the current task into the Archive section.
- Consult the `argus-schedule` skill to create, list, or update local cron or one-shot argus tasks — anything needing local filesystem access (`~/.argus`, local DBs, dotfiles).

Completing and archiving are independent axes — one does not imply the other.
