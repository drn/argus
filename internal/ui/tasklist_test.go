package ui

import (
	"fmt"
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

	// Cursor starts on the first task (skips project header).
	c := tl.scroll.Cursor()
	if tl.rows[c].kind != rowTask {
		t.Error("expected cursor to start on a task row, not a project header")
	}
	got := tl.Selected()
	if got == nil {
		t.Fatal("expected task selected initially")
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

	// Navigate down past the first project's tasks into the next project.
	for i := 0; i < 20; i++ {
		tl.CursorDown()
		if tl.expanded != firstProject {
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

	// Cursor skips project header, lands directly on the task.
	got := tl.Selected()
	if got == nil || got.Name != "Task 1" {
		t.Error("expected Task 1 selected")
	}
	c := tl.scroll.Cursor()
	if tl.rows[c].kind != rowTask {
		t.Error("cursor should be on a task row, not the project header")
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

func TestTaskList_CursorSkipsProjectHeaders(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{Name: "A1", Project: "alpha", Status: model.StatusInProgress},
		{Name: "A2", Project: "alpha", Status: model.StatusInProgress},
		{Name: "B1", Project: "beta", Status: model.StatusPending},
	}
	tl.SetTasks(tasks)

	// Cursor should never land on a project header during navigation.
	for i := 0; i < 10; i++ {
		c := tl.scroll.Cursor()
		if c >= 0 && c < len(tl.rows) && tl.rows[c].kind == rowProject {
			t.Errorf("cursor on project header at step %d (row %d)", i, c)
		}
		tl.CursorDown()
	}

	for i := 0; i < 10; i++ {
		c := tl.scroll.Cursor()
		if c >= 0 && c < len(tl.rows) && tl.rows[c].kind == rowProject {
			t.Errorf("cursor on project header at up step %d (row %d)", i, c)
		}
		tl.CursorUp()
	}
}

func TestTaskList_CursorUpAcrossProjects(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{Name: "A1", Project: "alpha", Status: model.StatusInProgress},
		{Name: "A2", Project: "alpha", Status: model.StatusInProgress},
		{Name: "B1", Project: "beta", Status: model.StatusPending},
	}
	tl.SetTasks(tasks)

	// Navigate down to B1.
	for i := 0; i < 10; i++ {
		tl.CursorDown()
		if sel := tl.Selected(); sel != nil && sel.Name == "B1" {
			break
		}
	}
	if sel := tl.Selected(); sel == nil || sel.Name != "B1" {
		t.Fatal("expected to reach B1")
	}

	// Press up — should go to A2 (last task in alpha), not A1.
	tl.CursorUp()
	sel := tl.Selected()
	if sel == nil || sel.Name != "A2" {
		name := "<nil>"
		if sel != nil {
			name = sel.Name
		}
		t.Errorf("expected A2 (last task in alpha) when going up from beta, got %s", name)
	}
}

func TestTaskList_ExpandedProjectRemoved(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)

	// Start with two projects. Force alpha expanded by setting it before SetTasks.
	tl.expanded = "alpha"
	tasks := []*model.Task{
		{Name: "A1", Project: "alpha", Status: model.StatusComplete},
		{Name: "A2", Project: "alpha", Status: model.StatusComplete},
		{Name: "B1", Project: "beta", Status: model.StatusPending},
	}
	tl.SetTasks(tasks)

	if tl.expanded != "alpha" {
		t.Fatalf("expected alpha expanded, got %q", tl.expanded)
	}

	// Simulate pruning: remove all alpha tasks, only beta remains.
	tl.SetTasks([]*model.Task{
		{Name: "B1", Project: "beta", Status: model.StatusPending},
	})

	// The expanded project should switch to beta (the only remaining project).
	if tl.expanded != "beta" {
		t.Errorf("expected beta expanded after alpha removed, got %q", tl.expanded)
	}

	// Beta's tasks should be visible in rows.
	taskRows := 0
	for _, r := range tl.rows {
		if r.kind == rowTask {
			taskRows++
		}
	}
	if taskRows != 1 {
		t.Errorf("expected 1 visible task row, got %d", taskRows)
	}
}

func TestTaskList_AdjacentTask(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{ID: "t1", Name: "Task A1", Project: "alpha"},
		{ID: "t2", Name: "Task A2", Project: "alpha"},
		{ID: "t3", Name: "Task B1", Project: "beta"},
	}
	tl.SetTasks(tasks)

	// Next from first task
	next := tl.AdjacentTask("t1", +1)
	if next == nil || next.ID != "t2" {
		t.Errorf("expected t2 after t1, got %v", next)
	}

	// Next from second task
	next = tl.AdjacentTask("t2", +1)
	if next == nil || next.ID != "t3" {
		t.Errorf("expected t3 after t2, got %v", next)
	}

	// Next from last task — should be nil
	next = tl.AdjacentTask("t3", +1)
	if next != nil {
		t.Errorf("expected nil after last task, got %v", next)
	}

	// Prev from last task
	prev := tl.AdjacentTask("t3", -1)
	if prev == nil || prev.ID != "t2" {
		t.Errorf("expected t2 before t3, got %v", prev)
	}

	// Prev from first task — should be nil
	prev = tl.AdjacentTask("t1", -1)
	if prev != nil {
		t.Errorf("expected nil before first task, got %v", prev)
	}

	// Unknown task ID
	unknown := tl.AdjacentTask("nonexistent", +1)
	if unknown != nil {
		t.Errorf("expected nil for unknown task, got %v", unknown)
	}
}

func TestTaskList_AdjacentTask_Empty(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetTasks(nil)
	if tl.AdjacentTask("any", +1) != nil {
		t.Error("expected nil for empty list")
	}
}

func TestTaskList_projectTasks(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{Name: "A1", Project: "alpha"},
		{Name: "A2", Project: "alpha"},
		{Name: "B1", Project: "beta"},
		{Name: "No Project", Project: ""},
	}
	tl.SetTasks(tasks)

	got := tl.projectTasks("alpha")
	if len(got) != 2 {
		t.Errorf("expected 2 alpha tasks, got %d", len(got))
	}

	got = tl.projectTasks("beta")
	if len(got) != 1 {
		t.Errorf("expected 1 beta task, got %d", len(got))
	}

	got = tl.projectTasks(uncategorized)
	if len(got) != 1 {
		t.Errorf("expected 1 uncategorized task, got %d", len(got))
	}

	got = tl.projectTasks("nonexistent")
	if len(got) != 0 {
		t.Errorf("expected 0 tasks for nonexistent project, got %d", len(got))
	}
}

