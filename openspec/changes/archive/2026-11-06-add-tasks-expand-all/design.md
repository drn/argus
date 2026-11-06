## Context

The classic Tasks list uses `expanded`/`autoExpand` to show one active project at a time. The approved persistent rail draft introduced independent folds. Aaron's clarified request replaces that rail-specific policy with the same two-mode control as the classic list.

## Goals / non-goals

A Tasks-only hotkey keeps all active projects open until toggled back, across restarts. Named web/macOS follow-ups are in the proposal.

## Decisions

- Use rebindable `tasklist.expand_all`, default `F`, in both Tasks layouts. Filter input and focused terminals receive literal F. No Settings row: like H, this is a remembered list control.
- Default false preserves classic focus-fold and explicitly changes the approved rail's initial behavior to focus-fold. All-open forces every active project open; Space/Enter folder actions cannot override it. In focus-fold selecting a project expands it and closes other active projects.
- Keep Pinned open and Archive's outer fold controlled as before in each layout. All-open opens every project inside an open Archive; toggling F does not itself expose Archive.
- Persist `ui.tasks_expand_all` with a local-only *db.DB guard, false when absent, process-local remotely. Log persistence failure without disabling the live choice.
- Preserve selection by identity; before toggle-off rebuild, derive the focused project from that selection. The jump switcher's private Tasks list copies the mode.

## Risks / trade-offs

- All-open replaces independently configurable folds → explicitly called out for approval.
- More rows affect cursor restoration/adjacency → test both layouts, filters/H, archive, and selection preservation.

## Migration plan

Absent preference means classic focus-fold. Reconcile and archive the final base requirements after the Tasks-tab rail change.
