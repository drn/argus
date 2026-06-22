## MODIFIED Requirements

### Requirement: Conservative delete semantics for multi-binding safety (area 7)

`Ctrl+D` delete in the Hera rail SHALL NEVER hard-delete a DB row. It ARCHIVES every hera record and reclaims only the real resources. Specifically, delete:

- ARCHIVES the hera role row(s) and ENDS their live binding (the same archive-not-delete primitive the `R` retire key uses), never `DeleteHeraRole`;
- ARCHIVES the orchestrator row (`ArchiveHeraOrchestrator`), never `DeleteHeraOrchestrator`;
- ARCHIVES the argus task row (`db.SetArchived`), never `db.Delete`;
- retains the role's inbox/messages — because the role is archived (not deleted), its messages stay attached to the archived role as history (no message rows are deleted, and no message-archive column is required);
- RECLAIMS only the real resources: stops the session and removes the worktree + LOCAL and REMOTE branch.

Deleting a ROLE reclaims the worktree + archives the task ONLY if that task has exactly one live binding; a MULTI-bound task is PRESERVED — left fully alone (not archived, worktree kept). The role row is archived + its binding ended either way.

Deleting a COORDINATOR / orchestrator HEADER SHALL cascade the SAME archive-and-reclaim over the full subtree rooted at the selected orchestrator (`BridgeSubtree(root)`): that orchestrator, every nested sub-coordinator, and all their agents are archived + their worktrees reclaimed. A task bound live in an orchestrator OUTSIDE the subtree is PRESERVED (left fully alone). This reverses the previous behavior, where deleting an orchestrator cascade-DELETED its hera rows and preserved every underlying argus task.

The cascade gates behind a count-bearing confirmation modal that states how many orchestrators and agents are ARCHIVED, how many worktrees + branches are RECLAIMED (including any internal-bridge worktree between two subtree orchestrators), and how many tasks are preserved. Rename and spawn-prompt use a text-input modal.

`Ctrl+D` remains the only delete key (unchanged binding). The difference from the `a` archive key: `a` archives but KEEPS the worktree/session alive; `Ctrl+D` archives AND reclaims them.

Derived from: `internal/tui/heraactions.go` (`heraOpenDelete`, `heraDeleteRole`, `heraReclaimAndArchiveTask`, `heraCascadeDeleteFrom`, `heraDoCascadeDelete`, `heraTaskBoundOutside`), `internal/tui/hera/ops.go` (`ArchiveOrchestrator`, `RetireRole`), `internal/tui/hera/model.go` (`BridgeSubtree`), `context/knowledge/gotchas/hera-view.md`.

`NOTE:` NET zero hard deletes from any hera table — the Archive section keeps the full record (roles, orchestrators, inboxes). The one remaining non-hera delete (`db.SetArchived` dropping a task's queued LEGACY `task_messages`, a different table) is established retire/archive behavior and out of scope.

#### Scenario: Deleting a sole-bound role archives the task and reclaims its worktree

- **WHEN** a role is deleted and its task has exactly one live binding
- **THEN** the session is stopped, the worktree + local and remote branch are reclaimed, and the role row, its binding, and the argus task row are ARCHIVED (none are hard-deleted)

#### Scenario: Deleting a multi-bound role preserves the task

- **WHEN** a role is deleted and its task holds live bindings in more than one orchestrator
- **THEN** the role row is archived + its binding ended; the task is left fully alone (not archived, worktree kept) and its other-orchestrator binding survives

#### Scenario: Deleting a coordinator cascade-archives the full subtree and reclaims worktrees

- **WHEN** `Ctrl+D` is pressed on a coordinator / orchestrator header and the operator confirms
- **THEN** that orchestrator, every nested sub-coordinator, and all their agents are ARCHIVED — their sessions stopped and each sole-bound task's worktree + local and remote branch reclaimed — with nothing hard-deleted (rows kept in the Archive)
- **AND** a task bound live in an orchestrator outside the subtree is preserved (left fully alone)

#### Scenario: Cascade confirm states the counts in archive/reclaim terms

- **WHEN** `Ctrl+D` is pressed on a coordinator / orchestrator header
- **THEN** a confirmation modal opens stating how many orchestrators and agents are archived, how many worktrees + branches are reclaimed (counting the internal-bridge worktree in a multi-level subtree), and how many tasks are preserved
