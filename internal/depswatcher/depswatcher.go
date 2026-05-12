// Package depswatcher runs the DAG-resolution loop for blocked-pending
// tasks. A task created via task_create with a non-empty depends_on stays
// in StatusPending with no agent process; this watcher polls the DB on a
// short interval and, for every such task whose dependencies have all
// reached StatusComplete, starts the agent session via
// agent.StartPendingBlocked. The watcher owns no other state.
//
// Failure semantics: a dependency that ended at StatusComplete with a
// {"failed": true, ...} result blob is, from the watcher's perspective,
// "complete" — the orchestrator agent owns interpreting the failed flag
// and explicitly stopping or archiving downstream tasks. This keeps the
// daemon's contract simple (status is the only gate) and matches the
// handoff convention.
package depswatcher

import (
	"sync"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// defaultInterval matches the cron scheduler's tick — both loops run cheap
// reads against the DB and there's no benefit to checking faster than
// once per minute for a workflow that human reviewers gate via PRs.
// Tests override via SetInterval.
const defaultInterval = time.Minute

// Watcher polls the DB for blocked-pending tasks and starts them once
// their deps complete. Embed-friendly: no exported fields other than
// configuration hooks set via Set* methods.
type Watcher struct {
	db       *db.DB
	runner   agent.SessionProvider
	interval time.Duration

	stopCh chan struct{}
	mu     sync.Mutex

	// onStart, when set, is called after the watcher successfully starts a
	// previously-blocked task. Useful for push notifications and tests.
	// Read/written under mu.
	onStart func(*model.Task)
}

// New builds a Watcher bound to the given DB and runner. The watcher does
// not start ticking until Start is called.
func New(database *db.DB, runner agent.SessionProvider) *Watcher {
	return &Watcher{
		db:       database,
		runner:   runner,
		interval: defaultInterval,
		stopCh:   make(chan struct{}),
	}
}

// Start runs the watcher loop until Stop is called. Blocks; call in a
// goroutine. The first tick fires immediately on entry so any tasks
// that became unblocked while the daemon was down get started without
// waiting a full interval.
func (w *Watcher) Start() {
	uxlog.Log("[depswatcher] starting (interval=%s)", w.interval)
	w.tick()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			uxlog.Log("[depswatcher] stopped")
			return
		case <-ticker.C:
			w.tick()
		}
	}
}

// Stop signals Start to exit. Safe to call multiple times.
func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.stopCh:
		// already stopped
	default:
		close(w.stopCh)
	}
}

// SetInterval overrides the tick interval. Has no effect on an already-
// running loop; callers must Stop and re-Start to pick up a new value.
// Test-only knob; production code uses the default.
func (w *Watcher) SetInterval(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.interval = d
}

// SetOnStart registers a callback fired after a blocked task is started.
// nil clears the callback. Safe before or after Start.
func (w *Watcher) SetOnStart(cb func(*model.Task)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onStart = cb
}

// startCallback returns the current OnStart callback under the mutex so
// concurrent SetOnStart calls cannot race the tick path.
func (w *Watcher) startCallback() func(*model.Task) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.onStart
}

// tick runs one resolution pass: walk every task, identify pending rows
// with non-empty depends_on, and start any whose deps are all complete.
// Errors are logged via uxlog but never propagated — a bad task on row N
// must not block rows N+1..M from starting.
func (w *Watcher) tick() {
	tasks, err := w.db.Tasks()
	if err != nil {
		uxlog.Log("[depswatcher] load tasks: %v", err)
		return
	}
	// Index by ID for cheap dep lookups within the loop.
	byID := make(map[string]*model.Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	for _, t := range tasks {
		if !w.isBlockedPending(t) {
			continue
		}
		if !w.depsResolved(t, byID) {
			continue
		}
		w.unblock(t)
	}
}

// isBlockedPending returns true when the task is a candidate for the
// watcher: pending status, non-empty depends_on, not archived.
func (w *Watcher) isBlockedPending(t *model.Task) bool {
	if t == nil || t.Archived {
		return false
	}
	if t.Status != model.StatusPending {
		return false
	}
	return len(t.DependsOn) > 0
}

// depsResolved returns true when every dependency has reached
// StatusComplete. A missing dep (referenced ID is gone from the DB) is
// treated as unresolved — the watcher refuses to start a task whose
// upstream contract is broken. The orchestrator agent should detect
// the missing dep via task_get and remediate.
func (w *Watcher) depsResolved(t *model.Task, byID map[string]*model.Task) bool {
	for _, depID := range t.DependsOn {
		dep := byID[depID]
		if dep == nil || dep.Status != model.StatusComplete {
			return false
		}
	}
	return true
}

// unblock starts the agent session for a previously-blocked task. We
// re-fetch from the DB to defend against the task having advanced state
// (orchestrator marked it complete to skip, another tick got there first,
// or the user archived it to abort the stack). Both Status and Archived
// must be re-checked: an archived row keeps Status=Pending, so checking
// status alone would still start the agent on a row the user just
// abandoned. The status guard inside StartPendingBlocked is the canonical
// race protection for the status flip.
func (w *Watcher) unblock(t *model.Task) {
	latest, err := w.db.Get(t.ID)
	if err != nil || latest == nil {
		uxlog.Log("[depswatcher] reload %s: %v", t.ID, err)
		return
	}
	if latest.Status != model.StatusPending || latest.Archived {
		// State changed between scan and unblock decision. Quietly skip —
		// the next tick re-evaluates from a fresh snapshot.
		return
	}
	uxlog.Log("[depswatcher] unblocking %s (%s); deps=%v", latest.ID, latest.Name, latest.DependsOn)
	if _, err := agent.StartPendingBlocked(w.db, w.runner, latest); err != nil {
		uxlog.Log("[depswatcher] start %s failed (will retry): %v", latest.ID, err)
		return
	}
	if cb := w.startCallback(); cb != nil {
		cb(latest)
	}
}
