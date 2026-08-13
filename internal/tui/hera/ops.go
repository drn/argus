package hera

import (
	"fmt"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/uxlog"
)

// MutateStore is the narrow WRITE seam the thin mutation layer drives. Every
// method is an EXISTING M1 store method on *db.DB — Ops re-uses them rather than
// re-implementing any orchestrator/role/binding/status logic. The only spawn
// path (born-bound worker creation) is intentionally NOT here: it lives in the
// shared agent.SpawnHeraWorker primitive, called by the App directly (it needs
// the runner + worktree engine, which Ops does not own).
//
// BUG-022 BEDROCK RULE: the hard-delete verbs (DeleteHeraRole /
// DeleteHeraOrchestrator) are DELIBERATELY ABSENT from this interface — the
// end-of-life surface can never hard-delete a hera row. "Hide" is the archive
// toggle (reversible) and "nuke" stamps nuked_at (NukeHeraRole /
// NukeHeraOrchestrator), both of which RETAIN the row.
//
// Local mode passes the real *db.DB, which satisfies this implicitly. Remote
// mode has no *db.DB, so the App never constructs Ops there and the Hera tab's
// mutation keys are inert (see HeraPage remote-mode guards).
type MutateStore interface {
	HeraOrchestrator(id int64) (*db.HeraOrchestrator, error)
	HeraRole(id int64) (*db.HeraRole, error)

	ArchiveHeraOrchestrator(id int64) error
	UnarchiveHeraOrchestrator(id int64) error
	PinHeraOrchestrator(id int64) error
	UnpinHeraOrchestrator(id int64) error
	RenameHeraOrchestrator(id int64, newName string) error
	NukeHeraOrchestrator(id int64) error
	SetHeraOrchestratorKanbanStatus(id int64, status db.HeraKanbanStatus) error

	ArchiveHeraRole(id int64) error
	UnarchiveHeraRole(id int64) error
	PinHeraRole(id int64) error
	UnpinHeraRole(id int64) error
	RenameHeraRole(id int64, newName string) error
	NukeHeraRole(id int64) error

	HeraRoleStatusFor(roleID int64) (*db.HeraRoleStatus, error)
	UpsertHeraRoleStatus(roleID int64, status db.HeraRoleStatusValue) error
	RollHeraWorkerToReview(taskID string) (bool, error)
	ClearHeraReadyToClose(taskID string) error

	HeraLiveBindingByRole(roleID int64) (*db.HeraBinding, error)
	EndHeraBinding(bindingID int64, reason string) error
}

// Ops is the thin in-process mutation layer the Hera-view rail keys drive. Each
// method is a small adapter over one or two EXISTING M1 store calls — it adds
// the (role,orchestrator) targeting, the toggle-direction read, the status
// ladder, and uxlog instrumentation, but contains NO orchestrator/role/binding
// business logic of its own. Single source of truth: the actual writes are the
// M1 methods on *db.DB.
//
// Multi-binding isolation: every op acts on a specific role id or orchestrator
// id taken from the SELECTED Selection — never on a bare task id — so deleting
// role R in orchestrator A never touches the same task's role in orchestrator B
// (they are distinct role rows).
type Ops struct {
	store MutateStore
}

// NewOps builds the mutation layer over an M1 store.
func NewOps(store MutateStore) *Ops { return &Ops{store: store} }

// heraStatusLadder is the linear advance/revert ordering for the rail `s`/`S`
// keys. It steps the HERA ROLE STATUS (the marker the rail renders), NOT the
// argus workflow status — the argus task status is owned by the session
// lifecycle + the M4 finish policy. Reaching `done` on a worker also rolls its
// task to in_review (mirrors the hera_status("done") MCP arm, BUG-050).
var heraStatusLadder = []db.HeraRoleStatusValue{
	db.HeraStatusIdle,
	db.HeraStatusWorking,
	db.HeraStatusBlocked,
	db.HeraStatusDone,
}

