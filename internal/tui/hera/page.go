package hera

import (
	"strings"

	"github.com/drn/argus/internal/tui/planview"
	"github.com/drn/argus/internal/tui/terminal"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// heraRailWidth is the fixed character width of the navigation rail (matches
// Hera's RailWidth ballpark, trimmed for Argus chrome).
const heraRailWidth = 34

// Base border titles for the two right-hand panes. Draw appends a clipboard
// affordance to the focused terminal pane's title when a payload is staged.
const (
	coordPaneTitle = " Coordinator "
	agentPaneTitle = " Agent "
)

// clipboardHintTitle returns base with a "(ctrl+y copy)" affordance appended
// when show is true. Kept ASCII so each rune is exactly one terminal cell (the
// border-title truncation math assumes single-width runes — see the agent
// header's matching note). The Hera-view analogue of the agent header's
// "ctrl+y to copy" hint, consistent with how the view labels panes.
func clipboardHintTitle(base string, show bool) string {
	if !show {
		return base
	}
	return strings.TrimRight(base, " ") + " (ctrl+y copy) "
}

// HeraPage is the top-level Hera-view page added to the App's Pages. It lays
// out the three Hera regions — rail | coordinator (HERA) pane | agent/details
// pane. 6b makes the two right regions REAL: the middle pane and the agent pane
// are live terminal.TerminalPanes fed from the in-process runner (NOT SSE —
// Hera's proxy/ fan-out is replaced by the runner's ring buffer, which the
// TerminalPane polls), and the rail selection drives which session each shows.
// When a coordinator is selected the right region renders the read-only Details
// summary instead of a terminal. Layout is computed in Draw (like DAGPage)
// rather than via a tview.Flex, so the wrapper holds tview focus and routes
// input to the region the FocusMachine selects.
//
// Remote-mode degradation: when the App can't resolve a local *db.DB hera
// reader (i.e. --remote mode, where apistore has no hera methods), it
// constructs the page with a nil reader. The page then renders an
// "Hera unavailable in remote mode" banner instead of a rail, never wires a
// SessionResolver, and never feeds the panes — it never panics and never breaks
// the --remote build (see gotchas/remote-tui.md).
type HeraPage struct {
	*tview.Box

	rail      *Rail
	focus     *FocusMachine
	refresher *Refresher
	reader    HeraReader
	remote    bool

	// 6b pane feeds.
	coordPane *terminal.TerminalPane // middle: orchestrator coordinator session
	agentPane *terminal.TerminalPane // right: worker/leaf session (terminal mode)
	details   *DetailsView           // right: coordinator roster (details mode)
	resolve   SessionResolver        // runner seam; nil in remote mode
	prMeta    map[string]map[string]string
	// needsInput is the authoritative per-task needs-input set the App pushes each
	// tick (App.needsInputIDs — the SAME idle-gated agent.DetectNeedsInput set the
	// task list consumes). doRefresh threads it into BuildModel so each live role
	// carries its own needs-input flag and the subtree rollup is computed (BUG-018).
	needsInput map[string]bool

	// Plan-DAG render mode of the Details region (coordinator selection only).
	// When a coordinator is selected the Details region stacks the read-only
	// roster (top) over this embedded plan graph (bottom) — both render at once,
	// no toggle. Nodes/edges are projected in-process from the rail's model by
	// heraPlanNodesWithBridge (the planned + live worker roles and their
	// hera_blocks edges — the plan a coordinator authored), so there is no
	// provider seam. It replaced the retired orchestration-tree projection.
	plan *planview.Widget // embedded plan-DAG graph

	// planOrchID is the orchestrator ID whose plan is currently projected into the
	// plan widget (0 when none). rebuildPlan compares it to the incoming root to
	// decide SetData (a genuine selection change — full cursor reset) vs UpdateData
	// (the same orchestrator re-projected on a ~1s refresh tick — preserve the
	// operator's cursor + fanned groups). See gotchas/hera-view.md.
	planOrchID int64

	// Selection + per-pane bound task (so SetSession only fires on change).
	sel         Selection
	detailsMode bool
	coordBound  string
	agentBound  string

	// 6c mutation callbacks. The rail-focus key handler maps keys to these,
	// passing the current Selection (the multi-binding-disambiguated (role,orch)
	// context). The App wires them to handlers that own the modals / confirms /
	// refresh; the thin DB writes themselves live in hera.Ops + agent.SpawnHeraWorker.
	//
	// nil-safe: a nil callback (remote mode never wires them; an intentionally
	// unbound action) makes the key an inert no-op — never a panic.
	OnSpawnWorker   func(Selection) // `w` — spawn worker under selected coordinator's orchestrator
	OnRename        func(Selection) // `r` — rename selected role/orchestrator (modal)
	OnArchiveToggle func(Selection) // `a` — HIDE/unhide selected worker (Tier-1 EOL; reversible, keeps session+worktree)
	OnPinToggle     func(Selection) // `P` — pin/unpin selected role/orchestrator
	OnStatusAdvance func(Selection) // `s` — advance selected role status one rung
	OnStatusRevert  func(Selection) // `S` — revert selected role status one rung
	OnDelete        func(Selection) // ctrl+d — NUKE selected role/orchestrator (Tier-2 EOL; removes from rail + reclaims worktree, confirm)
	OnReattach      func(Selection) // Enter on a dead-session row — restart its session
	OnAdopt         func(Selection) // `J` — adopt freelancer / reparent coordinator (orch picker)

	// Creation + EOL keys (BUG-006/022). OnNewCoordinator is selection-INDEPENDENT
	// — it fires even on an empty rail, so it is dispatched directly (not via the
	// selection-gated `fire`). OnClearArchive acts on the current Selection.
	// (BUG-022 removed `R` retire and the rail-wide `Ctrl+R` prune — the two-state
	// model is `a` HIDE / `Ctrl+D` NUKE / `C` clear-this-coordinator's-archive.)
	OnNewCoordinator func(Selection) // `n` — new top-level coordinator (full new-task modal; selection used only to default the project, fires even when empty)
	OnClearArchive   func(Selection) // `C` — NUKE every Tier-1 hidden item in the selected coordinator's archive (confirm)

	// OnCopyClipboard fires on `ctrl+y` while a TERMINAL pane (coordinator or
	// worker) is focused AND that pane's task has an agent-staged clipboard
	// payload, passing the focused pane's bound task ID. The App copies the
	// staged payload for THAT task to the OS clipboard (the Hera view shows
	// several tasks at once, so the payload must come from the focused pane, not
	// a single global active task). nil-safe: unwired in remote mode / when the
	// runner is not daemon-backed, making `ctrl+y` fall through to the PTY.
	OnCopyClipboard func(taskID string)

	// clipReady is set by the App each tick (SetClipboardHint): true when the
	// focused terminal pane's task has an agent-staged clipboard payload. It
	// gates the `ctrl+y` interception (so ctrl+y still falls through to the PTY
	// for an in-agent yank when nothing is staged — mirroring the main agent
	// view) and drives the `(ctrl+y copy)` border-title affordance in Draw.
	clipReady bool

	// OnFocusChange is called whenever the focused Hera region changes so the
	// app can update focus-aware UI (e.g. the bottom status bar hint set). It
	// receives the new focus state and fires on both keyboard and mouse changes.
	// nil-safe: unwired in remote mode, never panics.
	OnFocusChange func(Focus)

	// Region rects from the last Draw (mouse hit-testing in regionAt).
	coordX, coordW int
	agentX, agentW int
}

// NewHeraPage builds the page against a hera reader. Pass nil for remote mode.
func NewHeraPage(reader HeraReader) *HeraPage {
	p := &HeraPage{
		Box:       tview.NewBox(),
		rail:      NewRail(),
		focus:     NewFocusMachine(),
		reader:    reader,
		remote:    reader == nil,
		coordPane: terminal.NewTerminalPane(),
		agentPane: terminal.NewTerminalPane(),
		details:   NewDetailsView(),
		plan:      planview.New(),
	}
	p.coordPane.SetBorderTitle(coordPaneTitle)
	// Retitle the embedded graph so it reads as the plan DAG, not a second
	// top-level " DAG " tab (gotchas/hera-view.md).
	p.plan.SetTitle(" Plan ")
	p.rail.SetFocused(true)
	// Rebind the panes whenever the rail cursor lands on a different role.
	p.rail.SetOnSelectionChanged(p.applySelection)
	// Drill-in (D6) is owned by the page, not the App: it needs the rail bridge
	// index to resolve the child orchestrator and the in-package projection to
	// build the child plan. OnEnter (jump-to-agent-view, the App's concern) is
	// wired separately on the exposed widget via Plan().
	p.plan.OnDrillIn = p.drillIntoChild
	p.refresher = NewRefresher(DefaultRefreshDebounce, p.doRefresh)
	return p
}

// drillIntoChild is the plan widget's OnDrillIn handler (D6): the cursor sits on
// a sub-coordinator node whose id is its bound coordinator-bridge task. Resolve
// the child orchestrator through the rail bridge index, project the child's plan
// DAG, and push it onto the widget's nav stack with the "Details ▸ <orch> · Plan"
// title (D6). A bridge miss (the child orchestrator is gone) is a no-op so the
// widget stays on the parent plan. MUST run on the tview main thread (it reads
// the rail model and reprojects — pure in-memory, no I/O).
func (p *HeraPage) drillIntoChild(id string) {
	child := p.rail.Model().bridgeIndex()[id]
	if child == nil {
		uxlog.Log("[hera-view] plan drill-in: no child orch for task=%s", id)
		return
	}
	nodes, edges := heraPlanNodesWithBridge(child, p.rail.Model().bridgeIndex())
	p.plan.PushOrch("Details ▸ "+child.Name+" · Plan", nodes, edges)
	uxlog.Log("[hera-view] plan drill-in: task=%s child=%s nodes=%d edges=%d depth=%d",
		id, child.Name, len(nodes), len(edges), p.plan.DrillDepth())
}

// Reconcile late-binds live sessions and is the App-tick hook (main thread).
// Exposed so the App can drive the nil→live upgrade each tick while the Hera
// tab is active. No-op in remote mode.
func (p *HeraPage) Reconcile() { p.reconcileSessions() }

// CoordPane and AgentPane expose the terminal panes so the App can wire their
// OnBranchChange (log-only forceRedraw) and OnNeedRedraw (QueueUpdateDraw)
// callbacks, exactly as it wires the main agent view's pane.
func (p *HeraPage) CoordPane() *terminal.TerminalPane { return p.coordPane }
func (p *HeraPage) AgentPane() *terminal.TerminalPane { return p.agentPane }

// Plan exposes the embedded plan-DAG widget so the App can wire its OnEnter
// (jump to a leaf node's agent view), OnDrillIn (push the child orchestrator's
// plan), and OnBranchChange (log-only forceRedraw) callbacks. The page owns the
// widget's rect, focus, and node/edge set (projected by heraPlanNodesWithBridge
// from the rail model); the App owns what OnEnter does. The page itself wires
// OnDrillIn (it needs the rail bridge index + the projection) — see applySelection.
func (p *HeraPage) Plan() *planview.Widget { return p.plan }

// Rail exposes the inner rail (test seam + 6b wiring).
func (p *HeraPage) Rail() *Rail { return p.rail }

// RailFiltering reports whether the rail is in `/` search INPUT mode. The App's
// global key handler consults it so the global rune shortcuts (1/2/3/q/?) fall
// through to the rail as filter input while the operator is typing. Safe in
// remote mode (the rail exists but is never focused).
func (p *HeraPage) RailFiltering() bool { return p.rail.Filtering() }

// SetRailStateStore wires the rail's persistence seam (BUG-002), restoring the
// saved fold/selection state immediately. The App calls it once with the local
// *db.DB; remote mode never calls it, so persistence stays off.
func (p *HeraPage) SetRailStateStore(s RailStateStore) { p.rail.SetStateStore(s) }

// Machine exposes the focus machine (test seam + 6b wiring). Not named Focus()
// because that collides with tview.Primitive's Focus(func(tview.Primitive)).
func (p *HeraPage) Machine() *FocusMachine { return p.focus }

// IsRemote reports whether the page is in the remote-degraded mode.
func (p *HeraPage) IsRemote() bool { return p.remote }

// SetNeedsInput records the task IDs the App detected as blocked on a user
// prompt this tick (App.needsInputIDs — the SAME authoritative, idle-gated
// agent.DetectNeedsInput set the task list consumes). doRefresh threads it into
// BuildModel so each live role carries its own needs-input flag and the subtree
// rollup is computed (BUG-018). Pure setter — the tick already schedules the
// rebuild. MUST run on the tview thread (main-goroutine-only, like SetModel).
func (p *HeraPage) SetNeedsInput(ids []string) {
	if len(ids) == 0 {
		p.needsInput = nil
		return
	}
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	p.needsInput = m
}

// SetClipboardHint toggles whether the focused terminal pane advertises a
// staged agent clipboard payload via a `(ctrl+y copy)` border-title affordance.
// The App refreshes it each tick from the daemon for the focused pane's task
// (refreshHeraClipboardHint), mirroring the main agent view's per-tick
// refreshClipboardCache. It also gates the `ctrl+y` interception so the key
// still reaches the PTY for an in-agent yank when nothing is staged. Pure
// setter; Draw renders it. MUST run on the tview main thread.
func (p *HeraPage) SetClipboardHint(show bool) { p.clipReady = show }

// ClipboardHint reports whether the focused terminal pane currently advertises a
// staged clipboard payload (the state SetClipboardHint last set). Exposed for
// the App's tick wiring + tests, mirroring the agent header's ClipboardHint().
func (p *HeraPage) ClipboardHint() bool { return p.clipReady }

// ScheduleRefresh requests a debounced rail rebuild. Called from the app tick
// while the Hera tab is active; bursts coalesce to one rebuild per window.
// MUST run on the tview thread.
func (p *HeraPage) ScheduleRefresh() { p.refresher.Schedule() }

// Refresh forces an immediate rail rebuild (used on tab entry so the rail is
// fresh the instant the tab opens). MUST run on the tview thread.
func (p *HeraPage) Refresh() {
	p.refresher.Schedule()
	p.refresher.Flush()
}

// doRefresh rebuilds the model from the reader and hands it to the rail. In
// remote mode the reader is nil → BuildModel returns an empty model and Draw
// renders the unavailable banner, so this stays a cheap no-op.
func (p *HeraPage) doRefresh() {
	m, err := BuildModel(p.reader, p.needsInput)
	if err != nil {
		uxlog.Log("[hera-view] rail refresh failed: %v", err)
		return
	}
	p.rail.SetModel(m)
	// Best-effort PR indicator source (namespace "pr", same daemon-populated
	// cache the task list reads — never fetched here). A read error just leaves
	// the PR mark off.
	if p.reader != nil {
		if pr, perr := p.reader.ListMetaByNamespace("pr"); perr == nil {
			p.prMeta = pr
		}
	}
	// Feed the same PR cache to the rail so managed rows render a PR indicator
	// (best-effort; nil just leaves the cells off).
	p.rail.SetPRMeta(p.prMeta)
	// SetModel rebuilt the model's backing arrays, so the prior Selection
	// pointers are stale — re-derive and rebind (task IDs usually unchanged, so
	// bindPane is a no-op and the emulators are preserved).
	p.applySelection()
	uxlog.Log("[hera-view] rail refreshed: pinned=%d active=%d archived=%d freelance=%d (remote=%v)",
		len(m.Pinned), len(m.Active), len(m.Archived), len(m.Freelance), p.remote)
}

// Draw computes the three-region layout and paints each region, covering the
// full bounding rect (DrawBorderedPanel / FillArea) so no stale cells survive —
// per the CLAUDE.md UX-rendering rules (no Sync; full-rect coverage instead).
func (p *HeraPage) Draw(screen tcell.Screen) {
	p.DrawForSubclass(screen, p)
	x, y, w, h := p.GetInnerRect()
	if w <= 0 || h <= 0 {
		return
	}

	if p.remote {
		p.drawRemoteBanner(screen, x, y, w, h)
		return
	}

	railW := heraRailWidth
	if railW > w {
		railW = w
	}

	// Right area: coordinator (HERA) pane + agent/details pane, split evenly.
	// When the terminal is too narrow for a right area, the rail takes it all
	// and both right regions are marked absent so focus can't land on them.
	rx := x + railW
	rw := w - railW
	coordW := 0
	agentW := 0
	if rw > 0 {
		coordW = rw / 2
		agentW = rw - coordW
	}
	// Reconcile focus-machine present flags from the NORMAL split geometry —
	// independent of fullscreen — so Tab traversal can still walk to the hidden
	// pane while one pane is zoomed.
	p.focus.SetCoordPresent(coordW >= 2)
	p.focus.SetAgentPresent(agentW >= 2)

	// Advertise a staged clipboard payload on the focused terminal pane via a
	// `(ctrl+y copy)` border-title affordance. clipReady is the focused pane's
	// hint state (refreshed each tick), so at most one pane shows it.
	p.coordPane.SetBorderTitle(clipboardHintTitle(coordPaneTitle, p.clipReady && p.focus.State() == FocusCoord))
	p.agentPane.SetBorderTitle(clipboardHintTitle(agentPaneTitle, p.clipReady && !p.detailsMode && p.focus.State() == FocusAgent))

	p.rail.SetFocused(p.focus.State() == FocusRail)
	p.rail.SetRect(x, y, railW, h)
	p.rail.Draw(screen)

	// Fullscreen: the rail stays put and the single focused content pane fills
	// the entire area to its right. The other content region is not drawn and
	// its hit-test rect collapses to zero so a stale click/scroll can't land on
	// it. Same full-rect coverage + no-Sync rules as the split path.
	if p.focus.Fullscreen() && rw > 0 && p.focus.State() != FocusRail {
		switch p.focus.State() {
		case FocusCoord:
			p.coordX, p.coordW = rx, rw
			p.agentX, p.agentW = rx+rw, 0
			p.coordPane.SetFocused(true)
			p.coordPane.SetRect(rx, y, rw, h)
			p.coordPane.Draw(screen)
		case FocusAgent:
			p.agentX, p.agentW = rx, rw
			p.coordX, p.coordW = rx, 0
			if p.detailsMode {
				p.drawDetailsRegion(screen, y, rw, h)
			} else {
				p.agentPane.SetFocused(true)
				p.agentPane.SetRect(p.agentX, y, rw, h)
				p.agentPane.Draw(screen)
			}
		}
		return
	}

	// Normal split. Record region rects so Tab traversal and mouse hit-testing
	// see the live layout.
	p.coordX, p.coordW = rx, coordW
	p.agentX, p.agentW = rx+coordW, agentW

	if coordW >= 2 {
		p.coordPane.SetFocused(p.focus.State() == FocusCoord)
		p.coordPane.SetRect(rx, y, coordW, h)
		p.coordPane.Draw(screen)
	}
	if agentW >= 2 {
		if p.detailsMode {
			p.drawDetailsRegion(screen, y, agentW, h)
		} else {
			p.agentPane.SetFocused(p.focus.State() == FocusAgent)
			p.agentPane.SetRect(p.agentX, y, agentW, h)
			p.agentPane.Draw(screen)
		}
	}
}

// drawDetailsRegion stacks the read-only roster (top) over the embedded plan
// DAG (bottom) for a selected coordinator — both render at once, no toggle. The
// roster is sized to its natural content height, capped at half the region so
// the plan graph always keeps at least half; the plan fills the remainder. Each
// widget draws its own bordered panel covering its full sub-rect, so no stale
// cells survive (no Sync — CLAUDE.md UX-rendering rules). The plan graph is the
// interactive surface, so it owns the focused border.
func (p *HeraPage) drawDetailsRegion(screen tcell.Screen, y, w, h int) {
	// Roster sized to its content, capped at half the region so the plan graph
	// keeps at least half, then clamped to the region height for tiny panes.
	rosterH := min(p.details.ContentHeight(), h/2)
	rosterH = max(rosterH, 3)
	rosterH = min(rosterH, h)
	p.details.Draw(screen, p.agentX, y, w, rosterH, false)

	planH := h - rosterH
	if planH >= 2 {
		p.plan.SetRect(p.agentX, y+rosterH, w, planH)
		p.plan.SetFocused(p.focus.State() == FocusAgent)
		p.plan.Draw(screen)
	} else {
		// Pane too short to show the plan graph — zero its rect so a stale rect
		// from a prior (taller) frame can't catch a click/scroll over the roster
		// area. MouseHandler always forwards coordinator-region events to p.plan,
		// which gates on InRect; an empty rect makes that gate reject everything.
		p.plan.SetRect(0, 0, 0, 0)
	}
}

// drawRemoteBanner renders the remote-mode degradation message over the whole
// page area.
func (p *HeraPage) drawRemoteBanner(screen tcell.Screen, x, y, w, h int) {
	inner := widget.DrawBorderedPanel(screen, x, y, w, h, " Hera ", theme.StyleBorder)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}
	msg := "Hera unavailable in remote mode"
	row := inner.Y + inner.H/2
	col := inner.X + (inner.W-len(msg))/2
	if col < inner.X {
		col = inner.X
	}
	widget.DrawText(screen, col, row, inner.W, msg, theme.StyleDimmed)
}

