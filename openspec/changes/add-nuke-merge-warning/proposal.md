## Why

Every Hera nuke (`Ctrl+D` on a role, `Ctrl+D` cascading a coordinator's subtree, `C` clearing a hidden archive) deletes a task's worktree and both its local and remote git branch with zero visibility into whether that work was ever merged. This is the actual mechanism behind the 737-task stuck-task backlog a historical audit found: nuking with no merge-status awareness is how work silently becomes unrecoverable. `add-merge-safety-classifier` builds the shared classifier; this change is its first consumer — surfacing what it knows at the one moment a human is already making a delete decision, without ever blocking that decision.

## What Changes

- Every nuke confirmation (`heraNukeRole`'s single-role confirm, `heraCascadeNukeFrom`'s subtree confirm, `heraClearArchive`'s hidden-archive confirm) runs the merge-safety classifier's Tier A (local-only, no network) check against each task about to be reclaimed BEFORE the confirm modal opens, and folds the result into the confirm message.
- Single-role nuke: the confirm message states plainly whether the branch's work is confirmed merged, or warns that it could not be confirmed and proceeding may lose unmerged work.
- Cascade nuke and clear-archived (both already bulk/summary confirms): the existing count-based summary gains a count of how many of the reclaimed tasks are confirmed merged vs. not confirmed.
- **Never a hard block.** In every case the operator can still confirm and proceed — this is a warning, not a gate. Aaron's explicit direction: "never a hard block."
- Classification runs in a background goroutine before the confirm modal is constructed (never inline on the tview goroutine, matching this codebase's existing git-op-off-UI-thread invariant) — network calls (Tier B) are explicitly OUT of scope for this interactive path; only Tier A (local git, no `gh`) runs here, so a nuke's confirm never waits on GitHub or blocks on network latency.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the "Conservative delete semantics for multi-binding safety (area 7)" requirement gains a merge-safety-check step that runs before each nuke confirm and folds its result into the confirm message, without changing any of the requirement's existing archive/reclaim/status-advancement behavior.

## Impact

- `internal/tui/heraactions.go`: `heraOpenDelete`'s single-role branch, `heraCascadeNukeFrom`, and `heraClearArchive` each gain an async classifier call before their existing `openHeraConfirm` call. `heraNukeRole`, `heraDoCascadeNuke`, `heraReclaimAndArchiveTask`, and `heraNukeArchivedRole` themselves are unchanged — this only affects what the CONFIRM shows, never the reclaim mechanics.
- Depends on `add-merge-safety-classifier` landing first.
- No schema change, no new REST endpoint, no new MCP tool. TUI-only (the confirm-modal flow this touches has no REST/web/macOS equivalent today — Hera mutations are already TUI-only per this repo's standing, named Frontend Parity gap).
