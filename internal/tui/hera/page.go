package hera

import (
	"github.com/drn/argus/internal/tui/dagview"
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

// detailsSubMode selects what the right (Details) region renders when a
// COORDINATOR role is selected. M7 folds the Sugiyama DAG widget in as a second
// sub-mode of that region; `g` (handled in handleDetailsKey) toggles between
// them. A worker/leaf selection ignores this — it always shows the AGENT
// terminal (the sub-mode is sticky in the background but only consulted while a
// coordinator is selected).
type detailsSubMode uint8

const (
	subModeRoster detailsSubMode = iota // read-only worker roster (M6b default)
	subModeDAG                          // embedded dependency-graph DAG (M7)
)

// DAGNodeProvider returns the dagview nodes scoped to the given orchestrator's
// live-bound tasks, projected through Argus's `dagNodesFromTasks` (archived-drop
// + orphan-filter). The App wires it (it owns db.Tasks() + the shared
// projection); remote mode never sets it, so the DAG render mode shows an empty
// graph there. Keeping the projection in the App avoids forking dagNodesFromTasks
// into the hera package (web/TUI parity) and a Tasks() seam onto HeraReader.
type DAGNodeProvider func(orch *OrchView) []dagview.Node

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

	// M7 DAG render mode of the Details region (coordinator selection only).
	dag         *dagview.Widget // embedded dependency graph; reused from the legacy DAG tab
	dagProvider DAGNodeProvider // scoped-node projection seam (nil in remote mode)

	// Selection + per-pane bound task (so SetSession only fires on change).
	sel         Selection
	detailsMode bool
	detailsSub  detailsSubMode // roster ↔ DAG within a coordinator's Details region
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
	OnArchiveToggle func(Selection) // `a` — archive/unarchive selected role/orchestrator
	OnPinToggle     func(Selection) // `P` — pin/unpin selected role/orchestrator
	OnStatusAdvance func(Selection) // `s` — advance selected role status one rung
	OnStatusRevert  func(Selection) // `S` — revert selected role status one rung
	OnDelete        func(Selection) // ctrl+d — delete selected role/orchestrator (confirm)
	OnReattach      func(Selection) // Enter on a dead-session row — restart its session

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
		dag:       dagview.New(),
	}
	p.coordPane.SetBorderTitle(" Coordinator ")
	// Retitle the embedded DAG so it reads as the coordinator's dependency graph,
	// not a second top-level " DAG " tab (gotchas/dag-rendering.md).
	p.dag.SetTitle(" Dependencies ")
	p.rail.SetFocused(true)
	// Rebind the panes whenever the rail cursor lands on a different role.
	p.rail.SetOnSelectionChanged(p.applySelection)
	p.refresher = NewRefresher(DefaultRefreshDebounce, p.doRefresh)
	return p
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

// DAG exposes the embedded DAG widget so the App can wire its callbacks
// (OnEnter/OnLink/OnUnlink/OnHalt/OnBranchChange) exactly as it wires the legacy
// DAG tab's widget — the link/unlink/halt handlers are shared between the two
// surfaces. The page owns the widget's rect, focus, and node set; the App owns
// what its callbacks do.
func (p *HeraPage) DAG() *dagview.Widget { return p.dag }

// SetDAGNodeProvider wires the orchestrator-scoped node projection. Called once
// by the App in local mode; left nil in remote mode (the DAG render mode then
// shows an empty graph).
func (p *HeraPage) SetDAGNodeProvider(fn DAGNodeProvider) { p.dagProvider = fn }

// DetailsSubMode reports the current Details-region sub-mode (test seam).
func (p *HeraPage) DetailsSubMode() detailsSubMode { return p.detailsSub }

// Rail exposes the inner rail (test seam + 6b wiring).
func (p *HeraPage) Rail() *Rail { return p.rail }

// Machine exposes the focus machine (test seam + 6b wiring). Not named Focus()
// because that collides with tview.Primitive's Focus(func(tview.Primitive)).
func (p *HeraPage) Machine() *FocusMachine { return p.focus }

