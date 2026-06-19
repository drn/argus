// Package heragater runs the hera plan-DAG resolution loop. It mirrors the
// retired internal/depswatcher pattern (a short tick-loop that found pending
// tasks whose deps were complete and started them) but is hera-native: it finds
// PLANNED NODES (worker-kind hera roles with no binding ever) whose blockers
// have ALL reached hera ROLE-status `done`, and materializes each into a live
// worker via agent.CreateAndStart against the pre-created role.
//
// Gate contract (design.md "Gate on role-status done"):
//   - The gate is the blocker's hera ROLE status reaching `done` — the worker's
//     explicit "I'm finished" — NOT the argus TASK status. A finished hera worker
//     rolls its task to in_review (never auto-complete), so task status would fire
//     prematurely (in_review) or never (complete).
//   - A blocker still `working` (e.g. iterating on CI by pushing PRs) keeps the
//     dependent PLANNED; the next phase never starts under churning work.
//   - A blocker whose SESSION ENDED without ever reaching `done` (crash/failure)
//     HOLDS the dependent (no materialize) and pings the coordinator. No worker is
//     ever spawned-and-parked behind dead or unfinished work.
//   - A missing blocker role (deleted mid-plan) is pruned: the dependent is no
//     longer blocked by it (HeraBlockersOf only returns extant blockers).
//
// Idempotency: a planned node ceases to be a planned node the instant it acquires
// a binding (ListHeraPlannedNodes keys on "no binding EVER"), so a status flap
// (done→working→done) cannot double-spawn — once materialized, the role is gone
// from the planned set. The materialize call itself is the single spawn point.
package heragater

import (
	"sync"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// defaultInterval matches the retired depswatcher / cron tick — the workflow is
// gated by minute-scale worker lifecycles, so checking faster buys nothing.
// Tests override via SetInterval.
const defaultInterval = time.Minute

// Materializer binds + starts a pre-created planned role. agent.MaterializeHeraWorker
// satisfies this via the daemon adapter; tests inject a fake. Returns the live
// task or an error (a materialize failure HOLDS the node — it stays planned and
// the next tick retries).
type Materializer func(role *db.HeraRole, taskPrompt, project, branch, backend, model string) error

// CoordinatorPinger sends a free-form hold/failure notice to a coordinator role.
// Wired to hera.Service.Send via the daemon adapter; tests inject a fake. The
// sender is the HELD planned node's role (a real hera_roles row even without a
// binding), so Service.Send's self-send guard is never tripped — the gater is
// "the node telling its coordinator it can't start".
type CoordinatorPinger func(fromRoleID, coordRoleID int64, body, tldr string) error

// Watcher polls the DB for ready planned nodes and materializes them. Embed-
// friendly: configuration via Set* methods, no exported state.
type Watcher struct {
	db          *db.DB
	materialize Materializer
	ping        CoordinatorPinger
	interval    time.Duration

	stopCh chan struct{}
	mu     sync.Mutex

	// onMaterialize, when set, fires after a node is materialized. Test/notify hook.
	onMaterialize func(role *db.HeraRole)
	// heldPings dedups failure-hold pings per (blockedRole, blockerRole) so a
	// holding node doesn't spam the coordinator every tick.
	heldPings map[[2]int64]bool
}

// New builds a Watcher. It does not tick until Start is called.
func New(database *db.DB, materialize Materializer, ping CoordinatorPinger) *Watcher {
	return &Watcher{
		db:          database,
		materialize: materialize,
		ping:        ping,
		interval:    defaultInterval,
		stopCh:      make(chan struct{}),
		heldPings:   map[[2]int64]bool{},
	}
}

// Start runs the watcher loop until Stop. Blocks; call in a goroutine. The first
// tick fires immediately so a node that became ready while the daemon was down
// materializes without waiting a full interval.
func (w *Watcher) Start() {
	uxlog.Log("[heragater] starting (interval=%s)", w.interval)
	w.Tick()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			uxlog.Log("[heragater] stopped")
			return
		case <-ticker.C:
			w.Tick()
		}
	}
}

// Stop signals Start to exit. Safe to call multiple times.
func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.stopCh:
	default:
		close(w.stopCh)
	}
}

// SetInterval overrides the tick interval (test-only; no effect on a running loop).
func (w *Watcher) SetInterval(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.interval = d
}

// SetOnMaterialize registers a callback fired after a node is materialized.
func (w *Watcher) SetOnMaterialize(cb func(role *db.HeraRole)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onMaterialize = cb
}

