package tui2

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
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

func TestTaskListView_AutoExpandFirstProject(t *testing.T) {
	tl := NewTaskListView()
	// expanded is "" — should auto-expand first project
	tl.SetTasks(makeTasks())

	if tl.expanded != "alpha" {
		t.Errorf("expanded = %q, want 'alpha' (first project auto-expanded)", tl.expanded)
	}

	// Should have task rows visible
	task := tl.SelectedTask()
	if task == nil {
		t.Fatal("cursor should be on a task row after auto-expand")
	}
	if task.Project != "alpha" {
		t.Errorf("selected task project = %q, want 'alpha'", task.Project)
	}
}

func TestTaskListView_CursorNavigatesCrossProject(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())

	// Should start in alpha
	if tl.expanded != "alpha" {
		t.Fatalf("expanded = %q, want alpha", tl.expanded)
	}

	// Navigate down past alpha tasks into beta
	for i := 0; i < 10; i++ {
		tl.CursorDown()
	}

	// Should have auto-expanded beta
	task := tl.SelectedTask()
	if task == nil {
		t.Fatal("cursor should be on a task after navigating down")
	}
	if task.Project != "beta" {
		t.Errorf("after navigating down, project = %q, want 'beta'", task.Project)
	}
	if tl.expanded != "beta" {
		t.Errorf("expanded = %q, want 'beta' after navigating into it", tl.expanded)
	}
}

func TestTaskListView_Tick(t *testing.T) {
	tl := NewTaskListView()
	if tl.tickEven {
		t.Error("tickEven should start false")
	}
	tl.Tick()
	if !tl.tickEven {
		t.Error("tickEven should be true after one tick")
	}
	tl.Tick()
	if tl.tickEven {
		t.Error("tickEven should be false after two ticks")
	}
}

func TestTaskListView_SetIdle(t *testing.T) {
	tl := NewTaskListView()
	tl.SetIdle([]string{"1", "3"})
	if !tl.idle["1"] {
		t.Error("task 1 should be idle")
	}
	if !tl.idle["3"] {
		t.Error("task 3 should be idle")
	}
	if tl.idle["2"] {
		t.Error("task 2 should not be idle")
	}
}

func TestTaskListView_ProjectStatusIcon(t *testing.T) {
	tl := NewTaskListView()

	tests := []struct {
		name     string
		tasks    []*model.Task
		running  map[string]bool
		idle     map[string]bool
		wantChar rune
	}{
		{
			name:     "all pending",
			tasks:    []*model.Task{{ID: "1", Status: model.StatusPending}},
			wantChar: '○',
		},
		{
			name:     "in progress running",
			tasks:    []*model.Task{{ID: "1", Status: model.StatusInProgress}},
			running:  map[string]bool{"1": true},
			wantChar: '●', // tickEven is false
		},
		{
			name:     "all complete",
			tasks:    []*model.Task{{ID: "1", Status: model.StatusComplete}},
			wantChar: '✓',
		},
		{
			name:     "in review",
			tasks:    []*model.Task{{ID: "1", Status: model.StatusInReview}},
			wantChar: '◎',
		},
		{
			name: "mixed complete and pending",
			tasks: []*model.Task{
				{ID: "1", Status: model.StatusComplete},
				{ID: "2", Status: model.StatusPending},
			},
			wantChar: '✓',
		},
		{
			name:     "all in progress idle",
			tasks:    []*model.Task{{ID: "1", Status: model.StatusInProgress}},
			running:  map[string]bool{"1": true},
			idle:     map[string]bool{"1": true},
			wantChar: '☾',
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tl.running = tt.running
			if tl.running == nil {
				tl.running = map[string]bool{}
			}
			tl.idle = tt.idle
			if tl.idle == nil {
				tl.idle = map[string]bool{}
			}
			tl.tickEven = false
			icon, _ := tl.projectStatusIcon(tt.tasks)
			if icon != tt.wantChar {
				t.Errorf("projectStatusIcon() = %c, want %c", icon, tt.wantChar)
			}
		})
	}
}

