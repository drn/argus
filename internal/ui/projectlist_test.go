package ui

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

func TestNewProjectList(t *testing.T) {
	pl := NewProjectList(DefaultTheme())
	if pl.scroll.Cursor() != 0 {
		t.Errorf("initial cursor = %d, want 0", pl.scroll.Cursor())
	}
	if pl.Selected() != nil {
		t.Error("selected should be nil with no projects")
	}
}

func TestProjectList_SetProjects(t *testing.T) {
	pl := NewProjectList(DefaultTheme())
	projects := map[string]config.Project{
		"charlie": {Path: "/c"},
		"alpha":   {Path: "/a"},
		"bravo":   {Path: "/b"},
	}
	pl.SetProjects(projects)

	if len(pl.projects) != 3 {
		t.Fatalf("expected 3 projects, got %d", len(pl.projects))
	}
}

func TestProjectList_SortProjects(t *testing.T) {
	entries := []projectEntry{
		{Name: "charlie"},
		{Name: "alpha"},
		{Name: "bravo"},
	}
	sortProjects(entries)
	expected := []string{"alpha", "bravo", "charlie"}
	for i, name := range expected {
		if entries[i].Name != name {
			t.Errorf("entries[%d].Name = %q, want %q", i, entries[i].Name, name)
		}
	}
}

func TestProjectList_SortProjects_AlreadySorted(t *testing.T) {
	entries := []projectEntry{
		{Name: "a"},
		{Name: "b"},
		{Name: "c"},
	}
	sortProjects(entries)
	for i, e := range entries {
		expected := string(rune('a' + i))
		if e.Name != expected {
			t.Errorf("entries[%d].Name = %q, want %q", i, e.Name, expected)
		}
	}
}

func TestProjectList_SortProjects_Empty(t *testing.T) {
	// Should not panic
	sortProjects(nil)
	sortProjects([]projectEntry{})
}

func TestProjectList_CursorUpDown(t *testing.T) {
	pl := NewProjectList(DefaultTheme())
	pl.SetSize(80, 40)
	pl.SetProjects(map[string]config.Project{
		"a": {Path: "/a"},
		"b": {Path: "/b"},
		"c": {Path: "/c"},
	})

	if pl.scroll.Cursor() != 0 {
		t.Fatalf("initial cursor = %d", pl.scroll.Cursor())
	}

	pl.CursorDown()
	if pl.scroll.Cursor() != 1 {
		t.Errorf("cursor after down = %d, want 1", pl.scroll.Cursor())
	}

	pl.CursorDown()
	if pl.scroll.Cursor() != 2 {
		t.Errorf("cursor after down x2 = %d, want 2", pl.scroll.Cursor())
	}

	// Past end
	pl.CursorDown()
	if pl.scroll.Cursor() != 2 {
		t.Errorf("cursor after down at end = %d, want 2", pl.scroll.Cursor())
	}

	pl.CursorUp()
	if pl.scroll.Cursor() != 1 {
		t.Errorf("cursor after up = %d, want 1", pl.scroll.Cursor())
	}

	pl.CursorUp()
	if pl.scroll.Cursor() != 0 {
		t.Errorf("cursor after up x2 = %d, want 0", pl.scroll.Cursor())
	}

	// Before start
	pl.CursorUp()
	if pl.scroll.Cursor() != 0 {
		t.Errorf("cursor after up at start = %d, want 0", pl.scroll.Cursor())
	}
}

func TestProjectList_Selected(t *testing.T) {
	pl := NewProjectList(DefaultTheme())
	pl.SetProjects(map[string]config.Project{
		"alpha": {Path: "/a"},
		"bravo": {Path: "/b"},
	})

	sel := pl.Selected()
	if sel == nil {
		t.Fatal("selected should not be nil")
	}
	if sel.Name != "alpha" {
		t.Errorf("selected = %q, want 'alpha' (first sorted)", sel.Name)
	}

	pl.CursorDown()
	sel = pl.Selected()
	if sel.Name != "bravo" {
		t.Errorf("selected after down = %q, want 'bravo'", sel.Name)
	}
}

func TestProjectList_Selected_Empty(t *testing.T) {
	pl := NewProjectList(DefaultTheme())
	if pl.Selected() != nil {
		t.Error("selected should be nil with no projects")
	}
}

func TestProjectList_ViewEmpty(t *testing.T) {
	pl := NewProjectList(DefaultTheme())
	view := pl.View()
	if !strings.Contains(view, "No projects configured") {
		t.Error("empty view should show 'No projects configured'")
	}
}

