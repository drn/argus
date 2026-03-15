package ui

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

func TestSettingsView_Sections(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetWarnings(nil)
	sv.SetProjects(map[string]config.Project{
		"argus": {Path: "/tmp/argus", Branch: "master"},
	})
	sv.SetBackends(map[string]config.Backend{
		"claude": {Command: "claude --dangerously-skip-permissions"},
	})

	view := sv.View()
	if !strings.Contains(view, "STATUS") {
		t.Error("expected STATUS section header")
	}
	if !strings.Contains(view, "PROJECTS") {
		t.Error("expected PROJECTS section header")
	}
	if !strings.Contains(view, "BACKENDS") {
		t.Error("expected BACKENDS section header")
	}
	if !strings.Contains(view, "argus") {
		t.Error("expected project name 'argus'")
	}
	if !strings.Contains(view, "claude") {
		t.Error("expected backend name 'claude'")
	}
}

func TestSettingsView_WarningDisplay(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetWarnings([]string{"In-process mode: sessions won't persist"})
	sv.SetProjects(nil)
	sv.SetBackends(nil)

	view := sv.View()
	if !strings.Contains(view, "In-process mode") {
		t.Error("expected warning text in view")
	}
}

func TestSettingsView_NoWarnings(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetWarnings(nil)
	sv.SetProjects(nil)
	sv.SetBackends(nil)

	view := sv.View()
	if !strings.Contains(view, "All systems nominal") {
		t.Error("expected 'All systems nominal' when no warnings")
	}
}

func TestSettingsView_CursorNavigation(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetWarnings(nil)
	sv.SetProjects(map[string]config.Project{
		"proj1": {Path: "/tmp/proj1"},
		"proj2": {Path: "/tmp/proj2"},
	})
	sv.SetBackends(map[string]config.Backend{
		"claude": {Command: "claude"},
	})

	// Initial cursor should be on the first selectable row (status "all good")
	sel := sv.Selected()
	if sel == nil {
		t.Fatal("expected a selected row")
	}
	if sel.kind != settingsRowWarning {
		t.Errorf("expected initial selection to be warning row, got kind %d", sel.kind)
	}

	// Move down — should land on a project row (skipping section header)
	sv.CursorDown()
	sel = sv.Selected()
	if sel == nil || sel.kind != settingsRowProject {
		t.Error("expected cursor to be on a project row after down")
	}

	// Move up — should go back to status row
	sv.CursorUp()
	sel = sv.Selected()
	if sel == nil || sel.kind != settingsRowWarning {
		t.Error("expected cursor to be on warning row after up")
	}
}

func TestSettingsView_SelectedProject(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetWarnings(nil)
	sv.SetProjects(map[string]config.Project{
		"myproj": {Path: "/tmp/myproj", Branch: "main"},
	})
	sv.SetBackends(nil)

	// Move to project row
	sv.CursorDown()
	proj := sv.SelectedProject()
	if proj == nil {
		t.Fatal("expected a selected project")
	}
	if proj.Name != "myproj" {
		t.Errorf("expected project 'myproj', got '%s'", proj.Name)
	}
}

func TestSettingsView_SelectedBackend(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetWarnings(nil)
	sv.SetProjects(nil)
	sv.SetBackends(map[string]config.Backend{
		"claude": {Command: "claude --skip"},
	})

	// Move past status and empty projects to backend row
	sv.CursorDown() // (no projects) placeholder
	sv.CursorDown() // claude backend
	be := sv.SelectedBackend()
	if be == nil {
		t.Fatal("expected a selected backend")
	}
	if be.Name != "claude" {
		t.Errorf("expected backend 'claude', got '%s'", be.Name)
	}
}

func TestSettingsView_RenderDetail_Warning(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetWarnings([]string{"In-process mode: sessions won't persist"})
	sv.SetProjects(nil)
	sv.SetBackends(nil)

	detail := sv.RenderDetail(60, 20)
	if !strings.Contains(detail, "Warning") {
		t.Error("expected warning detail panel")
	}
}

func TestSettingsView_RenderDetail_Project(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetWarnings(nil)
	sv.SetProjects(map[string]config.Project{
		"argus": {Path: "/dev/argus", Branch: "master", Backend: "claude"},
	})
	sv.SetBackends(nil)

	sv.CursorDown() // move to project
	detail := sv.RenderDetail(60, 20)
	if !strings.Contains(detail, "argus") {
		t.Error("expected project name in detail")
	}
	if !strings.Contains(detail, "/dev/argus") {
		t.Error("expected project path in detail")
	}
}

func TestSettingsView_RenderDetail_Backend(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetWarnings(nil)
	sv.SetProjects(nil)
	sv.SetBackends(map[string]config.Backend{
		"claude": {Command: "claude --skip"},
	})

	sv.CursorDown() // (no projects)
	sv.CursorDown() // claude backend
	detail := sv.RenderDetail(60, 20)
	if !strings.Contains(detail, "claude") {
		t.Error("expected backend name in detail")
	}
	if !strings.Contains(detail, "claude --skip") {
		t.Error("expected backend command in detail")
	}
}

func TestSettingsView_TaskCounts(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetProjects(map[string]config.Project{
		"proj": {Path: "/tmp"},
	})
	sv.SetTasks([]*model.Task{
		{Project: "proj", Status: model.StatusPending},
		{Project: "proj", Status: model.StatusInProgress},
		{Project: "proj", Status: model.StatusComplete},
	})

	sc := sv.TaskCounts("proj")
	if sc.Pending != 1 || sc.InProgress != 1 || sc.Complete != 1 {
		t.Errorf("unexpected task counts: %+v", sc)
	}
	if sc.Total() != 3 {
		t.Errorf("expected total 3, got %d", sc.Total())
	}
}

func TestSettingsView_EmptyView(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	// Don't set anything — rows are empty
	view := sv.View()
	if !strings.Contains(view, "No settings") {
		t.Error("expected empty state message")
	}
}

func TestSettingsView_ZeroDimensions(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(0, 0)
	sv.SetWarnings(nil)
	sv.SetProjects(nil)
	sv.SetBackends(nil)
	// Should not panic
	_ = sv.View()
	_ = sv.RenderDetail(0, 0)
}

func TestModel_DaemonConnectedWarning(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	runner := agent.NewRunner(nil)

	// Not connected to daemon — should show warning
	m := NewModel(database, runner, false)
	m.activeTab = tabSettings
	m.width = 120
	m.height = 40
	view := m.View()
	if !strings.Contains(view, "In-process mode") {
		t.Error("expected in-process mode warning when daemon not connected")
	}

	// Connected to daemon — no warning
	m2 := NewModel(database, runner, true)
	m2.activeTab = tabSettings
	m2.width = 120
	m2.height = 40
	view2 := m2.View()
	if strings.Contains(view2, "In-process mode") {
		t.Error("should not show in-process warning when daemon connected")
	}
	if !strings.Contains(view2, "All systems nominal") {
		t.Error("expected 'All systems nominal' when daemon connected")
	}
}
