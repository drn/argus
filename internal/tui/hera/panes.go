package hera

import (
	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/tui/keyenc"
	"github.com/drn/argus/internal/tui/terminal"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
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
	p.detailsMode = p.sel.IsCoordinator()

	// HERA (middle) pane: the selected orchestrator's coordinator session.
	p.bindPane(p.coordPane, &p.coordBound, p.sel.CoordTaskID(), "coord")

	// AGENT (right) region: terminal for a worker, Details for a coordinator.
	if p.detailsMode {
		p.bindPane(p.agentPane, &p.agentBound, "", "agent")
		p.details.SetOrch(p.sel.Orch, p.prMeta)
		// In DAG sub-mode, reproject this coordinator's dependency subgraph
		// (roster sub-mode reads straight from the model, no rebuild needed).
		if p.detailsSub == subModeDAG {
			p.rebuildDAG()
		}
	} else {
		p.bindPane(p.agentPane, &p.agentBound, p.sel.TaskID(), "agent")
	}
}

// bindPane feeds tp from the runner session for taskID (or unbinds it when
// taskID is ""). It is a no-op when the bound task is unchanged so the tick's
// repeated calls don't reset the emulator. bound is the page's record of which
// task the pane currently shows.
func (p *HeraPage) bindPane(tp *terminal.TerminalPane, bound *string, taskID, label string) {
	if *bound == taskID {
		return
	}
	if taskID == "" {
		uxlog.Log("[hera-view] %s pane unbind (was task=%s)", label, *bound)
		tp.SetSession(nil)
		tp.SetTaskID("")
		*bound = ""
		return
	}
	var sess agentview.TerminalAdapter
	if p.resolve != nil {
		sess = p.resolve(taskID)
	}
	// SetTaskID first so a finished task with no live session can replay its
	// on-disk log; SetSession then attaches the live ring when present.
	tp.SetTaskID(taskID)
	tp.SetSession(sess)
	if sess != nil {
		// A session previously sized for the full-width main agent view must be
		// resized to this (narrower) hera pane, or the agent keeps painting at
		// its old width. ForceResyncPTY arms an unconditional resize on the next
		// Draw even when the seeded ptyCols already matches; SyncPanes then
		// applies it off the main thread. This is the CLAUDE.md rule-5 size
		// alignment — never papered over with Sync.
		tp.ForceResyncPTY()
		uxlog.Log("[hera-view] %s pane feed-start task=%s (live)", label, taskID)
	} else {
		uxlog.Log("[hera-view] %s pane bind task=%s (no live session; replay)", label, taskID)
	}
	*bound = taskID
}

// reconcileSessions late-binds a live session that started after the pane was
// bound (e.g. a coordinator/worker whose session came up a tick later), mirror
// of the main agent view's tick re-resolution. MUST run on the tview main
// thread. A pane that already holds a (live or dead) session is left alone — a
// fresh selection or refresh rebinds it.
func (p *HeraPage) reconcileSessions() {
	if p.remote {
		return
	}
	p.reconcileOne(p.coordPane, p.coordBound, "coord")
	if !p.detailsMode {
		p.reconcileOne(p.agentPane, p.agentBound, "agent")
	}
}

func (p *HeraPage) reconcileOne(tp *terminal.TerminalPane, taskID, label string) {
	if taskID == "" || p.resolve == nil || tp.Session() != nil {
		return
	}
	if sess := p.resolve(taskID); sess != nil {
		tp.SetSession(sess)
		tp.ForceResyncPTY()
		uxlog.Log("[hera-view] %s pane feed-start task=%s (late bind)", label, taskID)
	}
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
		tp.ScrollUp(paneScrollStep)
		return
	case tcell.KeyPgDn:
		tp.ScrollDown(paneScrollStep)
		return
	}
	sess := tp.Session()
	if sess == nil || !sess.Alive() {
		return
	}
	if b := keyenc.Encode(ev); len(b) > 0 {
		if _, err := sess.WriteInput(b); err != nil {
			uxlog.Log("[hera-view] pane write failed: %v", err)
		}
	}
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