func TestProjectList_ViewWithEntries(t *testing.T) {
	pl := NewProjectList(DefaultTheme())
	pl.SetSize(80, 40)
	pl.SetProjects(map[string]config.Project{
		"myproject": {Path: "/home/user/myproject", Branch: "main", Backend: "claude"},
	})

	view := pl.View()
	if !strings.Contains(view, "myproject") {
		t.Error("view should contain project name")
	}
	if !strings.Contains(view, "/home/user/myproject") {
		t.Error("view should contain project path")
	}
	if !strings.Contains(view, "main") {
		t.Error("view should contain branch name")
	}
	// Should show "no tasks" since no tasks were set
	if !strings.Contains(view, "no tasks") {
		t.Error("view should show 'no tasks' when project has no tasks")
	}
}

func TestProjectList_ViewWithTaskCounts(t *testing.T) {
	pl := NewProjectList(DefaultTheme())
	pl.SetSize(80, 40)
	pl.SetProjects(map[string]config.Project{
		"myproject": {Path: "/tmp/proj"},
	})
	pl.SetTasks([]*model.Task{
		{Project: "myproject", Status: model.StatusPending},
		{Project: "myproject", Status: model.StatusInProgress},
		{Project: "myproject", Status: model.StatusComplete},
	})

	view := pl.View()
	if !strings.Contains(view, "3 tasks") {
		t.Error("view should show task count badge")
	}
}

func TestProjectList_ViewNoTasksLabel(t *testing.T) {
	pl := NewProjectList(DefaultTheme())
	pl.SetSize(80, 40)
	pl.SetProjects(map[string]config.Project{
		"proj": {Path: "/tmp/proj"},
	})

	view := pl.View()
	if !strings.Contains(view, "no tasks") {
		t.Error("view should show 'no tasks' when project has no tasks")
	}
}

func TestProjectList_SetProjectsClampsClursor(t *testing.T) {
	pl := NewProjectList(DefaultTheme())
	pl.SetProjects(map[string]config.Project{
		"a": {}, "b": {}, "c": {},
	})
	pl.scroll.cursor = 2

	// Set fewer projects
	pl.SetProjects(map[string]config.Project{
		"a": {},
	})
	if pl.scroll.Cursor() != 0 {
		t.Errorf("cursor should be clamped to 0, got %d", pl.scroll.Cursor())
	}
}

func TestProjectList_SetTasks(t *testing.T) {
	pl := NewProjectList(DefaultTheme())
	pl.SetProjects(map[string]config.Project{
		"alpha": {Path: "/a"},
		"bravo": {Path: "/b"},
	})

	tasks := []*model.Task{
		{Project: "alpha", Status: model.StatusPending},
		{Project: "alpha", Status: model.StatusInProgress},
		{Project: "alpha", Status: model.StatusComplete},
		{Project: "bravo", Status: model.StatusInReview},
	}
	pl.SetTasks(tasks)

	ac := pl.TaskCounts("alpha")
	if ac.Pending != 1 || ac.InProgress != 1 || ac.Complete != 1 || ac.Total() != 3 {
		t.Errorf("alpha counts = %+v, want 1/1/0/1", ac)
	}

	bc := pl.TaskCounts("bravo")
	if bc.InReview != 1 || bc.Total() != 1 {
		t.Errorf("bravo counts = %+v, want 0/0/1/0", bc)
	}

	// Unknown project should return zero counts
	uc := pl.TaskCounts("unknown")
	if uc.Total() != 0 {
		t.Errorf("unknown project should have 0 tasks, got %d", uc.Total())
	}
}

func TestProjectList_MiniStatus(t *testing.T) {
	pl := NewProjectList(DefaultTheme())
	pl.SetSize(80, 40)
	pl.SetProjects(map[string]config.Project{
		"proj": {Path: "/tmp"},
	})
	pl.SetTasks([]*model.Task{
		{Project: "proj", Status: model.StatusPending},
		{Project: "proj", Status: model.StatusPending},
		{Project: "proj", Status: model.StatusInProgress},
	})

	view := pl.View()
	// Should contain status indicators (○ for pending, ● for in-progress)
	if !strings.Contains(view, "○") {
		t.Error("view should contain pending status indicator")
	}
	if !strings.Contains(view, "●") {
		t.Error("view should contain in-progress status indicator")
	}
}

func TestStatusCounts_Total(t *testing.T) {
	sc := statusCounts{Pending: 2, InProgress: 1, InReview: 3, Complete: 4}
	if sc.Total() != 10 {
		t.Errorf("Total() = %d, want 10", sc.Total())
	}
}

func TestStatusCounts_Total_Zero(t *testing.T) {
	sc := statusCounts{}
	if sc.Total() != 0 {
		t.Errorf("Total() = %d, want 0", sc.Total())
	}
}