func TestTaskList_taskStatusIcon(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)

	statuses := []model.Status{
		model.StatusPending,
		model.StatusInProgress,
		model.StatusInReview,
		model.StatusComplete,
	}
	for _, s := range statuses {
		task := &model.Task{ID: "t1", Status: s}
		icon := tl.taskStatusIcon(task)
		if icon == "" {
			t.Errorf("expected non-empty icon for status %v", s)
		}
	}
}

func TestTaskList_taskStatusIcon_InProgressVariants(t *testing.T) {
	tl := NewTaskList(DefaultTheme())

	// Not running → moon icon
	task := &model.Task{ID: "t1", Status: model.StatusInProgress}
	icon := tl.taskStatusIcon(task)
	if !strings.Contains(icon, "\uF186") {
		t.Error("expected moon icon for non-running in-progress task")
	}

	// Running but idle → moon icon
	tl.SetRunning([]string{"t1"})
	tl.SetIdle([]string{"t1"})
	icon = tl.taskStatusIcon(task)
	if !strings.Contains(icon, "\uF186") {
		t.Error("expected moon icon for idle in-progress task")
	}

	// Running and active, tickEven=true → alternate icon
	tl.SetIdle(nil)
	tl.tickEven = true
	icon = tl.taskStatusIcon(task)
	if strings.Contains(icon, "\uF186") {
		t.Error("expected alternate icon for active running task on even tick")
	}
}