// InputHandler routes keys by the focused region. Tab/BackTab walk the focus
// ladder (rail→coord→agent, skipping absent regions); Ctrl+Alt+Left/Right walk
// the same ladder (Retreat/Advance) so the focus can move between the rail,
// coordinator, and subagent regions without stealing a plain arrow key from a
// focused terminal pane (it mirrors the main agent view's Ctrl+Alt+arrow pane
// switch); Ctrl+Q escapes back to the rail. Rail-focused input drives
// cursor/collapse; a focused terminal pane receives forwarded keystrokes
// (interactive) and PgUp/PgDn scrollback. Plain Left/Right are no longer eaten
// by the global handler (tab nav is 1/2/3 only), so they now reach this handler:
// in a focused terminal pane they forward to the PTY like any other key; in
// coordinator-details mode they drive the embedded DAG's cursor (via
// handleDetailsKey); rail-focused, Left moves the cursor to the parent
// coordinator row (BUG-016) and Right is unused.
func (p *HeraPage) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return p.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		// Fire OnFocusChange on every exit so callers (e.g. the status bar) see
		// the updated focus region on the same frame as the change. Deferred so
		// it fires even on early returns (Tab, CtrlQ, Enter, …).
		defer p.notifyFocusChange()
		if p.remote {
			return
		}
		// Match the main agent view's Ctrl+Alt+arrow pane switch (app.go): accept
		// EITHER modifier, not strictly both — terminals are inconsistent about
		// which of Ctrl/Alt they report for this chord, and the loose check is the
		// proven pattern already wired for the agent view's pane navigation.
		ctrlAlt := event.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0
		switch event.Key() {
		case tcell.KeyCtrlZ:
			// Fullscreen the focused content pane (plugin parity). ALWAYS consume
			// — even when this is a no-op on the rail — so the 0x1A byte can never
			// fall through to a focused pane's PTY and SIGTSTP-suspend its agent.
			// That silent fall-through was the footgun this binding closes.
			p.focus.ToggleFullscreen()
			uxlog.Log("[hera-view] ctrl+z fullscreen toggle: state=%d fullscreen=%v", p.focus.State(), p.focus.Fullscreen())
			return
		case tcell.KeyTab, tcell.KeyBacktab:
			// Tab / Shift-Tab walk the focus ladder ONLY from the rail (entering a
			// pane) and from the read-only Details/tree region. Once a TERMINAL pane
			// is focused they must pass THROUGH to the agent PTY so the agent's own
			// autocomplete works (e.g. `/plugi`+Tab → `/plugin`) — BUG-019. The TUI
			// can't tell whether the agent consumed Tab, so it can't be smart about
			// it; it forwards unconditionally. Escape a pane without Tab via Ctrl+Q
			// or the Ctrl+Alt+←/→ ladder below, so the operator is never trapped.
			if p.terminalPaneFocused() {
				break // fall through to the per-region forwardKey (PTY)
			}
			if event.Key() == tcell.KeyTab {
				p.focus.Advance()
			} else {
				p.focus.Retreat()
			}
			return
		case tcell.KeyCtrlQ:
			p.focus.ToRail()
			return
		case tcell.KeyCtrlY:
			// Copy the agent-staged clipboard payload for the focused TERMINAL
			// pane's task. Conditional intercept, mirroring the main agent view:
			// steal ctrl+y from the PTY ONLY when a payload is staged (clipReady,
			// refreshed each tick for the focused pane's task). When nothing is
			// staged — or focus is on the rail / coordinator details (no PTY) — fall
			// through to the per-region dispatch so vim/emacs-style yank still
			// reaches the agent. The App's callback resolves the payload from
			// FocusedTerminalTaskID, so the copy is scoped to the focused pane.
			if p.clipReady && p.terminalPaneFocused() && p.OnCopyClipboard != nil {
				if id := p.FocusedTerminalTaskID(); id != "" {
					uxlog.Log("[hera-view] ctrl+y copy staged clipboard: task=%s", id)
					p.OnCopyClipboard(id)
					return
				}
			}
		case tcell.KeyLeft:
			if ctrlAlt {
				p.focus.Retreat()
				return
			}
			// Rail-focused Left: move the selection up to the parent coordinator row
			// (nearest rrOrch header or bridging row with smaller depth). Scoped to
			// FocusRail only — when a pane is focused, Left passes through to the PTY
			// unchanged via the per-region dispatch below (BUG-016).
			if p.focus.State() == FocusRail {
				p.rail.CursorToParent()
				return
			}
		case tcell.KeyRight:
			if ctrlAlt {
				p.focus.Advance()
				return
			}
		// Cmd+Up / Cmd+Down (tcell: KeyUp/KeyDown + ModCtrl|ModAlt, the mod-7
		// encoding iTerm2 maps Cmd+arrow onto) move the rail selection without
		// changing pane focus. They are intercepted here — BEFORE the
		// per-region forward — so the mod-7 escape sequence never reaches the
		// focused pane's PTY (BUG-002).
		case tcell.KeyUp:
			if ctrlAlt {
				p.rail.CursorUp()
				return
			}
		case tcell.KeyDown:
			if ctrlAlt {
				p.rail.CursorDown()
				return
			}
		}
		switch p.focus.State() {
		case FocusRail:
			if p.handleRailMutation(event) {
				return
			}
			p.rail.InputHandler()(event, setFocus)
		case FocusCoord:
			p.forwardKey(p.coordPane, event)
		case FocusAgent:
			if p.detailsMode {
				p.handleDetailsKey(event, setFocus)
			} else {
				p.forwardKey(p.agentPane, event)
			}
		}
	})
}

