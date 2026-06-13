// Package heraadopt runs the native Hera auto-adopt watcher (Milestone 4 of
// merging Hera into Argus — see context/plans/merge-hera-into-argus.md). It
// preserves Hera's strict auto-adopt rule D4: a task that is linked
// (depends_on) under a coordinator's task is adopted as a worker role under
// that coordinator's orchestrator — but ONLY when the parent holds exactly one
// live coordinator binding (under a non-archived orchestrator) AND the child
// carries meta:hera.role=worker. Anything else is skipped silently (logged).
//
// # Why a ground-truth tick loop, not event-ring consumption
//
// The plan sketched "consume the in-process events ring (events.Ring.Subscribe)
// and watch link.created". That API does not exist: internal/events exposes
// only Emit + a single atomic Sink, and the DB events ring is populated ONLY
// when the API server installs that sink (cfg.API.Enabled) — so link.created
// rows are absent whenever the REST API is off. The model the plan actually
// names — depswatcher.Watcher — is itself a ground-truth re-derivation loop, and
// that is what we implement here: every tick re-derives adoption decisions from
// current task + link + binding state. This is strictly more robust than an
// event cursor (which was Hera's known debt: the cursor advanced even on handler
// failure, dropping adoptions): it is inherently at-least-once, idempotent, and
// self-healing — it catches up links created while the watcher (or the whole
// daemon) was down, and a transient failure simply retries on the next tick.
//
// # Race (d): adopt watcher vs depswatcher
//
// Both are tick loops reacting to task/link state, so they MUST NOT both mutate
// the same task. depswatcher OWNS session starts and task-status flips. This
// watcher OWNS hera binding rows ONLY — it never starts/stops a session and
// never changes a task's status. Adoption keys exclusively off binding-table
// state: if the child already holds a live binding under the target
// orchestrator, adoption is an idempotent no-op. The two loops therefore never
// contend on the same mutable field.
package heraadopt