func TestTaskList_projectStatusIcon(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)

	tests := []struct {
		name     string
		statuses []model.Status
		wantIcon string
	}{
		{"all pending", []model.Status{model.StatusPending, model.StatusPending}, model.StatusPending.Display()},
		{"all complete", []model.Status{model.StatusComplete, model.StatusComplete}, model.StatusComplete.Display()},
		{"has in_progress", []model.Status{model.StatusPending, model.StatusInProgress}, "\uF186"}, // moon (not running)
		{"has in_review", []model.Status{model.StatusPending, model.StatusInReview}, model.StatusInReview.Display()},
		{"in_review beats in_progress", []model.Status{model.StatusInReview, model.StatusInProgress}, model.StatusInReview.Display()},
		{"mixed pending+complete", []model.Status{model.StatusPending, model.StatusComplete}, model.StatusComplete.Display()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tasks []*model.Task
			for i, s := range tt.statuses {
				tasks = append(tasks, &model.Task{ID: fmt.Sprintf("t%d", i), Status: s})
			}
			icon := tl.projectStatusIcon(tasks)
			if !strings.Contains(icon, tt.wantIcon) {
				t.Errorf("expected icon to contain %q, got %q", tt.wantIcon, icon)
			}
		})
	}
}

func TestTaskList_taskStatusIcon_IdleUnvisited(t *testing.T) {
	tl := NewTaskList(DefaultTheme())

	task := &model.Task{ID: "t1", Status: model.StatusInProgress}

	// Idle and unvisited → in review icon
	tl.SetRunning([]string{"t1"})
	tl.SetIdle([]string{"t1"})
	tl.SetIdleUnvisited([]string{"t1"})
	icon := tl.taskStatusIcon(task)
	if !strings.Contains(icon, model.StatusInReview.Display()) {
		t.Errorf("expected in-review icon for idle+unvisited task, got %q", icon)
	}

	// After clearing idleUnvisited → back to moon
	tl.SetIdleUnvisited([]string{})
	icon = tl.taskStatusIcon(task)
	if !strings.Contains(icon, "\uF186") {
		t.Error("expected moon icon once idle+unvisited cleared")
	}
}

func TestTaskList_projectStatusIcon_IdleUnvisited(t *testing.T) {
	tl := NewTaskList(DefaultTheme())

	tasks := []*model.Task{
		{ID: "t1", Status: model.StatusInProgress},
		{ID: "t2", Status: model.StatusPending},
	}

	// Idle+unvisited → project shows in review
	tl.SetRunning([]string{"t1"})
	tl.SetIdle([]string{"t1"})
	tl.SetIdleUnvisited([]string{"t1"})
	icon := tl.projectStatusIcon(tasks)
	if !strings.Contains(icon, model.StatusInReview.Display()) {
		t.Errorf("expected in-review project icon for idle+unvisited task, got %q", icon)
	}

	// Mixed: one idle+unvisited, one actively running → in review wins
	tasks2 := []*model.Task{
		{ID: "t1", Status: model.StatusInProgress}, // idle+unvisited
		{ID: "t2", Status: model.StatusInProgress}, // actively running
	}
	tl.SetRunning([]string{"t1", "t2"})
	tl.SetIdle([]string{"t1"})
	tl.SetIdleUnvisited([]string{"t1"})
	icon = tl.projectStatusIcon(tasks2)
	if !strings.Contains(icon, model.StatusInReview.Display()) {
		t.Errorf("expected in-review to win over in-progress, got %q", icon)
	}
}

func TestTaskList_projectStatusIcon_InProgressAnimation(t *testing.T) {
	tl := NewTaskList(DefaultTheme())

	tasks := []*model.Task{
		{ID: "t1", Status: model.StatusInProgress},
		{ID: "t2", Status: model.StatusPending},
	}

	// Not running → moon
	icon := tl.projectStatusIcon(tasks)
	if !strings.Contains(icon, "\uF186") {
		t.Error("expected moon icon when in-progress task not running")
	}

	// Running and active, tickEven → alternate icon
	tl.SetRunning([]string{"t1"})
	tl.tickEven = true
	icon = tl.projectStatusIcon(tasks)
	if !strings.Contains(icon, model.StatusInProgress.DisplayAlt()) {
		t.Error("expected alternate icon for active running task on even tick")
	}
}

