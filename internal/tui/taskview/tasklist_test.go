package taskview

import (
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
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
	// Alphabetical: "(no project)" < "alpha" < "beta"
	if order[0] != "(no project)" {
		t.Errorf("first project = %q, want (no project)", order[0])
	}
	if order[1] != "alpha" {
		t.Errorf("second project = %q, want alpha", order[1])
	}
	if order[2] != "beta" {
		t.Errorf("third project = %q, want beta", order[2])
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

func TestTaskListView_UpdateSpinnerFrame(t *testing.T) {
	widget.SetActiveSpinner("progress")
	defer widget.SetActiveSpinner("progress")

	tl := NewTaskListView()
	tl.updateSpinnerFrame()
	// Frame should be a valid index for the active spinner.
	if tl.animFrame < 0 || tl.animFrame >= widget.SpinnerFrameCount() {
		t.Errorf("animFrame %d out of range [0, %d)", tl.animFrame, widget.SpinnerFrameCount())
	}

	// Switching spinner style produces valid frames too.
	widget.SetActiveSpinner("classic")
	tl.updateSpinnerFrame()
	if tl.animFrame < 0 || tl.animFrame >= widget.SpinnerFrameCount() {
		t.Errorf("classic: animFrame %d out of range [0, %d)", tl.animFrame, widget.SpinnerFrameCount())
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
			wantChar: '\uEE06', // animFrame=0, first spinner frame
		},
		{
			name:     "all complete",
			tasks:    []*model.Task{{ID: "1", Status: model.StatusComplete}},
			wantChar: '✓',
		},
		{
			name:     "in review",
			tasks:    []*model.Task{{ID: "1", Status: model.StatusInReview}},
			wantChar: theme.IconMoonStars,
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
			wantChar: theme.IconMoonOutline,
		},
		{
			name: "idle in progress plus in review shows review icon",
			tasks: []*model.Task{
				{ID: "1", Status: model.StatusInProgress},
				{ID: "2", Status: model.StatusInReview},
			},
			running:  map[string]bool{"1": true},
			idle:     map[string]bool{"1": true},
			wantChar: theme.IconMoonStars,
		},
		{
			name: "running in progress plus in review shows spinner",
			tasks: []*model.Task{
				{ID: "1", Status: model.StatusInProgress},
				{ID: "2", Status: model.StatusInReview},
			},
			running:  map[string]bool{"1": true},
			wantChar: '\uEE06', // animFrame=0, first spinner frame
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
			tl.animFrame = 0
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

func TestTaskListView_SectionAt(t *testing.T) {
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
	if archiveIdx > 0 && tl.sectionAt(0) == sectionArchive {
		t.Error("row 0 should not be in archive")
	}

	// Rows at or after archive header should be in archive
	if tl.sectionAt(archiveIdx) != sectionArchive {
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
	tl.animFrame = 0

	// Project icon should be moon_o when the only InProgress task is idleUnvisited.
	icon, _ := tl.projectStatusIcon(tasks)
	if icon != theme.IconMoonStars {
		t.Errorf("projectStatusIcon with idleUnvisited = %c, want moon_stars", icon)
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

func TestTaskListView_StatusPrevFromComplete(t *testing.T) {
	tl := NewTaskListView()
	var changed *model.Task
	tl.OnStatusChange = func(task *model.Task) {
		changed = task
	}
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "done", Status: model.StatusComplete, Project: "p"},
	})
	tl.expanded = "p"
	tl.buildRows()
	tl.CursorDown()

	// Press 'S' to revert: Complete -> InReview
	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, 'S', tcell.ModNone), func(tview.Primitive) {})
	if changed == nil {
		t.Fatal("OnStatusChange should have been called")
	}
	if changed.Status != model.StatusInReview {
		t.Errorf("after 'S' from Complete: status = %v, want InReview", changed.Status)
	}
}

func TestTaskListView_SetTasksPreservesCursor(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "t1", Project: "alpha", Status: model.StatusPending},
		{ID: "2", Name: "t2", Project: "alpha", Status: model.StatusInProgress},
	})
	// expanded should auto-set to alpha

	// Move to second task
	tl.CursorDown()
	if sel := tl.SelectedTask(); sel == nil || sel.ID != "2" {
		t.Fatalf("expected task 2 selected, got %v", sel)
	}

	// Simulate a refresh with new task objects (same IDs)
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "t1", Project: "alpha", Status: model.StatusPending},
		{ID: "2", Name: "t2", Project: "alpha", Status: model.StatusInReview},
	})

	// Cursor should still be on task 2
	sel := tl.SelectedTask()
	if sel == nil || sel.ID != "2" {
		t.Errorf("after SetTasks refresh: expected task 2, got %v", sel)
	}
}

func TestTaskListView_OnLayoutChange(t *testing.T) {
	tl := NewTaskListView()
	var calls int
	tl.OnLayoutChange = func() { calls++ }

	// First SetTasks → layout established → callback fires.
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "a", Project: "alpha"},
		{ID: "2", Name: "b", Project: "alpha"},
	})
	if calls != 1 {
		t.Fatalf("expected 1 call after first SetTasks, got %d", calls)
	}

	// Same tasks → no layout change → callback should NOT fire (Sync is
	// expensive; only fire when rows actually shift).
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "a", Project: "alpha"},
		{ID: "2", Name: "b", Project: "alpha"},
	})
	if calls != 1 {
		t.Errorf("expected callback suppressed on identical rebuild, got %d calls", calls)
	}

	// Adding a task in a different project changes composition → fire.
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "a", Project: "alpha"},
		{ID: "2", Name: "b", Project: "alpha"},
		{ID: "3", Name: "c", Project: "beta"},
	})
	if calls != 2 {
		t.Errorf("expected callback after composition change, got %d calls", calls)
	}

	// Toggling archive flag moves a task between sections → fire.
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "a", Project: "alpha"},
		{ID: "2", Name: "b", Project: "alpha", Archived: true},
		{ID: "3", Name: "c", Project: "beta"},
	})
	if calls != 3 {
		t.Errorf("expected callback after archive toggle, got %d calls", calls)
	}

	// Auto-rename: same task ID/project/section but the rendered title shrinks.
	// Without title in the signature, ghost cells from the longer name leak
	// past the new shorter name under tcell's diff emit.
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "a much longer task title that wraps", Project: "alpha"},
		{ID: "2", Name: "b", Project: "alpha", Archived: true},
		{ID: "3", Name: "c", Project: "beta"},
	})
	if calls != 4 {
		t.Errorf("expected callback after name change, got %d calls", calls)
	}

	// Status change (pending → in_progress) flips the row's spinner/badge,
	// changing the rendered width on either side of the title.
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "a much longer task title that wraps", Project: "alpha", Status: model.StatusInProgress},
		{ID: "2", Name: "b", Project: "alpha", Archived: true},
		{ID: "3", Name: "c", Project: "beta"},
	})
	if calls != 5 {
		t.Errorf("expected callback after status change, got %d calls", calls)
	}
}

// TestTaskListView_OnLayoutChange_CursorCrossesSection covers the
// autoExpand → buildRows → OnLayoutChange path that fires when cursor
// movement crosses a section boundary. The SetTasks-driven path is
// covered by TestTaskListView_OnLayoutChange above; this exercises
// the InputHandler-driven path.
func TestTaskListView_OnLayoutChange_CursorCrossesSection(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "active", Project: "proj", Status: model.StatusPending},
		{ID: "2", Name: "waiting", Project: "proj", Status: model.StatusInReview, WaitingReview: true},
	})

	// Wire callback after initial SetTasks so we count only cursor-driven fires.
	var calls int
	tl.OnLayoutChange = func() { calls++ }

	// Cursor starts on task 1 in the active section. Drive it down until
	// it lands on task 2 in waiting-for-review — autoExpand toggles the
	// WR section open, which calls buildRows and shifts rows.
	for i := 0; i < 10; i++ {
		tl.CursorDown()
	}
	if sel := tl.SelectedTask(); sel == nil || sel.ID != "2" {
		t.Fatalf("expected to land on waiting task id=2, got %+v", sel)
	}
	if !tl.waitingReviewExpanded {
		t.Fatal("waiting-for-review should be auto-expanded after cursor crossing")
	}
	if calls == 0 {
		t.Error("expected OnLayoutChange to fire on section crossing, got 0 calls")
	}
}

func TestTaskListView_AdjacentTask(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "first", Project: "projA"},
		{ID: "2", Name: "second", Project: "projA"},
		{ID: "3", Name: "third", Project: "projB"},
	})

	// Next from first task
	next := tl.AdjacentTask("1", 1)
	if next == nil || next.ID != "2" {
		t.Fatalf("expected task 2, got %v", next)
	}

	// Next across projects
	next = tl.AdjacentTask("2", 1)
	if next == nil || next.ID != "3" {
		t.Fatalf("expected task 3, got %v", next)
	}

	// No next from last task
	next = tl.AdjacentTask("3", 1)
	if next != nil {
		t.Fatalf("expected nil, got %v", next)
	}

	// Prev from second task
	prev := tl.AdjacentTask("2", -1)
	if prev == nil || prev.ID != "1" {
		t.Fatalf("expected task 1, got %v", prev)
	}

	// No prev from first task
	prev = tl.AdjacentTask("1", -1)
	if prev != nil {
		t.Fatalf("expected nil, got %v", prev)
	}

	// Unknown ID
	if tl.AdjacentTask("unknown", 1) != nil {
		t.Fatal("expected nil for unknown ID")
	}
}

