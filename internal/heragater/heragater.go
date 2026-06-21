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
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
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
	db               *db.DB
	materialize      Materializer
	subCoordMaterial Materializer
	ping             CoordinatorPinger
	interval         time.Duration

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

// logf routes gater diagnostics to slog (→ the daemon's daemon.log). The gater
// runs in the daemon process, where uxlog is uninitialized (uxlog is the TUI
// layer's logger, init'd only in runTUI), so uxlog.Log is a silent no-op here —
// the gater must use slog like the rest of the daemon's hera code.
func (w *Watcher) logf(format string, args ...any) {
	slog.Info(fmt.Sprintf(format, args...))
}

// Start runs the watcher loop until Stop. Blocks; call in a goroutine. The first
// tick fires immediately so a node that became ready while the daemon was down
// materializes without waiting a full interval.
func (w *Watcher) Start() {
	w.logf("[heragater] starting (interval=%s)", w.interval)
	w.Tick()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			w.logf("[heragater] stopped")
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

// SetSubCoordMaterializer registers the second materialize seam used for subcoord
// plan nodes (add-hera-subcoord-nodes). Wired to agent.MaterializeHeraSubCoordinator
// via the daemon adapter; tests inject a fake. When unset, a subcoord node falls
// back to the worker materializer (defensive — production always wires it).
func (w *Watcher) SetSubCoordMaterializer(fn Materializer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.subCoordMaterial = fn
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
		w.logf("[heragater] list planned nodes: %v", err)
		return
	}
	plannedByID := make(map[int64]*db.HeraRole, len(planned))
	for _, node := range planned {
		plannedByID[node.ID] = node
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
	w.rearmHeldPings(plannedByID)
}

// rearmHeldPings sweeps the held-ping dedup (D4) so a held node is re-reported
// after its blocker recovers then re-fails, and announces a recovery exactly
// once. For each (node, blocker) key currently marked:
//
//   - If the NODE is no longer a planned node (it materialized — gained a binding
//     — or was archived/removed), delete the key SILENTLY. An already-running
//     node's reopened blocker is physics, not actionable (Non-Goals): no notice.
//   - Else recompute the blocker's outcome. If it is still blockerFailed, keep the
//     key (the hold stands). Otherwise clear it:
//   - If it cleared because the blocker RECOVERED (the blocker role still exists
//     and its outcome is now working or done), emit EXACTLY ONE "unblocked"
//     notice to the coordinator, sent FROM the held node's own role (same ping
//     seam holdAndPing uses, so the self-send guard never trips).
//   - If it cleared because the edge/role VANISHED (the blocker role is gone, or
//     the edge was removed so the blocker no longer gates this node), no notice.
//
// After a key is cleared, a later re-failure re-arms naturally: holdAndPing sets
// the key again the next time the node is held.
func (w *Watcher) rearmHeldPings(plannedByID map[int64]*db.HeraRole) {
	w.mu.Lock()
	keys := make([][2]int64, 0, len(w.heldPings))
	for k := range w.heldPings {
		keys = append(keys, k)
	}
	w.mu.Unlock()

	for _, key := range keys {
		nodeID, blockerID := key[0], key[1]
		node, stillPlanned := plannedByID[nodeID]
		if !stillPlanned {
			// Materialized or removed — un-spawnable, no notice (Non-Goals).
			w.clearHeldKey(key)
			w.logf("[heragater] re-arm: cleared held key (node %d, blocker %d) — node no longer planned (silent)", nodeID, blockerID)
			continue
		}
		if w.blockerOutcome(blockerID) == blockerFailed {
			continue // hold stands; key kept
		}
		// No longer failed → clear. Distinguish recovered vs vanished: a RECOVERED
		// blocker still gates this node (edge present) and its role still exists.
		recovered := w.blockerRecovered(nodeID, blockerID)
		w.clearHeldKey(key)
		if recovered {
			w.emitRecoveryNotice(node, blockerID)
			continue
		}
		w.logf("[heragater] re-arm: cleared held key (node %d, blocker %d) — blocker edge/role vanished (no notice)", nodeID, blockerID)
	}
}

// blockerRecovered reports whether a cleared held key cleared because the blocker
// genuinely RECOVERED (its role still exists, it still gates the node via an
// extant edge, and its outcome is now working or done) versus the edge/role
// having VANISHED. Distinguishing the two is what gates the recovery notice: only
// a real recovery is actionable; a removed edge/role is not.
func (w *Watcher) blockerRecovered(nodeID, blockerID int64) bool {
	// Role must still exist.
	if _, err := w.db.HeraRole(blockerID); err != nil {
		return false
	}
	// Edge must still gate this node.
	blockerIDs, err := w.db.HeraBlockersOf(nodeID)
	if err != nil {
		return false
	}
	stillEdge := false
	for _, bid := range blockerIDs {
		if bid == blockerID {
			stillEdge = true
			break
		}
	}
	if !stillEdge {
		return false
	}
	switch w.blockerOutcome(blockerID) {
	case blockerWorking, blockerDone:
		return true
	default:
		return false
	}
}

// clearHeldKey deletes one (node, blocker) entry from the dedup map.
func (w *Watcher) clearHeldKey(key [2]int64) {
	w.mu.Lock()
	delete(w.heldPings, key)
	w.mu.Unlock()
}

// emitRecoveryNotice sends a one-time "unblocked" notice to the coordinator that
// a held node's blocker has recovered, sent FROM the held node's own role (same
// self-send-safe ping seam as holdAndPing). A delivery failure is logged but the
// key stays cleared — the hold is genuinely gone, and re-failure re-arms anyway;
// we do not resurrect the dedup just to retry an advisory recovery notice.
func (w *Watcher) emitRecoveryNotice(node *db.HeraRole, blockerID int64) {
	coords, err := w.db.ListHeraRolesByKind(node.OrchestratorID, db.HeraKindCoordinator)
	if err != nil || len(coords) == 0 {
		w.logf("[heragater] recovery %d: no coordinator to notify: %v", node.ID, err)
		return
	}
	blockerName := ""
	if r, rErr := w.db.HeraRole(blockerID); rErr == nil {
		blockerName = r.Name
	}
	body := "Planned node " + node.Name + " is UNBLOCKED: its previously-failed blocker " + blockerName +
		" has recovered. It will materialize once all its blockers reach role-status done."
	tldr := "unblocked: " + node.Name + " blocker " + blockerName + " recovered"
	if w.ping != nil {
		if pErr := w.ping(node.ID, coords[0].ID, body, tldr); pErr != nil {
			w.logf("[heragater] recovery %d: notify coordinator failed (key stays cleared): %v", node.ID, pErr)
			return
		}
	}
	w.logf("[heragater] recovery: node %d (%s) unblocked — blocker %s recovered; notified coordinator", node.ID, node.Name, blockerName)
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
		w.logf("[heragater] blockers of %d: %v", node.ID, err)
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
	if st, err := w.db.HeraRoleStatusFor(blockerID); err == nil {
		switch st.Status {
		case db.HeraStatusDone:
			return blockerDone
		case db.HeraStatusFailed:
			// Explicit self-declared defeat (D2 gating half). A worker that reports
			// `failed` holds its dependents IMMEDIATELY — no need to wait for its
			// session to end. This takes precedence over the session-death inference
			// path below (which only catches crashes / silent give-up).
			return blockerFailed
		}
	}
	// Not done. A COORDINATOR never reaches role-status done — its session is alive
	// for the whole orchestration (BUG-003). An alive coordinator blocker must NOT
	// be classified failed (it has not ended); treat it as permanently WORKING so
	// the dependent stays PLANNED with no false "failed blocker" hold-ping. This is
	// defense-in-depth: AddHeraBlock now rejects a coordinator-as-blocker edge, but
	// the gater also protects any such edges already present in the live DB. Only
	// when the coordinator's session has genuinely ended (no live binding) does it
	// fall through to the normal had-binding-ended-without-done failure path.
	if role, rErr := w.db.HeraRole(blockerID); rErr == nil && role.Kind == db.HeraKindCoordinator {
		if _, lbErr := w.db.HeraLiveBindingByRole(blockerID); lbErr == nil {
			return blockerWorking
		}
	}
	// Not done. Is the session still alive?
	binding, err := w.db.HeraLiveBindingByRole(blockerID)
	if err != nil {
		// No LIVE binding. Two very different cases must NOT be conflated:
		//   - NEVER bound (no binding ever): the blocker is itself a planned node
		//     still waiting on its own blockers. It has not failed — it simply has
		//     not materialized yet. Treat as WORKING so the dependent stays PLANNED.
		//     (A transitive chain A→B→C must not hold C just because B has not
		//     materialized while A is still in flight — that was the bug.)
		//   - HAD a binding that ended without reaching done: its session ran and
		//     is gone without success → failed.
		bindings, lErr := w.db.ListHeraBindingsByRole(blockerID)
		if lErr != nil || len(bindings) == 0 {
			return blockerWorking // never started yet → pending, not failed
		}
		return blockerFailed // ran and ended without done
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
		w.logf("[heragater] materialize %d: resolve orch: %v", node.ID, err)
		return
	}
	coordName := "coord"
	if coords, cErr := w.db.ListHeraRolesByKind(node.OrchestratorID, db.HeraKindCoordinator); cErr == nil && len(coords) > 0 {
		coordName = coords[0].Name
	}
	project := node.ArgusProject
	branch := w.resolveBaseBranch(node)

	// Route on node kind (add-hera-subcoord-nodes). A subcoord node materializes
	// via the sub-coord seam (a distinct coordinator agent owning a child
	// orchestrator); everything else is a leaf worker. Idempotency, gating, and
	// base-branch resolution are identical — only the materialize step differs.
	if node.NodeKind == db.HeraNodeKindSubCoord {
		w.materializeSubCoord(node, orch.Name, coordName, project, branch)
		return
	}

	taskPrompt := agent.HeraCheckInOrientation(orch.Name, coordName) + "\n\n---\n\n" + node.Prompt

	if err := w.materialize(node, taskPrompt, project, branch, "", ""); err != nil {
		w.logf("[heragater] materialize %d (%s) FAILED (stays planned, retry next tick): %v", node.ID, node.Name, err)
		return
	}
	w.logf("[heragater] materialized node %d (%s) in orch %q (base_branch=%q)", node.ID, node.Name, orch.Name, branch)
	if cb := w.materializeCallback(); cb != nil {
		cb(node)
	}
}

// materializeSubCoord materializes a subcoord plan node via the sub-coord seam.
// The delivered prompt is the coordinator orientation (naming a derived child-orch
// placeholder + the parent + the spawn/plan tools) + the check-in/poll-inbox
// standing order + the node's goal (its stored prompt). The seam
// (agent.MaterializeHeraSubCoordinator) de-collides the real child-orchestrator
// name at materialize time, so the orientation uses the node name as the visible
// child-orch reference. A nil seam falls back to the worker materializer
// (defensive; production always wires SetSubCoordMaterializer).
func (w *Watcher) materializeSubCoord(node *db.HeraRole, parentOrchName, coordName, project, branch string) {
	w.mu.Lock()
	seam := w.subCoordMaterial
	w.mu.Unlock()
	if seam == nil {
		slog.Warn("[heragater] MISCONFIGURATION: subcoord seam not wired (SetSubCoordMaterializer not called before Start); node will materialize as a worker-kind agent instead of a coordinator",
			"node_id", node.ID, "node_name", node.Name)
		seam = w.materialize
	}

	orientation := agent.HeraSubCoordinatorOrientation(node.Name, parentOrchName, "coord")
	checkIn := agent.HeraCheckInOrientation(parentOrchName, coordName)
	taskPrompt := orientation + "\n\n---\n\n" + checkIn + "\n\n---\n\n" + node.Prompt

	if err := seam(node, taskPrompt, project, branch, "", ""); err != nil {
		w.logf("[heragater] materialize SUBCOORD %d (%s) FAILED (stays planned, retry next tick): %v", node.ID, node.Name, err)
		return
	}
	w.logf("[heragater] materialized SUBCOORD node %d (%s) in orch %q (child_orch=%s, base_branch=%q)", node.ID, node.Name, parentOrchName, node.Name, branch)
	if cb := w.materializeCallback(); cb != nil {
		cb(node)
	}
}

// resolveBaseBranch returns the branch the new worktree should stack on.
//
// For a node WITH blockers (non-root): the branch of the highest-id
// (most-recently-created) DONE blocker that has a task with a branch. This gives
// a clean stacked-PR base when a node depends on a single upstream worker; for
// fan-in the latest blocker wins (the coordinator can re-base via the check-in if
// a different base is wanted). This behavior is unchanged by
// add-hera-plan-base-branch.
//
// For a ROOT node (no blocker branch resolves) the fallback order is
// (add-hera-plan-base-branch): the orchestrator's explicit base_branch when set →
// otherwise the coordinator role's bound-task branch → otherwise "" (CreateAndStart
// then applies the project default, as before this change). Every step degrades to
// "" on a miss — never a panic — so an orchestrator with no coordinator, or a
// coordinator with no branch, falls through to the historical behavior.
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
	if branch != "" {
		// A blocker branch resolved — non-root path, unchanged.
		return branch
	}
	// Root node: no blocker branch. Resolve the configurable root base.
	return w.resolveRootBaseBranch(node)
}

