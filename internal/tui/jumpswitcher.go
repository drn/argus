package tui

import (
	heramodel "github.com/drn/argus/internal/hera/model"
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
// The Projects column is fed a FRESH Hera model (heraPage.Refresh first) so
// it matches the real rail even when opened from the Tasks tab or agent view,
// where the per-tick rail rebuild is gated off. MUST run on the tview
// goroutine without a.mu held (it reads agentState under a.mu).
func (a *App) openJumpSwitcher() {
	if a.jumpSwitcher != nil {
		uxlog.Log("[switcher] open ignored: already open")
		return
	}
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
	a.jumpSwitcher = NewSwitcherPopup(a.jumpSwitcherFeed(), seed)
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

// jumpSwitcherFeed snapshots the data the real Tasks list and Projects rail
// are fed (see refreshTasksWithIDs), for the popup's private copies. Never
// takes a.mu: it runs both from openJumpSwitcher and from the per-tick refeed
// inside refreshTasksWithIDs, whose callers already hold it.
func (a *App) jumpSwitcherFeed() SwitcherFeed {
	workers, coords := a.readHeraRoles()
	idleUnvisited := make([]string, 0, len(a.idleUnvisited))
	for id := range a.idleUnvisited {
		idleUnvisited = append(idleUnvisited, id)
	}
	f := SwitcherFeed{
		Tasks:         a.tasks,
		Managed:       a.readManagedTasks(),
		HeraWorkers:   workers,
		HeraCoords:    coords,
		Running:       a.runningIDs,
		Idle:          a.idleIDs,
		IdleUnvisited: idleUnvisited,
		// The SAME sticky set the real Tasks list gets; the Projects column's
		// (?) comes from the Hera model, which heraPage.SetNeedsInput already
		// fed with needsInputForHeraRail's admission of this set.
		NeedsInput: a.needsInputIDs,
		PRStates:   a.readPRStates(),
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
// fingerprint, so an idle tick costs no BuildModel. Called from
// refreshTasksWithIDs with a.mu held; must not take it.
func (a *App) refeedJumpSwitcher() {
	if a.mode != modeJumpSwitcher || a.jumpSwitcher == nil {
		return
	}
	if a.heraPage != nil && !a.heraPage.IsRemote() {
		a.heraPage.ScheduleRefresh()
	}
	a.jumpSwitcher.Refeed(a.jumpSwitcherFeed())
}

// handleJumpSwitcherKey routes every key to the popup while it is open
// (handleGlobalKey sends modeJumpSwitcher here ahead of every global action,
// so ctrl+g cycles the popup's own cursor instead of the global jump, and
// ctrl+j is a no-op), then acts on its outcome.
func (a *App) handleJumpSwitcherKey(event *tcell.EventKey) {
	p := a.jumpSwitcher
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
//     the Projects tab, then land like rail Enter via JumpToTask (ancestor
//     expansion + reattach + pane focus); a row with no task of its own lands
//     on its orchestrator row via SelectOrch. Both failing leaves the operator
//     on the Projects tab.
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