func TestTaskListView_EnterSkipsCompleted(t *testing.T) {
	tl := NewTaskListView()
	tl.expanded = "beta"
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "done-task", Project: "beta", Status: model.StatusComplete},
		{ID: "2", Name: "active-task", Project: "beta", Status: model.StatusInProgress},
	})

	var selected *model.Task
	tl.OnSelect = func(task *model.Task) { selected = task }

	// Navigate to the completed task
	for tl.SelectedTask() == nil || tl.SelectedTask().Status != model.StatusComplete {
		tl.CursorDown()
	}

	// Enter on completed task should NOT fire OnSelect
	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) {})
	if selected != nil {
		t.Error("Enter on completed task should not fire OnSelect")
	}

	// Navigate to the in-progress task
	for tl.SelectedTask() == nil || tl.SelectedTask().Status != model.StatusInProgress {
		tl.CursorDown()
	}

	// Enter on in-progress task should fire OnSelect
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) {})
	if selected == nil || selected.ID != "2" {
		t.Error("Enter on in-progress task should fire OnSelect")
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

func TestTaskListView_SetIdleUnvisited(t *testing.T) {
	tl := NewTaskListView()
	tl.SetIdleUnvisited([]string{"1", "3"})
	if !tl.idleUnvisited["1"] {
		t.Error("task 1 should be idle-unvisited")
	}
	if tl.idleUnvisited["2"] {
		t.Error("task 2 should not be idle-unvisited")
	}
	if !tl.idleUnvisited["3"] {
		t.Error("task 3 should be idle-unvisited")
	}
}

func TestTaskListView_IdleSet(t *testing.T) {
	tl := NewTaskListView()
	tl.SetIdle([]string{"a", "b"})
	s := tl.IdleSet()
	if !s["a"] || !s["b"] {
		t.Error("IdleSet should return the current idle map")
	}
}

func TestTaskListView_IdleUnvisitedPromotion(t *testing.T) {
	tl := NewTaskListView()
	tasks := []*model.Task{
		{ID: "1", Status: model.StatusInProgress, Project: "p"},
	}
	tl.idleUnvisited = map[string]bool{"1": true}
	tl.running = map[string]bool{"1": true}
	tl.idle = map[string]bool{"1": true}
	tl.tickEven = false

	// Project icon should be InReview (◎) when the only InProgress task is idleUnvisited.
	icon, _ := tl.projectStatusIcon(tasks)
	if icon != '◎' {
		t.Errorf("projectStatusIcon with idleUnvisited = %c, want ◎", icon)
	}
}

func TestTaskListView_StatusCycleKeys(t *testing.T) {
	tl := NewTaskListView()
	var changed *model.Task
	tl.OnStatusChange = func(task *model.Task) {
		changed = task
	}
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "task1", Status: model.StatusPending, Project: "p"},
	})
	tl.expanded = "p"
	tl.buildRows()
	// Move cursor to the task row (skip project header).
	tl.CursorDown()

	// Press 's' to advance status: Pending -> InProgress
	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone), func(tview.Primitive) {})
	if changed == nil {
		t.Fatal("OnStatusChange should have been called")
	}
	if changed.Status != model.StatusInProgress {
		t.Errorf("after 's': status = %v, want InProgress", changed.Status)
	}

	// Press 's' again: InProgress -> InReview
	changed = nil
	handler(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone), func(tview.Primitive) {})
	if changed == nil {
		t.Fatal("OnStatusChange should have been called")
	}
	if changed.Status != model.StatusInReview {
		t.Errorf("after second 's': status = %v, want InReview", changed.Status)
	}

	// Press 'S' to revert: InReview -> InProgress
	changed = nil
	handler(tcell.NewEventKey(tcell.KeyRune, 'S', tcell.ModNone), func(tview.Primitive) {})
	if changed == nil {
		t.Fatal("OnStatusChange should have been called")
	}
	if changed.Status != model.StatusInProgress {
		t.Errorf("after 'S': status = %v, want InProgress", changed.Status)
	}
}

func TestTaskListView_RunningTaskAnimation(t *testing.T) {
	tl := NewTaskListView()
	tasks := []*model.Task{
		{ID: "1", Status: model.StatusInProgress, Project: "p"},
	}
	tl.running = map[string]bool{"1": true}
	tl.idle = map[string]bool{}

	// tickEven=false: running task at project level should show ●
	tl.tickEven = false
	icon, _ := tl.projectStatusIcon(tasks)
	if icon != '●' {
		t.Errorf("tickEven=false: got %c, want ●", icon)
	}

	// tickEven=true: running task at project level should show ◉
	tl.tickEven = true
	icon, _ = tl.projectStatusIcon(tasks)
	if icon != '◉' {
		t.Errorf("tickEven=true: got %c, want ◉", icon)
	}
}

