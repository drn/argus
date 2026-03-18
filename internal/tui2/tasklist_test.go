package tui2

import (
	"testing"

	"github.com/drn/argus/internal/model"
)

func makeTasks() []*model.Task {
	return []*model.Task{
		{ID: "1", Name: "task-a", Project: "alpha", Status: model.StatusPending},
		{ID: "2", Name: "task-b", Project: "alpha", Status: model.StatusInProgress},
		{ID: "3", Name: "task-c", Project: "beta", Status: model.StatusComplete},
		{ID: "4", Name: "task-d", Project: "beta", Status: model.StatusPending, Archived: true},
	}
}

func TestTaskListView_SetTasks(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())

	if !tl.HasTasks() {
		t.Error("HasTasks should be true")
	}
	if len(tl.rows) == 0 {
		t.Error("rows should not be empty after SetTasks")
	}
}

func TestTaskListView_BuildRows(t *testing.T) {
	tl := NewTaskListView()
	tl.expanded = "alpha"
	tl.SetTasks(makeTasks())

	// Should have: rowProject(alpha), rowTask(a), rowTask(b), rowProject(beta), rowArchiveHeader
	// Because alpha is expanded, its tasks are shown. beta is collapsed (no tasks shown).
	// Archived task-d is in archive section.
	var projects, tasks, archives int
	for _, r := range tl.rows {
		switch r.kind {
		case rowProject:
			projects++
		case rowTask:
			tasks++
		case rowArchiveHeader:
			archives++
		}
	}
	if tasks != 2 { // only alpha's tasks are expanded
		t.Errorf("task rows = %d, want 2", tasks)
	}
	if projects < 2 { // alpha + beta
		t.Errorf("project rows = %d, want >=2", projects)
	}
	if archives != 1 {
		t.Errorf("archive header rows = %d, want 1", archives)
	}
}

func TestTaskListView_CursorNavigation(t *testing.T) {
	tl := NewTaskListView()
	tl.expanded = "alpha"
	tl.SetTasks(makeTasks())

	// Cursor should start at the first task row
	task := tl.SelectedTask()
	if task == nil {
		t.Fatal("expected a task at cursor position")
	}

	tl.CursorDown()
	task2 := tl.SelectedTask()
	if task2 == nil {
		t.Fatal("expected a task after CursorDown")
	}
	if task2.ID == task.ID {
		t.Error("CursorDown should move to a different task")
	}

	tl.CursorUp()
	task3 := tl.SelectedTask()
	if task3 == nil {
		t.Fatal("expected a task after CursorUp")
	}
	if task3.ID != task.ID {
		t.Errorf("CursorUp should return to first task, got %q", task3.ID)
	}
}

func TestTaskListView_SetRunning(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())
	tl.SetRunning([]string{"2"})

	if !tl.running["2"] {
		t.Error("task 2 should be running")
	}
}

func TestTaskListView_Empty(t *testing.T) {
	tl := NewTaskListView()
	if tl.HasTasks() {
		t.Error("empty list should not have tasks")
	}
	if tl.Empty() == "" {
		t.Error("Empty() should return placeholder text")
	}
}

func TestGroupByProject(t *testing.T) {
	tasks := []*model.Task{
		{ID: "1", Project: "alpha"},
		{ID: "2", Project: "beta"},
		{ID: "3", Project: "alpha"},
		{ID: "4", Project: ""},
	}
	order, groups := groupByProject(tasks)

	if len(order) != 3 {
		t.Errorf("len(order) = %d, want 3", len(order))
	}
	if order[0] != "alpha" {
		t.Errorf("first project = %q, want alpha", order[0])
	}
	if len(groups["alpha"]) != 2 {
		t.Errorf("alpha tasks = %d, want 2", len(groups["alpha"]))
	}
	if len(groups["(no project)"]) != 1 {
		t.Errorf("no-project tasks = %d, want 1", len(groups["(no project)"]))
	}
}

func TestTaskListView_IsInArchive(t *testing.T) {
	tl := NewTaskListView()
	tl.archiveExpanded = true
	tl.archiveProject = "beta"
	tl.SetTasks(makeTasks())

	// Find the archive header index
	archiveIdx := -1
	for i, r := range tl.rows {
		if r.kind == rowArchiveHeader {
			archiveIdx = i
			break
		}
	}
	if archiveIdx < 0 {
		t.Fatal("no archive header found")
	}

	// Rows before archive header should not be in archive
	if archiveIdx > 0 && tl.isInArchive(0) {
		t.Error("row 0 should not be in archive")
	}

	// Rows at or after archive header should be in archive
	if !tl.isInArchive(archiveIdx) {
		t.Error("archive header row should be in archive")
	}
}