import (
	"errors"
	"sync"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// defaultInterval matches depswatcher's cadence — both are cheap DB reads and
// there is no benefit to re-deriving adoption faster than once a minute for a
// human-gated multi-agent workflow. Tests override via SetInterval.
const defaultInterval = time.Minute

// Store is the narrow DB surface the watcher + reconciler need. Satisfied by
// *db.DB; tests inject a fake to exercise D4 and the failure/retry paths
// without SQLite.
type Store interface {
	Tasks() ([]*model.Task, error)
	Get(id string) (*model.Task, error)
	ListMetaByNamespace(namespace string) (map[string]map[string]string, error)
	ListHeraLiveBindingsByTask(taskID string) ([]*db.HeraBinding, error)
	ListHeraLiveBindings() ([]*db.HeraBinding, error)
	HeraRole(id int64) (*db.HeraRole, error)
	HeraOrchestrator(id int64) (*db.HeraOrchestrator, error)
	HeraLiveBindingByTaskAndOrchestrator(taskID string, orchID int64) (*db.HeraBinding, error)
	UniqueHeraRoleName(orchID int64, base string) (string, error)
	CreateHeraRoleWithBinding(roleIn db.CreateHeraRoleInput, taskID, worktreePath string) (*db.HeraRole, *db.HeraBinding, error)
	EndHeraBinding(bindingID int64, reason string) error
	SetMeta(taskID, namespace, key, value string) error
}

// Watcher re-derives auto-adoption decisions on a tick. Embed-friendly: no
// exported fields other than the Set* configuration hooks.
type Watcher struct {
	store    Store
	interval time.Duration

	stopCh chan struct{}
	mu     sync.Mutex

	// onAdopt, when set, is called after a child is adopted (worker role +
	// binding created). Useful for tests and metrics. Read/written under mu.
	onAdopt func(childTaskID string, role *db.HeraRole, binding *db.HeraBinding)
}

// New builds a Watcher bound to store. It does not tick until Start is called.
func New(store Store) *Watcher {
	return &Watcher{
		store:    store,
		interval: defaultInterval,
		stopCh:   make(chan struct{}),
	}
}

// Start runs the watcher loop until Stop is called. Blocks; call in a
// goroutine. The first tick fires immediately so links created while the daemon
// was down get adopted without waiting a full interval (mirrors depswatcher).
func (w *Watcher) Start() {
	uxlog.Log("[hera-adopt] starting (interval=%s)", w.interval)
	w.tick()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			uxlog.Log("[hera-adopt] stopped")
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

// SetInterval overrides the tick interval. Has no effect on an already-running
// loop; callers must Stop and re-Start to pick up a new value. Test-only knob.
func (w *Watcher) SetInterval(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.interval = d
}

// SetOnAdopt registers a callback fired after a successful adoption. nil clears
// it. Safe before or after Start.
func (w *Watcher) SetOnAdopt(cb func(childTaskID string, role *db.HeraRole, binding *db.HeraBinding)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onAdopt = cb
}

func (w *Watcher) adoptCallback() func(string, *db.HeraRole, *db.HeraBinding) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.onAdopt
}

// Tick runs one adoption pass. Exported so the daemon's tests (and any future
// caller) can drive a single deterministic pass without racing the ticker.
func (w *Watcher) Tick() { w.tick() }

// tick re-derives adoption decisions from current ground truth: for every
// non-archived child task that is linked to at least one parent AND carries
// meta:hera.role=worker, evaluate each parent link against rule D4. Errors are
// logged and never propagated — a bad task must not block the rest of the pass.
func (w *Watcher) tick() {
	meta, err := w.store.ListMetaByNamespace(db.HeraMetaNamespace)
	if err != nil {
		uxlog.Log("[hera-adopt] load hera meta: %v", err)
		return
	}
	tasks, err := w.store.Tasks()
	if err != nil {
		uxlog.Log("[hera-adopt] load tasks: %v", err)
		return
	}
	for _, child := range tasks {
		if child == nil || child.Archived || len(child.DependsOn) == 0 {
			continue
		}
		m := meta[child.ID]
		if m == nil || m[db.HeraMetaKeyRole] != string(db.HeraKindWorker) {
			continue // D4(b): child must carry meta:hera.role=worker
		}
		for _, parentID := range child.DependsOn {
			w.maybeAdopt(parentID, child, m[db.HeraMetaKeyPrompt])
		}
	}
}

// maybeAdopt evaluates rule D4 for a single (parent, child) link and adopts the
// child when eligible. promptHint is the child's optional meta:hera.prompt,
// stored on the adopted role for the Details pane (tolerated empty).
func (w *Watcher) maybeAdopt(parentID string, child *model.Task, promptHint string) {
	parentBindings, err := w.store.ListHeraLiveBindingsByTask(parentID)
	if err != nil {
		uxlog.Log("[hera-adopt] parent %s bindings: %v", parentID, err)
		return
	}
	if len(parentBindings) == 0 {
		return // parent isn't bound to anything hera owns — not ours to adopt
	}

	// Collect the parent's coordinator bindings under NON-archived orchestrators.
	// An archived parent orchestrator is treated as "no longer a coordinator" so
	// adoption is skipped (matches Hera AdoptHandler).
	var coordOrchs []*db.HeraOrchestrator
	for _, b := range parentBindings {
		role, err := w.store.HeraRole(b.RoleID)
		if err != nil {
			uxlog.Log("[hera-adopt] parent %s role %d: %v", parentID, b.RoleID, err)
			return // lookup error — bail this parent, retry next tick
		}
		if role.Kind != db.HeraKindCoordinator {
			continue
		}
		orch, err := w.store.HeraOrchestrator(role.OrchestratorID)
		if err != nil {
			uxlog.Log("[hera-adopt] parent %s orchestrator %d: %v", parentID, role.OrchestratorID, err)
			return
		}
		if orch.ArchivedAt != nil {
			continue // D4: archived parent orchestrators filtered out
		}
		coordOrchs = append(coordOrchs, orch)
	}

	switch len(coordOrchs) {
	case 0:
		return // D4: parent holds no live coordinator binding — skip silently
	case 1:
		// proceed
	default:
		// D4: exactly-one required so the child's orchestrator is unambiguous.
		uxlog.Log("[hera-adopt] skip child=%s parent=%s: parent has %d coordinator bindings (ambiguous; attach explicitly via hera_join)",
			child.ID, parentID, len(coordOrchs))
		return
	}
	orch := coordOrchs[0]

	// Idempotent skip: if the child already holds a live binding under this
	// orchestrator (born-bound spawn, a prior adoption, or a manual hera_join),
	// there is nothing to do. This is the ONLY state the watcher keys off, which
	// is what keeps it from contending with depswatcher (race d).
	_, err = w.store.HeraLiveBindingByTaskAndOrchestrator(child.ID, orch.ID)
	if err == nil {
		return // already bound under this orchestrator — no-op
	}
	if !errors.Is(err, db.ErrHeraNotFound) {
		uxlog.Log("[hera-adopt] child=%s orch=%d binding lookup: %v", child.ID, orch.ID, err)
		return
	}

	// Adopt: create the worker role + binding for the child under the parent's
	// orchestrator. Transactional (role+binding in one tx); the partial unique
	// index backstops a race with a concurrent born-bound spawn.
	name, err := w.store.UniqueHeraRoleName(orch.ID, child.Name)
	if err != nil {
		uxlog.Log("[hera-adopt] child=%s unique name: %v", child.ID, err)
		return
	}
	role, binding, err := w.store.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID,
		Name:           name,
		Kind:           db.HeraKindWorker,
		ArgusProject:   child.Project,
		Prompt:         promptHint,
	}, child.ID, child.Worktree)
	if err != nil {
		// Includes a lost race against a concurrent adoption/spawn (unique-index
		// violation). Retried next tick; the idempotent skip above then short-
		// circuits, so a replay is safe.
		uxlog.Log("[hera-adopt] child=%s adopt under orch=%s failed (retry next tick): %v", child.ID, orch.Name, err)
		return
	}

	// Mirror meta:hera.role=worker (already present, but re-stamp to self-heal a
	// missing mirror). Best-effort — never undo the adoption.
	if mErr := w.store.SetMeta(child.ID, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindWorker)); mErr != nil {
		uxlog.Log("[hera-adopt] child=%s meta mirror failed (non-fatal): %v", child.ID, mErr)
	}

	uxlog.Log("[hera-adopt] adopted child=%s as role=%q under orch=%q (binding=%d)",
		child.ID, role.Name, orch.Name, binding.ID)
	if cb := w.adoptCallback(); cb != nil {
		cb(child.ID, role, binding)
	}
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