// IsRemote reports whether the page is in the remote-degraded mode.
func (p *HeraPage) IsRemote() bool { return p.remote }

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
	m, err := BuildModel(p.reader)
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
	// Record region rects + reconcile focus-machine present flags BEFORE
	// drawing, so Tab traversal and mouse hit-testing see the live layout.
	p.coordX, p.coordW = rx, coordW
	p.agentX, p.agentW = rx+coordW, agentW
	p.focus.SetCoordPresent(coordW >= 2)
	p.focus.SetAgentPresent(agentW >= 2)

	p.rail.SetFocused(p.focus.State() == FocusRail)
	p.rail.SetRect(x, y, railW, h)
	p.rail.Draw(screen)

	if coordW >= 2 {
		p.coordPane.SetFocused(p.focus.State() == FocusCoord)
		p.coordPane.SetRect(rx, y, coordW, h)
		p.coordPane.Draw(screen)
	}
	if agentW >= 2 {
		switch {
		case p.detailsMode && p.detailsSub == subModeDAG:
			// Coordinator + DAG sub-mode: the dependency graph fills the region.
			// The widget draws its own bordered panel (retitled " Dependencies "),
			// covering the full sub-rect — no stale cells, no Sync.
			p.dag.SetRect(p.agentX, y, agentW, h)
			p.dag.SetFocused(p.focus.State() == FocusAgent)
			p.dag.Draw(screen)
		case p.detailsMode:
			p.details.Draw(screen, p.agentX, y, agentW, h, p.focus.State() == FocusAgent)
		default:
			p.agentPane.SetFocused(p.focus.State() == FocusAgent)
			p.agentPane.SetRect(p.agentX, y, agentW, h)
			p.agentPane.Draw(screen)
		}
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
// ladder (rail→coord→agent, skipping absent regions); Ctrl+Q escapes back to
// the rail. Rail-focused input drives cursor/collapse; a focused terminal pane
// receives forwarded keystrokes (interactive) and PgUp/PgDn scrollback. Left/
// Right are no longer eaten by the global handler (tab nav is 1/2/3 only), so
// they now reach this handler: in a focused terminal pane they forward to the
// PTY like any other key; rail-focused they currently fall through unused.
func (p *HeraPage) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return p.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		if p.remote {
			return
		}
		switch event.Key() {
		case tcell.KeyTab:
			p.focus.Advance()
			return
		case tcell.KeyBacktab:
			p.focus.Retreat()
			return
		case tcell.KeyCtrlQ:
			p.focus.ToRail()
			return
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

// handleDetailsKey routes keys for a focused Details region (coordinator
// selected). `g` toggles roster ↔ DAG; in DAG sub-mode every other key is
// forwarded to the embedded dagview widget (cursor nav + l/L/h/Enter, which
// fire the OnLink/OnUnlink/OnHalt/OnEnter callbacks the App wired). `g` is free:
// the dagview keyset is arrows/hjkl/l/L/h/Enter, and the global handler reserves
// only 1/2/3/q/? — see gotchas/keybindings.md. Roster sub-mode is read-only, so
// non-`g` keys are dropped there.
func (p *HeraPage) handleDetailsKey(event *tcell.EventKey, setFocus func(tview.Primitive)) {
	if event.Key() == tcell.KeyRune && event.Rune() == 'g' {
		p.toggleDetailsSubMode()
		return
	}
	if p.detailsSub == subModeDAG {
		p.dag.InputHandler()(event, setFocus)
	}
}

// toggleDetailsSubMode flips the Details region between the roster and the DAG.
// Entering DAG mode (re)builds the orchestrator-scoped node set so the graph is
// fresh; the sub-mode is sticky across selections (a user who prefers the DAG
// keeps seeing it as they move between coordinators).
func (p *HeraPage) toggleDetailsSubMode() {
	if p.detailsSub == subModeRoster {
		p.detailsSub = subModeDAG
		p.rebuildDAG()
		uxlog.Log("[hera-view] details DAG mode ON")
	} else {
		p.detailsSub = subModeRoster
		uxlog.Log("[hera-view] details DAG mode OFF")
	}
}

// rebuildDAG reprojects the selected orchestrator's dependency subgraph into the
// embedded widget. A nil provider (remote mode) or no orchestrator selection
// yields an empty graph. MUST run on the tview main thread (SetNodes recomputes
// layout but touches no I/O).
func (p *HeraPage) rebuildDAG() {
	if p.dagProvider == nil || p.sel.Orch == nil {
		p.dag.SetNodes(nil)
		return
	}
	nodes := p.dagProvider(p.sel.Orch)
	p.dag.SetNodes(nodes)
	uxlog.Log("[hera-view] DAG render mode: orch=%s nodes=%d", p.sel.Orch.Name, len(nodes))
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
	sel := p.rail.Selection()
	switch event.Key() {
	case tcell.KeyCtrlD:
		return p.fire(p.OnDelete, sel)
	case tcell.KeyEnter:
		// Enter "enters" the selected role: restart a dead session (reattach)
		// then move focus into the pane to interact. A live row just advances
		// focus. An empty selection only advances focus.
		taskID := sel.TaskID()
		if taskID != "" && p.OnReattach != nil && (p.resolve == nil || p.resolve(taskID) == nil) {
			uxlog.Log("[hera-view] reattach key on task=%s (no live session)", taskID)
			p.OnReattach(sel)
		}
		p.focus.Advance()
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
		}
	}
	return false
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
				switch {
				case p.detailsMode && p.detailsSub == subModeDAG:
					consumed, _ = p.dag.MouseHandler()(action, event, setFocus)
				case !p.detailsMode:
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