func TestTaskList_renderProjectHeader_SingleIcon(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{ID: "t1", Name: "A1", Project: "alpha", Status: model.StatusPending},
		{ID: "t2", Name: "A2", Project: "alpha", Status: model.StatusComplete},
	}
	tl.SetTasks(tasks)

	var b strings.Builder
	tl.renderProjectHeader(&b, "alpha", false, false)
	output := b.String()

	// Mixed pending+complete → dimmed check icon (single icon, not per-task).
	checkGlyph := model.StatusComplete.Display()
	if !strings.Contains(output, checkGlyph) {
		t.Errorf("expected check glyph %q in header output", checkGlyph)
	}
	if strings.Contains(output, "…") {
		t.Error("should not contain ellipsis — no longer using per-task icons")
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

func TestTaskList_ArchivedSection(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{ID: "t1", Name: "Active task", Project: "proj"},
		{ID: "t2", Name: "Archived task", Project: "proj", Archived: true},
	}
	tl.SetTasks(tasks)

	// Should have archive header row.
	hasArchiveHeader := false
	for _, r := range tl.rows {
		if r.kind == rowArchiveHeader {
			hasArchiveHeader = true
		}
	}
	if !hasArchiveHeader {
		t.Error("expected archive header row when archived tasks exist")
	}

	// Archive is always expanded — both active and archived tasks should be in rows.
	activeFound, archivedFound := false, false
	for _, r := range tl.rows {
		if r.kind == rowTask {
			if r.task.ID == "t1" {
				activeFound = true
			}
			if r.task.ID == "t2" {
				archivedFound = true
			}
		}
	}
	if !activeFound {
		t.Error("expected active task in rows")
	}
	if archivedFound {
		t.Error("archived task should not be visible when archive is collapsed")
	}
}

func TestTaskList_ArchiveAutoExpand(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{ID: "t1", Name: "Active", Project: "proj"},
		{ID: "t2", Name: "Archived", Project: "proj", Archived: true},
	}
	tl.SetTasks(tasks)

	// Archive should be collapsed initially.
	archivedFound := false
	for _, r := range tl.rows {
		if r.kind == rowTask && r.task.ID == "t2" {
			archivedFound = true
		}
	}
	if archivedFound {
		t.Error("archived task should not be visible initially")
	}

	// Navigate down to archive header — archive should auto-expand.
	for range 10 {
		tl.CursorDown()
	}

	archivedFound = false
	for _, r := range tl.rows {
		if r.kind == rowTask && r.task.ID == "t2" {
			archivedFound = true
		}
	}
	if !archivedFound {
		t.Error("expected archived task visible after cursor enters archive section")
	}

	// Navigate back up past archive header — archive should auto-collapse.
	for range 10 {
		tl.CursorUp()
	}

	archivedFound = false
	for _, r := range tl.rows {
		if r.kind == rowTask && r.task.ID == "t2" {
			archivedFound = true
		}
	}
	if archivedFound {
		t.Error("archived task should not be visible after cursor leaves archive section")
	}
}

func TestTaskList_NoArchiveHeaderWithoutArchivedTasks(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{ID: "t1", Name: "Active", Project: "proj"},
	}
	tl.SetTasks(tasks)

	for _, r := range tl.rows {
		if r.kind == rowArchiveHeader {
			t.Error("should not have archive header when no archived tasks")
		}
	}
}

func TestTaskList_ArchiveViewRendersHeader(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{ID: "t1", Name: "Active", Project: "proj"},
		{ID: "t2", Name: "Archived", Project: "proj", Archived: true},
	}
	tl.SetTasks(tasks)

	v := tl.View()
	if !strings.Contains(v, "Archive") {
		t.Error("expected 'Archive' in view output")
	}
}

