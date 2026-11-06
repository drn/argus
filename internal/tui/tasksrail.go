package tui

import (
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/keymap"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
)

func (a *App) setTasksRail(enabled bool) {
	wasAgent := a.mode == modeAgent
	activeTaskID := a.agentState.TaskID
	a.tasksRailEnabled = enabled
	a.tasklist.SetRailMode(enabled)
	if enabled && wasAgent && activeTaskID != "" {
		a.tasklist.SelectByID(activeTaskID)
	}
	if !enabled {
		a.tasklist.SetFocused(false)
		a.tasklist.SetCursorDimmed(false)
	}
	uxlog.Log("[tasks-rail] layout enabled=%v", enabled)
	if a.header.ActiveTab() != widget.TabTasks {
		return
	}
	if enabled {
		a.showTasksRail()
		if wasAgent {
			a.enterTasksRailTerminal()
		}
		return
	}
	a.mu.Lock()
	a.tasksRailViewing = false
	a.mu.Unlock()
	if wasAgent {
		a.root.ResizeItem(a.header, 0, 0)
		a.pages.SwitchToPage("agent")
		a.tapp.SetFocus(a.agentPane)
	} else {
		a.exitAgentView()
	}
}

func (a *App) showTasksRail() {
	a.tasksRailPending = false
	a.agentPane.SetPending(false)
	a.mu.Lock()
	a.tasksRailViewing = true
	a.mu.Unlock()
	a.root.ResizeItem(a.header, 1, 0)
	a.pages.SwitchToPage("tasks-rail")
	a.focusTasksRail()
	a.reconcileTasksRail()
	a.tapp.SetFocus(a.tasksRailPage)
}

func (a *App) showTasksPage() {
	if a.tasksRailEnabled {
		a.showTasksRail()
		return
	}
	a.pages.SwitchToPage("tasks")
}

// focusTasksRail changes only region state, so it is safe during Draw when a
// resize removes the terminal. The wrapper remains the focused primitive.
func (a *App) focusTasksRail() {
	if !a.tasksRailEnabled || !a.tasksRailViewing {
		return
	}
	changed := a.mode == modeAgent
	a.mu.Lock()
	a.mode = modeTaskList
	a.mu.Unlock()
	a.tasklist.SetFocused(true)
	a.tasklist.SetCursorDimmed(false)
	a.agentPane.SetFocused(false)
	a.filePanel.SetFocused(false)
	if changed {
		if a.focusTracker != nil && a.agentState.TaskID != "" {
			a.focusTracker.SetFocused(a.agentState.TaskID, false)
		}
		uxlog.Log("[tasks-rail] focus rail task=%s", a.agentState.TaskID)
	}
}

func (a *App) previewRailTask(task *model.Task) {
	if !a.tasksRailEnabled || a.header.ActiveTab() != widget.TabTasks {
		return
	}
	if task != nil && a.agentState.TaskID == task.ID && a.tasksRailPage.Bound {
		return
	}
	if task == nil && !a.tasksRailPage.Bound && a.agentState.TaskID == "" {
		return
	}
	a.focusTasksRail()
	if task == nil {
		if a.focusTracker != nil && a.agentState.TaskID != "" {
			a.focusTracker.SetFocused(a.agentState.TaskID, false)
		}
		a.mu.Lock()
		a.agentState.Reset("", "")
		a.mu.Unlock()
		a.agentPane.SetSession(nil)
		a.agentPane.SetTaskID("")
		a.agentPane.ResetVT()
		a.agentPane.ExitDiffMode()
		a.tasksRailPage.Bound = false
		a.worktreeDir = ""
		uxlog.Log("[tasks-rail] selection cleared")
		return
	}
	a.bindTaskAgent(task, false, false)
	uxlog.Log("[tasks-rail] preview task=%s", task.ID)
}

func (a *App) reconcileTasksRail() {
	if !a.tasksRailEnabled || !a.tasksRailViewing || a.header.ActiveTab() != widget.TabTasks {
		return
	}
	// Modals and pending task creation own their bindings until they finish.
	if a.mode != modeTaskList && a.mode != modeAgent {
		return
	}
	if a.tasksRailPending {
		return
	}
	a.previewRailTask(a.tasklist.SelectedTask())
}

func (a *App) enterTasksRailTerminal() {
	task := a.tasklist.SelectedTask()
	if task == nil || !a.tasksRailPage.ContentVisible() {
		return
	}
	a.mu.Lock()
	a.mode = modeAgent
	a.agentFocus = focusTerminal
	a.mu.Unlock()
	a.tasklist.SetFocused(false)
	a.tasklist.SetCursorDimmed(true)
	a.agentPane.SetFocused(true)
	delete(a.idleUnvisited, task.ID)
	a.viewedWhileAgent[task.ID] = true
	a.syncIdleUnvisited()
	a.tapp.SetFocus(a.tasksRailPage)
	if a.focusTracker != nil {
		a.focusTracker.SetFocused(task.ID, true)
	}
	uxlog.Log("[tasks-rail] focus terminal task=%s", task.ID)
}

func (a *App) handleTasksRailKey(e *tcell.EventKey) bool {
	if !a.tasksRailEnabled || !a.tasksRailViewing || a.header.ActiveTab() != widget.TabTasks || (a.mode != modeAgent && a.mode != modeTaskList) {
		return false
	}
	if a.mode == modeTaskList && a.tapp.GetFocus() != a.tasksRailPage && a.tapp.GetFocus() != a.tasklist {
		a.tapp.SetFocus(a.tasksRailPage)
	}
	if a.tasklist.Filtering() && a.mode == modeTaskList {
		return false
	}
	if a.mode == modeTaskList {
		if e.Key() == tcell.KeyTab {
			a.enterTasksRailTerminal()
			return true
		}
		if e.Key() == tcell.KeyCtrlZ {
			return true
		}
		if act, _ := a.activeKeymap().Resolve(keymap.CtxAgent, e); act == keymap.ActAgentPaneRight {
			a.enterTasksRailTerminal()
			return true
		}
	} else if act, _ := a.activeKeymap().Resolve(keymap.CtxAgent, e); act == keymap.ActAgentPaneLeft && (a.agentZen || a.agentFocus == focusTerminal) {
		a.focusTasksRail()
		return true
	}
	return false
}