// terminalPaneFocused reports whether the focused region forwards keystrokes to
// a live agent PTY: the coordinator pane (always a terminal), or the agent pane
// in terminal mode (a worker/leaf selection — NOT the coordinator Details/tree
// region, which has no PTY). When true, Tab/Shift-Tab pass THROUGH to the PTY so
// the agent's autocomplete works instead of walking the focus ladder (BUG-019).
func (p *HeraPage) terminalPaneFocused() bool {
	switch p.focus.State() {
	case FocusCoord:
		return true
	case FocusAgent:
		return !p.detailsMode
	default:
		return false
	}
}

// handleDetailsKey routes keys for a focused Details region (coordinator
// selected). The region stacks the read-only roster over the embedded plan
// graph, so the plan widget is the only interactive surface: nav (j/k/h/l +
// arrows), Enter/Space (fan-out/collapse a group, drill into a sub-coordinator,
// or jump to a leaf's agent view via the wired OnEnter callback), and Esc
// (drill out). At drill-depth 0 the widget no-ops Esc, so the page handles it
// here as a pane-escape back to the rail (otherwise the operator would be
// trapped in the plan region). The global handler reserves only 1/2/3/q/? — see
// gotchas/keybindings.md.
func (p *HeraPage) handleDetailsKey(event *tcell.EventKey, setFocus func(tview.Primitive)) {
	if event.Key() == tcell.KeyEscape && p.plan.DrillDepth() == 0 {
		// Root of the plan nav stack: Esc escapes the pane back to the rail
		// rather than dead-ending in the widget's root no-op.
		p.focus.ToRail()
		return
	}
	p.plan.InputHandler()(event, setFocus)
}