func (w *Watcher) materializeCallback() func(role *db.HeraRole) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.onMaterialize
}

// nodeState classifies the readiness of a planned node from its blockers.
type nodeState int

const (
	// stateReady — every blocker reached role-status done; materialize.
	stateReady nodeState = iota
	// statePlanned — at least one blocker is still working (session live, not
	// done); keep planned, no ping.
	statePlanned
	// stateHeld — at least one blocker's session ended without reaching done;
	// hold and ping the coordinator.
	stateHeld
)

// Tick runs one resolution pass: classify every planned node and act. Errors are
// logged via uxlog but never propagated — a bad node on one row must not block
// the rest. Exported so the daemon and tests can drive a single pass directly.
func (w *Watcher) Tick() {
	planned, err := w.db.ListHeraPlannedNodes()
	if err != nil {
		uxlog.Log("[heragater] list planned nodes: %v", err)
		return
	}
	for _, node := range planned {
		state, failedBlocker := w.classify(node)
		switch state {
		case stateReady:
			w.materializeNode(node)
		case statePlanned:
			// nothing to do — still waiting on working blockers
		case stateHeld:
			w.holdAndPing(node, failedBlocker)
		}
	}
}

// classify computes a node's state from its blockers, returning the failing
// blocker role (for the hold ping) when stateHeld. Rules:
//   - No blockers → ready (nothing to wait on).
//   - All blockers done → ready (materialize).
//   - Any blocker failed (session ended without done) → held; you cannot proceed
//     past dead upstream work. The first failed blocker is reported for the ping.
//   - Otherwise (some blocker still working, none failed) → planned: keep waiting,
//     no ping, because the work may still complete.
//
// A node is materialized ONLY when every blocker is done; failed beats working for
// the held-vs-planned decision so a dependent never silently waits forever behind
// a crashed blocker.
func (w *Watcher) classify(node *db.HeraRole) (nodeState, *db.HeraRole) {
	blockerIDs, err := w.db.HeraBlockersOf(node.ID)
	if err != nil {
		uxlog.Log("[heragater] blockers of %d: %v", node.ID, err)
		return statePlanned, nil // transient: leave planned, retry next tick
	}
	if len(blockerIDs) == 0 {
		return stateReady, nil
	}
	allDone := true
	var failed *db.HeraRole
	for _, bid := range blockerIDs {
		switch w.blockerOutcome(bid) {
		case blockerDone:
			// satisfied
		case blockerWorking:
			allDone = false
		case blockerFailed:
			allDone = false
			if failed == nil {
				if r, rErr := w.db.HeraRole(bid); rErr == nil {
					failed = r
				}
			}
		}
	}
	if allDone {
		return stateReady, nil
	}
	if failed != nil {
		return stateHeld, failed
	}
	return statePlanned, nil
}

type blockerOutcome int

const (
	blockerWorking blockerOutcome = iota
	blockerDone
	blockerFailed
)

// blockerOutcome reports whether a blocker role is done, still working, or failed
// (session ended without done). The gate is hera ROLE-status `done`. A blocker
// not yet done is "working" while its bound session is alive (task in_progress or
// pending), and "failed" once its session has ended (task no longer in_progress
// AND not pending, or the binding has been ended) without ever reaching done.
func (w *Watcher) blockerOutcome(blockerID int64) blockerOutcome {
	if st, err := w.db.HeraRoleStatusFor(blockerID); err == nil && st.Status == db.HeraStatusDone {
		return blockerDone
	}
	// Not done. Is the session still alive?
	binding, err := w.db.HeraLiveBindingByRole(blockerID)
	if err != nil {
		// No live binding (ended or never bound) → the session is gone without
		// reaching done. A never-bound blocker (still a planned node itself) also
		// lands here: it has not started, let alone finished, so the dependent
		// is held — never spawn behind an upstream node that has not run.
		return blockerFailed
	}
	t, err := w.db.Get(binding.ArgusTaskID)
	if err != nil || t == nil {
		return blockerFailed
	}
	if t.Status == model.StatusInProgress || t.Status == model.StatusPending {
		return blockerWorking
	}
	// Session ended (in_review/complete) without role-status done → failed.
	return blockerFailed
}