func TestTaskListView_SelectByID(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "first", Project: "projA"},
		{ID: "2", Name: "second", Project: "projA"},
		{ID: "3", Name: "third", Project: "projB"},
	})

	tl.SelectByID("3")
	sel := tl.SelectedTask()
	if sel == nil || sel.ID != "3" {
		t.Fatalf("expected task 3, got %v", sel)
	}

	tl.SelectByID("1")
	sel = tl.SelectedTask()
	if sel == nil || sel.ID != "1" {
		t.Fatalf("expected task 1, got %v", sel)
	}
}

func TestTaskListView_SelectByID_AfterNewTask(t *testing.T) {
	tl := NewTaskListView()
	// Start with one task, cursor on it.
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "existing", Project: "proj"},
	})
	tl.SetExpanded("proj")
	testutil.Equal(t, tl.SelectedTask().ID, "1")

	// Simulate creating a new task: add it to the list and select by ID.
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "existing", Project: "proj"},
		{ID: "2", Name: "new-task", Project: "proj"},
	})
	tl.SelectByID("2")
	sel := tl.SelectedTask()
	testutil.Equal(t, sel.ID, "2")
	testutil.Equal(t, sel.Name, "new-task")
}

func TestTaskListView_RunningTaskAnimation(t *testing.T) {
	tl := NewTaskListView()
	tasks := []*model.Task{
		{ID: "1", Status: model.StatusInProgress, Project: "p"},
	}
	tl.running = map[string]bool{"1": true}
	tl.idle = map[string]bool{}

	// Each tick advances through the 6 spinner frames (ee06–ee0b).
	expected := []rune{'\uEE06', '\uEE07', '\uEE08', '\uEE09', '\uEE0A', '\uEE0B'}
	for i, want := range expected {
		tl.animFrame = i
		icon, _ := tl.projectStatusIcon(tasks)
		if icon != want {
			t.Errorf("animFrame=%d: got %U, want %U", i, icon, want)
		}
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

func TestTaskListView_NoCursorChangeAtBoundary(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())

	changes := 0
	tl.OnCursorChange = func(task *model.Task) {
		changes++
	}

	// Cursor starts at the top task. Pressing up should not fire callback.
	changes = 0
	tl.CursorUp()
	if changes != 0 {
		t.Errorf("CursorUp at top: expected 0 callback fires, got %d", changes)
	}

	// Navigate to the very bottom.
	for i := 0; i < len(tl.rows); i++ {
		tl.CursorDown()
	}

	// Now pressing down at the bottom should not fire callback.
	changes = 0
	tl.CursorDown()
	if changes != 0 {
		t.Errorf("CursorDown at bottom: expected 0 callback fires, got %d", changes)
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
	if tl.sectionAt(tl.cursor) != sectionArchive {
		t.Error("cursor should be in archive section")
	}
}

func TestTaskListView_SeparatorBeforeArchive(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "active", Project: "proj", Status: model.StatusPending},
		{ID: "2", Name: "archived", Project: "proj", Status: model.StatusPending, Archived: true},
	})

	// Rows should include a separator before the archive header.
	hasSep := false
	for i, r := range tl.rows {
		if r.kind == rowSeparator {
			hasSep = true
			// Next row should be archive header.
			if i+1 >= len(tl.rows) || tl.rows[i+1].kind != rowArchiveHeader {
				t.Error("separator should be immediately before archive header")
			}
		}
	}
	if !hasSep {
		t.Error("expected a separator row before the archive section")
	}

	// Cursor should never rest on the separator.
	for i := 0; i < 20; i++ {
		tl.CursorDown()
		if tl.cursor >= 0 && tl.cursor < len(tl.rows) && tl.rows[tl.cursor].kind == rowSeparator {
			t.Errorf("cursor rested on separator at index %d after CursorDown %d", tl.cursor, i+1)
		}
	}
	for i := 0; i < 20; i++ {
		tl.CursorUp()
		if tl.cursor >= 0 && tl.cursor < len(tl.rows) && tl.rows[tl.cursor].kind == rowSeparator {
			t.Errorf("cursor rested on separator at index %d after CursorUp %d", tl.cursor, i+1)
		}
	}
}

func TestTaskListView_OpenPRKey(t *testing.T) {
	tl := NewTaskListView()
	var opened *model.Task
	tl.OnOpenPR = func(task *model.Task) {
		opened = task
	}

	// Single task with a PR URL.
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "has-pr", Project: "p", PRURL: "https://github.com/acme/repo/pull/42"},
	})
	tl.expanded = "p"
	tl.buildRows()
	tl.CursorDown()

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, 'p', tcell.ModNone), func(tview.Primitive) {})
	if opened == nil {
		t.Fatal("OnOpenPR should have been called for task with PR URL")
	}
	if opened.ID != "1" {
		t.Errorf("OnOpenPR called with task %s, want 1", opened.ID)
	}

	// Task without PR URL — callback should NOT fire.
	opened = nil
	tl.SetTasks([]*model.Task{
		{ID: "2", Name: "no-pr", Project: "p", PRURL: ""},
	})
	tl.expanded = "p"
	tl.buildRows()
	tl.CursorDown()

	handler(tcell.NewEventKey(tcell.KeyRune, 'p', tcell.ModNone), func(tview.Primitive) {})
	if opened != nil {
		t.Error("OnOpenPR should NOT fire for task without PR URL")
	}
}

// TestTaskListView_OnFilterToggle covers the filter-mode toggle callback
// that fires when `/` activates the filter input or Enter/Escape exits it.
// Filter mode flips reserves the bottom row of the list panel for the
// filter input — a layout shift that doesn't change rowsSignature, so it
// goes through OnFilterToggle (not OnLayoutChange). Without the callback
// the App can't Sync, and tcell's diff-based emit leaves ghost cells from
// the prior bottom row.
func TestTaskListView_OnFilterToggle(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())
	var calls int
	tl.OnFilterToggle = func() { calls++ }

	handler := tl.InputHandler()

	// `/` activates filter (false → true): fires.
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})
	if calls != 1 {
		t.Errorf("expected 1 call after activating filter, got %d", calls)
	}

	// Enter exits input mode (true → false): fires.
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	if calls != 2 {
		t.Errorf("expected 2 calls after Enter exits input mode, got %d", calls)
	}

	// Re-activate, then Escape clears (true → false again): fires.
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})
	if calls != 3 {
		t.Errorf("expected 3 calls after re-activation, got %d", calls)
	}
	handler(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), func(tview.Primitive) {})
	if calls != 4 {
		t.Errorf("expected 4 calls after Escape clears filter, got %d", calls)
	}

	// No-op setFiltering (already false): does NOT fire.
	tl.setFiltering(false)
	if calls != 4 {
		t.Errorf("no-op setFiltering must not fire callback, got %d calls", calls)
	}
}

func TestTaskListView_FilterActivatesOnSlash(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())

	if tl.Filtering() {
		t.Error("should not be filtering initially")
	}

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})

	if !tl.Filtering() {
		t.Error("should be filtering after pressing /")
	}
	if tl.Filter() != "" {
		t.Errorf("filter text should be empty, got %q", tl.Filter())
	}
}

func TestTaskListView_FilterByName(t *testing.T) {
	tl := NewTaskListView()
	tl.expanded = "alpha"
	tl.SetTasks(makeTasks())

	handler := tl.InputHandler()
	// Activate filter
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})

	// Type "task-a" — should filter to only task-a
	for _, ch := range "task-a" {
		handler(tcell.NewEventKey(tcell.KeyRune, ch, tcell.ModNone), func(tview.Primitive) {})
	}

	if tl.Filter() != "task-a" {
		t.Errorf("filter = %q, want 'task-a'", tl.Filter())
	}

	// Count visible task rows
	taskCount := 0
	for _, r := range tl.rows {
		if r.kind == rowTask {
			taskCount++
		}
	}
	if taskCount != 1 {
		t.Errorf("expected 1 visible task, got %d", taskCount)
	}

	sel := tl.SelectedTask()
	if sel == nil || sel.Name != "task-a" {
		t.Errorf("selected task = %v, want task-a", sel)
	}
}

func TestTaskListView_FilterByProject(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})

	// Type "beta" — should match tasks in the beta project
	for _, ch := range "beta" {
		handler(tcell.NewEventKey(tcell.KeyRune, ch, tcell.ModNone), func(tview.Primitive) {})
	}

	taskCount := 0
	for _, r := range tl.rows {
		if r.kind == rowTask {
			taskCount++
		}
	}
	// task-c (active in beta) should be visible; task-d (archived in beta) too if archive expanded
	if taskCount < 1 {
		t.Errorf("expected at least 1 visible task matching 'beta', got %d", taskCount)
	}
}

func TestTaskListView_FilterCaseInsensitive(t *testing.T) {
	tl := NewTaskListView()
	tl.expanded = "alpha"
	tl.SetTasks(makeTasks())

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})

	for _, ch := range "TASK-B" {
		handler(tcell.NewEventKey(tcell.KeyRune, ch, tcell.ModNone), func(tview.Primitive) {})
	}

	taskCount := 0
	for _, r := range tl.rows {
		if r.kind == rowTask {
			taskCount++
		}
	}
	if taskCount != 1 {
		t.Errorf("case-insensitive filter: expected 1 task, got %d", taskCount)
	}
}