func TestTaskList_CursorSkipsArchiveHeader(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{ID: "t1", Name: "Active", Project: "proj"},
		{ID: "t2", Name: "Archived", Project: "proj", Archived: true},
	}
	tl.SetTasks(tasks)

	// Navigate down — cursor should skip the archive header and land on a task.
	// Archive should auto-expand as cursor passes through the archive section.
	for range 20 {
		tl.CursorDown()
	}

	c := tl.scroll.Cursor()
	if c >= 0 && c < len(tl.rows) && tl.rows[c].kind == rowArchiveHeader {
		t.Error("cursor should never land on the archive header")
	}

	// Archive should have auto-expanded when cursor entered the section.
	if !tl.archiveExpanded {
		t.Error("expected archive to auto-expand when cursor passes through archive section")
	}
}

func TestTaskList_ArchiveMultipleProjects(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{ID: "t1", Name: "Active", Project: "alpha"},
		{ID: "t2", Name: "Archived A", Project: "alpha", Archived: true},
		{ID: "t3", Name: "Archived B", Project: "beta", Archived: true},
	}
	tl.SetTasks(tasks)

	// Navigate down to archive section to trigger auto-expand.
	for range 10 {
		tl.CursorDown()
	}

	// Both archived projects should appear as headers in the archive section.
	archiveProjectHeaders := 0
	inArchive := false
	for _, r := range tl.rows {
		if r.kind == rowArchiveHeader {
			inArchive = true
			continue
		}
		if inArchive && r.kind == rowProject {
			archiveProjectHeaders++
		}
	}
	if archiveProjectHeaders < 2 {
		t.Errorf("expected at least 2 project headers in archive section, got %d", archiveProjectHeaders)
	}
}

func TestTaskList_ArchivedProjectOnlyInArchive(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{ID: "t1", Name: "Active", Project: "alpha"},
		{ID: "t2", Name: "Archived only", Project: "beta", Archived: true},
	}
	tl.SetTasks(tasks)

	// "beta" has only archived tasks — it should NOT appear in the main section.
	for _, r := range tl.rows {
		if r.kind == rowProject && r.project == "beta" && !tl.isInArchiveSection(0) {
			t.Error("project with only archived tasks should not appear in main section")
		}
	}

	// Navigate to archive section to auto-expand.
	for range 10 {
		tl.CursorDown()
	}

	// Now "beta" should appear as a project header in the archive section.
	betaInArchive := false
	for i, r := range tl.rows {
		if r.kind == rowProject && r.project == "beta" && tl.isInArchiveSection(i) {
			betaInArchive = true
		}
	}
	if !betaInArchive {
		t.Error("expected beta project to appear in archive section")
	}
}

func TestTaskList_ArchiveAutoExpandProjectSwitch(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 40)
	tasks := []*model.Task{
		{ID: "t1", Name: "Active", Project: "alpha"},
		{ID: "t2", Name: "Archived A", Project: "alpha", Archived: true},
		{ID: "t3", Name: "Archived B1", Project: "beta", Archived: true},
		{ID: "t4", Name: "Archived B2", Project: "beta", Archived: true},
	}
	tl.SetTasks(tasks)

	// Navigate into archive section.
	for range 10 {
		tl.CursorDown()
	}

	if !tl.archiveExpanded {
		t.Fatal("expected archive to be expanded")
	}

	// The first archive project should be expanded.
	firstArchiveProject := tl.archiveProject
	if firstArchiveProject == "" {
		t.Fatal("expected an archive project to be expanded")
	}

	// Navigate down to find a task from a different archive project.
	for range 10 {
		tl.CursorDown()
	}

	sel := tl.Selected()
	if sel == nil {
		t.Fatal("expected a task to be selected in archive")
	}

	// If there are multiple archive projects, the expanded project should have switched.
	if tl.archiveProject == firstArchiveProject && sel.Project != firstArchiveProject {
		t.Error("archive project should auto-expand when cursor moves to a different project")
	}
}