// rebuildPlan reprojects the given orchestrator's PLAN DAG — its planned
// (never-bound) and live worker roles as nodes, its hera_blocks blocking edges
// as dependency edges — into the embedded plan widget. `root` is the
// orchestrator in Details view — the selected one for a top-level coordinator,
// or the bridged CHILD for a worker-bridge sub-coord (BUG-004). A nil root
// yields an empty graph. The bridge index (Model.bridgeIndex) lets the
// projection stamp Node.Drillable on a worker whose bound task coordinates a
// child orchestrator (D6), so the single-arg heraPlanNodes is NOT used here —
// it leaves Drillable false. MUST run on the tview main thread
// (heraPlanNodesWithBridge is a pure read over the rail's already-built model;
// SetData recomputes layout but touches no I/O).
func (p *HeraPage) rebuildPlan(root *OrchView) {
	// Same orchestrator as the last projection (and not drilled in) → this is a
	// refresh tick re-projecting an unchanged-or-evolved plan: route through
	// UpdateData so the operator's cursor and fanned groups survive. A different
	// orchestrator (or nil) is a genuine selection change → SetData full-resets.
	// A drill-in stack from a previous coordinator is abandoned either way (pop to
	// root) so the visible plan is this root's, not a buried child frame.
	sameOrch := root != nil && p.planOrchID == root.ID && p.plan.DrillDepth() == 0
	for p.plan.DrillDepth() > 0 {
		p.plan.PopOrch()
	}
	if root == nil {
		p.planOrchID = 0
		p.plan.SetData(nil, nil)
		return
	}
	nodes, edges := heraPlanNodesWithBridge(root, p.rail.Model().bridgeIndex())
	if sameOrch {
		p.plan.UpdateData(nodes, edges)
	} else {
		p.plan.SetData(nodes, edges)
	}
	p.planOrchID = root.ID
	uxlog.Log("[hera-view] plan render: orch=%s nodes=%d edges=%d sameOrch=%v",
		root.Name, len(nodes), len(edges), sameOrch)
}