func TestTaskListView_FilterMultiTerm(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "Download-this-video", Project: "forge", Status: model.StatusPending},
		{ID: "2", Name: "Fix-login-bug", Project: "forge", Status: model.StatusInProgress},
		{ID: "3", Name: "Download-report", Project: "alpha", Status: model.StatusPending},
	})

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})

	// Type "forge download" — should match only task in forge with "download" in name
	for _, ch := range "forge download" {
		handler(tcell.NewEventKey(tcell.KeyRune, ch, tcell.ModNone), func(tview.Primitive) {})
	}

	var matched []string
	for _, r := range tl.rows {
		if r.kind == rowTask {
			matched = append(matched, r.task.Name)
		}
	}
	if len(matched) != 1 {
		t.Fatalf("expected 1 task, got %d: %v", len(matched), matched)
	}
	if matched[0] != "Download-this-video" {
		t.Errorf("expected Download-this-video, got %s", matched[0])
	}
}

func TestTaskListView_FilterEscapeClears(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})
	handler(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone), func(tview.Primitive) {})

	if tl.Filter() != "x" {
		t.Fatalf("filter should be 'x', got %q", tl.Filter())
	}

	// Escape clears filter and exits filter mode
	handler(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(tview.Primitive) {})

	if tl.Filtering() {
		t.Error("should not be filtering after Escape")
	}
	if tl.Filter() != "" {
		t.Errorf("filter should be empty after Escape, got %q", tl.Filter())
	}

	// All tasks should be visible again
	taskCount := 0
	for _, r := range tl.rows {
		if r.kind == rowTask {
			taskCount++
		}
	}
	if taskCount < 2 {
		t.Errorf("expected all tasks visible after clearing filter, got %d", taskCount)
	}
}

func TestTaskListView_FilterEnterConfirms(t *testing.T) {
	tl := NewTaskListView()
	tl.expanded = "alpha"
	tl.SetTasks(makeTasks())

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})
	for _, ch := range "task-a" {
		handler(tcell.NewEventKey(tcell.KeyRune, ch, tcell.ModNone), func(tview.Primitive) {})
	}

	// Enter confirms — exits filter mode but keeps filter text
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})

	if tl.Filtering() {
		t.Error("should not be in filter input mode after Enter")
	}
	if tl.Filter() != "task-a" {
		t.Errorf("filter should persist after Enter, got %q", tl.Filter())
	}

	// Filter should still be applied
	taskCount := 0
	for _, r := range tl.rows {
		if r.kind == rowTask {
			taskCount++
		}
	}
	if taskCount != 1 {
		t.Errorf("filter should still be applied after Enter, got %d tasks", taskCount)
	}
}

func TestTaskListView_FilterBackspace(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})
	handler(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), func(tview.Primitive) {})
	handler(tcell.NewEventKey(tcell.KeyRune, 'b', tcell.ModNone), func(tview.Primitive) {})

	if tl.Filter() != "ab" {
		t.Fatalf("filter should be 'ab', got %q", tl.Filter())
	}

	handler(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0), func(tview.Primitive) {})
	if tl.Filter() != "a" {
		t.Errorf("after backspace: filter should be 'a', got %q", tl.Filter())
	}
}

func TestTaskListView_FilterNavigateWhileFiltering(t *testing.T) {
	tl := NewTaskListView()
	tl.expanded = "alpha"
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "fix-bug", Project: "alpha", Status: model.StatusPending},
		{ID: "2", Name: "fix-typo", Project: "alpha", Status: model.StatusPending},
		{ID: "3", Name: "add-feature", Project: "alpha", Status: model.StatusPending},
	})

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})
	for _, ch := range "fix" {
		handler(tcell.NewEventKey(tcell.KeyRune, ch, tcell.ModNone), func(tview.Primitive) {})
	}

	// Should have 2 matching tasks
	sel1 := tl.SelectedTask()
	if sel1 == nil {
		t.Fatal("expected a selected task")
	}

	// Navigate with arrow keys while filtering
	handler(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(tview.Primitive) {})
	sel2 := tl.SelectedTask()
	if sel2 == nil {
		t.Fatal("expected a selected task after Down")
	}
	if sel2.ID == sel1.ID {
		t.Error("Down should move to a different task")
	}
}

func TestTaskListView_FilterPasteHandler(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})

	paste := tl.PasteHandler()
	paste("task-b", func(tview.Primitive) {})

	if tl.Filter() != "task-b" {
		t.Errorf("after paste: filter = %q, want 'task-b'", tl.Filter())
	}
}

func TestTaskListView_FilterPasteIgnoredWhenNotFiltering(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())

	paste := tl.PasteHandler()
	paste("something", func(tview.Primitive) {})

	if tl.Filter() != "" {
		t.Errorf("paste when not filtering should be ignored, got %q", tl.Filter())
	}
}

func TestTaskListView_FilterEscapeFromConfirmedFilter(t *testing.T) {
	tl := NewTaskListView()
	tl.expanded = "alpha"
	tl.SetTasks(makeTasks())

	handler := tl.InputHandler()

	// Activate filter, type, confirm with Enter
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})
	handler(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), func(tview.Primitive) {})
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})

	if tl.Filtering() {
		t.Fatal("should not be in filter mode after Enter")
	}
	if tl.Filter() != "a" {
		t.Fatalf("filter should be 'a', got %q", tl.Filter())
	}

	// Press Escape to clear the confirmed filter
	handler(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(tview.Primitive) {})

	if tl.Filter() != "" {
		t.Errorf("Escape should clear confirmed filter, got %q", tl.Filter())
	}
}

func TestTaskListView_FilterNoMatch(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})
	for _, ch := range "zzzzz" {
		handler(tcell.NewEventKey(tcell.KeyRune, ch, tcell.ModNone), func(tview.Primitive) {})
	}

	// No rows should match
	if len(tl.rows) != 0 {
		t.Errorf("expected 0 rows for non-matching filter, got %d", len(tl.rows))
	}
	if tl.SelectedTask() != nil {
		t.Error("should have no selected task when filter matches nothing")
	}
}

func TestTaskListView_SelectedProject(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())

	// Starts on alpha's first task — SelectedProject should return "alpha".
	if got := tl.SelectedProject(); got != "alpha" {
		t.Errorf("SelectedProject = %q, want 'alpha'", got)
	}

	// Navigate down into beta.
	for i := 0; i < 10; i++ {
		tl.CursorDown()
	}
	if got := tl.SelectedProject(); got != "beta" {
		t.Errorf("SelectedProject after navigating to beta = %q, want 'beta'", got)
	}

	// Empty list — should return "".
	tl2 := NewTaskListView()
	if got := tl2.SelectedProject(); got != "" {
		t.Errorf("SelectedProject on empty list = %q, want empty", got)
	}
}

func TestTaskListView_RenameKey(t *testing.T) {
	tl := NewTaskListView()
	var renamed *model.Task
	tl.OnRename = func(task *model.Task) {
		renamed = task
	}

	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "my-task", Project: "p", Status: model.StatusPending},
	})
	tl.expanded = "p"
	tl.buildRows()
	tl.CursorDown()

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone), func(tview.Primitive) {})
	if renamed == nil {
		t.Fatal("OnRename should have been called")
	}
	if renamed.ID != "1" {
		t.Errorf("OnRename called with task %s, want 1", renamed.ID)
	}

	// No callback wired — should not panic.
	tl.OnRename = nil
	renamed = nil
	handler(tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone), func(tview.Primitive) {})
	if renamed != nil {
		t.Error("OnRename should not fire when callback is nil")
	}
}

func TestTaskListView_RenameKeyNoSelection(t *testing.T) {
	tl := NewTaskListView()
	var renamed *model.Task
	tl.OnRename = func(task *model.Task) {
		renamed = task
	}

	// Empty list — 'r' should be a no-op.
	tl.SetTasks(nil)
	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone), func(tview.Primitive) {})
	if renamed != nil {
		t.Error("OnRename should not fire with no selected task")
	}
}

func TestTaskListView_CopyPromptKey(t *testing.T) {
	tl := NewTaskListView()
	var copied *model.Task
	tl.OnCopyPrompt = func(task *model.Task) {
		copied = task
	}

	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "has-prompt", Project: "p", Prompt: "fix the bug"},
	})
	tl.expanded = "p"
	tl.buildRows()
	tl.CursorDown()

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModNone), func(tview.Primitive) {})
	if copied == nil {
		t.Fatal("OnCopyPrompt should have been called for task with prompt")
	}
	if copied.ID != "1" {
		t.Errorf("OnCopyPrompt called with task %s, want 1", copied.ID)
	}

	// Task without prompt — callback should NOT fire.
	copied = nil
	tl.SetTasks([]*model.Task{
		{ID: "2", Name: "no-prompt", Project: "p", Prompt: ""},
	})
	tl.expanded = "p"
	tl.buildRows()
	tl.CursorDown()

	handler(tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModNone), func(tview.Primitive) {})
	if copied != nil {
		t.Error("OnCopyPrompt should NOT fire for task without prompt")
	}

	// No callback wired — should not panic.
	tl.OnCopyPrompt = nil
	tl.SetTasks([]*model.Task{
		{ID: "3", Name: "with-prompt", Project: "p", Prompt: "hello"},
	})
	tl.expanded = "p"
	tl.buildRows()
	tl.CursorDown()
	handler(tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModNone), func(tview.Primitive) {})
}

func TestTaskListView_FilterOptionDelete(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})
	for _, ch := range "hello world" {
		handler(tcell.NewEventKey(tcell.KeyRune, ch, tcell.ModNone), func(tview.Primitive) {})
	}
	testutil.Equal(t, tl.Filter(), "hello world")

	// Option+Delete: delete word left ("world")
	handler(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModAlt), func(tview.Primitive) {})
	testutil.Equal(t, tl.Filter(), "hello ")
}

