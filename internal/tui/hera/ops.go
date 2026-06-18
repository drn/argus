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
	DeleteHeraOrchestrator(id int64) error

	ArchiveHeraRole(id int64) error
	UnarchiveHeraRole(id int64) error
	PinHeraRole(id int64) error
	UnpinHeraRole(id int64) error
	RenameHeraRole(id int64, newName string) error
	DeleteHeraRole(id int64) error

	HeraRoleStatusFor(roleID int64) (*db.HeraRoleStatus, error)
	UpsertHeraRoleStatus(roleID int64, status db.HeraRoleStatusValue) error
	RollHeraWorkerToReview(taskID string) (bool, error)

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
func (o *Ops) ArchiveToggle(sel Selection) error {
	if r := sel.Role; r != nil {
		cur, err := o.store.HeraRole(r.RoleID)
		if err != nil {
			uxlog.Log("[hera-view] archive role %d: read failed: %v", r.RoleID, err)
			return err
		}
		if cur.ArchivedAt != nil {
			uxlog.Log("[hera-view] unarchive role %d (%s) orch=%d", r.RoleID, r.Name, r.OrchID)
			return o.store.UnarchiveHeraRole(r.RoleID)
		}
		uxlog.Log("[hera-view] archive role %d (%s) orch=%d", r.RoleID, r.Name, r.OrchID)
		return o.store.ArchiveHeraRole(r.RoleID)
	}
	if ov := sel.Orch; ov != nil {
		cur, err := o.store.HeraOrchestrator(ov.ID)
		if err != nil {
			uxlog.Log("[hera-view] archive orch %d: read failed: %v", ov.ID, err)
			return err
		}
		if cur.ArchivedAt != nil {
			uxlog.Log("[hera-view] unarchive orch %d (%s)", ov.ID, ov.Name)
			return o.store.UnarchiveHeraOrchestrator(ov.ID)
		}
		uxlog.Log("[hera-view] archive orch %d (%s)", ov.ID, ov.Name)
		return o.store.ArchiveHeraOrchestrator(ov.ID)
	}
	return errNoTarget
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
	if next == db.HeraStatusDone && r.Kind == db.HeraKindWorker && r.TaskID != "" {
		if flipped, rErr := o.store.RollHeraWorkerToReview(r.TaskID); rErr != nil {
			uxlog.Log("[hera-view] status(done): worker roll failed for %s (status still set): %v", r.TaskID, rErr)
		} else if flipped {
			uxlog.Log("[hera-view] status(done): rolled worker task %s to in_review", r.TaskID)
		}
	}
	return nil
}

// RetireRole performs the hera-side end-of-life for a worker role (`R` key,
// BUG-010): it sets the role status to `done`, ends THIS role's live binding
// (stamped user_deleted so a retired worker never bridges a child), and archives
// the role row. When rollTask is set (the task is solely bound to this role) it
// also rolls the worker's task to in_review + ready_to_close, mirroring
// hera_status("done"). The App owns the argus-task side (stop session + archive
// task when sole-bound) BEFORE calling this. Each step is soft-fail except the
// final archive, so a transient binding/status error still lands the archive.
func (o *Ops) RetireRole(r *RoleView, rollTask bool) error {
	if r == nil {
		return errNoTarget
	}
	if err := o.store.UpsertHeraRoleStatus(r.RoleID, db.HeraStatusDone); err != nil {
		uxlog.Log("[hera-view] retire role %d: status set failed: %v", r.RoleID, err)
	}
	if rollTask && r.Kind == db.HeraKindWorker && r.TaskID != "" {
		if flipped, rErr := o.store.RollHeraWorkerToReview(r.TaskID); rErr != nil {
			uxlog.Log("[hera-view] retire role %d: worker roll failed for %s: %v", r.RoleID, r.TaskID, rErr)
		} else if flipped {
			uxlog.Log("[hera-view] retire role %d: rolled worker task %s to in_review", r.RoleID, r.TaskID)
		}
	}
	if b, err := o.store.HeraLiveBindingByRole(r.RoleID); err == nil && b != nil {
		if eErr := o.store.EndHeraBinding(b.ID, db.HeraEndReasonUserDeleted); eErr != nil {
			uxlog.Log("[hera-view] retire role %d: end binding %d failed: %v", r.RoleID, b.ID, eErr)
		}
	}
	uxlog.Log("[hera-view] retire role %d (%s) orch=%d (rollTask=%v)", r.RoleID, r.Name, r.OrchID, rollTask)
	return o.store.ArchiveHeraRole(r.RoleID)
}

// DeleteRole removes a role row (its bindings + status cascade in M1). The
// App handles the underlying argus task + worktree separately, before calling
// this, so this is a thin row delete only.
func (o *Ops) DeleteRole(roleID int64) error {
	uxlog.Log("[hera-view] delete role row %d", roleID)
	return o.store.DeleteHeraRole(roleID)
}

// ArchiveOrchestrator archives an orchestrator row (NOT a hard delete) — the
// terminal DB op for the Ctrl+D cascade (BUG-017). Per the Hera delete model
// ("the database doesn't get any deletes"), delete ARCHIVES every DB record and
// reclaims only the real resources (worktree/branch/session); the orchestrator
// and its roles persist in the Archive section as history. Its roles are
// archived + their bindings ended individually by the caller (RetireRole); the
// argus tasks are archived (sole-bound) or preserved (multi-bound). Unconditional
// archive (unlike ArchiveToggle, which flips on current state).
func (o *Ops) ArchiveOrchestrator(orchID int64) error {
	uxlog.Log("[hera-view] archive orchestrator %d (cascade delete; no hard deletes)", orchID)
	return o.store.ArchiveHeraOrchestrator(orchID)
}

// DeleteOrchestrator removes an orchestrator and (via M1 cascade) all its roles,
// bindings, and status rows. Used ONLY by the prune paths (`C`/`Ctrl+R`), which
// COMPLETE finished work and close emptied orchestrators — distinct from Ctrl+D
// delete, which archives (never hard-deletes). The underlying argus tasks are
// deliberately NOT deleted — they survive as unbound tasks.
func (o *Ops) DeleteOrchestrator(orchID int64) error {
	uxlog.Log("[hera-view] delete orchestrator %d (cascades roles+bindings; argus tasks preserved)", orchID)
	return o.store.DeleteHeraOrchestrator(orchID)
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