// resolveRootBaseBranch resolves a ROOT node's base branch
// (add-hera-plan-base-branch): explicit orchestrator base_branch → coordinator
// role's bound-task branch → "". Logs which source won. Never panics.
func (w *Watcher) resolveRootBaseBranch(node *db.HeraRole) string {
	if orch, err := w.db.HeraOrchestrator(node.OrchestratorID); err == nil && orch.BaseBranch != "" {
		w.logf("[heragater] root base for node %d (%s): %q (source=explicit)", node.ID, node.Name, orch.BaseBranch)
		return orch.BaseBranch
	}
	if branch := w.coordinatorBranch(node.OrchestratorID); branch != "" {
		w.logf("[heragater] root base for node %d (%s): %q (source=coordinator)", node.ID, node.Name, branch)
		return branch
	}
	w.logf("[heragater] root base for node %d (%s): \"\" (source=project-default)", node.ID, node.Name)
	return ""
}

// coordinatorBranch returns the branch of the orchestrator's coordinator role's
// bound task, or "" if no coordinator role, no live/last binding, or no task
// branch is resolvable. Mirrors the blocker path's live-then-latest binding
// fallback so a coordinator that has gone idle still resolves.
func (w *Watcher) coordinatorBranch(orchID int64) string {
	coords, err := w.db.ListHeraRolesByKind(orchID, db.HeraKindCoordinator)
	if err != nil || len(coords) == 0 {
		return ""
	}
	coordID := coords[0].ID
	binding, err := w.db.HeraLiveBindingByRole(coordID)
	if err != nil {
		bindings, lErr := w.db.ListHeraBindingsByRole(coordID)
		if lErr != nil || len(bindings) == 0 {
			return ""
		}
		binding = bindings[0] // most recent first
	}
	t, err := w.db.Get(binding.ArgusTaskID)
	if err != nil || t == nil {
		return ""
	}
	return t.Branch
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
		w.logf("[heragater] hold %d: no coordinator to ping: %v", node.ID, err)
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
			w.logf("[heragater] hold %d: ping coordinator failed (will retry next tick): %v", node.ID, pErr)
			return
		}
	}
	// Only dedup AFTER a successful ping (or when there is no pinger wired).
	w.mu.Lock()
	w.heldPings[key] = true
	w.mu.Unlock()
	w.logf("[heragater] held node %d (%s) behind failed blocker %s; pinged coordinator", node.ID, node.Name, failedBlocker.Name)
}
