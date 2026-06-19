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

	// DAG render mode of the Details region (coordinator selection only). When a
	// coordinator is selected the Details region stacks the read-only roster
	// (top) over this embedded orchestration-tree graph (bottom) — both render at
	// once, no toggle. Nodes are projected in-process from the rail's model by
	// heraTreeNodes (the role hierarchy), so there is no provider seam.
	dag *dagview.Widget // embedded orchestration-tree graph

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
	OnArchiveToggle func(Selection) // `a` — archive/unarchive selected role/orchestrator
	OnPinToggle     func(Selection) // `P` — pin/unpin selected role/orchestrator
	OnStatusAdvance func(Selection) // `s` — advance selected role status one rung
	OnStatusRevert  func(Selection) // `S` — revert selected role status one rung
	OnDelete        func(Selection) // ctrl+d — delete selected role/orchestrator (confirm)
	OnReattach      func(Selection) // Enter on a dead-session row — restart its session
	OnAdopt         func(Selection) // `J` — adopt freelancer / reparent coordinator (orch picker)

	// EOL / creation keys (BUG-005/006/010/011/012). OnNewCoordinator and
	// OnPruneDone are selection-INDEPENDENT — they fire even on an empty rail, so
	// they are dispatched directly (not via the selection-gated `fire`). OnRetire
	// and OnPruneDescendants act on the current Selection.
	OnNewCoordinator   func(Selection) // `n` — new top-level coordinator (full new-task modal; selection used only to default the project, fires even when empty)
	OnRetire           func(Selection) // `R` — retire the selected worker (confirm)
	OnPruneDescendants func(Selection) // `C` — prune the selected coordinator's archived descendants (confirm)
	OnPruneDone        func()          // ctrl+R — rail-wide prune of finished coords + agents (confirm)

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
		dag:       dagview.New(),
	}
	p.coordPane.SetBorderTitle(" Coordinator ")
	// Retitle the embedded graph so it reads as the orchestration tree, not a
	// second top-level " DAG " tab (gotchas/hera-view.md).
	p.dag.SetTitle(" Orchestration Tree ")
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

// DAG exposes the embedded orchestration-tree widget so the App can wire its
// OnEnter (jump to a node's agent view) and OnBranchChange (log-only forceRedraw)
// callbacks. The page owns the widget's rect, focus, and node set (projected by
// heraTreeNodes from the rail model); the App owns what OnEnter does.
func (p *HeraPage) DAG() *dagview.Widget { return p.dag }

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

// drawDetailsRegion stacks the read-only roster (top) over the embedded
// orchestration tree (bottom) for a selected coordinator — both render at once,
// no toggle. The roster is sized to its natural content height, capped at half
// the region so the tree always keeps at least half; the tree fills the
// remainder. Each widget draws its own bordered panel covering its full
// sub-rect, so no stale cells survive (no Sync — CLAUDE.md UX-rendering rules).
// The tree is the interactive surface, so it owns the focused border.
func (p *HeraPage) drawDetailsRegion(screen tcell.Screen, y, w, h int) {
	// Roster sized to its content, capped at half the region so the DAG keeps
	// at least half, then clamped to the region height for tiny panes.
	rosterH := min(p.details.ContentHeight(), h/2)
	rosterH = max(rosterH, 3)
	rosterH = min(rosterH, h)
	p.details.Draw(screen, p.agentX, y, w, rosterH, false)

	dagH := h - rosterH
	if dagH >= 2 {
		p.dag.SetRect(p.agentX, y+rosterH, w, dagH)
		p.dag.SetFocused(p.focus.State() == FocusAgent)
		p.dag.Draw(screen)
	} else {
		// Pane too short to show the DAG — zero its rect so a stale rect from a
		// prior (taller) frame can't catch a click/scroll over the roster area.
		// MouseHandler always forwards coordinator-region events to p.dag, which
		// gates on InRect; an empty rect makes that gate reject everything.
		p.dag.SetRect(0, 0, 0, 0)
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
		case tcell.KeyTab:
			p.focus.Advance()
			return
		case tcell.KeyBacktab:
			p.focus.Retreat()
			return
		case tcell.KeyCtrlQ:
			p.focus.ToRail()
			return
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

// handleDetailsKey routes keys for a focused Details region (coordinator
// selected). The region stacks the read-only roster over the embedded
// orchestration tree, so the tree is the only interactive surface: every key
// forwards to it (cursor nav j/k/arrows + Enter, which fires the OnEnter
// callback the App wired — jump to the node's agent view). The global handler
// reserves only 1/2/3/q/? — see gotchas/keybindings.md.
func (p *HeraPage) handleDetailsKey(event *tcell.EventKey, setFocus func(tview.Primitive)) {
	p.dag.InputHandler()(event, setFocus)
}

// rebuildDAG reprojects the given orchestrator's orchestration SUBTREE (the role
// hierarchy — coordinator → workers → sub-coordinators → their workers) into the
// embedded widget. `root` is the orchestrator in Details view — the selected one
// for a top-level coordinator, or the bridged CHILD for a worker-bridge sub-coord
// (BUG-004). A nil root yields an empty graph. MUST run on the tview main thread
// (heraTreeNodes is a pure read over the rail's already-built model; SetNodes
// recomputes layout but touches no I/O).
func (p *HeraPage) rebuildDAG(root *OrchView) {
	if root == nil {
		p.dag.SetNodes(nil)
		return
	}
	nodes := heraTreeNodes(p.rail.Model(), root)
	p.dag.SetNodes(nodes)
	uxlog.Log("[hera-view] tree render: orch=%s nodes=%d", root.Name, len(nodes))
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
	case tcell.KeyCtrlR:
		// Rail-wide prune of finished coords + agents (BUG-012). Rail-scoped: this
		// runs only while FocusRail, so it never collides with the agent-view
		// Ctrl+R (Claude session switcher) which lives in modeAgent. Fires even on
		// an empty rail (selection-independent), so it bypasses `fire`.
		if p.OnPruneDone != nil {
			p.OnPruneDone()
		}
		return true
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
		case 'R':
			// Retire the selected worker (BUG-010). Acts on the selection.
			return p.fire(p.OnRetire, sel)
		case 'C':
			// Prune the selected coordinator's archived descendants (BUG-011).
			return p.fire(p.OnPruneDescendants, sel)
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
					// Coordinator: the DAG (bottom of the stacked region) is the
					// interactive surface; it ignores clicks outside its own rect,
					// so the roster area above it is inert.
					consumed, _ = p.dag.MouseHandler()(action, event, setFocus)
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