// handleRailMutation maps the rail-focus mutation keyset to the page's mutation
// callbacks, acting on the CURRENT rail selection. It returns true when it
// consumed the key (so the caller skips rail navigation). Keys with no target
// (empty selection) or no wired callback fall through as inert no-ops.
//
// The keyset mirrors Hera's rail bindings, adapted to Argus key routing — see
// gotchas/keybindings.md for the full keymap + global-collision audit. Tab /
// Ctrl+Q (focus ladder) are handled by the caller before this; nav keys
// (j/k/↑/↓/space) fall through to the rail.
func (p *HeraPage) handleRailMutation(event *tcell.EventKey) bool {
	// While the rail is in `/` search INPUT mode every keystroke is filter input,
	// not a command: return false so the key falls through to rail.InputHandler
	// (which appends it to the query / handles Esc·Enter·Backspace). This also
	// suppresses the Enter-reattach and Ctrl+D paths below while typing.
	if p.rail.Filtering() {
		return false
	}
	sel := p.rail.Selection()
	switch event.Key() {
	case tcell.KeyCtrlD:
		return p.fire(p.OnDelete, sel)
	case tcell.KeyEnter:
		// Enter "enters" the selected role and revives its session first, then
		// moves focus into the pane. Reattach fires for:
		//   * a DEAD session (no live session in the runner) — any role; or
		//   * a LIVE worker/freelance role — the App then revives it ONLY if it
		//     looks suspended/stuck (idle, not parked at a prompt), so a
		//     SIGTSTP'd worker can be brought back from the Hera view. A live
		//     COORDINATOR is navigate-only (operator-interactive), so Enter never
		//     restarts a healthy coordinator.
		// An empty selection only advances focus. On a coordinator (the folded
		// orchestrator header) the selected role is nil, so FocusTaskID falls
		// back to the orchestrator's coordinator task — Enter on a header
		// reattaches a DEAD coordinator session (and is navigate-only when live).
		taskID := sel.FocusTaskID()
		if taskID != "" && p.OnReattach != nil {
			live := p.resolve != nil && p.resolve(taskID) != nil
			if !live || !sel.IsCoordinator() {
				uxlog.Log("[hera-view] reattach key on task=%s (live=%v coordinator=%v)", taskID, live, sel.IsCoordinator())
				p.OnReattach(sel)
			}
		}
		// Land on the AGENT (right) terminal for a plain worker selection so the
		// user can type into it immediately. For coordinators and worker-bridge
		// sub-coordinators (detailsMode) the right region is the Details summary,
		// not a live terminal — advance to the COORD (middle) pane instead.
		// The empty-rail case (sel.Orch == nil) also advances to coord.
		if !p.detailsMode && sel.Orch != nil {
			p.focus.SetRegion(FocusAgent)
		} else {
			p.focus.Advance()
		}
		return true
	case tcell.KeyRune:
		switch event.Rune() {
		case 'w':
			return p.fire(p.OnSpawnWorker, sel)
		case 'r':
			return p.fire(p.OnRename, sel)
		case 'a':
			return p.fire(p.OnArchiveToggle, sel)
		case 'P':
			return p.fire(p.OnPinToggle, sel)
		case 's':
			return p.fire(p.OnStatusAdvance, sel)
		case 'S':
			return p.fire(p.OnStatusRevert, sel)
		case 'J':
			// Adopt a freelancer into / re-parent a coordinator under a chosen
			// orchestrator. Rail-focus-only (a focused pane forwards `J` to the
			// PTY via forwardKey, never reaching here). The handler sorts out
			// freelance vs coordinator vs not-applicable and surfaces feedback.
			return p.fire(p.OnAdopt, sel)
		case 'n':
			// New top-level coordinator (BUG-006). Selection-INDEPENDENT — it is
			// the bootstrap affordance, so it fires even on an empty rail and does
			// NOT route through the selection-gated `fire`.
			if p.OnNewCoordinator != nil {
				p.OnNewCoordinator(sel)
			}
			return true
		case 'C':
			// Clear the selected coordinator's archive: NUKE every Tier-1 hidden
			// item under it (BUG-022). Acts on the selection.
			return p.fire(p.OnClearArchive, sel)
		}
	}
	return false
}

