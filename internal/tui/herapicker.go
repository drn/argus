package tui

import (
	"errors"
	"sort"
	"strings"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/orch"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// herapicker.go owns the M7 DAG link/unlink parent picker and the orchestrator
// node-scoping helper for the embedded Hera Details-pane DAG. The picker reuses
// TaskSwitcherModal (retitled for link/unlink) and routes the chosen parent
// through orch.Link / orch.Unlink — Link's internal FindCycle rejects a
// cycle-forming edge, which doLink surfaces as a notice with the link untouched.

// scopeTasksToOrch filters the full task list down to the argus tasks live-bound
// to the selected orchestrator's roles. This is the input to dagNodesFromTasks
// for the Details-pane DAG render mode: the rail shows the orchestrator tree;
// the DAG shows the depends_on edges among THIS orchestrator's tasks (the two
// are orthogonal — see the plan's DAG-fold design). Reuses the shared projection
// rather than forking it, so web/TUI DAG parity holds.
func scopeTasksToOrch(tasks []*model.Task, o *hera.OrchView) []*model.Task {
	if o == nil {
		return nil
	}
	ids := make(map[string]bool, len(o.Roles))
	for i := range o.Roles {
		if o.Roles[i].Live && o.Roles[i].TaskID != "" {
			ids[o.Roles[i].TaskID] = true
		}
	}
	out := make([]*model.Task, 0, len(ids))
	for _, t := range tasks {
		if ids[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

// findTask returns the task with the given ID, or nil.
func findTask(tasks []*model.Task, id string) *model.Task {
	for _, t := range tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// sortTaskPickerEntries orders picker rows alphabetically by name
// (case-insensitive), breaking ties on ID for determinism.
func sortTaskPickerEntries(entries []taskSwitcherEntry) {
	sort.Slice(entries, func(i, j int) bool {
		li, lj := strings.ToLower(entries[i].Name), strings.ToLower(entries[j].Name)
		if li != lj {
			return li < lj
		}
		return entries[i].ID < entries[j].ID
	})
}

// doLink adds parent as a dependency of child via orch.Link. orch.Link stages
// the edge and runs FindCycle; a cycle-forming link returns *orch.CycleError and
// leaves the graph unchanged. We surface that as a clear notice (no link
// created) rather than an opaque error. On success both DAG surfaces + the task
// list refresh.
func (a *App) doLink(child, parent string) {
	if child == "" || parent == "" {
		return
	}
	if err := orch.Link(a.db, child, parent); err != nil {
		var ce *orch.CycleError
		if errors.As(err, &ce) {
			uxlog.Log("[hera-view] link rejected (cycle): %s → %s path=%v", child, parent, ce.Path)
			a.header.SetNotice("link rejected: would create a dependency cycle")
			a.forceRedraw("dag link cycle")
			return
		}
		uxlog.Log("[hera-view] link failed: %s → %s: %v", child, parent, err)
		a.header.SetNotice("link failed: " + err.Error())
		a.forceRedraw("dag link error")
		return
	}
	uxlog.Log("[hera-view] linked %s → %s", child, parent)
	a.header.SetNotice("linked")
	a.afterDependencyChange()
}

// doUnlink removes parent from child's dependencies via orch.Unlink (which
// cannot induce a cycle). Refreshes both DAG surfaces + the task list.
func (a *App) doUnlink(child, parent string) {
	if child == "" || parent == "" {
		return
	}
	if err := orch.Unlink(a.db, child, parent); err != nil {
		uxlog.Log("[hera-view] unlink failed: %s ↛ %s: %v", child, parent, err)
		a.header.SetNotice("unlink failed: " + err.Error())
		a.forceRedraw("dag unlink error")
		return
	}
	uxlog.Log("[hera-view] unlinked %s ↛ %s", child, parent)
	a.header.SetNotice("unlinked")
	a.afterDependencyChange()
}

// afterDependencyChange rebuilds every surface that renders depends_on edges
// after a link/unlink: the legacy DAG widget, the embedded Hera DAG (via the
// rail refresh → applySelection → rebuildDAG path), and the task list.
func (a *App) afterDependencyChange() {
	a.refreshDAG()
	a.heraPage.Refresh()
	a.refreshTasksLocal()
	a.forceRedraw("dependency changed")
}

// openHeraPicker shows the retitled task picker for link/unlink. submit is
// called with the chosen task ID on Enter. Mirrors the openHeraInput /
// openHeraConfirm plumbing: a dedicated mode + early dispatch keeps 1/2/3/q from
// leaking while the picker is open, and the close path returns focus to the
// Hera page.
func (a *App) openHeraPicker(title, help string, entries []taskSwitcherEntry, submit func(string)) {
	a.heraPickerModal = NewTaskSwitcherModal(entries)
	a.heraPickerModal.SetTitles(title, help)
	a.heraPickerSubmit = submit
	a.mode = modeHeraPicker
	a.pages.AddPage("herapicker", a.heraPickerModal, true, true)
	a.pages.SwitchToPage("herapicker")
	a.tapp.SetFocus(a.heraPickerModal)
}

func (a *App) handleHeraPickerKey(event *tcell.EventKey) {
	a.heraPickerModal.InputHandler()(event, func(tview.Primitive) {})
	if a.heraPickerModal.Canceled() {
		uxlog.Log("[hera-view] link/unlink picker canceled")
		a.closeHeraPicker()
		return
	}
	if a.heraPickerModal.Selected() {
		parent := a.heraPickerModal.SelectedTask()
		submit := a.heraPickerSubmit
		a.closeHeraPicker()
		if parent != "" && submit != nil {
			submit(parent)
		}
	}
}

func (a *App) closeHeraPicker() {
	a.mode = modeTaskList
	a.heraPickerModal = nil
	a.heraPickerSubmit = nil
	a.pages.RemovePage("herapicker")
	a.pages.SwitchToPage("hera")
	a.tapp.SetFocus(a.heraPage)
}
