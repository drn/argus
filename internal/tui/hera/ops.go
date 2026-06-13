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
// status one rung along heraStatusLadder, clamping at the ends. Only roles have
// a status — an orchestrator-header selection is a no-op. When a WORKER role
// reaches `done` the bound task is rolled to in_review + stamped
// ready_to_close (BUG-050 parity with hera_status("done")); that roll is
// soft-fail so the status update always lands.
func (o *Ops) StepStatus(sel Selection, dir int) error {
	r := sel.Role
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

// DeleteRole removes a role row (its bindings + status cascade in M1). The
// App handles the underlying argus task + worktree separately, before calling
// this, so this is a thin row delete only.
func (o *Ops) DeleteRole(roleID int64) error {
	uxlog.Log("[hera-view] delete role row %d", roleID)
	return o.store.DeleteHeraRole(roleID)
}

// DeleteOrchestrator removes an orchestrator and (via M1 cascade) all its roles,
// bindings, and status rows. The underlying argus tasks are deliberately NOT
// deleted — they are first-class Argus entities and survive as unbound tasks
// (delete them individually from the Tasks tab). This keeps one rail keystroke
// from wiping multiple live worktrees.
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
