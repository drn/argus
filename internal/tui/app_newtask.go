package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// newTaskTracker holds the TUI's view of task_meta ui.new flags. announced is
// per-process: an id is announced at most once, and the first read seeds it
// silently so a TUI launch doesn't re-announce flags left from before.
type newTaskTracker struct {
	ids       map[string]bool
	announced map[string]bool
	seeded    bool
}

// readNewTasks returns the task IDs whose task_meta ui.new flag is "true".
func (a *App) readNewTasks() map[string]bool {
	raw, err := a.db.ListMetaByNamespace(db.UIMetaNamespace)
	if err != nil {
		uxlog.Log("[newtask] tui: read ui meta failed: %v", err)
		return nil
	}
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]bool, len(raw))
	for taskID, kv := range raw {
		if kv[db.UIMetaKeyNew] == "true" {
			out[taskID] = true
		}
	}
	return out
}

// syncNewTasks refreshes the new-task set, pushes it to the task list, and
// shows a status-bar notice for ids first observed after the seeding read.
func (a *App) syncNewTasks() {
	ids := a.readNewTasks()
	a.newTasks.ids = ids
	a.tasklist.SetNewTasks(ids)

	if a.newTasks.announced == nil {
		a.newTasks.announced = make(map[string]bool)
	}
	if !a.newTasks.seeded {
		for id := range ids {
			a.newTasks.announced[id] = true
		}
		a.newTasks.seeded = true
		if len(ids) > 0 {
			uxlog.Log("[newtask] seeded %d existing flag(s)", len(ids))
		}
		return
	}

	byID := make(map[string]*model.Task, len(a.tasks))
	for _, t := range a.tasks {
		byID[t.ID] = t
	}
	fresh := make([]string, 0)
	for id := range ids {
		if a.newTasks.announced[id] {
			continue
		}
		if _, ok := byID[id]; !ok {
			continue
		}
		fresh = append(fresh, id)
	}
	if len(fresh) == 0 {
		return
	}
	sort.Strings(fresh)
	parts := make([]string, 0, len(fresh))
	for _, id := range fresh {
		a.newTasks.announced[id] = true
		t := byID[id]
		parts = append(parts, fmt.Sprintf("%s (%s)", t.Name, t.Project))
		uxlog.Log("[newtask] announced id=%s project=%s", id, t.Project)
	}
	a.statusbar.SetInfo("✦ New task: " + strings.Join(parts, ", "))
}

// clearNewTask drops the ui.new flag for a task the user just opened. Remote
// mode has no meta write path, so it is a no-op there.
func (a *App) clearNewTask(taskID string) {
	if !a.newTasks.ids[taskID] {
		return
	}
	d, ok := a.db.(*db.DB)
	if !ok {
		return
	}
	if err := d.DeleteMeta(taskID, db.UIMetaNamespace, db.UIMetaKeyNew); err != nil {
		uxlog.Log("[newtask] clear failed id=%s: %v", taskID, err)
		return
	}
	next := make(map[string]bool, len(a.newTasks.ids))
	for id := range a.newTasks.ids {
		if id != taskID {
			next[id] = true
		}
	}
	a.newTasks.ids = next
	a.tasklist.SetNewTasks(next)
	uxlog.Log("[newtask] cleared id=%s", taskID)
}
