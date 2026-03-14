package ui

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
)

func TestNewProjectList(t *testing.T) {
	pl := NewProjectList(DefaultTheme())
	if pl.cursor != 0 {
		t.Errorf("initial cursor = %d, want 0", pl.cursor)
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

	if pl.cursor != 0 {
		t.Fatalf("initial cursor = %d", pl.cursor)
	}

	pl.CursorDown()
	if pl.cursor != 1 {
		t.Errorf("cursor after down = %d, want 1", pl.cursor)
	}

	pl.CursorDown()
	if pl.cursor != 2 {
		t.Errorf("cursor after down x2 = %d, want 2", pl.cursor)
	}

	// Past end
	pl.CursorDown()
	if pl.cursor != 2 {
		t.Errorf("cursor after down at end = %d, want 2", pl.cursor)
	}

	pl.CursorUp()
	if pl.cursor != 1 {
		t.Errorf("cursor after up = %d, want 1", pl.cursor)
	}

	pl.CursorUp()
	if pl.cursor != 0 {
		t.Errorf("cursor after up x2 = %d, want 0", pl.cursor)
	}

	// Before start
	pl.CursorUp()
	if pl.cursor != 0 {
		t.Errorf("cursor after up at start = %d, want 0", pl.cursor)
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
	if !strings.Contains(view, "claude") {
		t.Error("view should contain backend name")
	}
	if !strings.Contains(view, "/home/user/myproject") {
		t.Error("view should contain project path")
	}
	if !strings.Contains(view, "main") {
		t.Error("view should contain branch name")
	}
}

func TestProjectList_ViewDefaultBackend(t *testing.T) {
	pl := NewProjectList(DefaultTheme())
	pl.SetSize(80, 40)
	pl.SetProjects(map[string]config.Project{
		"proj": {Path: "/tmp/proj"},
	})

	view := pl.View()
	if !strings.Contains(view, "default") {
		t.Error("view should show 'default' when backend is empty")
	}
}

func TestProjectList_SetProjectsClampsClursor(t *testing.T) {
	pl := NewProjectList(DefaultTheme())
	pl.SetProjects(map[string]config.Project{
		"a": {}, "b": {}, "c": {},
	})
	pl.cursor = 2

	// Set fewer projects
	pl.SetProjects(map[string]config.Project{
		"a": {},
	})
	if pl.cursor != 0 {
		t.Errorf("cursor should be clamped to 0, got %d", pl.cursor)
	}
}