func ladderIndex(s db.HeraRoleStatusValue) int {
	for i, v := range heraStatusLadder {
		if v == s {
			return i
		}
	}
	return 0 // unknown / unset → treat as idle (bottom rung)
}

// ArchiveToggle archives or unarchives the selected role (or its orchestrator
// when the cursor is on an orchestrator header). Direction is read from the
// CURRENT row state so the toggle always matches what the operator sees.
// Returns archived=true when this call just moved the row from active to
// archived (the HIDE direction) and archived=false on the un-hide direction
// or on any error – callers that need to react ONLY to the hide direction
// (e.g. heraHide's session-stop, add-hera-accept-lifecycle) read this return
// value instead of re-deriving it with a second read.
func (o *Ops) ArchiveToggle(sel Selection) (archived bool, err error) {
	if r := sel.Role; r != nil {
		cur, err := o.store.HeraRole(r.RoleID)
		if err != nil {
			uxlog.Log("[hera-view] archive role %d: read failed: %v", r.RoleID, err)
			return false, err
		}
		if cur.ArchivedAt != nil {
			uxlog.Log("[hera-view] unarchive role %d (%s) orch=%d", r.RoleID, r.Name, r.OrchID)
			return false, o.store.UnarchiveHeraRole(r.RoleID)
		}
		uxlog.Log("[hera-view] archive role %d (%s) orch=%d", r.RoleID, r.Name, r.OrchID)
		if err := o.store.ArchiveHeraRole(r.RoleID); err != nil {
			return false, err
		}
		return true, nil
	}
	if ov := sel.Orch; ov != nil {
		cur, err := o.store.HeraOrchestrator(ov.ID)
		if err != nil {
			uxlog.Log("[hera-view] archive orch %d: read failed: %v", ov.ID, err)
			return false, err
		}
		if cur.ArchivedAt != nil {
			uxlog.Log("[hera-view] unarchive orch %d (%s)", ov.ID, ov.Name)
			return false, o.store.UnarchiveHeraOrchestrator(ov.ID)
		}
		uxlog.Log("[hera-view] archive orch %d (%s)", ov.ID, ov.Name)
		if err := o.store.ArchiveHeraOrchestrator(ov.ID); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, errNoTarget
}

// PinToggle pins or unpins the selected role (or orchestrator). Direction is
// read from the current row state. Pin and archive are mutually exclusive — the
// M1 Pin verbs clear archived_at, so pinning an archived row unarchives it.
func (o *Ops) PinToggle(sel Selection) error {
	if r := sel.Role; r != nil {
		cur, err := o.store.HeraRole(r.RoleID)
		if err != nil {
			uxlog.Log("[hera-view] pin role %d: read failed: %v", r.RoleID, err)
			return err
		}
		if cur.PinnedAt != nil {
			uxlog.Log("[hera-view] unpin role %d (%s) orch=%d", r.RoleID, r.Name, r.OrchID)
			return o.store.UnpinHeraRole(r.RoleID)
		}
		uxlog.Log("[hera-view] pin role %d (%s) orch=%d", r.RoleID, r.Name, r.OrchID)
		return o.store.PinHeraRole(r.RoleID)
	}
	if ov := sel.Orch; ov != nil {
		cur, err := o.store.HeraOrchestrator(ov.ID)
		if err != nil {
			uxlog.Log("[hera-view] pin orch %d: read failed: %v", ov.ID, err)
			return err
		}
		if cur.PinnedAt != nil {
			uxlog.Log("[hera-view] unpin orch %d (%s)", ov.ID, ov.Name)
			return o.store.UnpinHeraOrchestrator(ov.ID)
		}
		uxlog.Log("[hera-view] pin orch %d (%s)", ov.ID, ov.Name)
		return o.store.PinHeraOrchestrator(ov.ID)
	}
	return errNoTarget
}

// Rename renames the selected role (or orchestrator) to newName. A name
// conflict surfaces db.ErrHeraNameConflict for the caller to show.
func (o *Ops) Rename(sel Selection, newName string) error {
	if r := sel.Role; r != nil {
		uxlog.Log("[hera-view] rename role %d (%s) → %q orch=%d", r.RoleID, r.Name, newName, r.OrchID)
		return o.store.RenameHeraRole(r.RoleID, newName)
	}
	if ov := sel.Orch; ov != nil {
		uxlog.Log("[hera-view] rename orch %d (%s) → %q", ov.ID, ov.Name, newName)
		return o.store.RenameHeraOrchestrator(ov.ID, newName)
	}
	return errNoTarget
}

// StepStatus advances (dir>0) or reverts (dir<0) the selected role's hera
// status one rung along heraStatusLadder, clamping at the ends. The target is
// sel.StatusRole(): the selected role, or — for a coordinator header selection
// (the folded coordinator has no child row) — the orchestrator's coordinator
// role, so a coordinator's `✓` cycles from the header (BUG-014). An empty
// selection or a coordinator-less header resolves to nil and is a no-op. When a
// WORKER role reaches `done` the bound task is rolled to in_review + stamped
// ready_to_close (BUG-050 parity with hera_status("done")); that roll is
// soft-fail so the status update always lands, and it is GUARDED on Kind ==
// worker so stepping a coordinator to `done` never rolls a task.
func (o *Ops) StepStatus(sel Selection, dir int) error {
	r := sel.StatusRole()
	if r == nil {
		return errNoTarget
	}
	cur := db.HeraStatusIdle
	if r.HasStatus {
		cur = r.Status
	}
	idx := ladderIndex(cur) + sign(dir)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(heraStatusLadder) {
		idx = len(heraStatusLadder) - 1
	}
	next := heraStatusLadder[idx]
	if next == cur && r.HasStatus {
		return nil // already clamped at an end
	}
	uxlog.Log("[hera-view] status step role %d (%s) %s → %s orch=%d", r.RoleID, r.Name, cur, next, r.OrchID)
	if err := o.store.UpsertHeraRoleStatus(r.RoleID, next); err != nil {
		uxlog.Log("[hera-view] status step role %d failed: %v", r.RoleID, err)
		return err
	}
	if r.Kind == db.HeraKindWorker && r.TaskID != "" {
		if next == db.HeraStatusDone {
			if flipped, rErr := o.store.RollHeraWorkerToReview(r.TaskID); rErr != nil {
				uxlog.Log("[hera-view] status(done): worker roll failed for %s (status still set): %v", r.TaskID, rErr)
			} else if flipped {
				uxlog.Log("[hera-view] status(done): rolled worker task %s to in_review", r.TaskID)
			}
		} else {
			// Stepping a worker OUT of `done` clears the ready_to_close review mark
			// (the inverse of the done-roll). Without this the mark wins the glyph
			// precedence and the rail row stays pinned to the review ✓ even though
			// the status moved — the status step is invisible (BUG-024). Soft-fail so
			// the status update always lands.
			if cErr := o.store.ClearHeraReadyToClose(r.TaskID); cErr != nil {
				uxlog.Log("[hera-view] status step: clear ready_to_close failed for %s (status still set): %v", r.TaskID, cErr)
			}
		}
	}
	return nil
}

// kanbanOrder is the rail-order cycle the m/M rail keys step through
// (add-hera-kanban-status). Deliberately its own ladder/index pair, distinct
// from heraStatusLadder/ladderIndex above: those CLAMP at both ends (s/S
// steps a role's hera_role_status); this one WRAPS at both ends (m/M steps an
// orchestrator's kanban_status) per Aaron's explicit "wrapping to active"
// instruction — overloading one shared ladder with a wrap flag would make the
// existing clamped caller (StepStatus) harder to read for no shared benefit.
var kanbanOrder = []db.HeraKanbanStatus{
	db.HeraKanbanActive,
	db.HeraKanbanBacklog,
	db.HeraKanbanBlocked,
	db.HeraKanbanDone,
}

func kanbanIndex(s db.HeraKanbanStatus) int {
	for i, v := range kanbanOrder {
		if v == s {
			return i
		}
	}
	return 0 // unknown/unset → treat as active (the default's rung)
}

// KanbanStep advances (dir>0) or reverts (dir<0) the selected TOP-LEVEL
// coordinator's kanban status one rung along kanbanOrder, WRAPPING at both
// ends (active→backlog→blocked→done→active forward; the reverse backward) —
// unlike StepStatus, which clamps. The target is sel.KanbanTarget(): an
// orchestrator HEADER selection (no role selected) that is a true root (no
// canonical parent). A role selection, a nested orchestrator header, or an
// empty selection resolve to nil and are a silent no-op (errNoTarget).
func (o *Ops) KanbanStep(sel Selection, dir int) error {
	ov := sel.KanbanTarget()
	if ov == nil {
		return errNoTarget
	}
	idx := (kanbanIndex(ov.KanbanStatus) + sign(dir) + len(kanbanOrder)) % len(kanbanOrder)
	next := kanbanOrder[idx]
	uxlog.Log("[hera-view] kanban step orch %d (%s) %s → %s", ov.ID, ov.Name, ov.KanbanStatus, next)
	if err := o.store.SetHeraOrchestratorKanbanStatus(ov.ID, next); err != nil {
		uxlog.Log("[hera-view] kanban step orch %d failed: %v", ov.ID, err)
		return err
	}
	return nil
}

// NukeRole performs the hera-side Tier-2 end-of-life for a role (BUG-022): it
// ends THIS role's live binding (stamped user_deleted so a nuked role never
// bridges a child) and marks the role row NUKED (nuked_at + archived_at) so it
// leaves the rail entirely — NEVER a hard delete. The role row, its status, its
// inbox messages, and its bound argus task all survive for DB-only recovery. The
// App owns the argus-task + worktree side (stop session, reclaim worktree, archive
// task when sole-bound) BEFORE calling this. The binding-end is soft-fail so a
// transient error still lands the nuke mark.
func (o *Ops) NukeRole(r *RoleView) error {
	if r == nil {
		return errNoTarget
	}
	if b, err := o.store.HeraLiveBindingByRole(r.RoleID); err == nil && b != nil {
		if eErr := o.store.EndHeraBinding(b.ID, db.HeraEndReasonUserDeleted); eErr != nil {
			uxlog.Log("[hera-view] nuke role %d: end binding %d failed: %v", r.RoleID, b.ID, eErr)
		}
	}
	uxlog.Log("[hera-view] nuke role %d (%s) orch=%d", r.RoleID, r.Name, r.OrchID)
	return o.store.NukeHeraRole(r.RoleID)
}

// NukeOrchestrator marks an orchestrator row NUKED (Tier-2 EOL, BUG-022) — the
// terminal DB op for the Ctrl+D / C cascade. Per the bedrock rule ("a hera row
// is never hard-deleted") this stamps nuked_at (+ archived_at), making the
// orchestrator invisible to the rail while retaining the row for DB-only
// recovery; its roles are nuked + their bindings ended individually by the
// caller (NukeRole), and the argus tasks are archived (sole-bound) or preserved
// (multi-bound). It is the nuke analog of ArchiveOrchestrator — no hard delete.
func (o *Ops) NukeOrchestrator(orchID int64) error {
	uxlog.Log("[hera-view] nuke orchestrator %d (cascade; no hard deletes)", orchID)
	return o.store.NukeHeraOrchestrator(orchID)
}

// errNoTarget is returned when an op is invoked with an empty selection (no role
// and no orchestrator). Callers treat it as a silent no-op.
var errNoTarget = fmt.Errorf("hera: no selection target")

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	default:
		return 0
	}
}
