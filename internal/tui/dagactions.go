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

// openLinkPickerForTask is the `l` keybinding handler on the DAG (both the
// embedded Hera Details DAG and the legacy DAG tab share this). It opens a
// fuzzy parent picker over every non-archived task that is not already a parent
// of the child (and not the child itself); selecting one routes to orch.Link,
// which rejects a cycle-forming edge via its internal FindCycle (see doLink).
//
// Candidates are NOT scoped to the orchestrator: a depends_on link is a global
// relationship and may target a task outside the DAG's view. The DAG render mode
// is orchestrator-scoped for *viewing*; linking is unconstrained. See
// gotchas/dag-rendering.md.
func (a *App) openLinkPickerForTask(child string) {
	if child == "" {
		return
	}
	tasks, err := a.db.Tasks()
	if err != nil {
		uxlog.Log("[tui] DAG link picker: db.Tasks failed: %v", err)
		a.header.SetNotice("link failed: " + err.Error())
		a.forceRedraw("dag link error")
		return
	}
	childTask := findTask(tasks, child)
	if childTask == nil {
		uxlog.Log("[tui] DAG link picker: child %s vanished", child)
		return
	}
	existingParent := make(map[string]bool, len(childTask.DependsOn))
	for _, p := range childTask.DependsOn {
		existingParent[p] = true
	}
	entries := make([]taskSwitcherEntry, 0, len(tasks))
	for _, t := range tasks {
		if t.Archived || t.ID == child || existingParent[t.ID] {
			continue
		}
		entries = append(entries, taskSwitcherEntry{ID: t.ID, Name: t.Name, Project: t.Project, Status: t.Status})
	}
	if len(entries) == 0 {
		uxlog.Log("[tui] DAG link picker: no candidate parents for child=%s", child)
		a.header.SetNotice("link: no available parent tasks")
		a.forceRedraw("dag link empty")
		return
	}
	sortTaskPickerEntries(entries)
	uxlog.Log("[tui] DAG link picker: child=%s %d candidate parents", child, len(entries))
	a.openHeraPicker(" Link → parent ", "↑/↓ select  Enter link  Esc cancel", entries, func(parent string) {
		a.doLink(child, parent)
	})
}

// openUnlinkPickerForTask is the `L` keybinding handler: it offers the child's
// CURRENT parents (its live depends_on edges) and routes the chosen one to
// orch.Unlink. A child with no parents surfaces a notice rather than an empty
// picker.
func (a *App) openUnlinkPickerForTask(child string) {
	if child == "" {
		return
	}
	tasks, err := a.db.Tasks()
	if err != nil {
		uxlog.Log("[tui] DAG unlink picker: db.Tasks failed: %v", err)
		a.header.SetNotice("unlink failed: " + err.Error())
		a.forceRedraw("dag unlink error")
		return
	}
	childTask := findTask(tasks, child)
	if childTask == nil || len(childTask.DependsOn) == 0 {
		uxlog.Log("[tui] DAG unlink picker: child=%s has no parents", child)
		a.header.SetNotice("unlink: task has no parent links")
		a.forceRedraw("dag unlink empty")
		return
	}
	entries := make([]taskSwitcherEntry, 0, len(childTask.DependsOn))
	for _, pid := range childTask.DependsOn {
		if p := findTask(tasks, pid); p != nil {
			entries = append(entries, taskSwitcherEntry{ID: p.ID, Name: p.Name, Project: p.Project, Status: p.Status})
		} else {
			// Stale ref (parent deleted/archived): still offer it so the dangling
			// edge can be cleaned up. Label with the raw ID.
			entries = append(entries, taskSwitcherEntry{ID: pid, Name: pid})
		}
	}
	sortTaskPickerEntries(entries)
	uxlog.Log("[tui] DAG unlink picker: child=%s %d parents", child, len(entries))
	a.openHeraPicker(" Unlink parent ", "↑/↓ select  Enter unlink  Esc cancel", entries, func(parent string) {
		a.doUnlink(child, parent)
	})
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
	a.heraPage.Refresh() // rebuilds the embedded Hera DAG when it's the active surface
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
