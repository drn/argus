package ui

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
)

func testTasks() []*model.Task {
	return []*model.Task{
		{Name: "Fix login bug", Project: "webapp"},
		{Name: "Add API endpoint", Project: "backend"},
		{Name: "Update docs", Project: "webapp"},
	}
}

func TestTaskList_Selected_Empty(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	if tl.Selected() != nil {
		t.Error("expected nil for empty list")
	}
}

func TestTaskList_Selected(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 20)
	tasks := testTasks()
	tl.SetTasks(tasks)

	// Cursor starts on the first project header; Selected returns first task in that project.
	got := tl.Selected()
	if got == nil {
		t.Fatal("expected non-nil selected task")
	}
	// First project should be "webapp" (both projects have pending status, alphabetical: backend < webapp,
	// but order depends on input order within same priority tier — they both have pending tasks).
	// The first project header's Selected() returns its first task.
	if got.Project != tl.rows[0].project {
		t.Errorf("expected task from first project %q, got project %q", tl.rows[0].project, got.Project)
	}
}

func TestTaskList_CursorNavigation(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tl.SetTasks(testTasks())

	// Cursor starts at row 0 (first project header).
	// Moving down should enter the expanded project's tasks.
	tl.CursorDown()
	got := tl.Selected()
	if got == nil {
		t.Fatal("expected task after cursor down")
	}
	// Should be on a task row now.
	c := tl.scroll.Cursor()
	if tl.rows[c].kind != rowTask {
		t.Error("expected cursor on a task row after one down")
	}
}

func TestTaskList_AutoExpand(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{Name: "Task A1", Project: "alpha"},
		{Name: "Task A2", Project: "alpha"},
		{Name: "Task B1", Project: "beta"},
	}
	tl.SetTasks(tasks)

	// Initially the first project is expanded.
	firstProject := tl.expanded
	if firstProject == "" {
		t.Fatal("expected a project to be expanded")
	}

	// Navigate down past the first project's tasks to the next project header.
	for i := 0; i < 20; i++ {
		tl.CursorDown()
		c := tl.scroll.Cursor()
		if c < len(tl.rows) && tl.rows[c].kind == rowProject && tl.rows[c].project != firstProject {
			break
		}
	}

	// Now the expanded project should have changed.
	if tl.expanded == firstProject {
		t.Error("expected expanded project to change after navigating to a different project")
	}

	// The new project's tasks should be visible in the rows.
	found := false
	for _, r := range tl.rows {
		if r.kind == rowTask && r.project == tl.expanded {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected tasks from newly expanded project to be in rows")
	}

	// The old project's tasks should NOT be visible.
	for _, r := range tl.rows {
		if r.kind == rowTask && r.project == firstProject {
			t.Error("expected old project's tasks to be collapsed")
			break
		}
	}
}

func TestTaskList_SelectedOnHeader(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{Name: "Task 1", Project: "proj"},
	}
	tl.SetTasks(tasks)

	// Cursor is on the project header.
	if tl.rows[0].kind != rowProject {
		t.Fatal("expected first row to be project header")
	}
	got := tl.Selected()
	if got == nil || got.Name != "Task 1" {
		t.Error("Selected() on header should return first task in project")
	}
}

func TestTaskList_UncategorizedGroup(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{Name: "Orphan task", Project: ""},
		{Name: "Project task", Project: "myproj"},
	}
	tl.SetTasks(tasks)

	// Find the uncategorized header.
	hasUncategorized := false
	for _, r := range tl.rows {
		if r.kind == rowProject && r.project == uncategorized {
			hasUncategorized = true
			break
		}
	}
	if !hasUncategorized {
		t.Error("expected Uncategorized group for tasks with empty project")
	}
}

func TestTaskList_FilterWithGroups(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tl.SetTasks(testTasks())

	tl.SetFilter("webapp")

	// Only webapp tasks should be in the rows.
	for _, r := range tl.rows {
		if r.kind == rowTask && r.task.Project != "webapp" {
			t.Errorf("expected only webapp tasks, got project %q", r.task.Project)
		}
	}

	// Should have exactly one project header (webapp).
	headerCount := 0
	for _, r := range tl.rows {
		if r.kind == rowProject {
			headerCount++
			if r.project != "webapp" {
				t.Errorf("expected webapp header, got %q", r.project)
			}
		}
	}
	if headerCount != 1 {
		t.Errorf("expected 1 project header, got %d", headerCount)
	}
}

func TestTaskList_SingleProject(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{Name: "Task 1", Project: "solo"},
		{Name: "Task 2", Project: "solo"},
	}
	tl.SetTasks(tasks)

	// Single project should be expanded with all tasks visible.
	taskRows := 0
	for _, r := range tl.rows {
		if r.kind == rowTask {
			taskRows++
		}
	}
	if taskRows != 2 {
		t.Errorf("expected 2 task rows for single project, got %d", taskRows)
	}
}

func TestTaskList_ViewRendersHeaders(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tl.SetTasks(testTasks())

	v := tl.View()
	if !strings.Contains(v, "▾") && !strings.Contains(v, "▸") {
		t.Error("expected chevron indicators in view output")
	}
}

func TestTaskList_Filter(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tl.SetTasks(testTasks())

	tl.SetFilter("webapp")
	got := tl.Selected()
	if got == nil {
		t.Fatal("expected selected task after filter")
	}
	if got.Project != "webapp" {
		t.Errorf("expected webapp task, got %q", got.Project)
	}
}

func TestTaskList_FilterCaseInsensitive(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tl.SetTasks(testTasks())

	tl.SetFilter("API")
	got := tl.Selected()
	if got == nil || got.Name != "Add API endpoint" {
		t.Error("filter should be case-insensitive")
	}
}

func TestTaskList_FilterNoMatch(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetTasks(testTasks())

	tl.SetFilter("zzz")
	if tl.Selected() != nil {
		t.Error("expected nil for no matches")
	}
}

func TestTaskList_ClearFilter(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tl.SetTasks(testTasks())

	tl.SetFilter("webapp")
	tl.SetFilter("")

	// Should show all projects again.
	projectCount := 0
	for _, r := range tl.rows {
		if r.kind == rowProject {
			projectCount++
		}
	}
	if projectCount < 2 {
		t.Errorf("expected at least 2 project headers after clearing filter, got %d", projectCount)
	}
}

func TestTaskList_SetTasks_ClampsCursor(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tl.SetTasks(testTasks())

	// Move cursor down several times.
	for i := 0; i < 5; i++ {
		tl.CursorDown()
	}

	// Shrink to 1 task.
	tl.SetTasks([]*model.Task{{Name: "only one", Project: "solo"}})
	if tl.scroll.Cursor() >= len(tl.rows) {
		t.Errorf("cursor should be clamped, got %d with %d rows", tl.scroll.Cursor(), len(tl.rows))
	}
}

func TestTaskList_View_Empty(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetTasks(nil)

	v := tl.View()
	if v == "" {
		t.Error("should render empty state message")
	}
}