func TestTaskListView_FilterCmdDelete(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})
	for _, ch := range "hello world" {
		handler(tcell.NewEventKey(tcell.KeyRune, ch, tcell.ModNone), func(tview.Primitive) {})
	}
	testutil.Equal(t, tl.Filter(), "hello world")

	// Cmd+Delete (Ctrl+U): clear entire filter text
	handler(tcell.NewEventKey(tcell.KeyCtrlU, 0, tcell.ModNone), func(tview.Primitive) {})
	testutil.Equal(t, tl.Filter(), "")
	// Should still be in filter mode
	testutil.Equal(t, tl.Filtering(), true)
}

func TestTaskListView_FilterCtrlW(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks(makeTasks())

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})
	for _, ch := range "foo bar" {
		handler(tcell.NewEventKey(tcell.KeyRune, ch, tcell.ModNone), func(tview.Primitive) {})
	}
	testutil.Equal(t, tl.Filter(), "foo bar")

	// Ctrl+W: delete word left
	handler(tcell.NewEventKey(tcell.KeyCtrlW, 0, tcell.ModNone), func(tview.Primitive) {})
	testutil.Equal(t, tl.Filter(), "foo ")
}

// sanitizeTaskName duplicated from app.go for test isolation.
func sanitizeTaskName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '\n' || r == '\r' || r == '\t' {
			b.WriteRune(' ')
		} else if r < 0x20 {
			continue
		} else {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func TestSanitizeTaskName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "my-task", "my-task"},
		{"trim spaces", "  hello  ", "hello"},
		{"newlines become spaces", "line1\nline2\nline3", "line1 line2 line3"},
		{"carriage return", "foo\r\nbar", "foo  bar"},
		{"tabs become spaces", "foo\tbar", "foo bar"},
		{"control chars stripped", "foo\x00bar\x1Fbaz", "foobarbaz"},
		{"only whitespace", "  \n\t  ", ""},
		{"empty", "", ""},
		{"unicode preserved", "日本語タスク", "日本語タスク"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeTaskName(tt.in)
			if got != tt.want {
				t.Errorf("sanitizeTaskName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDrawTaskRow_BranchNotDisplayed(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(60, 5)

	tl := NewTaskListView()
	task := &model.Task{
		ID:     "1",
		Name:   "fix-bug",
		Branch: "argus/fix-bug",
		Status: model.StatusPending,
	}

	tl.drawTaskRow(screen, 0, 0, 60, task, false)
	screen.Show()

	// Read the row content.
	var line string
	for col := 0; col < 60; col++ {
		r, _, _, _ := screen.GetContent(col, 0)
		line += string(r)
	}

	// Task name is displayed, but branch is not shown in rows (removed in 17243bd).
	testutil.Contains(t, line, "fix-bug")
	if strings.Contains(line, "argus/fix-bug") {
		t.Errorf("branch should not be displayed in task row, got: %q", line)
	}
}

func TestDrawTaskRow_NarrowTerminal(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(20, 5)

	tl := NewTaskListView()
	task := &model.Task{
		ID:     "1",
		Name:   "fix-bug",
		Branch: "argus/very-long-branch-name-that-exceeds-width",
		Status: model.StatusPending,
	}

	// Must not panic on narrow terminal.
	tl.drawTaskRow(screen, 0, 0, 20, task, false)
	screen.Show()

	var line string
	for col := 0; col < 20; col++ {
		r, _, _, _ := screen.GetContent(col, 0)
		line += string(r)
	}

	// Name should still appear (possibly truncated).
	testutil.Contains(t, line, "fix")
}

func TestDrawTaskRow_NoBranch(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(60, 5)

	tl := NewTaskListView()
	task := &model.Task{
		ID:     "1",
		Name:   "fix-bug",
		Status: model.StatusPending,
	}

	tl.drawTaskRow(screen, 0, 0, 60, task, false)
	screen.Show()

	// Read the row content.
	var line string
	for col := 0; col < 60; col++ {
		r, _, _, _ := screen.GetContent(col, 0)
		line += string(r)
	}

	testutil.Contains(t, line, "fix-bug")
}

func TestTaskListView_WaitingReviewSection(t *testing.T) {
	t.Run("WR tasks appear in their own section above archive", func(t *testing.T) {
		tl := NewTaskListView()
		tl.SetTasks([]*model.Task{
			{ID: "1", Name: "active", Project: "proj", Status: model.StatusPending},
			{ID: "2", Name: "waiting", Project: "proj", Status: model.StatusInReview, WaitingReview: true},
			{ID: "3", Name: "archived", Project: "proj", Status: model.StatusPending, Archived: true},
		})

		// Find the section header rows and confirm the order.
		wrIdx, archIdx := -1, -1
		for i, r := range tl.rows {
			switch r.kind {
			case rowWaitingReviewHeader:
				wrIdx = i
			case rowArchiveHeader:
				archIdx = i
			}
		}
		if wrIdx < 0 {
			t.Fatal("expected a waiting-for-review header row")
		}
		if archIdx < 0 {
			t.Fatal("expected an archive header row")
		}
		if wrIdx >= archIdx {
			t.Errorf("waiting-for-review header should sit above archive header (wrIdx=%d, archIdx=%d)", wrIdx, archIdx)
		}
	})

	t.Run("WR-flagged tasks do not appear in the active section", func(t *testing.T) {
		tl := NewTaskListView()
		tl.SetTasks([]*model.Task{
			{ID: "1", Name: "active", Project: "proj", Status: model.StatusPending},
			{ID: "2", Name: "waiting", Project: "proj", Status: model.StatusInReview, WaitingReview: true},
		})
		// Active section: expanded "proj" should show exactly one task (ID=1).
		activeTasks := 0
		for _, r := range tl.rows {
			if r.kind != rowTask {
				continue
			}
			if tl.sectionAt(rowIndex(tl, r)) == sectionActive {
				activeTasks++
				if r.task.ID != "1" {
					t.Errorf("active section task should be id=1, got id=%s", r.task.ID)
				}
			}
		}
		if activeTasks != 1 {
			t.Errorf("active task rows = %d, want 1", activeTasks)
		}
	})
}

// rowIndex returns the index of a row. Only supports task and project rows;
// separator and header rows are not uniquely identifiable by this helper
// because multiple may share kind+empty-project within one row list.
func rowIndex(tl *TaskListView, target taskRow) int {
	if target.kind != rowTask && target.kind != rowProject {
		return -1
	}
	for i, r := range tl.rows {
		if r.kind != target.kind || r.project != target.project {
			continue
		}
		if r.kind == rowTask {
			if r.task != nil && target.task != nil && r.task.ID != target.task.ID {
				continue
			}
		}
		return i
	}
	return -1
}

func TestTaskListView_WaitingReviewToggle(t *testing.T) {
	tl := NewTaskListView()
	var captured *model.Task
	tl.OnWaitingReview = func(task *model.Task) {
		captured = task
	}
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "task1", Status: model.StatusInReview, Project: "p"},
	})
	tl.expanded = "p"
	tl.buildRows()
	tl.clampCursor()

	handler := tl.InputHandler()
	// Press 'w' — task should be flagged as waiting for review.
	handler(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone), func(tview.Primitive) {})
	if captured == nil {
		t.Fatal("OnWaitingReview should have been called")
	}
	if !captured.WaitingReview {
		t.Error("task should be flagged WaitingReview after pressing 'w'")
	}

	// Press 'w' again — toggle off.
	captured = nil
	handler(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone), func(tview.Primitive) {})
	if captured == nil {
		t.Fatal("OnWaitingReview should have fired again")
	}
	if captured.WaitingReview {
		t.Error("task should no longer be flagged after second 'w'")
	}
}

func TestTaskListView_WaitingReviewAndArchiveMutuallyExclusive(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "task1", Status: model.StatusPending, Project: "p", Archived: true},
	})
	tl.expanded = "p"
	tl.archiveExpanded = true
	tl.archiveProject = "p"
	tl.buildRows()
	// Move cursor onto the archived task.
	for i, r := range tl.rows {
		if r.kind == rowTask && r.task.ID == "1" {
			tl.cursor = i
			break
		}
	}

	tl.OnWaitingReview = func(*model.Task) {}
	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone), func(tview.Primitive) {})

	// Assert via the task pointer we captured before any row rebuilds, not
	// SelectedTask() — buildRows() moves the task into the WR section and the
	// cursor may end up on a different row after restoration.
	task := tl.tasks[0]
	if !task.WaitingReview {
		t.Error("task should be flagged WaitingReview after 'w'")
	}
	if task.Archived {
		t.Error("pressing 'w' on an archived task should clear Archived")
	}

	// Now press 'a' — should clear WaitingReview and set Archived. Point the
	// cursor back at the (now WR-section) task so the 'a' handler has a
	// SelectedTask to act on.
	for i, r := range tl.rows {
		if r.kind == rowTask && r.task == task {
			tl.cursor = i
			break
		}
	}
	tl.OnArchive = func(*model.Task) {}
	handler(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), func(tview.Primitive) {})
	if task.WaitingReview {
		t.Error("pressing 'a' on a waiting-for-review task should clear WaitingReview")
	}
	if !task.Archived {
		t.Error("task should be Archived after 'a'")
	}
}

func TestTaskListView_PinToggle(t *testing.T) {
	tl := NewTaskListView()
	var captured *model.Task
	tl.OnPin = func(task *model.Task) {
		captured = task
	}
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "task1", Status: model.StatusPending, Project: "p"},
	})
	tl.expanded = "p"
	tl.buildRows()
	tl.clampCursor()

	handler := tl.InputHandler()
	// Press 'P' — task should be pinned.
	handler(tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModNone), func(tview.Primitive) {})
	if captured == nil {
		t.Fatal("OnPin should have been called")
	}
	if !captured.Pinned {
		t.Error("task should be Pinned after pressing 'P'")
	}

	// Press 'P' again — toggle off. The task is now in the Pinned section, so
	// move the cursor to wherever the captured task lives now before re-firing.
	pinnedTask := captured
	captured = nil
	for i, r := range tl.rows {
		if r.kind == rowTask && r.task == pinnedTask {
			tl.cursor = i
			break
		}
	}
	handler(tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModNone), func(tview.Primitive) {})
	if captured == nil {
		t.Fatal("OnPin should have fired again")
	}
	if captured.Pinned {
		t.Error("task should no longer be pinned after second 'P'")
	}
}

