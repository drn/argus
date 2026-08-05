package hera

import (
	"time"

	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/tui/keyenc"
	"github.com/drn/argus/internal/tui/terminal"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// paneScrollStep is how many lines a PgUp/PgDn scrolls a focused pane.
const paneScrollStep = 10

// SessionResolver resolves the live agent session for a task ID, returning the
// agentview.TerminalAdapter the pane feeds from, or nil when no live session
// exists. It is the in-process-runner seam that REPLACES Hera's proxy/ SSE
// fan-out: the App wires it to runner.Get, and the TerminalPane polls that
// session's in-process ring buffer (RecentOutput*) on every Draw — the same
// machinery the main agent view uses. There is no SSE/HTTP hop and no
// ArgusStateCache polling; the daemon-owned PTY ring is the single source.
//
// Remote mode (--remote / apistore) never sets a resolver: the page renders the
// "unavailable" banner and the panes are never fed.
type SessionResolver func(taskID string) agentview.TerminalAdapter

// SetSessionResolver wires the runner seam. Called once by the App in local
// mode; left nil in remote mode.
func (p *HeraPage) SetSessionResolver(fn SessionResolver) { p.resolve = fn }

// RerenderKicker evaluates the shared size-drift kill+resume decision
// (agent.ShouldKickRerender) for taskID's session at panelCols — the SAME
// decision the main agent view applies on entry (App.maybeKickRerender), but
// using the CALLING PANE's own current width rather than the main agent
// view's. A plain PTY resize (ForceResyncPTY) only re-flows live UI; it
// cannot repair scrollback already committed at a different width, because
// cursor-positioning codes baked into earlier PTY output stay wrong once
// re-emulated at a new size (BUG-073's sibling, reached by a fresh Hera pane
// bind rather than a resize or a ring-buffer wrap — see
// gotchas/pty-terminal.md and gotchas/hera-view.md). No-op on a nil/errored
// task lookup, a dead/absent session, an already-pending kick, or a
// non-resumable backend.
//
// A busy agent or one blocked on a user prompt (agent.RerenderDeferBusy /
// RerenderDeferPrompt) is NOT a terminal outcome: the decision resolves
// asynchronously (an RPC round-trip dispatched via QueueUpdateDraw, well
// after this call returns), and rerender.go's own doc comment on
// RerenderDeferBusy requires the caller to "retry on the next opportunity."
// onDeferred is invoked (later, on the tview main goroutine) exactly when
// that happens, so the Hera-side caller can re-arm and try again — see
// maybeKickPaneRerender and BUG-077 in gotchas/hera-view.md. Called with nil
// for outcomes that need no retry (fired, skipped, unresumable).
type RerenderKicker func(taskID string, panelCols uint16, onDeferred func())

// SetRerenderKicker wires the size-drift kill+resume seam. Called once by the
// App in local mode (next to SetSessionResolver); left nil in remote mode, so
// bindPane's guard below is a no-op there exactly like resolve's.
func (p *HeraPage) SetRerenderKicker(fn RerenderKicker) { p.kickRerender = fn }

// --- the coord-vs-agent session-selection rule (documented in gotchas) -------
//
// On every rail selection change applySelection rebinds the two right-hand
// panes from the in-process runner:
//
//   * HERA (middle) pane  -> the SELECTED ORCHESTRATOR's coordinator task
//     session, ALWAYS — regardless of whether a coordinator or a worker is
//     selected. This gives constant "what is this orchestrator's coordinator
//     doing" context. When the selected role IS the coordinator, this is the
//     selected task itself.
//
//   * AGENT / Details (right) region is mode-switched by the selected role:
//       - coordinator selected -> Details summary (worker roster), no terminal.
//       - worker/freelance/leaf selected -> AGENT terminal of the selected
//         role's bound task.
//
// Multi-binding: a task that is worker in orchestrator A and coordinator in B
// surfaces as two distinct roles (one per orchestrator). Selecting the
// role-under-A feeds the AGENT pane from that task and the HERA pane from A's
// coordinator; selecting the role-under-B feeds the HERA pane from that task
// (B's coordinator = the task itself) and renders B's roster in Details. The
// disambiguator is ALWAYS the selected role's orchestrator (Selection.Orch),
// never the bare task ID — so the two roles drive two different contexts.

// applySelection recomputes the selection from the rail cursor and rebinds the
// panes accordingly. MUST run on the tview main thread (it calls SetSession,
// which is main-goroutine-only — see terminalpane.go). Called from
// rail.onSelectionChanged (cursor move) and from doRefresh (after SetModel
// rebuilds the model's backing arrays, so the stale Selection pointers refresh).
func (p *HeraPage) applySelection() {
	if p.remote {
		return
	}
	p.sel = p.rail.Selection()

	// detailsOrch resolves the orchestrator whose Details (roster + tree) and HERA
	// coordinator pane this selection drives, or nil for a plain worker/leaf (which
	// renders the agent terminal). It treats a worker-bridge sub-coordinator as the
	// coordinator it really is — see BUG-004.
	detailsOrch := p.detailsOrch()
	p.detailsMode = detailsOrch != nil

	// AGENT (right) region: terminal for a worker, Details for a coordinator (top-
	// level OR a worker-bridge sub-coord). The HERA (middle) pane always feeds from
	// the coordinator session in view: for Details mode that is the resolved
	// orchestrator's coordinator (== the sub-coord's own session for a bridge row),
	// for a worker it is the selected orchestrator's coordinator.
	if p.detailsMode {
		p.bindPane(p.coordPane, &p.coordBound, &p.coordKickedFor, &p.coordKickPending, detailsOrch.CoordTaskID(), "coord")
		p.bindPane(p.agentPane, &p.agentBound, &p.agentKickedFor, &p.agentKickPending, "", "agent")
		p.details.SetOrch(detailsOrch, p.prMeta)
		// The Details region stacks the roster over the plan graph, so reproject
		// this coordinator's plan DAG on every selection (the roster reads straight
		// from the model; the plan widget needs the scoped node/edge set rebuilt).
		p.rebuildPlan(detailsOrch)
	} else {
		p.bindPane(p.coordPane, &p.coordBound, &p.coordKickedFor, &p.coordKickPending, p.sel.CoordTaskID(), "coord")
		p.bindPane(p.agentPane, &p.agentBound, &p.agentKickedFor, &p.agentKickPending, p.sel.TaskID(), "agent")
	}
}

// detailsOrch resolves the orchestrator whose Details pane (roster + orchestration
// tree) and HERA coordinator pane the current selection should drive, or nil when
// the selection is a plain worker/leaf that renders the agent terminal instead.
//
//   - top-level coordinator (an orch header row, or an explicit coordinator-kind
//     role): the selected orchestrator itself (Selection.Orch).
//   - worker-bridge sub-coordinator (a worker ROW that bridges a child
//     orchestrator — Selection.BridgeChildOrchID != 0): the CHILD orchestrator.
//     The bridging worker IS the child's coordinator (it holds the same argus task
//     the child's coordinator role is bound to), so its Details view must reflect
//     the child's roster + subtree, exactly like any other coordinator — never the
//     agent terminal. This is the BUG-004 fix.
//
// Selection.BridgeChildOrchID is set by Rail.Selection when the cursor rests on a
// bridging worker row (its collOrchID). Coordinator-spawned sub-teams already
// select as their OWN header row, so they hit the first case and are unaffected.
//
// Only the pane/details/tree ROUTING follows the child; p.sel (the mutation
// context read via SelectionContext) is left pointing at the parent worker role,
// so Ctrl+D and the other mutations still act on the worker, never the child
// orchestrator — the conservative multi-binding safety the rail nesting documents.
func (p *HeraPage) detailsOrch() *OrchView {
	if p.sel.IsCoordinator() {
		return p.sel.Orch
	}
	if p.sel.BridgeChildOrchID != 0 {
		m := p.rail.Model()
		return m.OrchByID(p.sel.BridgeChildOrchID)
	}
	return nil
}

// bindPane feeds tp from the runner session for taskID (or unbinds it when
// taskID is ""). It is a no-op when the bound task is unchanged so the tick's
// repeated calls don't reset the emulator. bound is the page's record of which
// task the pane currently shows. kickedFor tracks which bound taskID has
// already had its size-drift kick evaluated (see maybeKickPaneRerender) —
// reset here on unbind so a later rebind to the SAME task gets a fresh
// evaluation rather than being silently skipped by a stale marker. pending is
// the paired kick-debounce state (see kickPending); it is ALSO reset to its
// zero value on unbind — without this, a rebind to the SAME task after an
// unbind would compare equal to the stale pending.taskID and keep the OLD
// (possibly long-past) deadline, firing the kick immediately instead of
// dwelling afresh.
func (p *HeraPage) bindPane(tp *terminal.TerminalPane, bound, kickedFor *string, pending *kickPending, taskID, label string) {
	if *bound == taskID {
		return
	}
	if taskID == "" {
		uxlog.Log("[hera-view] %s pane unbind (was task=%s)", label, *bound)
		tp.SetTaskID("")
		tp.ResetVT()
		tp.SetSession(nil)
		*bound = ""
		*kickedFor = ""
		*pending = kickPending{}
		return
	}
	var sess agentview.TerminalAdapter
	if p.resolve != nil {
		sess = p.resolve(taskID)
	}
	// SetTaskID first so a finished task with no live session can replay its
	// on-disk log; ResetVT clears any emulator/replay/scroll-anchor state left
	// by the PREVIOUSLY bound task before it can bleed into this one's render
	// — the same SetTaskID→ResetVT→SetSession order onTaskSelect and
	// enterPendingAgentView use for the main agent view; SetSession then
	// attaches the live ring when present.
	tp.SetTaskID(taskID)
	tp.ResetVT()
	tp.SetSession(sess)
	// Enter on this pane while its session is dead/nil revives the pane's bound
	// task (BUG-001). The closure reads live page state at fire time (never a
	// snapshot), so it stays correct as the selection moves; reattachPane targets
	// the right task per pane (agent → selected worker, coord → its coordinator).
	tp.OnReattach = func() { p.reattachPane(tp) }
	if sess != nil {
		// A session previously sized for the full-width main agent view must be
		// resized to this (narrower) hera pane, or the agent keeps painting at
		// its old width. ForceResyncPTY arms an unconditional resize on the next
		// Draw even when the seeded ptyCols already matches; SyncPanes then
		// applies it off the main thread. This is the CLAUDE.md rule-5 size
		// alignment — never papered over with Sync.
		//
		// The size-drift kill+resume kick (a plain resize can't repair
		// scrollback already committed at a different width) is deliberately
		// NOT evaluated here: bindPane runs synchronously in the input
		// handler, before Draw() has had a chance to give a newly-shown pane
		// (e.g. the agent pane, hidden while a coordinator was selected in
		// details mode) its real rect — this pane's own tracked width can
		// still be 0 (or stale) at this point. See maybeKickPaneRerender,
		// called from Draw() right after SetRect, once the width is fresh.
		tp.ForceResyncPTY()
		uxlog.Log("[hera-view] %s pane feed-start task=%s (live)", label, taskID)
	} else {
		uxlog.Log("[hera-view] %s pane bind task=%s (no live session; replay)", label, taskID)
	}
	*bound = taskID
}

// KickDebounce is the wall-clock dwell maybeKickPaneRerender waits, once a
// bound pane's width first crosses agent.RerenderMargin, before it actually
// invokes the wired RerenderKicker. Hera's own layout swings a coordinator/
// agent pane between full-width (fullscreen/Details mode) and roughly
// half-width (split mode) on ORDINARY RAIL NAV ALONE — no fullscreen toggle
// or terminal resize needed — so crossing the margin on every hop of a rapid
// Cmd+Arrow traversal binds several distinct tasks in quick succession. Each
// kick is a real Session.Stop()+restart+full-conversation replay, so without
// a dwell a fast multi-row traversal bursts many of these back to back (the
// "kick storm" — see gotchas/hera-view.md). 300ms is comfortably longer than
// a single keystroke-to-Draw round trip yet short enough that a genuine
// dwell-and-stay still kicks promptly.
const KickDebounce = 300 * time.Millisecond

// KickRetryInterval is the wall-clock dwell maybeKickPaneRerender waits
// before retrying a kick that the App deferred (agent.RerenderDeferBusy /
// RerenderDeferPrompt — see RerenderKicker's onDeferred). Deliberately much
// longer than KickDebounce: the initial dwell only has to survive a single
// fast rail traversal, but a busy agent can legitimately stay busy for the
// entire remainder of a long tool call — retrying at the same 300ms cadence
// would re-issue the decision RPCs (IsIdle/InitialPTYSize) and repeat the
// "rerender deferred" uxlog line every 300ms for the whole busy stretch
// (BUG-077). 10s keeps retry pressure on the daemon negligible while still
// catching the agent going idle well within a session's lifetime.
const KickRetryInterval = 10 * time.Second

// kickPending tracks an armed-but-unfired size-drift kick candidate for one
// pane (HeraPage.coordKickPending / agentKickPending). The zero value means
// nothing is pending. cols is updated on every Draw so the eventual kick (if
// it fires) uses the LATEST observed width, not the width from the moment
// the dwell first armed.
type kickPending struct {
	taskID   string
	cols     int
	deadline time.Time
}

// maybeKickPaneRerender evaluates the shared size-drift kill+resume decision
// (see RerenderKicker) for a pane's currently bound task, using cols from the
// rect Draw() JUST set on it. Called from each of Draw's four SetRect+Draw
// call sites (fullscreen coord/agent, split coord/agent) right after
// SetRect, so cols is always fresh — never bindPane's own possibly-stale
// tracked width (see bindPane's doc comment for why).
//
// The actual kickRerender call is debounced by KickDebounce (see its doc
// comment): the first call that sees a newly-bound task (pending.taskID !=
// bound) arms pending with a fresh deadline instead of firing immediately.
// Only a LATER call, once the dwell has elapsed AND the SAME task is still
// bound, actually invokes kickRerender. A rebind to a DIFFERENT task before
// the dwell elapses re-arms pending against the new task, discarding the old
// one un-fired — this is what suppresses the kick storm from a fast rail
// traversal. bindPane ALSO resets pending to its zero value on unbind: without
// that, a rebind to the SAME task after an intervening unbind would compare
// equal to the stale pending.taskID and keep the OLD deadline, firing
// immediately instead of dwelling afresh — caught by
// TestPanes_KickDebounce_UnbindMidDwellThenRebindSameTask (see design.md
// Decision 2).
//
// kickedFor is a per-pane marker (HeraPage.coordKickedFor / agentKickedFor)
// preventing a redundant evaluation every frame while the current bind's
// decision is still outstanding OR already resolved with nothing left to do;
// it is NOT the correctness gate against re-kicking (that is
// App.isRedundantAttach's job, keyed by task ID and width) — it only avoids
// paying for a DB lookup + runner.Get on every Draw. bindPane resets it to ""
// on unbind, so a later rebind to the same task still gets a fresh
// evaluation.
//
// BUG-077: kickedFor must NOT be sticky across a DEFERRED outcome
// (agent.RerenderDeferBusy / RerenderDeferPrompt). rerender.go's own doc
// comment on RerenderDeferBusy requires the caller to "retry on the next
// opportunity" — a pane bound while its agent is busy (exactly "mid active
// streaming," the common case) would otherwise never get a second chance at
// the kill+resume for the rest of that bind's lifetime, leaving it exposed to
// Hera's own resize churn (see KickDebounce's doc comment) with only a plain
// SIGWINCH — which cannot repair scrollback already committed at a different
// width — for as long as the pane stays bound. The onDeferred callback fires
// later, asynchronously, on the tview main goroutine (via QueueUpdateDraw)
// once the App resolves the decision; it re-arms kickedFor/pending for a
// retry after KickRetryInterval, guarded on kickedFor still naming THIS
// bind's task (a rebind — same task or different — already reset kickedFor
// itself and must win over a late-arriving stale callback).
func (p *HeraPage) maybeKickPaneRerender(bound string, kickedFor *string, cols int, pending *kickPending) {
	if p.kickRerender == nil || bound == "" || cols <= 0 || *kickedFor == bound {
		return
	}
	now := p.kickClockNow()
	if pending.taskID != bound {
		*pending = kickPending{taskID: bound, deadline: now.Add(KickDebounce)}
	}
	pending.cols = cols
	if now.Before(pending.deadline) {
		return // still dwelling
	}
	*kickedFor = bound
	deferredFor := bound
	// cols is a terminal column count — bounded by realistic screen widths,
	// nowhere near uint16's range; gosec G115 flags the conversion but it's
	// safe (matches the pattern already used for the analogous conversion in
	// terminalpane.go's ring-wrap catch-up).
	p.kickRerender(bound, uint16(pending.cols), func() { //nolint:gosec // see comment
		if *kickedFor != deferredFor {
			return // superseded by a rebind — don't clobber its fresh state
		}
		*kickedFor = ""
		*pending = kickPending{taskID: deferredFor, deadline: p.kickClockNow().Add(KickRetryInterval)}
	})
}

// kickClockNow returns the current time for the kick debounce, defaulting to
// time.Now and overridable via SetKickClock (test seam).
func (p *HeraPage) kickClockNow() time.Time {
	if p.kickNow != nil {
		return p.kickNow()
	}
	return time.Now()
}

// SetKickClock overrides the kick-debounce clock (test seam) — mirrors
// Refresher.SetNow. Production code never calls this.
func (p *HeraPage) SetKickClock(fn func() time.Time) { p.kickNow = fn }

// reconcileSessions (re)resolves the live session for each fed pane on the tick,
// mirror of the main agent view's tick re-resolution. MUST run on the tview main
// thread. It covers both late-bind (a session that came up after selection) and
// the BUG-013 dead-handle case (see reconcileOne).
func (p *HeraPage) reconcileSessions() {
	if p.remote {
		return
	}
	p.reconcileOne(p.coordPane, p.coordBound, "coord")
	if !p.detailsMode {
		p.reconcileOne(p.agentPane, p.agentBound, "agent")
	}
}

// reconcileOne (re)resolves the live session for a pane. It handles two cases a
// one-shot bind misses:
//
//   - LATE BIND: the pane is bound to a task (taskID != "") but holds no session
//     yet — the coordinator/worker session came up a tick after selection.
//
//   - DEAD HANDLE (BUG-013): the pane holds a session whose stream the daemon
//     tore down (StreamLost relay / daemon bounce) while the agent PROCESS is
//     still alive, so Session().Alive() is false. Without re-resolution the dead
//     handle is never replaced (bindPane no-ops on the unchanged taskID) and
//     forwardKey silently drops every keystroke until a full TUI restart re-dials
//     the stream. Re-resolving asks the provider for a fresh handle — the daemon
//     client re-dials a new stream on a cache-miss Get when the daemon still
//     reports the process alive — and swaps it in, restoring input WITHOUT a
//     restart.
//
// A live, present session is left alone. A dead handle is replaced ONLY by a
// genuinely live, DIFFERENT handle: when the provider yields nothing (process
// really gone → fall back to on-disk log replay) or the same dead handle (client
// cache not yet evicted → retry next tick) the pane is left untouched, so the
// emulator is never needlessly reset. MUST run on the tview main thread
// (SetSession is main-goroutine-only).
func (p *HeraPage) reconcileOne(tp *terminal.TerminalPane, taskID, label string) {
	if taskID == "" || p.resolve == nil {
		return
	}
	cur := tp.Session()
	if cur != nil && cur.Alive() {
		return // live session already bound
	}
	sess := p.resolve(taskID)
	if sess == nil {
		return // no session available (process gone → replay handles it)
	}
	if cur != nil && (sess == cur || !sess.Alive()) {
		return // replacing a dead handle: only a fresh, live, distinct one qualifies
	}
	// ResetVT before SetSession: this is the recycle_coord kill+respawn
	// transition (same task, brand-new session) as much as it is a
	// StreamLost re-dial — a genuinely different handle is "new content
	// incoming" either way, so any emulator/replay/scroll-anchor state left
	// over from the OLD handle must not survive into the new one's render
	// (BUG: recycled coordinator panes showed stale cells from the prior
	// session at the top of the pane).
	tp.ResetVT()
	tp.SetSession(sess)
	tp.ForceResyncPTY()
	if cur == nil {
		uxlog.Log("[hera-view] %s pane feed-start task=%s (late bind)", label, taskID)
	} else {
		uxlog.Log("[hera-view] %s pane re-resolve task=%s (replaced dead session)", label, taskID)
	}
}

// paneBinding maps a pane back to its tracked bound task ID and a log label, so
// forwardKey can re-resolve a dead handle without threading extra state.
func (p *HeraPage) paneBinding(tp *terminal.TerminalPane) (boundID, label string) {
	if tp == p.coordPane {
		return p.coordBound, "coord"
	}
	return p.agentBound, "agent"
}

// SyncPanes applies any pending PTY resizes for both panes. Called from the app
// tick GOROUTINE (NOT a QueueUpdateDraw callback) — SyncPTYSize issues a Resize
// RPC and is designed to block off the main thread. It is a no-op for an
// unbound pane and for a pane whose Draw has not run this frame (pendingResize
// stays zero when the Hera tab is not active, so an off-tab call cannot fight
// the main agent view's own resize of the same task). uxlogging of the actual
// size delta happens inside TerminalPane.
func (p *HeraPage) SyncPanes() {
	if p.remote {
		return
	}
	p.coordPane.SyncPTYSize()
	p.agentPane.SyncPTYSize()
}

// FocusedTerminalTaskID returns the argus task ID feeding the currently-focused
// TERMINAL pane — the coordinator pane (always a terminal) or a worker/leaf
// agent pane in terminal mode. It returns "" when the focused region is the rail
// or the coordinator details/tree region (no PTY, nothing to copy) and in remote
// mode. The App uses it to (a) poll the agent-staged clipboard for that task on
// the tick (the discoverability hint) and (b) resolve which task `ctrl+y`
// copies from — the Hera view shows several tasks at once, so the copy must be
// scoped to the focused pane, not a single global active task.
func (p *HeraPage) FocusedTerminalTaskID() string {
	if p.remote {
		return ""
	}
	switch p.focus.State() {
	case FocusCoord:
		return p.coordBound
	case FocusAgent:
		if !p.detailsMode {
			return p.agentBound
		}
	}
	return ""
}

// IsBoundToTask reports whether taskID currently feeds either terminal pane
// (coordinator or agent/worker) — regardless of which pane has keyboard
// focus, or whether the agent region is showing Details instead of a
// terminal. Unlike FocusedTerminalTaskID (scoped to the one focused pane,
// for the clipboard/copy seam), this answers "is the Hera view showing this
// task's live output at all" — the App uses it (alongside the classic
// fullscreen agent view's own TaskID check) to decide whether a
// size-drift kill+resume kick (heraKickRerender, BUG-074) should
// auto-restart in place instead of silently letting the task settle at
// InReview, since the Hera tab never sets the App's mode to its fullscreen
// agent-view mode (BUG-076 — see gotchas/hera-view.md).
func (p *HeraPage) IsBoundToTask(taskID string) bool {
	return taskID != "" && (p.coordBound == taskID || p.agentBound == taskID)
}

// SelectionContext returns the current (role, orchestrator, task) selection.
//
// 6c EXTENSION POINT: this is the clean seam mutations hang off. A mutation
// (promote / archive / link / open-PR / status advance, etc.) reads this to act
// on Selection.Role under Selection.Orch — the orchestrator is the multi-binding
// disambiguator. 6b only feeds panes; it implements no mutations.
func (p *HeraPage) SelectionContext() Selection { return p.sel }

// forwardKey routes a key to a focused terminal pane: PgUp/PgDn scroll its
// scrollback; everything else is encoded (shared keyenc, identical to the main
// agent PTY path) and written to the live session so the pane is interactive.
func (p *HeraPage) forwardKey(tp *terminal.TerminalPane, ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyPgUp:
		// BUG-031: a full-screen agent (alt-screen) has no linear scrollback;
		// entering argus's scroll mode would replay its in-place frames as
		// garbage. Suppress + tell the user to scroll within the agent (the
		// mouse wheel is forwarded to it — BUG-026). ScrollUp also self-guards.
		if tp.InAltScreen() {
			if p.OnInfo != nil {
				p.OnInfo("Fullscreen agent — scroll within the agent")
			}
			return
		}
		tp.ScrollUp(paneScrollStep)
		return
	case tcell.KeyPgDn:
		tp.ScrollDown(paneScrollStep)
		return
	}
	sess := tp.Session()
	if sess == nil || !sess.Alive() {
		// BUG-013: the bound session is missing, or its stream died while the agent
		// process is still alive (daemon tore the stream down on a StreamLost relay
		// or bounce). This used to drop the keystroke SILENTLY and the pane stayed
		// frozen until a full TUI restart. Log it, then attempt an immediate
		// re-resolve — the daemon client re-dials a fresh stream when the process is
		// still alive — and retry the write on the fresh handle.
		boundID, label := p.paneBinding(tp)
		uxlog.Log("[hera-view] forwardKey: %s pane session dead/nil (task=%s) — re-resolving", label, boundID)
		p.reconcileOne(tp, boundID, label)
		sess = tp.Session()
		if sess == nil || !sess.Alive() {
			// No live session to write to. Route the event to the pane's own
			// InputHandler: Enter revives the worker via OnReattach (BUG-001 — the
			// "Session not running - press Enter to start" overlay's promise), and
			// every other key is a no-op (dropped, as before). setFocus is unused by
			// the pane's handler, so a no-op suffices.
			uxlog.Log("[hera-view] forwardKey: %s pane re-resolve found no live session (task=%s) — routing to pane InputHandler (Enter revives)", label, boundID)
			tp.InputHandler()(ev, func(tview.Primitive) {})
			return
		}
	}
	if b := keyenc.Encode(ev); len(b) > 0 {
		// BUG-008: real input snaps the pane to the live tail. If the user
		// scrolled up to browse history, the keystroke echoes at the live
		// bottom (off-screen) unless we reset scroll first; PgUp/PgDn already
		// returned above, so only genuine input reaches here. Output-driven
		// growth is unaffected (anchor-lock still pins scrolled-up content).
		tp.ResetScroll()
		if _, err := sess.WriteInput(b); err != nil {
			uxlog.Log("[hera-view] pane write failed: %v", err)
		}
	}
}

