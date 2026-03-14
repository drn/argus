package ui

import (
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
	tasks := testTasks()
	tl.SetTasks(tasks)

	got := tl.Selected()
	if got == nil || got.Name != "Fix login bug" {
		t.Error("expected first task selected")
	}
}

func TestTaskList_CursorNavigation(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 20)
	tl.SetTasks(testTasks())

	tl.CursorDown()
	if got := tl.Selected(); got.Name != "Add API endpoint" {
		t.Errorf("after down: got %q", got.Name)
	}

	tl.CursorDown()
	if got := tl.Selected(); got.Name != "Update docs" {
		t.Errorf("after 2nd down: got %q", got.Name)
	}

	// Should not go past end
	tl.CursorDown()
	if got := tl.Selected(); got.Name != "Update docs" {
		t.Error("should stay at end")
	}

	tl.CursorUp()
	if got := tl.Selected(); got.Name != "Add API endpoint" {
		t.Errorf("after up: got %q", got.Name)
	}

	// Should not go past beginning
	tl.CursorUp()
	tl.CursorUp()
	if got := tl.Selected(); got.Name != "Fix login bug" {
		t.Error("should stay at beginning")
	}
}

func TestTaskList_Filter(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetTasks(testTasks())

	tl.SetFilter("webapp")
	if got := tl.Selected(); got == nil || got.Name != "Fix login bug" {
		t.Error("filter should show webapp tasks")
	}

	// Should have 2 results (both webapp tasks)
	tl.CursorDown()
	if got := tl.Selected(); got == nil || got.Name != "Update docs" {
		t.Error("should have 2nd webapp task")
	}

	// Past end
	tl.CursorDown()
	if got := tl.Selected(); got.Name != "Update docs" {
		t.Error("should not go past filtered end")
	}
}

func TestTaskList_FilterCaseInsensitive(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
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
	tl.SetTasks(testTasks())

	tl.SetFilter("webapp")
	tl.SetFilter("")

	// Should show all tasks again
	count := 0
	for {
		if tl.Selected() == nil {
			break
		}
		count++
		tl.CursorDown()
		if tl.Selected() == nil || tl.scroll.Cursor() >= len(tl.filtered)-1 {
			count++
			break
		}
	}
	if count != 3 {
		t.Errorf("expected 3 tasks after clearing filter, navigated %d", count)
	}
}

func TestTaskList_SetTasks_ClampsCursor(t *testing.T) {
	tl := NewTaskList(DefaultTheme())
	tl.SetSize(80, 20)
	tl.SetTasks(testTasks())

	tl.CursorDown()
	tl.CursorDown() // cursor at 2

	// Shrink to 1 task
	tl.SetTasks([]*model.Task{{Name: "only one"}})
	if tl.scroll.Cursor() != 0 {
		t.Errorf("cursor should be clamped to 0, got %d", tl.scroll.Cursor())
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