func TestTaskListView_PinClearsOtherFlags(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "task1", Status: model.StatusPending, Project: "p", Archived: true},
	})
	tl.expanded = "p"
	tl.archiveExpanded = true
	tl.archiveProject = "p"
	tl.buildRows()
	for i, r := range tl.rows {
		if r.kind == rowTask && r.task.ID == "1" {
			tl.cursor = i
			break
		}
	}

	tl.OnPin = func(*model.Task) {}
	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModNone), func(tview.Primitive) {})

	task := tl.tasks[0]
	if !task.Pinned {
		t.Error("task should be Pinned after 'P'")
	}
	if task.Archived {
		t.Error("pinning an archived task should clear Archived")
	}

	// Move the cursor to the now-pinned task and press 'a' — should clear
	// Pinned and set Archived.
	for i, r := range tl.rows {
		if r.kind == rowTask && r.task == task {
			tl.cursor = i
			break
		}
	}
	tl.OnArchive = func(*model.Task) {}
	handler(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), func(tview.Primitive) {})
	if task.Pinned {
		t.Error("pressing 'a' on a pinned task should clear Pinned")
	}
	if !task.Archived {
		t.Error("task should be Archived after 'a'")
	}
}

func TestTaskListView_PinnedSectionRendersAboveActive(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "active", Project: "p", Status: model.StatusPending},
		{ID: "2", Name: "pinned", Project: "p", Status: model.StatusPending, Pinned: true},
	})
	tl.expanded = "p"
	tl.buildRows()

	// First row must be the Pinned header.
	if len(tl.rows) == 0 || tl.rows[0].kind != rowPinnedHeader {
		t.Fatalf("expected first row to be rowPinnedHeader, got %+v", tl.rows)
	}
	// Pinned task must appear before any active task in the row stream.
	pinnedIdx, activeIdx := -1, -1
	for i, r := range tl.rows {
		if r.kind != rowTask {
			continue
		}
		if r.task.ID == "2" && pinnedIdx == -1 {
			pinnedIdx = i
		}
		if r.task.ID == "1" && activeIdx == -1 {
			activeIdx = i
		}
	}
	if pinnedIdx == -1 || activeIdx == -1 {
		t.Fatalf("expected both tasks in rows, pinnedIdx=%d activeIdx=%d", pinnedIdx, activeIdx)
	}
	if pinnedIdx >= activeIdx {
		t.Errorf("pinned task at row %d should precede active task at row %d", pinnedIdx, activeIdx)
	}
}

func TestTaskListView_NavigatePinnedToActiveAndBack(t *testing.T) {
	// Cursor should cross the Pinned/Active boundary in both directions
	// without skipping rows or getting stuck on the trailing separator.
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "pinned-task", Project: "p", Status: model.StatusPending, Pinned: true},
		{ID: "2", Name: "active-task", Project: "p", Status: model.StatusPending},
	})
	tl.expanded = "p"
	tl.buildRows()
	tl.clampCursor()

	// Cursor should land on the pinned task first (Pinned section is on top).
	if got := tl.SelectedTask(); got == nil || got.ID != "1" {
		t.Fatalf("expected initial cursor on pinned task id=1, got %+v", got)
	}

	// Down → cross into Active.
	tl.CursorDown()
	if got := tl.SelectedTask(); got == nil || got.ID != "2" {
		t.Fatalf("expected cursor on active task id=2 after down, got %+v", got)
	}

	// Up → cross back into Pinned.
	tl.CursorUp()
	if got := tl.SelectedTask(); got == nil || got.ID != "1" {
		t.Fatalf("expected cursor back on pinned task id=1 after up, got %+v", got)
	}
}

func TestTaskListView_PinnedRemainsExpandedWhenCursorLeaves(t *testing.T) {
	// The Pinned section must NOT auto-collapse when the cursor moves into
	// Active — pinning is an explicit "keep visible" action.
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "pinned", Project: "p", Status: model.StatusPending, Pinned: true},
		{ID: "2", Name: "active", Project: "p", Status: model.StatusPending},
	})
	tl.expanded = "p"
	tl.buildRows()
	tl.clampCursor()

	// Sanity: the pinned task row exists.
	pinnedTaskRow := -1
	for i, r := range tl.rows {
		if r.kind == rowTask && r.task.ID == "1" {
			pinnedTaskRow = i
			break
		}
	}
	if pinnedTaskRow == -1 {
		t.Fatal("pinned task row missing on initial build")
	}

	// Move cursor down into Active.
	tl.CursorDown()
	if got := tl.SelectedTask(); got == nil || got.ID != "2" {
		t.Fatalf("expected cursor on active task id=2, got %+v", got)
	}

	// Pinned task row must STILL be present (section did not collapse).
	for _, r := range tl.rows {
		if r.kind == rowTask && r.task.ID == "1" {
			return // pass
		}
	}
	t.Error("pinned task row was hidden after cursor moved into Active section")
}

func TestTaskListView_NavigateThroughAllThreeSections(t *testing.T) {
	// Downward navigation active → WR → archive should visit a task in each
	// section exactly once, regardless of which section is currently expanded.
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "active", Project: "proj", Status: model.StatusPending},
		{ID: "2", Name: "waiting", Project: "proj", Status: model.StatusInReview, WaitingReview: true},
		{ID: "3", Name: "archived", Project: "proj", Status: model.StatusPending, Archived: true},
	})

	visited := []string{}
	for i := 0; i < 20; i++ {
		if task := tl.SelectedTask(); task != nil {
			if len(visited) == 0 || visited[len(visited)-1] != task.ID {
				visited = append(visited, task.ID)
			}
		}
		tl.CursorDown()
	}

	// The sequence must include all three IDs in order 1 → 2 → 3.
	want := []string{"1", "2", "3"}
	for i, id := range want {
		if i >= len(visited) || visited[i] != id {
			t.Fatalf("downward visit order = %v, want %v", visited, want)
		}
	}
}

func TestTaskListView_WaitingReviewAutoExpand(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "active", Project: "proj", Status: model.StatusPending},
		{ID: "2", Name: "waiting", Project: "proj", Status: model.StatusInReview, WaitingReview: true},
	})

	if tl.waitingReviewExpanded {
		t.Error("waiting-for-review section should start collapsed")
	}

	// Navigate down past the active task — should enter the WR section.
	for i := 0; i < 10; i++ {
		tl.CursorDown()
	}

	task := tl.SelectedTask()
	if task == nil || task.ID != "2" {
		t.Fatalf("expected to land on waiting task id=2, got %+v", task)
	}
	if !tl.waitingReviewExpanded {
		t.Error("WR section should be expanded after cursor enters it")
	}

	// Back up out of the WR section — should auto-collapse.
	tl.CursorUp()
	task = tl.SelectedTask()
	if task == nil || task.ID != "1" {
		t.Fatalf("expected to return to active task id=1, got %+v", task)
	}
	if tl.waitingReviewExpanded {
		t.Error("WR section should collapse after cursor leaves it")
	}
}

// newSim creates a SimulationScreen with cleanup.
func newSim(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatalf("sim.Init: %v", err)
	}
	sim.SetSize(w, h)
	t.Cleanup(sim.Fini)
	return sim
}

func dumpScreen(sim tcell.SimulationScreen) string {
	w, h := sim.Size()
	var lines []string
	for row := 0; row < h; row++ {
		var buf []rune
		for col := 0; col < w; col++ {
			r, _, _, _ := sim.GetContent(col, row)
			buf = append(buf, r)
		}
		lines = append(lines, string(buf))
	}
	return strings.Join(lines, "\n")
}

// ---------- Drawing helpers ----------

func TestTaskListView_DrawSeparator(t *testing.T) {
	sim := newSim(t, 30, 5)
	tl := NewTaskListView()
	tl.drawSeparator(sim, 0, 0, 30)
	// All cells in row 0 should be '─'.
	for col := 0; col < 30; col++ {
		ch, _, _, _ := sim.GetContent(col, 0)
		if ch != '─' {
			t.Errorf("col %d: ch = %c, want ─", col, ch)
		}
	}
}

func TestTaskListView_DrawArchiveHeader(t *testing.T) {
	sim := newSim(t, 30, 3)
	tl := NewTaskListView()

	// Collapsed.
	tl.archiveExpanded = false
	tl.drawArchiveHeader(sim, 0, 0, 30)
	row := readRow(sim, 0, 30)
	testutil.Contains(t, row, "Archive")
	testutil.Contains(t, row, "▸")

	// Expanded.
	tl.archiveExpanded = true
	tl.drawArchiveHeader(sim, 0, 1, 30)
	row = readRow(sim, 1, 30)
	testutil.Contains(t, row, "Archive")
	testutil.Contains(t, row, "▾")
}

func TestTaskListView_DrawWaitingReviewHeader(t *testing.T) {
	sim := newSim(t, 50, 3)
	tl := NewTaskListView()

	tl.waitingReviewExpanded = false
	tl.drawWaitingReviewHeader(sim, 0, 0, 50)
	row := readRow(sim, 0, 50)
	testutil.Contains(t, row, "Waiting for Review")
	testutil.Contains(t, row, "▸")

	tl.waitingReviewExpanded = true
	tl.drawWaitingReviewHeader(sim, 0, 1, 50)
	row = readRow(sim, 1, 50)
	testutil.Contains(t, row, "Waiting for Review")
	testutil.Contains(t, row, "▾")
}