// reattachPane revives the session backing tp via the page's OnReattach callback
// (wired by the App to heraReattach — the SAME revive path the rail's Enter uses).
// It builds a Selection targeting THIS pane's bound task: the agent pane revives
// the selected worker (p.sel), while the coordinator pane revives the orchestrator
// whose coordinator it shows — the resolved details orchestrator in detailsMode,
// else the selected orchestrator. A Selection with only Orch set is a coordinator
// selection (FocusTaskID → the coordinator task), so heraReattach treats it
// correctly (dead → restart, live coordinator → navigate-only).
func (p *HeraPage) reattachPane(tp *terminal.TerminalPane) {
	if p.OnReattach == nil {
		return
	}
	sel := p.sel
	if tp == p.coordPane {
		orch := p.sel.Orch
		if p.detailsMode {
			orch = p.detailsOrch()
		}
		sel = Selection{Orch: orch}
	}
	if sel.FocusTaskID() == "" {
		return
	}
	uxlog.Log("[hera-view] pane reattach: task=%s coordinator=%v", sel.FocusTaskID(), sel.IsCoordinator())
	p.OnReattach(sel)
}

// regionAt maps a screen x-coordinate to the focus region it falls in, using
// the rects recorded by the last Draw. Used for mouse hit-testing.
func (p *HeraPage) regionAt(x int) Focus {
	if p.agentW > 0 && x >= p.agentX {
		return FocusAgent
	}
	if p.coordW > 0 && x >= p.coordX {
		return FocusCoord
	}
	return FocusRail
}
