package tui

import (
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/uxlog"
)

func (a *App) setupProjectFirst(f *NewTaskForm) {
	if _, local := a.db.(*db.DB); !local {
		uxlog.Log("[newtask] project-first bypass: remote TUI")
		return
	}
	if !a.db.Config().UI.ProjectFirst {
		return
	}
	f.EnableProjectFirst(a.tasks)
}

// Branch RPCs may finish after cancel/reopen or a project change. Never publish
// a previous form's/project's result into the current form.
func (a *App) applyNewTaskBranches(f *NewTaskForm, path string, branches []string) {
	if a.newTaskForm != f || f.branchPath != path {
		uxlog.Log("[newtask] skipped stale branches for %s", path)
		return
	}
	f.SetBranchOptions(branches)
}