// notifyFocusChange fires OnFocusChange with the current focus state. Called
// after any keyboard or mouse event that may have shifted which region holds
// focus so callers (e.g. the status bar) can refresh focus-aware displays on
// the same frame as the change.
func (p *HeraPage) notifyFocusChange() {
	if p.OnFocusChange != nil {
		p.OnFocusChange(p.focus.State())
	}
}

// fire invokes a mutation callback when it is wired and the selection has a
// target, returning whether the key was consumed. A wired callback always
// consumes its key (even on an empty selection) so the keystroke never leaks
// to rail navigation; an unwired callback lets the key fall through.
func (p *HeraPage) fire(cb func(Selection), sel Selection) bool {
	if cb == nil {
		return false
	}
	if sel.Role == nil && sel.Orch == nil {
		return true // wired, but nothing selected — consume silently
	}
	cb(sel)
	return true
}

// PasteHandler forwards bracketed paste to the focused terminal pane (which
// wraps it in paste markers and writes it to the live session). Declared
// explicitly per the CLAUDE.md page-wrapper rule so bracket paste isn't
// silently dropped. Rail/details focus discard paste (no text input).
func (p *HeraPage) PasteHandler() func(pastedText string, setFocus func(p tview.Primitive)) {
	return p.WrapPasteHandler(func(text string, setFocus func(p tview.Primitive)) {
		if p.remote {
			return
		}
		switch p.focus.State() {
		case FocusCoord:
			p.coordPane.PasteHandler()(text, setFocus)
		case FocusAgent:
			if !p.detailsMode {
				p.agentPane.PasteHandler()(text, setFocus)
			}
		}
	})
}

