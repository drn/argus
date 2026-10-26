package tui

import (
	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/keymap"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// jumpSwitcherPage is the tview.Pages name the jump switcher overlay mounts
// under while open.
const jumpSwitcherPage = "jumpswitcher"

// openJumpSwitcher opens the ctrl+j jump switcher (global.switcher,
// add-two-column-switcher) over whatever is on screen. Reached from
// handleGlobalKey's unconditional CtxGlobal dispatch (every mode/focus that
// isn't a form, modal, or plugin view — those are routed earlier) and from
// the command palette. A no-op while the switcher is already open (its own
// key routing owns every key then, so this only guards direct callers).
//
// "Open" is keyed on a.mode, NOT on a.jumpSwitcher != nil: a path that swaps
// a.mode while the popup is up (closeHeraConfirm forcing modeTaskList after an
// async-classified confirm, or the fork-completion callback's onTaskSelect)
// leaves a stale non-nil popup behind that only closeJumpSwitcher would clear.
// Guarding on the pointer stranded ctrl+j dead until restart; the stale popup
// is discarded here and a fresh one opens instead.
//
// The Projects column is fed a FRESH Hera model (heraPage.Refresh first) so
// it matches the real rail even when opened from the Tasks tab or agent view,
// where the per-tick rail rebuild is gated off. MUST run on the tview
// goroutine without a.mu held (it reads agentState under a.mu).
func (a *App) openJumpSwitcher() {
	if a.mode == modeJumpSwitcher && a.jumpSwitcher != nil {
		uxlog.Log("[switcher] open ignored: already open")
		return
	}
	a.discardStaleJumpSwitcher()
	a.mu.Lock()
	viewing := a.agentState.TaskID
	a.mu.Unlock()

	current := ""
	switch {
	case a.mode == modeAgent:
		current = viewing
	case a.header.ActiveTab() == widget.TabHera && a.heraPage != nil:
		current = a.heraPage.Rail().Selection().FocusTaskID()
	default:
		if t := a.tasklist.SelectedTask(); t != nil {
			current = t.ID
		}
	}

	if a.heraPage != nil && !a.heraPage.IsRemote() {
		a.heraPage.Refresh()
	}
	seed := SwitcherSeed{CurrentTaskID: current, TasksExpanded: a.tasklist.Expanded()}
	if a.heraPage != nil {
		seed.Rail = a.heraPage.Rail()
	}
	a.jumpSwitcher = NewSwitcherPopup(a.jumpSwitcherFeed(a.readSwitcherReads()), seed)
	if b, ok := a.activeKeymap().BindingFor(keymap.CtxGlobal, keymap.ActGlobalJumpNeedsInput); ok {
		a.jumpSwitcher.SetNextNeedsInputKey(b)
	}
	a.jumpSwitcherReturnMode = a.mode
	a.mode = modeJumpSwitcher
	// The popup is not a Tasks-tab view, so the spinner redraw gate falls back
	// to its fleet-wide check (add-spinner-visible-scope) — recompute now
	// rather than waiting a tick, so the popup's glyphs animate immediately.
	a.mu.Lock()
	a.visibleActiveSpinner = a.computeVisibleActiveSpinner(a.runningIDs, a.idleIDs)
	a.mu.Unlock()
	a.pages.AddPage(jumpSwitcherPage, a.jumpSwitcher, true, true)
	a.tapp.SetFocus(a.jumpSwitcher)
	uxlog.Log("[switcher] opened from mode=%d tab=%d current=%q", a.jumpSwitcherReturnMode, a.header.ActiveTab(), current)
}

// switcherReads are the per-task DB-backed sets the real Tasks list is fed
// each tick (hera-managed set, hera role meta, PR states). The per-tick refeed
// hands over the values refreshTasksWithIDs already read; only the open path
// reads them itself (readSwitcherReads).
type switcherReads struct {
	managed, heraWorkers, heraCoords map[string]bool
	prStates                         map[string]model.PRState
}

// readSwitcherReads reads switcherReads fresh from the DB (open-time only).
func (a *App) readSwitcherReads() switcherReads {
	workers, coords := a.readHeraRoles()
	return switcherReads{managed: a.readManagedTasks(), heraWorkers: workers, heraCoords: coords, prStates: a.readPRStates()}
}

// jumpSwitcherFeed snapshots the data the real Tasks list and Projects rail
// are fed (see refreshTasksWithIDs), for the popup's private copies. Never
// takes a.mu: it runs both from openJumpSwitcher and from the per-tick refeed
// inside refreshTasksWithIDs, whose callers already hold it.
func (a *App) jumpSwitcherFeed(in switcherReads) SwitcherFeed {
	idleUnvisited := make([]string, 0, len(a.idleUnvisited))
	for id := range a.idleUnvisited {
		idleUnvisited = append(idleUnvisited, id)
	}
	f := SwitcherFeed{
		CoordinatorBoard: a.db.Config().UI.CoordinatorBoard,
		Tasks:            a.tasks,
		Managed:          in.managed,
		HeraWorkers:      in.heraWorkers,
		HeraCoords:       in.heraCoords,
		Running:          a.runningIDs,
		Idle:             a.idleIDs,
		IdleUnvisited:    idleUnvisited,
		// The SAME sticky set the real Tasks list gets; the Projects column's
		// (?) comes from the Hera model, which heraPage.SetNeedsInput already
		// fed with needsInputForHeraRail's admission of this set.
		NeedsInput: a.needsInputIDs,
		PRStates:   in.prStates,
		Remote:     true,
		HeraModel:  heramodel.Model{},
	}
	if a.heraPage != nil {
		f.Remote = a.heraPage.IsRemote()
		f.HeraModel = a.heraPage.Rail().Model()
		f.PRMeta = a.heraPage.Rail().PRMeta()
	}
	return f
}

// refeedJumpSwitcher re-snapshots both popup columns on an App tick while the
// switcher is open (design Risks: the model would otherwise go stale). The
// Hera rebuild goes through ScheduleRefresh — gated by the page's change
// fingerprint, so an idle tick costs no BuildModel — and the task-side sets
// are the ones the tick already read (in), so a refeed adds no DB reads.
// Called from refreshTasksWithIDs with a.mu held; must not take it.
func (a *App) refeedJumpSwitcher(in switcherReads) {
	if a.jumpSwitcher == nil {
		return
	}
	if a.mode != modeJumpSwitcher {
		a.discardStaleJumpSwitcher()
		return
	}
	if a.heraPage != nil && !a.heraPage.IsRemote() {
		a.heraPage.ScheduleRefresh()
	}
	a.jumpSwitcher.Refeed(a.jumpSwitcherFeed(in))
}

// discardStaleJumpSwitcher drops a popup left behind by a path that changed
// a.mode while it was open (see openJumpSwitcher). The mode/focus that path
// set is kept as-is — only the orphaned page and pointer go. A no-op when no
// popup exists or it is still the live mode.
func (a *App) discardStaleJumpSwitcher() {
	if a.jumpSwitcher == nil || a.mode == modeJumpSwitcher {
		return
	}
	a.jumpSwitcher = nil
	a.pages.RemovePage(jumpSwitcherPage)
	uxlog.Log("[switcher] discarded stale popup (mode=%d changed while open)", a.mode)
}

// handleJumpSwitcherKey routes every key to the popup while it is open
// (handleGlobalKey sends modeJumpSwitcher here ahead of every global action,
// so ctrl+g cycles the popup's own cursor instead of the global jump), then
// acts on its outcome. The global.switcher key — whatever it is bound to — is
// resolved first and swallowed, so pressing it again while open is a no-op
// rather than filter text (a rune binding) or anything else.
func (a *App) handleJumpSwitcherKey(event *tcell.EventKey) {
	p := a.jumpSwitcher
	if act, ok := a.activeKeymap().Resolve(keymap.CtxGlobal, event); ok && act == keymap.ActGlobalSwitcher {
		uxlog.Log("[switcher] switcher key while open: ignored")
		return
	}
	p.InputHandler()(event, func(tview.Primitive) {})
	switch {
	case p.Canceled():
		a.closeJumpSwitcher()
		uxlog.Log("[switcher] closed (cancel)")
	case p.Selected():
		sel := p.Selection()
		a.closeJumpSwitcher()
		a.routeJumpSwitcherSelection(sel)
	}
}

// closeJumpSwitcher removes the popup and restores whichever mode/focus was
// active when it opened — the same three-way (agent → Hera → Settings → Tasks)
// fallback closeCommandPaletteModal uses.
func (a *App) closeJumpSwitcher() {
	a.mode = a.jumpSwitcherReturnMode
	a.jumpSwitcher = nil
	a.pages.RemovePage(jumpSwitcherPage)
	switch {
	case a.mode == modeAgent:
		a.resumeAgentViewAfterSwitcher()
		a.tapp.SetFocus(a.agentPane)
	case a.header.ActiveTab() == widget.TabHera:
		a.tapp.SetFocus(a.heraPage)
	case a.header.ActiveTab() == widget.TabSettings:
		a.tapp.SetFocus(a.settingsPage)
	default:
		a.tapp.SetFocus(a.tasklist)
	}
}

// resumeAgentViewAfterSwitcher re-syncs the classic agent view the popup was
// covering. While a.mode was modeJumpSwitcher the agent's 200ms redraw loop
// exited (redrawLoopShouldExit) and startSession — e.g. a size-drift kick's
// restart, which isViewingTaskSession lets through while the popup is open —
// skipped attaching its fresh session to agentPane. Re-attach the runner's
// current session if it changed, and restart the redraw loop for a live one.
func (a *App) resumeAgentViewAfterSwitcher() {
	a.mu.Lock()
	taskID := a.agentState.TaskID
	a.mu.Unlock()
	if taskID == "" {
		return
	}
	sess := a.runner.Get(taskID)
	if sess == nil {
		return
	}
	if a.agentPane.Session() != sess {
		a.agentPane.SetSession(sess)
		a.agentPane.ForceResyncPTY()
		uxlog.Log("[switcher] close: re-attached session for task %s", taskID)
	}
	if sess.Alive() {
		a.startAgentRedrawLoop(taskID, sess)
	}
}

// routeJumpSwitcherSelection lands on the popup's chosen row (design D7),
// AFTER the popup has closed and the prior mode/focus is restored.
//
//   - Tasks row: open its agent view with autoStart=false (an exited session
//     is not resurrected), keeping the Tasks-tab cursor in sync so Ctrl+Q
//     lands on it. Choosing the task already being viewed just closes.
//   - Projects row: leave the classic agent view FIRST (exitAgentView —
//     switchTab(TabHera) is a plain mode/page swap that would leave the tab
//     header hidden and the old session teed to an invisible pane), switch to
//     the Projects tab, then land on the EXACT chosen row via JumpToRow
//     (ancestor expansion + reattach + pane focus, or rail focus for a
//     task-less orchestrator header). If that row is gone (the model changed
//     since the popup's snapshot), fall back to JumpToTask, then SelectOrch;
//     all failing leaves the operator on the Projects tab.
func (a *App) routeJumpSwitcherSelection(sel SwitcherSelection) {
	switch sel.Column {
	case SwitcherColumnTasks:
		a.mu.Lock()
		viewing := a.agentState.TaskID
		a.mu.Unlock()
		if a.mode == modeAgent && viewing == sel.TaskID {
			uxlog.Log("[switcher] select task %s: already viewing", sel.TaskID)
			return
		}
		task, err := a.db.Get(sel.TaskID)
		if err != nil || task == nil {
			uxlog.Log("[switcher] select task %s: lookup failed: %v", sel.TaskID, err)
			return
		}
		if a.mode != modeAgent && a.header.ActiveTab() != widget.TabTasks {
			a.switchTab(widget.TabTasks)
		}
		a.tasklist.SelectByID(task.ID)
		a.onTaskSelect(task, false)
		uxlog.Log("[switcher] select task %s (%s): agent view", task.ID, task.Name)
	case SwitcherColumnProjects:
		if a.heraPage == nil || a.heraPage.IsRemote() {
			uxlog.Log("[switcher] select projects row: hera unavailable (ignored)")
			return
		}
		if a.mode == modeAgent {
			a.exitAgentView()
		}
		a.switchTab(widget.TabHera)
		if a.heraPage.JumpToRow(sel.RoleID, sel.OrchID) {
			uxlog.Log("[switcher] select projects role=%d orch=%d task=%q: landed", sel.RoleID, sel.OrchID, sel.TaskID)
			return
		}
		if sel.TaskID != "" && a.heraPage.JumpToTask(sel.TaskID) {
			uxlog.Log("[switcher] select projects task %s: landed", sel.TaskID)
			return
		}
		if sel.OrchID != 0 && a.heraPage.SelectOrch(sel.OrchID) {
			uxlog.Log("[switcher] select projects orch %d: landed", sel.OrchID)
			return
		}
		uxlog.Log("[switcher] select projects task=%q orch=%d: no rail row, staying on Projects tab", sel.TaskID, sel.OrchID)
	}
}