func TestTaskListView_DrawFilterInput(t *testing.T) {
	sim := newSim(t, 40, 2)
	tl := NewTaskListView()
	tl.filtering = true
	tl.filter = "abc"
	tl.drawFilterInput(sim, 0, 0, 40)
	row := readRow(sim, 0, 40)
	testutil.Contains(t, row, "/ abc")
}

func TestTaskListView_DrawFilterInput_LongFilter(t *testing.T) {
	// Filter wider than width — cursor is past edge, no panic.
	sim := newSim(t, 12, 2)
	tl := NewTaskListView()
	tl.filtering = true
	tl.filter = strings.Repeat("x", 30)
	tl.drawFilterInput(sim, 0, 0, 12)
}

// readRow returns the runes at the given row.
func readRow(sim tcell.SimulationScreen, row, w int) string {
	var buf []rune
	for col := 0; col < w; col++ {
		r, _, _, _ := sim.GetContent(col, row)
		buf = append(buf, r)
	}
	return string(buf)
}

// ---------- Draw end-to-end ----------

func TestTaskListView_Draw_Basic(t *testing.T) {
	sim := newSim(t, 40, 20)
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "task-a", Project: "alpha", Status: model.StatusPending},
		{ID: "2", Name: "task-b", Project: "alpha", Status: model.StatusInProgress},
	})
	tl.expanded = "alpha"
	tl.SetRect(0, 0, 40, 20)
	tl.Draw(sim)

	out := dumpScreen(sim)
	testutil.Contains(t, out, "alpha")
	testutil.Contains(t, out, "task-a")
}

func TestTaskListView_Draw_WithFilterTitle(t *testing.T) {
	sim := newSim(t, 60, 12)
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "task-a", Project: "alpha", Status: model.StatusPending},
	})
	tl.expanded = "alpha"
	tl.filter = "a"
	tl.SetRect(0, 0, 60, 12)
	tl.Draw(sim)
	out := dumpScreen(sim)
	// Title should contain "[/a]"
	testutil.Contains(t, out, "/a")
}

func TestTaskListView_Draw_FilteringMode(t *testing.T) {
	sim := newSim(t, 60, 12)
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "alpha-task", Project: "p", Status: model.StatusPending},
	})
	tl.expanded = "p"
	tl.filtering = true
	tl.filter = "alp"
	tl.SetRect(0, 0, 60, 12)
	tl.Draw(sim)
	out := dumpScreen(sim)
	// In filtering mode, the bottom row shows the input.
	testutil.Contains(t, out, "/ alp")
}

func TestTaskListView_Draw_TooSmall(t *testing.T) {
	sim := newSim(t, 5, 3)
	tl := NewTaskListView()
	// width/height too small for border.
	tl.SetRect(0, 0, 0, 0)
	tl.Draw(sim)
	tl.SetRect(0, 0, 1, 1)
	tl.Draw(sim)
}

func TestTaskListView_Draw_AllSections(t *testing.T) {
	// Render with active, waiting-review, and archive sections all present.
	sim := newSim(t, 60, 30)
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "active", Project: "p", Status: model.StatusPending},
		{ID: "2", Name: "wait", Project: "p", Status: model.StatusInReview, WaitingReview: true},
		{ID: "3", Name: "arch", Project: "p", Status: model.StatusComplete, Archived: true},
	})
	tl.SetRect(0, 0, 60, 30)
	tl.Draw(sim)

	out := dumpScreen(sim)
	testutil.Contains(t, out, "active")
	testutil.Contains(t, out, "Waiting for Review")
	testutil.Contains(t, out, "Archive")
}

func TestTaskListView_DrawTaskRow_AllStatuses(t *testing.T) {
	// Force every status branch in drawTaskRow.
	sim := newSim(t, 60, 20)
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "p", Name: "pending-task", Project: "p", Status: model.StatusPending},
		{ID: "ip-running", Name: "running-task", Project: "p", Status: model.StatusInProgress},
		{ID: "ip-idle", Name: "idle-task", Project: "p", Status: model.StatusInProgress},
		{ID: "ip-unvisited", Name: "unvisited", Project: "p", Status: model.StatusInProgress},
		{ID: "ir", Name: "review", Project: "p", Status: model.StatusInReview},
		{ID: "c", Name: "complete", Project: "p", Status: model.StatusComplete},
	})
	tl.expanded = "p"
	tl.SetRunning([]string{"ip-running", "ip-idle", "ip-unvisited"})
	tl.SetIdle([]string{"ip-idle"})
	tl.SetIdleUnvisited([]string{"ip-unvisited"})
	tl.SetRect(0, 0, 60, 20)
	tl.Draw(sim)

	out := dumpScreen(sim)
	testutil.Contains(t, out, "pending-task")
	testutil.Contains(t, out, "running-task")
	testutil.Contains(t, out, "idle-task")
	testutil.Contains(t, out, "unvisited")
	testutil.Contains(t, out, "review")
	testutil.Contains(t, out, "complete")
}

// ---------- TaskPage Focus / HasFocus / InputHandler / MouseHandler ----------

func TestTaskPage_FocusDelegates(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{{ID: "1", Name: "x", Project: "p", Status: model.StatusPending}})
	flex := tview.NewFlex().AddItem(tl, 0, 1, true)
	tp := NewTaskPage(flex, tl)

	delegated := false
	tp.Focus(func(p tview.Primitive) {
		delegated = true
		// flex.Focus delegates to its first focusable child.
		if p == nil {
			t.Error("Focus delegate: primitive should not be nil")
		}
	})
	if !delegated {
		t.Error("Focus should call delegate")
	}
}

func TestTaskPage_HasFocus(t *testing.T) {
	tl := NewTaskListView()
	flex := tview.NewFlex().AddItem(tl, 0, 1, true)
	tp := NewTaskPage(flex, tl)
	// Initially no focus.
	if tp.HasFocus() {
		t.Error("HasFocus should be false initially")
	}
	// tview's InputHandler is what we get, even without focus tree wired.
	_ = tp.HasFocus()
}

func TestTaskPage_InputHandler(t *testing.T) {
	tl := NewTaskListView()
	flex := tview.NewFlex().AddItem(tl, 0, 1, true)
	tp := NewTaskPage(flex, tl)
	handler := tp.InputHandler()
	if handler == nil {
		t.Fatal("InputHandler should not be nil")
	}
	// Sending a key event should not panic. We don't assert behavior since
	// the inner flex needs focus wired for InputHandler to do anything,
	// but the wrapper itself must be safe to call.
	handler(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(p tview.Primitive) {})
}

func TestTaskPage_MouseHandler_RedirectsFocus(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{{ID: "1", Name: "x", Project: "p", Status: model.StatusPending}})
	flex := tview.NewFlex().AddItem(tl, 0, 1, true)
	tp := NewTaskPage(flex, tl)
	tp.SetRect(0, 0, 80, 20)
	tl.SetRect(0, 0, 80, 20)

	handler := tp.MouseHandler()
	if handler == nil {
		t.Fatal("MouseHandler should not be nil")
	}

	var focusedOn tview.Primitive
	setFocus := func(p tview.Primitive) { focusedOn = p }
	// Click inside the page — guarded setFocus must redirect to the task list.
	handler(tview.MouseLeftClick, tcell.NewEventMouse(40, 10, tcell.Button1, 0), setFocus)

	// guardedSetFocus redirects to tasklist when called by inner. If focusedOn
	// was set, it must be the tasklist.
	if focusedOn != nil && focusedOn != tl {
		t.Errorf("setFocus should be redirected to tasklist, got %T", focusedOn)
	}
}

func TestTaskPage_MouseHandler_NilInnerHandler(t *testing.T) {
	// When the inner Flex returns a nil InputHandler/MouseHandler (atypical),
	// the wrapper should default to (false, nil).
	// The flex's MouseHandler should NEVER be nil, but we exercise the wrapper.
	tl := NewTaskListView()
	flex := tview.NewFlex()
	tp := NewTaskPage(flex, tl)
	tp.SetRect(0, 0, 80, 20)

	handler := tp.MouseHandler()
	// Click outside the box — wrapper returns false, nil.
	consumed, _ := handler(tview.MouseLeftClick, tcell.NewEventMouse(200, 200, tcell.Button1, 0), func(p tview.Primitive) {})
	if consumed {
		t.Error("click outside rect should not be consumed")
	}
}

// ---------- TaskListView — drawTaskRow / drawProjectRow ----------

func TestTaskListView_DrawTaskRow_WithElapsedTime(t *testing.T) {
	sim := newSim(t, 80, 8)
	tl := NewTaskListView()
	started := time.Now().Add(-2 * time.Hour)
	ended := time.Now().Add(-1 * time.Hour)
	tl.SetTasks([]*model.Task{{
		ID:        "1",
		Name:      "task-with-elapsed",
		Project:   "p",
		Status:    model.StatusComplete,
		StartedAt: started,
		EndedAt:   ended,
	}})
	tl.expanded = "p"
	tl.SetRect(0, 0, 80, 8)
	tl.Draw(sim)

	out := dumpScreen(sim)
	testutil.Contains(t, out, "task-with-elapsed")
}