func TestTaskListView_ArchiveToggle(t *testing.T) {
	tl := NewTaskListView()
	var archived *model.Task
	tl.OnArchive = func(task *model.Task) {
		archived = task
	}
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "task1", Status: model.StatusPending, Project: "p"},
	})
	tl.expanded = "p"
	tl.buildRows()
	tl.clampCursor()

	// Press 'a' to archive the task
	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), func(tview.Primitive) {})
	if archived == nil {
		t.Fatal("OnArchive should have been called")
	}
	if !archived.Archived {
		t.Error("task should be archived after pressing 'a'")
	}

	// Press 'a' again to unarchive
	archived = nil
	handler(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), func(tview.Primitive) {})
	if archived == nil {
		t.Fatal("OnArchive should have been called again")
	}
	if archived.Archived {
		t.Error("task should be unarchived after pressing 'a' again")
	}
}

func TestTaskListView_CursorAlwaysOnTask(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())

	// Navigate through all rows — cursor should always be on a task.
	for i := 0; i < 20; i++ {
		task := tl.SelectedTask()
		if task == nil {
			t.Errorf("step %d down: cursor not on a task (cursor=%d)", i, tl.cursor)
		}
		tl.CursorDown()
	}
	for i := 0; i < 20; i++ {
		task := tl.SelectedTask()
		if task == nil {
			t.Errorf("step %d up: cursor not on a task (cursor=%d)", i, tl.cursor)
		}
		tl.CursorUp()
	}
}

func TestTaskListView_SkipProjectHeaders(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "t1", Project: "alpha", Status: model.StatusPending},
		{ID: "2", Name: "t2", Project: "beta", Status: model.StatusPending},
	})

	// Start on alpha's task
	if tl.SelectedTask() == nil || tl.SelectedTask().ID != "1" {
		t.Fatalf("expected to start on task 1, got %v", tl.SelectedTask())
	}

	// Move down — should skip beta's project header and land on task 2
	tl.CursorDown()
	task := tl.SelectedTask()
	if task == nil || task.ID != "2" {
		t.Errorf("after down: expected task 2, got %v", task)
	}

	// Move back up — should skip alpha's project header and land on task 1
	tl.CursorUp()
	task = tl.SelectedTask()
	if task == nil || task.ID != "1" {
		t.Errorf("after up: expected task 1, got %v", task)
	}
}

func TestTaskListView_UpLandsOnLastTask(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "t1", Project: "alpha", Status: model.StatusPending},
		{ID: "2", Name: "t2", Project: "alpha", Status: model.StatusPending},
		{ID: "3", Name: "t3", Project: "beta", Status: model.StatusPending},
	})

	// Navigate to beta's task
	for i := 0; i < 10; i++ {
		tl.CursorDown()
	}
	task := tl.SelectedTask()
	if task == nil || task.ID != "3" {
		t.Fatalf("expected to be on task 3, got %v", task)
	}

	// Move up — should land on last task of alpha (task 2), not first (task 1)
	tl.CursorUp()
	task = tl.SelectedTask()
	if task == nil || task.ID != "2" {
		t.Errorf("after up from beta: expected task 2 (last in alpha), got %v", task)
	}
}

func TestTaskListView_ArchiveAutoExpand(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "active", Project: "proj", Status: model.StatusPending},
		{ID: "2", Name: "archived", Project: "proj", Status: model.StatusPending, Archived: true},
	})

	// Archive should start collapsed
	if tl.archiveExpanded {
		t.Error("archive should start collapsed")
	}

	// Navigate down past all active tasks — should enter archive
	for i := 0; i < 10; i++ {
		tl.CursorDown()
	}

	// Should have auto-expanded archive and landed on the archived task
	task := tl.SelectedTask()
	if task == nil || task.ID != "2" {
		t.Errorf("expected to land on archived task 2, got %v", task)
	}
	if !tl.archiveExpanded {
		t.Error("archive should be expanded after navigating into it")
	}

	// Navigate back up out of archive — should auto-collapse
	tl.CursorUp()
	task = tl.SelectedTask()
	if task == nil || task.ID != "1" {
		t.Errorf("expected to land on task 1 after leaving archive, got %v", task)
	}
	if tl.archiveExpanded {
		t.Error("archive should be collapsed after leaving it")
	}
}

func TestTaskListView_ArchiveSectionAwareCursor(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "active", Project: "shared", Status: model.StatusPending},
		{ID: "2", Name: "archived", Project: "shared", Status: model.StatusPending, Archived: true},
	})

	// Navigate into archive section
	for i := 0; i < 10; i++ {
		tl.CursorDown()
	}

	task := tl.SelectedTask()
	if task == nil || task.ID != "2" {
		t.Errorf("expected archived task 2, got %v", task)
	}

	// The cursor should be in the archive section, not on the main "shared" project
	if !tl.isInArchive(tl.cursor) {
		t.Error("cursor should be in archive section")
	}
}
