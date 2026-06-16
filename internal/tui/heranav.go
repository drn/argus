package tui

import "github.com/drn/argus/internal/uxlog"

// openAgentForTask is the orchestration-tree "jump to this task's agent view"
// hook (wired to the embedded Hera DAG's OnEnter). Resolves the ID to a
// *model.Task and delegates to onTaskSelect so the flow matches the task-list
// Enter behaviour (focus, header, PTY resync). A no-op if the task vanished
// between snapshot and key press.
func (a *App) openAgentForTask(id string) {
	if id == "" {
		return
	}
	task, err := a.db.Get(id)
	if err != nil || task == nil {
		uxlog.Log("[tui] openAgentForTask: missing task %s: %v", id, err)
		return
	}
	a.onTaskSelect(task, false)
}
