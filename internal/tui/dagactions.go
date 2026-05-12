package tui

import (
	"errors"
	"strconv"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/orch"
	"github.com/drn/argus/internal/tui/dagview"
	"github.com/drn/argus/internal/uxlog"
)

// refreshDAG rebuilds the DAG widget's node set from the current DB
// snapshot. Called when the DAG tab is opened and whenever the tick loop
// notices a task mutation (status change, new task, archive flip).
//
// Project filter intentionally not applied here — the widget shows every
// task with at least one link plus orphans. A future iteration can scope
// to a single project via a dropdown without touching this entry point.
func (a *App) refreshDAG() {
	tasks, err := a.db.Tasks()
	if err != nil {
		uxlog.Log("[tui] refreshDAG: %v", err)
		return
	}
	nodes := make([]dagview.Node, 0, len(tasks))
	for _, t := range tasks {
		nodes = append(nodes, dagview.Node{
			ID:        t.ID,
			Name:      t.Name,
			Status:    t.Status.String(),
			Archived:  t.Archived,
			Result:    t.Result,
			DependsOn: append([]string(nil), t.DependsOn...),
		})
	}
	a.dagWidget.SetNodes(nodes)
}

// openAgentForTask is the DAG-side "jump to this task's agent view" hook.
// Resolves the ID to a *model.Task and delegates to onTaskSelect so the
// flow matches the existing task-list Enter behaviour (focus, header, PTY
// resync). A no-op if the task vanished between snapshot and key press.
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

// openLinkPickerForTask is the placeholder hook for the `l` keybinding on
// the DAG tab. The full picker UI is a follow-up — for now we surface a
// notice so users know to use the web UI or MCP. Stamping the notice
// instead of crashing keeps the keybinding discoverable without shipping
// half-done modal plumbing. See gotchas/dag-rendering.md.
func (a *App) openLinkPickerForTask(child string) {
	uxlog.Log("[tui] DAG link picker requested for child=%s — TUI picker is a follow-up; use web UI or MCP task_link", child)
	a.header.SetNotice("link from DAG: use web UI / task_link (TUI picker WIP)")
	a.forceRedraw("dag link notice")
}

// openUnlinkPickerForTask mirrors openLinkPickerForTask for `L`.
func (a *App) openUnlinkPickerForTask(child string) {
	uxlog.Log("[tui] DAG unlink picker requested for child=%s — TUI picker is a follow-up; use web UI or MCP task_unlink", child)
	a.header.SetNotice("unlink from DAG: use web UI / task_unlink (TUI picker WIP)")
	a.forceRedraw("dag unlink notice")
}

// confirmHaltDownstream is the `h` keybinding handler. Calls
// orch.HaltDownstream directly — destructive but reversible (archived rows
// can be unarchived, stopped tasks can be resumed via task list resume).
// A full confirm modal showing the affected set is a follow-up.
//
// Runs synchronously on the tview event loop; the call sequence is
// db.Tasks + per-row db.Get + db.SetArchived (mutex-locked) + runner.Stop
// (SIGTERM, non-blocking). On a large stack of in_progress descendants
// this could briefly stutter the UI. Tracked under "TUI follow-ups" in
// gotchas/dag-rendering.md.
func (a *App) confirmHaltDownstream(id string) {
	if id == "" {
		return
	}
	report, err := orch.HaltDownstream(a.db, a.runner, id, func(err error) bool {
		return errors.Is(err, agent.ErrSessionNotFound)
	})
	if err != nil {
		uxlog.Log("[tui] halt-downstream failed for %s: %v", id, err)
		a.header.SetNotice("halt failed: " + err.Error())
		a.forceRedraw("halt error")
		return
	}
	uxlog.Log("[tui] halt-downstream from %s: stopped=%v archived=%v notfound=%v",
		id, report.Stopped, report.Archived, report.NotFound)
	a.header.SetNotice("halted " + summarizeHalt(report))
	a.refreshDAG()
	a.refreshTasksLocal()
	a.forceRedraw("halt complete")
}

func summarizeHalt(r orch.HaltReport) string {
	count := len(r.Stopped) + len(r.Archived)
	if count == 0 {
		return "no downstream tasks"
	}
	return formatHaltCount(len(r.Stopped), len(r.Archived))
}

func formatHaltCount(stopped, archived int) string {
	switch {
	case stopped > 0 && archived > 0:
		return formatCount(stopped, "stopped") + ", " + formatCount(archived, "archived")
	case stopped > 0:
		return formatCount(stopped, "stopped")
	default:
		return formatCount(archived, "archived")
	}
}

func formatCount(n int, label string) string {
	suffix := ""
	if n != 1 {
		suffix = "s"
	}
	return strconv.Itoa(n) + " " + label + suffix
}

// Compile-time anchor for the dagview import so a refactor that drops
// refreshDAG's call site fails the build instead of leaving a dead import.
var _ = dagview.New
