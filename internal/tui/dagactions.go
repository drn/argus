package tui

import (
	"errors"
	"strconv"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/orch"
	"github.com/drn/argus/internal/tui/dagview"
	"github.com/drn/argus/internal/uxlog"
)

// refreshDAG rebuilds the DAG widget's node set from the current DB
// snapshot. Called when the DAG tab is opened and after a halt cascade
// completes (see confirmHaltDownstream). Not currently driven by the
// tick loop — task-list mutations refresh on tab entry instead.
//
// Filter rules — see dagNodesFromTasks: archived rows are dropped, and
// pure orphans (no live parents AND not referenced as a parent) are
// dropped. The DAG tab is for inspecting linked stacks; including every
// standalone task pushes the connected graph off-screen.
func (a *App) refreshDAG() {
	tasks, err := a.db.Tasks()
	if err != nil {
		uxlog.Log("[tui] refreshDAG: %v", err)
		return
	}
	nodes := dagNodesFromTasks(tasks)
	uxlog.Log("[tui] refreshDAG: %d tasks → %d nodes (%d filtered)", len(tasks), len(nodes), len(tasks)-len(nodes))
	a.dagWidget.SetNodes(nodes)
}

// dagNodesFromTasks projects the task list into the DAG widget's input set,
// applying the TUI's filter contract:
//
//  1. Archived tasks are dropped. The web UI exposes a toggle to include
//     them; the TUI does not yet — when it does, this is the seam to wire it.
//  2. Pure orphans (no *live* parents AND not referenced as a parent by any
//     surviving task) are dropped. "Live parent" means a DependsOn id that
//     resolves to a non-archived task in the current snapshot — a task with
//     `DependsOn: ["archived-or-deleted-id"]` counts as having no live
//     parents and is dropped if nobody references it either. Pure orphans
//     contribute no edges and pile up at layer 0, drowning the connected
//     graph in unrelated boxes.
//
// A task whose only parents are stale (archived or deleted) is dropped
// if it also has no live children — i.e. it would render as an isolated
// box at layer 0. If it still has at least one live child, it's kept and
// renders as a source node, since dropping it would vanish a real link
// from the middle of someone's stack.
//
// The filter is intentionally cycle-agnostic: orch.Link / orch.FindCycle
// prevent cycles at link time, so by the time Tasks() returns the DAG is
// already acyclic. A defective input with a self-loop or a mutual cycle
// passes through here unchanged and the layout's cycle guard handles it.
func dagNodesFromTasks(tasks []*model.Task) []dagview.Node {
	live := make(map[string]*model.Task, len(tasks))
	for _, t := range tasks {
		if t.Archived {
			continue
		}
		live[t.ID] = t
	}
	referenced := make(map[string]bool, len(live))
	for _, t := range live {
		for _, d := range t.DependsOn {
			if _, ok := live[d]; ok {
				referenced[d] = true
			}
		}
	}
	out := make([]dagview.Node, 0, len(live))
	for _, t := range tasks {
		if t.Archived {
			continue
		}
		hasParent := false
		for _, d := range t.DependsOn {
			if _, ok := live[d]; ok {
				hasParent = true
				break
			}
		}
		if !hasParent && !referenced[t.ID] {
			continue
		}
		// Archived is always false here — archived rows were filtered
		// above. The field stays in the projection so the widget's
		// status palette can still render grey-dim if a future toggle
		// opens up archived inclusion.
		out = append(out, dagview.Node{
			ID:        t.ID,
			Name:      t.Name,
			Status:    t.Status.String(),
			Archived:  false,
			Result:    t.Result,
			DependsOn: append([]string(nil), t.DependsOn...),
		})
	}
	return out
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

// formatCount renders "N label" — e.g. "2 stopped". The label is a past
// participle used adjectivally (the implicit noun is "tasks"), so it does
// not pluralise. Earlier revisions appended "s" for n != 1 and produced
// "2 stoppeds" / "3 archiveds"; that was a bug, not the intended shape.
func formatCount(n int, label string) string {
	return strconv.Itoa(n) + " " + label
}

// Compile-time anchor for the dagview import so a refactor that drops
// refreshDAG's call site fails the build instead of leaving a dead import.
var _ = dagview.New