// materializeNode resolves the materialization inputs (project, base_branch from
// the LATEST blocker's branch, check-in-prefixed prompt) and calls the injected
// materializer. A failure leaves the node planned (it has no binding yet) so the
// next tick retries.
func (w *Watcher) materializeNode(node *db.HeraRole) {
	orch, err := w.db.HeraOrchestrator(node.OrchestratorID)
	if err != nil {
		uxlog.Log("[heragater] materialize %d: resolve orch: %v", node.ID, err)
		return
	}
	coordName := "coord"
	if coords, cErr := w.db.ListHeraRolesByKind(node.OrchestratorID, db.HeraKindCoordinator); cErr == nil && len(coords) > 0 {
		coordName = coords[0].Name
	}
	project := node.ArgusProject
	branch := w.resolveBaseBranch(node)
	taskPrompt := agent.HeraCheckInOrientation(orch.Name, coordName) + "\n\n---\n\n" + node.Prompt

	if err := w.materialize(node, taskPrompt, project, branch, "", ""); err != nil {
		uxlog.Log("[heragater] materialize %d (%s) FAILED (stays planned, retry next tick): %v", node.ID, node.Name, err)
		return
	}
	uxlog.Log("[heragater] materialized node %d (%s) in orch %q (base_branch=%q)", node.ID, node.Name, orch.Name, branch)
	if cb := w.materializeCallback(); cb != nil {
		cb(node)
	}
}

// resolveBaseBranch returns the branch the new worktree should stack on: the
// branch of the highest-id (most-recently-created) DONE blocker that has a task
// with a branch. Empty when no blocker branch is resolvable (the project default
// is then used by CreateAndStart). This gives a clean stacked-PR base when a node
// depends on a single upstream worker; for fan-in the latest blocker wins (the
// coordinator can re-base via the check-in if a different base is wanted).
func (w *Watcher) resolveBaseBranch(node *db.HeraRole) string {
	blockerIDs, err := w.db.HeraBlockersOf(node.ID)
	if err != nil {
		return ""
	}
	var branch string
	var bestBindingID int64
	for _, bid := range blockerIDs {
		binding, bErr := w.db.HeraLiveBindingByRole(bid)
		if bErr != nil {
			// Fall back to the latest binding (the blocker may have gone idle/ended).
			bindings, lErr := w.db.ListHeraBindingsByRole(bid)
			if lErr != nil || len(bindings) == 0 {
				continue
			}
			binding = bindings[0] // most recent first
		}
		t, tErr := w.db.Get(binding.ArgusTaskID)
		if tErr != nil || t == nil || t.Branch == "" {
			continue
		}
		if binding.ID > bestBindingID {
			bestBindingID = binding.ID
			branch = t.Branch
		}
	}
	return branch
}

// holdAndPing notifies the coordinator that a node is held behind a failed
// blocker, deduped per (node, blocker) so it pings once per failure, not every
// tick.
func (w *Watcher) holdAndPing(node, failedBlocker *db.HeraRole) {
	key := [2]int64{node.ID, failedBlocker.ID}
	w.mu.Lock()
	already := w.heldPings[key]
	w.mu.Unlock()
	if already {
		return
	}
	coords, err := w.db.ListHeraRolesByKind(node.OrchestratorID, db.HeraKindCoordinator)
	if err != nil || len(coords) == 0 {
		uxlog.Log("[heragater] hold %d: no coordinator to ping: %v", node.ID, err)
		return
	}
	body := "Planned node " + node.Name + " is HELD: its blocker " + failedBlocker.Name +
		" finished without reaching role-status done (crash or unfinished work). " +
		"It will not materialize until you intervene (re-run the blocker, edit the plan, or mark it done)."
	tldr := "held: " + node.Name + " blocked by failed " + failedBlocker.Name
	if w.ping != nil {
		if pErr := w.ping(node.ID, coords[0].ID, body, tldr); pErr != nil {
			// A failed ping must NOT mark the key — leave it unset so the next tick
			// retries. Otherwise a transient delivery failure would silently swallow
			// the hold notice, violating the "hold AND notify" contract.
			uxlog.Log("[heragater] hold %d: ping coordinator failed (will retry next tick): %v", node.ID, pErr)
			return
		}
	}
	// Only dedup AFTER a successful ping (or when there is no pinger wired).
	w.mu.Lock()
	w.heldPings[key] = true
	w.mu.Unlock()
	uxlog.Log("[heragater] held node %d (%s) behind failed blocker %s; pinged coordinator", node.ID, node.Name, failedBlocker.Name)
}