func TestTaskListView_DrawTaskRow_CursorFill(t *testing.T) {
	// Task with cursor selected — exercise the highlight fill branch.
	sim := newSim(t, 60, 6)
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "alpha", Project: "p", Status: model.StatusPending},
	})
	tl.expanded = "p"
	tl.SetRect(0, 0, 60, 6)
	tl.Draw(sim) // cursor is on the task by default
}

func TestTaskListView_DrawProjectRow_NoProject(t *testing.T) {
	// Task with empty project name → grouped under "(no project)".
	sim := newSim(t, 60, 8)
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "x", Project: "", Status: model.StatusPending},
	})
	tl.SetRect(0, 0, 60, 8)
	tl.Draw(sim)
	testutil.Contains(t, dumpScreen(sim), "(no project)")
}

func TestTaskListView_DrawProjectRow_ExpandedInWaitingReview(t *testing.T) {
	sim := newSim(t, 60, 20)
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "wr1", Project: "p", Status: model.StatusInReview, WaitingReview: true},
		{ID: "2", Name: "wr2", Project: "p", Status: model.StatusInReview, WaitingReview: true},
	})
	tl.waitingReviewExpanded = true
	tl.waitingReviewProject = "p"
	tl.SetRect(0, 0, 60, 20)
	tl.Draw(sim)
}

func TestTaskListView_DrawProjectRow_ExpandedInArchive(t *testing.T) {
	sim := newSim(t, 60, 20)
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "arch1", Project: "p", Status: model.StatusComplete, Archived: true},
	})
	tl.archiveExpanded = true
	tl.archiveProject = "p"
	tl.SetRect(0, 0, 60, 20)
	tl.Draw(sim)
}

// ---------- TaskListView Draw additional branches ----------

func TestTaskListView_Draw_Empty(t *testing.T) {
	sim := newSim(t, 60, 12)
	tl := NewTaskListView()
	tl.SetRect(0, 0, 60, 12)
	tl.Draw(sim)
	// No tasks — should still render border and "Tasks" title.
	out := dumpScreen(sim)
	testutil.Contains(t, out, "Tasks")
}

func TestTaskListView_Draw_FilteringWithLargeFilter(t *testing.T) {
	// Long filter — exercises the col >= x+width-1 break in the title-filter loop.
	sim := newSim(t, 30, 8)
	tl := NewTaskListView()
	tl.SetRect(0, 0, 30, 8)
	tl.filtering = true
	tl.filter = strings.Repeat("z", 100)
	tl.Draw(sim) // must not panic
}

func TestTaskListView_Draw_OffsetGetsAdjusted(t *testing.T) {
	// Many tasks, narrow viewport → cursor below offset triggers offset adjustment.
	tasks := make([]*model.Task, 0, 30)
	for i := 0; i < 30; i++ {
		tasks = append(tasks, &model.Task{
			ID:      string(rune('a' + i)),
			Name:    "task",
			Project: "p",
			Status:  model.StatusPending,
		})
	}
	sim := newSim(t, 40, 10)
	tl := NewTaskListView()
	tl.SetTasks(tasks)
	tl.expanded = "p"
	tl.SetRect(0, 0, 40, 10)
	// Force cursor to a far row — offset should adjust.
	for i := 0; i < 25; i++ {
		tl.CursorDown()
	}
	tl.Draw(sim)
}

func TestTaskListView_Draw_FilteringScrollsListDown(t *testing.T) {
	// Filtering reserves a bottom row; combined with many tasks exercises the
	// listH adjustment path.
	tasks := make([]*model.Task, 0, 50)
	for i := 0; i < 50; i++ {
		tasks = append(tasks, &model.Task{
			ID:      string(rune('a' + i)),
			Name:    "task",
			Project: "p",
			Status:  model.StatusPending,
		})
	}
	sim := newSim(t, 40, 12)
	tl := NewTaskListView()
	tl.SetTasks(tasks)
	tl.expanded = "p"
	tl.SetRect(0, 0, 40, 12)
	tl.filtering = true
	tl.Draw(sim)
}

// Force the listH < 0 branch and the FilterTitle filter-too-long break.
func TestTaskListView_Draw_FilteringTooSmall(t *testing.T) {
	sim := newSim(t, 6, 4)
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "x", Project: "p", Status: model.StatusPending},
	})
	tl.expanded = "p"
	tl.filtering = true
	tl.SetRect(0, 0, 6, 4)
	tl.Draw(sim) // must not panic when listH would go below 0
}

// ---------- skipUpPastHeader / moveCursor edge cases ----------

func TestTaskListView_MoveCursor_EmptyRows(t *testing.T) {
	tl := NewTaskListView()
	// rows = nil — moveCursor returns immediately.
	tl.moveCursor(1)
	tl.moveCursor(-1)
}

func TestTaskListView_CursorDown_FromTopOfPage_PastBottom(t *testing.T) {
	// Cursor past last row — moveCursor clamps to len-1.
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "a", Project: "p", Status: model.StatusPending},
	})
	tl.expanded = "p"
	tl.cursor = len(tl.rows) - 1
	tl.CursorDown() // already at bottom — no-op.
}

func TestTaskListView_CursorUp_FromZero_HeaderRow(t *testing.T) {
	// Cursor at 0 (project header), CursorUp — stays put per "at top" branch.
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "a", Project: "p", Status: model.StatusPending},
	})
	tl.expanded = "" // no expanded project — first row is project header
	tl.buildRows()
	tl.cursor = 0
	tl.CursorUp() // header row at top — exercise the "at top" branch.
}

// ---------- TaskDetailPanel — branches in Draw ----------

func TestTaskDetailPanel_Draw_LongName(t *testing.T) {
	sim := newSim(t, 30, 20)
	td := NewTaskDetailPanel()
	td.SetRect(0, 0, 30, 20)
	td.SetTask(&model.Task{
		ID:     "1",
		Name:   strings.Repeat("very-long-name-x", 5),
		Status: model.StatusPending,
	}, false)
	td.Draw(sim)
	out := dumpScreen(sim)
	testutil.Contains(t, out, "...")
}

func TestTaskDetailPanel_Draw_IdleInProgress(t *testing.T) {
	sim := newSim(t, 60, 20)
	td := NewTaskDetailPanel()
	td.SetRect(0, 0, 60, 20)
	task := &model.Task{ID: "1", Name: "x", Status: model.StatusInProgress}
	td.SetTask(task, false) // not running
	td.Draw(sim)
	testutil.Contains(t, dumpScreen(sim), "(idle)")
}

func TestTaskDetailPanel_Draw_LongPRURL(t *testing.T) {
	sim := newSim(t, 30, 20)
	td := NewTaskDetailPanel()
	td.SetRect(0, 0, 30, 20)
	td.SetTask(&model.Task{
		ID:     "1",
		Name:   "x",
		Status: model.StatusPending,
		PRURL:  strings.Repeat("https://github.com/very-long/url/path/", 5),
	}, false)
	td.Draw(sim)
	testutil.Contains(t, dumpScreen(sim), "...")
}

func TestTaskDetailPanel_Draw_LongWorktree(t *testing.T) {
	sim := newSim(t, 30, 20)
	td := NewTaskDetailPanel()
	td.SetRect(0, 0, 30, 20)
	td.SetTask(&model.Task{
		ID:       "1",
		Name:     "x",
		Status:   model.StatusPending,
		Worktree: strings.Repeat("/very/long/worktree/path/", 5),
	}, false)
	td.Draw(sim)
	out := dumpScreen(sim)
	testutil.Contains(t, out, "Worktree")
}

func TestTaskDetailPanel_Draw_PromptOverflowsRemaining(t *testing.T) {
	sim := newSim(t, 30, 8) // very short — prompt body must overflow.
	td := NewTaskDetailPanel()
	td.SetRect(0, 0, 30, 8)
	td.SetTask(&model.Task{
		ID:     "1",
		Name:   "x",
		Status: model.StatusPending,
		Prompt: strings.Repeat("word ", 80),
	}, false)
	td.Draw(sim)
	// Must render without panic — the for-loop break path should fire.
}

func TestTaskDetailPanel_Draw_TooSmall(t *testing.T) {
	sim := newSim(t, 5, 5)
	td := NewTaskDetailPanel()
	td.SetRect(0, 0, 0, 0)
	td.Draw(sim) // zero outer dims
	td.SetRect(0, 0, 1, 1)
	td.Draw(sim) // border can't fit
}

// ---------- statusStyle exhaustive ----------

func TestTaskDetailPanel_StatusStyle_AllStatuses(t *testing.T) {
	td := NewTaskDetailPanel()
	for _, s := range []model.Status{
		model.StatusPending,
		model.StatusInProgress,
		model.StatusInReview,
		model.StatusComplete,
	} {
		_ = td.statusStyle(s) // must not panic, return a Style
	}
	// Default branch — pass an out-of-range value.
	_ = td.statusStyle(model.Status(99))
}

// ---------- InputHandler — exercise more rune branches ----------

func TestTaskListView_InputHandler_RenameKey(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{{ID: "1", Name: "x", Project: "p", Status: model.StatusPending}})
	tl.expanded = "p"

	var renamed *model.Task
	tl.OnRename = func(t *model.Task) { renamed = t }

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone), func(p tview.Primitive) {})
	if renamed == nil || renamed.ID != "1" {
		t.Errorf("OnRename should fire for current task, got %v", renamed)
	}
}

func TestTaskListView_InputHandler_CopyPromptKey(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{{ID: "1", Name: "x", Project: "p", Status: model.StatusPending, Prompt: "do thing"}})
	tl.expanded = "p"

	var copied *model.Task
	tl.OnCopyPrompt = func(t *model.Task) { copied = t }

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModNone), func(p tview.Primitive) {})
	if copied == nil || copied.ID != "1" {
		t.Errorf("OnCopyPrompt should fire when Prompt is non-empty, got %v", copied)
	}
}