// MouseHandler hit-tests the click against the recorded region rects, focuses
// that region, forwards scroll to the pane under the cursor, then anchors tview
// focus back on the page wrapper. The wrapper's InputHandler re-dispatches by
// region, so keyboard input keeps flowing even though tview's focus tracker
// sees the wrapper — the CLAUDE.md page-wrapper focus guard. Pattern matches
// DAGPage / TaskPage / SettingsPage.
func (p *HeraPage) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return p.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
		consumed := false
		click := action == tview.MouseLeftClick || action == tview.MouseLeftDown
		// Notify focus change after any click that may have moved the focused
		// region so callers see the updated state on the same frame.
		if click {
			defer p.notifyFocusChange()
		}
		if !p.remote {
			x, _ := event.Position()
			switch p.regionAt(x) {
			case FocusRail:
				consumed, _ = p.rail.MouseHandler()(action, event, setFocus)
				if click {
					p.focus.SetRegion(FocusRail)
				}
			case FocusCoord:
				consumed, _ = p.coordPane.MouseHandler()(action, event, setFocus)
				if click {
					p.focus.SetRegion(FocusCoord)
				}
			case FocusAgent:
				if p.detailsMode {
					// Coordinator: the plan graph (bottom of the stacked region) is
					// the interactive surface; it ignores clicks outside its own rect,
					// so the roster area above it is inert.
					consumed, _ = p.plan.MouseHandler()(action, event, setFocus)
				} else {
					consumed, _ = p.agentPane.MouseHandler()(action, event, setFocus)
				}
				if click {
					p.focus.SetRegion(FocusAgent)
				}
			}
		}
		if click {
			setFocus(p)
		}
		return consumed, nil
	})
}
