package hera

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// recycleWatcherInterval matches the existing ReliableNotify reconciler's
// cadence (design.md D5: "same 5s-tick cadence as the existing ReliableNotify
// reconciler") — recycle intent is a live coordinator waiting to wrap up, so
// it should be picked up about as promptly as a pane delivery is. Tests
// override via SetInterval.
const recycleWatcherInterval = 5 * time.Second

// RecycleWatcherStore is the DB surface RecycleWatcher needs beyond
// RecycleStore: a way to find every pending self-service recycle request and
// resolve which role owns it. Satisfied by the real *db.DB.
type RecycleWatcherStore interface {
	RecycleStore
	// ListMetaByNamespace returns every task's entries under namespace
	// "hera", used to find pending_recycle="true" rows in one query rather
	// than per-task.
	ListMetaByNamespace(namespace string) (map[string]map[string]string, error)
	// ListHeraLiveBindingsByTask returns every live binding for a task —
	// used instead of the single-binding HeraLiveBindingByTask so a task
	// holding 2+ live bindings (bound in more than one orchestrator) doesn't
	// make the watcher error out on ErrHeraAmbiguous; the watcher scans for
	// the coordinator-kind one itself (see tickTask).
	ListHeraLiveBindingsByTask(taskID string) ([]*db.HeraBinding, error)
	// Get returns the task row, used to read its current SessionID —
	// RecycleCoord's stray-job-cleanup key.
	Get(taskID string) (*model.Task, error)
}

// RecycleWatcher polls the DB for coordinators with a pending self-service
// recycle request (hera_status request_recycle=true) and drives them through
// RecycleCoord — deferring the actual kill-and-restart until each one's
// session goes idle. Mirrors internal/heragater.Watcher's shape: configure via
// New/SetInterval, run via Start (blocking, call in a goroutine), stop via
// Stop.
type RecycleWatcher struct {
	store  RecycleWatcherStore
	runner RecycleRunner

	interval time.Duration
	stopCh   chan struct{}
	mu       sync.Mutex
}

// NewRecycleWatcher builds a RecycleWatcher. It does not tick until Start is
// called.
func NewRecycleWatcher(store RecycleWatcherStore, runner RecycleRunner) *RecycleWatcher {
	return &RecycleWatcher{
		store:    store,
		runner:   runner,
		interval: recycleWatcherInterval,
		stopCh:   make(chan struct{}),
	}
}

// Start runs the watcher loop until Stop. Blocks; call in a goroutine.
func (w *RecycleWatcher) Start() {
	slog.Info("[hera] recycle watcher starting", "interval", w.interval)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			slog.Info("[hera] recycle watcher stopped")
			return
		case <-ticker.C:
			w.Tick()
		}
	}
}

// Stop signals Start to exit. Safe to call multiple times.
func (w *RecycleWatcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.stopCh:
	default:
		close(w.stopCh)
	}
}

// SetInterval overrides the tick interval (test-only; no effect on a running loop).
func (w *RecycleWatcher) SetInterval(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.interval = d
}

// Tick runs one sweep: every task_meta(hera, pending_recycle="true") row
// names a coordinator waiting to recycle. Each is driven through RecycleCoord
// with RecycleSelfService, which is itself the idle gate — a busy coordinator
// simply gets no-op'd this tick and picked up again on the next one. Errors
// are logged, not propagated, so one bad row can never wedge the sweep.
func (w *RecycleWatcher) Tick() {
	byTask, err := w.store.ListMetaByNamespace(db.HeraMetaNamespace)
	if err != nil {
		slog.Warn("[hera] recycle watcher: list meta failed", "err", err)
		return
	}

	for taskID, entries := range byTask {
		if entries[db.HeraMetaKeyPendingRecycle] != "true" {
			continue
		}
		if err := w.tickTask(taskID); err != nil {
			slog.Warn("[hera] recycle watcher: tick failed", "task", taskID, "err", err)
		}
	}
}

// tickTask resolves the role bound to taskID whose pending-recycle request
// should be driven through RecycleCoord (add-worker-bounce: any hera-bound
// role kind, not just coordinator). Uses ListHeraLiveBindingsByTask (not the
// single-binding HeraLiveBindingByTask) because a task legitimately holds 2+
// live bindings when it's joined more than one orchestrator (e.g. a nested
// sub-coordinator bound as coordinator of its own team while also live under
// its parent's) — HeraLiveBindingByTask would return ErrHeraAmbiguous for
// that task and leave a real pending-recycle request stuck retrying forever.
// Bindings are ordered oldest-first: the coordinator-kind one is preferred
// when the task holds one among 2+ live bindings (mirrors the
// single-coordinator-per-task assumption the rest of this primitive already
// makes); otherwise the first binding found is used, since a task with no
// coordinator binding has at most one live binding to recycle anyway.
func (w *RecycleWatcher) tickTask(taskID string) error {
	bindings, err := w.store.ListHeraLiveBindingsByTask(taskID)
	if err != nil {
		return fmt.Errorf("list bindings: %w", err)
	}
	if len(bindings) == 0 {
		return nil
	}
	roleID := bindings[0].RoleID
	for _, b := range bindings {
		role, err := w.store.HeraRole(b.RoleID)
		if err != nil {
			return fmt.Errorf("resolve role %d: %w", b.RoleID, err)
		}
		if role.Kind == db.HeraKindCoordinator {
			roleID = role.ID
			break
		}
	}
	task, err := w.store.Get(taskID)
	if err != nil {
		return fmt.Errorf("resolve session id: %w", err)
	}
	return RecycleCoord(w.store, w.runner, roleID, task.SessionID, RecycleSelfService)
}