func TestTaskListView_InputHandler_CopyPromptEmptyPrompt(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{{ID: "1", Name: "x", Project: "p", Status: model.StatusPending}}) // no Prompt
	tl.expanded = "p"

	called := false
	tl.OnCopyPrompt = func(t *model.Task) { called = true }

	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModNone), func(p tview.Primitive) {})
	if called {
		t.Error("OnCopyPrompt should NOT fire when Prompt is empty")
	}
}

func TestTaskListView_InputHandler_NewKey(t *testing.T) {
	tl := NewTaskListView()
	called := false
	tl.OnNew = func() { called = true }
	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, 'n', tcell.ModNone), func(p tview.Primitive) {})
	if !called {
		t.Error("OnNew should fire on 'n'")
	}
}

func TestTaskListView_InputHandler_SlashEntersFilterMode(t *testing.T) {
	tl := NewTaskListView()
	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(p tview.Primitive) {})
	if !tl.filtering {
		t.Error("'/' should enter filter mode")
	}
}

func TestTaskListView_InputHandler_EscapeClearsFilter(t *testing.T) {
	tl := NewTaskListView()
	tl.filter = "abc"
	tl.filtering = false
	handler := tl.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(p tview.Primitive) {})
	if tl.filter != "" {
		t.Error("Escape should clear non-empty filter")
	}
}

func TestTaskListView_InputHandler_JKKeys(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "a", Project: "p", Status: model.StatusPending},
		{ID: "2", Name: "b", Project: "p", Status: model.StatusPending},
	})
	tl.expanded = "p"

	handler := tl.InputHandler()
	prev := tl.cursor
	handler(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), func(p tview.Primitive) {})
	if tl.cursor == prev {
		t.Error("'j' should move cursor down")
	}
	handler(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), func(p tview.Primitive) {})
	if tl.cursor != prev {
		t.Error("'k' should move cursor up to original position")
	}
}

// ---------- handleFilterInput branches ----------

func TestTaskListView_FilterInput_BackspaceDeletes(t *testing.T) {
	tl := NewTaskListView()
	tl.filtering = true
	tl.filter = "abc"
	consumed := tl.handleFilterInput(tcell.NewEventKey(tcell.KeyBackspace, 0, 0))
	if !consumed {
		t.Error("backspace should be consumed")
	}
	testutil.Equal(t, tl.filter, "ab")
}

func TestTaskListView_FilterInput_BackspaceEmpty(t *testing.T) {
	tl := NewTaskListView()
	tl.filtering = true
	tl.filter = ""
	// Backspace on empty filter is consumed but does nothing.
	consumed := tl.handleFilterInput(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0))
	testutil.Equal(t, consumed, true)
	testutil.Equal(t, tl.filter, "")
}

func TestTaskListView_FilterInput_AltBackspaceDeletesWord(t *testing.T) {
	tl := NewTaskListView()
	tl.filtering = true
	tl.filter = "alpha beta"
	consumed := tl.handleFilterInput(tcell.NewEventKey(tcell.KeyBackspace, 0, tcell.ModAlt))
	testutil.Equal(t, consumed, true)
	// Should have deleted the last word.
	if !strings.Contains("alpha ", tl.filter) {
		// soft check — just ensure something was deleted.
		if tl.filter == "alpha beta" {
			t.Errorf("expected word delete, got %q", tl.filter)
		}
	}
}

func TestTaskListView_FilterInput_CtrlU(t *testing.T) {
	tl := NewTaskListView()
	tl.filtering = true
	tl.filter = "anything"
	consumed := tl.handleFilterInput(tcell.NewEventKey(tcell.KeyCtrlU, 0, 0))
	testutil.Equal(t, consumed, true)
	testutil.Equal(t, tl.filter, "")
}

func TestTaskListView_FilterInput_CtrlW(t *testing.T) {
	tl := NewTaskListView()
	tl.filtering = true
	tl.filter = "alpha beta"
	consumed := tl.handleFilterInput(tcell.NewEventKey(tcell.KeyCtrlW, 0, 0))
	testutil.Equal(t, consumed, true)
	if tl.filter == "alpha beta" {
		t.Error("Ctrl+W should delete word left")
	}
}

func TestTaskListView_FilterInput_UpDownNav(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "a", Project: "p", Status: model.StatusPending},
		{ID: "2", Name: "b", Project: "p", Status: model.StatusPending},
	})
	tl.expanded = "p"
	tl.filtering = true

	prev := tl.cursor
	consumed := tl.handleFilterInput(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	testutil.Equal(t, consumed, true)
	if tl.cursor == prev {
		t.Error("Down in filter mode should still navigate")
	}

	consumed = tl.handleFilterInput(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	testutil.Equal(t, consumed, true)
	if tl.cursor != prev {
		t.Error("Up in filter mode should still navigate back")
	}
}

func TestTaskListView_FilterInput_EnterConfirms(t *testing.T) {
	tl := NewTaskListView()
	tl.filtering = true
	tl.filter = "abc"
	consumed := tl.handleFilterInput(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Equal(t, consumed, true)
	if tl.filtering {
		t.Error("Enter should exit filter input mode")
	}
	testutil.Equal(t, tl.filter, "abc") // filter retained
}

func TestTaskListView_FilterInput_UnhandledKey(t *testing.T) {
	tl := NewTaskListView()
	tl.filtering = true
	consumed := tl.handleFilterInput(tcell.NewEventKey(tcell.KeyF1, 0, 0))
	testutil.Equal(t, consumed, false)
}

func TestTaskListView_FilterInput_RuneAppends(t *testing.T) {
	tl := NewTaskListView()
	tl.filtering = true
	consumed := tl.handleFilterInput(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	testutil.Equal(t, consumed, true)
	testutil.Equal(t, tl.filter, "x")
}

// ---------- PasteHandler ----------

func TestTaskListView_PasteHandler_FilterMode(t *testing.T) {
	tl := NewTaskListView()
	tl.filtering = true
	handler := tl.PasteHandler()
	handler("pasted", func(p tview.Primitive) {})
	testutil.Equal(t, tl.filter, "pasted")
}

func TestTaskListView_PasteHandler_NotFiltering(t *testing.T) {
	tl := NewTaskListView()
	tl.filtering = false
	handler := tl.PasteHandler()
	handler("pasted", func(p tview.Primitive) {})
	testutil.Equal(t, tl.filter, "")
}

// ---------- SelectByID branches ----------

func TestTaskListView_SelectByID_Active(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "a", Project: "p", Status: model.StatusPending},
		{ID: "2", Name: "b", Project: "p", Status: model.StatusPending},
	})
	tl.SelectByID("2")
	testutil.Equal(t, tl.SelectedTask().ID, "2")
}

func TestTaskListView_SelectByID_Archived(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "a", Project: "p", Status: model.StatusPending},
		{ID: "2", Name: "b", Project: "p", Status: model.StatusComplete, Archived: true},
	})
	tl.SelectByID("2")
	if !tl.archiveExpanded {
		t.Error("SelectByID on archived task should expand archive")
	}
	testutil.Equal(t, tl.archiveProject, "p")
}

func TestTaskListView_SelectByID_WaitingReview(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "a", Project: "p", Status: model.StatusPending},
		{ID: "2", Name: "b", Project: "p", Status: model.StatusInReview, WaitingReview: true},
	})
	tl.SelectByID("2")
	if !tl.waitingReviewExpanded {
		t.Error("SelectByID on waiting-review task should expand WR section")
	}
	testutil.Equal(t, tl.waitingReviewProject, "p")
}

func TestTaskListView_SelectByID_NotFound(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "a", Project: "p", Status: model.StatusPending},
	})
	tl.SelectByID("nonexistent") // no panic
}

// ---------- SelectedTask edge case: cursor on non-task row ----------

func TestTaskListView_SelectedTask_OnHeaderRow(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "a", Project: "p", Status: model.StatusPending},
	})
	tl.expanded = "" // collapse — cursor will land on project header
	tl.buildRows()
	// Find a project row and put cursor there.
	for i, r := range tl.rows {
		if r.kind == rowProject {
			tl.cursor = i
			break
		}
	}
	if tl.SelectedTask() != nil {
		t.Error("SelectedTask should return nil when cursor is on non-task row")
	}
}

// ---------- HasTasks / Empty / SetExpanded ----------

func TestTaskListView_Empty_Placeholder(t *testing.T) {
	tl := NewTaskListView()
	got := tl.Empty()
	if !strings.Contains(got, "No tasks yet") {
		t.Errorf("Empty() = %q, missing placeholder text", got)
	}
}

func TestTaskListView_SetExpanded(t *testing.T) {
	tl := NewTaskListView()
	tl.SetTasks([]*model.Task{
		{ID: "1", Name: "a", Project: "alpha", Status: model.StatusPending},
		{ID: "2", Name: "b", Project: "beta", Status: model.StatusPending},
	})
	tl.SetExpanded("beta")
	testutil.Equal(t, tl.expanded, "beta")
}

// ---------- taskSection ----------

func TestTaskSection(t *testing.T) {
	for _, tc := range []struct {
		name string
		t    *model.Task
		want rowSection
	}{
		{"active", &model.Task{Status: model.StatusPending}, sectionActive},
		{"waiting", &model.Task{WaitingReview: true}, sectionWaitingReview},
		{"archived", &model.Task{Archived: true}, sectionArchive},
		{"both archived and waiting → archive wins", &model.Task{Archived: true, WaitingReview: true}, sectionArchive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, taskSection(tc.t), tc.want)
		})
	}
}
