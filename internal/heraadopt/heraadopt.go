// Package heraadopt runs the daemon-startup hera-binding reconciliation sweep.
//
// It previously also hosted the auto-adopt watcher (Milestone 4 rule D4), which
// adopted a task linked via depends_on under a coordinator's task as a worker
// role. That mechanism was retired together with the depends_on DAG (Hera
// orchestration replaced it): born-bound workers (agent.SpawnHeraWorker) create
// their role bindings transactionally at spawn time, so there is no longer any
// depends_on link to adopt across. Only the startup reconciliation survives.
package heraadopt

import (
	"errors"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// Store is the narrow DB surface ReconcileBindings needs. Satisfied by *db.DB;
// tests inject a fake to exercise the missing-task / transient-error paths
// without SQLite.
type Store interface {
	Get(id string) (*model.Task, error)
	ListHeraLiveBindings() ([]*db.HeraBinding, error)
	EndHeraBinding(bindingID int64, reason string) error
}

// ReconcileBindings is the daemon-startup sweep (risk e): a task row may be
// deleted while the daemon is down, so the delete-cascade hook that normally
// ends its bindings never fires. This walks every live binding and ends the
// ones whose argus task row no longer exists, stamping end_reason="task_missing".
// Returns the number of bindings ended. Safe to call on every boot — it is
// idempotent (an already-ended binding is no longer "live" and is skipped).
func ReconcileBindings(store Store) (int, error) {
	bindings, err := store.ListHeraLiveBindings()
	if err != nil {
		return 0, err
	}
	ended := 0
	for _, b := range bindings {
		t, err := store.Get(b.ArgusTaskID)
		switch {
		case errors.Is(err, db.ErrTaskNotFound):
			fallthrough
		case err == nil && t == nil:
			if eErr := store.EndHeraBinding(b.ID, db.HeraEndReasonTaskMissing); eErr != nil {
				uxlog.Log("[hera-adopt] reconcile: end binding %d for missing task %s: %v", b.ID, b.ArgusTaskID, eErr)
				continue
			}
			ended++
			uxlog.Log("[hera-adopt] reconcile: ended binding %d (task %s missing)", b.ID, b.ArgusTaskID)
		case err != nil:
			// Transient lookup error — leave the binding live; a later boot
			// re-runs the sweep.
			uxlog.Log("[hera-adopt] reconcile: get task %s: %v", b.ArgusTaskID, err)
		}
	}
	return ended, nil
}
