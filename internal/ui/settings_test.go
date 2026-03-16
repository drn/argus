package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
	if !strings.Contains(view, "System status") {
		t.Error("expected 'System status' when no warnings")
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

	// Move down — should land on daemon logs row
	sv.CursorDown()
	sel = sv.Selected()
	if sel == nil || sel.kind != settingsRowDaemonLogs {
		t.Errorf("expected cursor to be on daemon logs row after first down, got kind %d", sel.kind)
	}

	// Move down — should land on UX logs row
	sv.CursorDown()
	sel = sv.Selected()
	if sel == nil || sel.kind != settingsRowUXLogs {
		t.Errorf("expected cursor to be on UX logs row after second down, got kind %d", sel.kind)
	}

	// Move down — should land on sandbox row (skipping section header)
	sv.CursorDown()
	sel = sv.Selected()
	if sel == nil || sel.kind != settingsRowSandbox {
		t.Errorf("expected cursor to be on sandbox row after third down, got kind %d", sel.kind)
	}

	// Move down again — should land on a project row (skipping section header)
	sv.CursorDown()
	sel = sv.Selected()
	if sel == nil || sel.kind != settingsRowProject {
		t.Error("expected cursor to be on a project row after fourth down")
	}

	// Move up four times — should go back to status row
	sv.CursorUp()
	sv.CursorUp()
	sv.CursorUp()
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

	// Move past daemon logs and sandbox to project row
	sv.CursorDown() // daemon logs
	sv.CursorDown() // UX logs
	sv.CursorDown() // sandbox
	sv.CursorDown() // project
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

	// Move past status, daemon logs, sandbox, and empty projects to backend row
	sv.CursorDown() // daemon logs
	sv.CursorDown() // UX logs
	sv.CursorDown() // sandbox
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

	sv.CursorDown() // daemon logs
	sv.CursorDown() // UX logs
	sv.CursorDown() // sandbox
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

	sv.CursorDown() // daemon logs
	sv.CursorDown() // UX logs
	sv.CursorDown() // sandbox
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

func TestSettingsView_SandboxSection(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetWarnings(nil)
	sv.SetProjects(nil)
	sv.SetBackends(nil)

	view := sv.View()
	if !strings.Contains(view, "SANDBOX") {
		t.Error("expected SANDBOX section header")
	}
	if !strings.Contains(view, "Disabled") {
		t.Error("expected 'Disabled' when sandbox not configured")
	}
}

func TestSettingsView_SandboxEnabled(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetSandboxConfig(true, true, nil, nil)

	view := sv.View()
	if !strings.Contains(view, "Enabled") {
		t.Error("expected 'Enabled' in view")
	}
}

func TestSettingsView_SandboxDetail(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetWarnings(nil)
	sv.SetSandboxConfig(true, true, []string{"/secrets"}, []string{"~/.cache"})

	// Navigate to sandbox row
	sv.CursorDown() // daemon logs
	sv.CursorDown() // UX logs
	sv.CursorDown() // sandbox row
	detail := sv.RenderDetail(60, 20)
	if !strings.Contains(detail, "Sandbox") {
		t.Error("expected 'Sandbox' in detail")
	}
	if !strings.Contains(detail, "Enabled") {
		t.Error("expected 'Enabled' in detail")
	}
	if !strings.Contains(detail, "sandbox-exec available") {
		t.Error("expected 'sandbox-exec available' in detail")
	}
}

func TestSettingsView_SandboxDetailUnavailable(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetWarnings(nil)
	sv.SetSandboxConfig(true, false, nil, nil)

	// Navigate to sandbox row
	sv.CursorDown() // daemon logs
	sv.CursorDown() // UX logs
	sv.CursorDown()
	detail := sv.RenderDetail(60, 20)
	if !strings.Contains(detail, "sandbox-exec not found") {
		t.Error("expected 'sandbox-exec not found' in detail")
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
	if !strings.Contains(view2, "System status") {
		t.Error("expected 'System status' when daemon connected")
	}
}

func TestModel_SandboxConfigForm(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	agent.ResetSandboxCache()
	runner := agent.NewRunner(nil)
	m := NewModel(database, runner, true)
	m.activeTab = tabSettings
	m.width = 120
	m.height = 40
	m.refreshSettings()

	// Navigate to sandbox row (down from initial "All systems nominal")
	m.settings.CursorDown() // daemon logs
	m.settings.CursorDown() // UX logs
	m.settings.CursorDown() // sandbox
	sel := m.settings.Selected()
	if sel == nil || sel.kind != settingsRowSandbox {
		t.Fatalf("expected cursor on sandbox row, got %v", sel)
	}

	// Sandbox should start disabled
	cfg := m.db.Config()
	if cfg.Sandbox.Enabled {
		t.Error("expected sandbox disabled initially")
	}

	// Press Enter to open sandbox config form
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	updated, _ := m.handleSettingsKey(enterMsg)
	m = updated.(Model)

	if m.current != viewSandboxConfig {
		t.Fatalf("expected viewSandboxConfig, got %d", m.current)
	}

	// Toggle enabled via ctrl+e
	updated, _ = m.handleSandboxConfigKey(tea.KeyMsg{Type: tea.KeyCtrlE})
	m = updated.(Model)

	if !m.sandboxconfig.enabled {
		t.Error("expected sandbox enabled after ctrl+e in form")
	}

	// Navigate to last field and submit
	m.sandboxconfig.focused = sbFieldExtraWrite
	updated, _ = m.handleSandboxConfigKey(enterMsg)
	m = updated.(Model)

	if m.current != viewTaskList {
		t.Fatalf("expected viewTaskList after submit, got %d", m.current)
	}

	cfg = m.db.Config()
	if !cfg.Sandbox.Enabled {
		t.Error("expected sandbox enabled after form submit")
	}
}

func TestModel_SandboxConfigFormCancel(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	agent.ResetSandboxCache()
	runner := agent.NewRunner(nil)
	m := NewModel(database, runner, true)
	m.activeTab = tabSettings
	m.width = 120
	m.height = 40
	m.refreshSettings()

	// Navigate to sandbox row and open form
	m.settings.CursorDown() // daemon logs
	m.settings.CursorDown() // UX logs
	m.settings.CursorDown() // sandbox
	updated, _ := m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	// Toggle enabled then cancel — should NOT persist
	m.handleSandboxConfigKey(tea.KeyMsg{Type: tea.KeyCtrlE})
	updated, _ = m.handleSandboxConfigKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if m.current != viewTaskList {
		t.Fatalf("expected viewTaskList after cancel, got %d", m.current)
	}

	cfg := m.db.Config()
	if cfg.Sandbox.Enabled {
		t.Error("expected sandbox still disabled after cancel")
	}
}

func TestModel_SandboxConfigFormDenyRead(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	agent.ResetSandboxCache()
	runner := agent.NewRunner(nil)
	m := NewModel(database, runner, true)
	m.activeTab = tabSettings
	m.width = 120
	m.height = 40
	m.refreshSettings()

	// Navigate to sandbox row and open form
	m.settings.CursorDown() // daemon logs
	m.settings.CursorDown() // UX logs
	m.settings.CursorDown() // sandbox
	updated, _ := m.handleSettingsKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	// Type deny read paths into the first field
	m.sandboxconfig.inputs[sbFieldDenyRead].SetValue("/secrets,/private")

	// Submit from last field
	m.sandboxconfig.focused = sbFieldExtraWrite
	updated, _ = m.handleSandboxConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	cfg := m.db.Config()
	if len(cfg.Sandbox.DenyRead) != 2 {
		t.Fatalf("expected 2 deny read paths, got %d: %v", len(cfg.Sandbox.DenyRead), cfg.Sandbox.DenyRead)
	}
	if cfg.Sandbox.DenyRead[0] != "/secrets" {
		t.Errorf("expected /secrets, got %q", cfg.Sandbox.DenyRead[0])
	}
}

func TestSettingsView_DaemonLogsRow(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetWarnings(nil)
	sv.SetProjects(nil)
	sv.SetBackends(nil)

	view := sv.View()
	if !strings.Contains(view, "Daemon Logs") {
		t.Error("expected 'Daemon Logs' row in settings view")
	}
}

func TestSettingsView_DaemonLogsDetail(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetWarnings(nil)
	sv.SetProjects(nil)
	sv.SetBackends(nil)

	// Navigate to daemon logs row (down from status "all good")
	sv.CursorDown() // daemon logs row
	sel := sv.Selected()
	if sel == nil || sel.kind != settingsRowDaemonLogs {
		t.Fatal("expected cursor on daemon logs row")
	}

	detail := sv.RenderDetail(60, 20)
	if !strings.Contains(detail, "Daemon Logs") {
		t.Error("expected 'Daemon Logs' in detail panel")
	}
	if !strings.Contains(detail, "enter") {
		t.Error("expected enter hint in detail panel")
	}
}

func TestModel_DaemonLogsView(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	runner := agent.NewRunner(nil)
	m := NewModel(database, runner, false)
	m.current = viewDaemonLogs
	m.width = 80
	m.height = 24
	m.daemonLogLines = []string{
		"2026-03-15 daemon listening on /tmp/daemon.sock",
		"2026-03-15 session started: task-1",
		"2026-03-15 session finished: task-1",
	}
	m.daemonLogOffset = 0

	view := m.View()
	if !strings.Contains(view, "Daemon Logs") {
		t.Error("expected 'Daemon Logs' title")
	}
	if !strings.Contains(view, "daemon listening") {
		t.Error("expected log content in view")
	}
	if !strings.Contains(view, "esc") {
		t.Error("expected close hint")
	}
}

func TestModel_DaemonLogsScroll(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	runner := agent.NewRunner(nil)
	m := NewModel(database, runner, false)
	m.current = viewDaemonLogs
	m.width = 80
	m.height = 24

	// Generate enough lines to require scrolling
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("log line %d", i)
	}
	m.daemonLogLines = lines
	m.daemonLogOffset = 0

	// Scroll down
	downMsg := tea.KeyMsg{Type: tea.KeyDown}
	updated, _ := m.handleDaemonLogsKey(downMsg)
	m = updated.(Model)
	if m.daemonLogOffset != 1 {
		t.Errorf("expected offset 1 after down, got %d", m.daemonLogOffset)
	}

	// Scroll up
	upMsg := tea.KeyMsg{Type: tea.KeyUp}
	updated, _ = m.handleDaemonLogsKey(upMsg)
	m = updated.(Model)
	if m.daemonLogOffset != 0 {
		t.Errorf("expected offset 0 after up, got %d", m.daemonLogOffset)
	}

	// Can't scroll past top
	updated, _ = m.handleDaemonLogsKey(upMsg)
	m = updated.(Model)
	if m.daemonLogOffset != 0 {
		t.Errorf("expected offset to stay 0, got %d", m.daemonLogOffset)
	}

	// Page down
	pgDownMsg := tea.KeyMsg{Type: tea.KeyPgDown}
	updated, _ = m.handleDaemonLogsKey(pgDownMsg)
	m = updated.(Model)
	if m.daemonLogOffset == 0 {
		t.Error("expected offset > 0 after page down")
	}
}

func TestModel_DaemonLogsMsg_Error(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	runner := agent.NewRunner(nil)
	m := NewModel(database, runner, false)
	m.activeTab = tabSettings
	m.width = 80
	m.height = 24

	// Simulate a failed log read (file not found)
	updated, _ := m.Update(DaemonLogsMsg{Err: fmt.Errorf("open daemon.log: no such file or directory")})
	m = updated.(Model)

	// Should NOT switch to log viewer
	if m.current == viewDaemonLogs {
		t.Error("should not switch to viewDaemonLogs on error")
	}
}

func TestModel_DaemonLogsClose_QKey(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	runner := agent.NewRunner(nil)
	m := NewModel(database, runner, false)
	m.current = viewDaemonLogs
	m.width = 80
	m.height = 24
	m.daemonLogLines = []string{"line 1"}

	// q should close
	qMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}
	updated, _ := m.handleDaemonLogsKey(qMsg)
	m = updated.(Model)
	if m.current != viewTaskList {
		t.Errorf("expected viewTaskList after q, got %d", m.current)
	}
	if m.activeTab != tabSettings {
		t.Error("expected settings tab after closing logs via q")
	}
}

func TestModel_DaemonLogsScroll_AllKeys(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	runner := agent.NewRunner(nil)
	m := NewModel(database, runner, false)
	m.current = viewDaemonLogs
	m.width = 80
	m.height = 24

	lines := make([]string, 200)
	for i := range lines {
		lines[i] = fmt.Sprintf("log line %d", i)
	}
	m.daemonLogLines = lines
	visible := m.daemonLogVisibleLines()

	// End key — jump to bottom
	updated, _ := m.handleDaemonLogsKey(tea.KeyMsg{Type: tea.KeyEnd})
	m = updated.(Model)
	expectedEnd := len(lines) - visible
	if m.daemonLogOffset != expectedEnd {
		t.Errorf("expected offset %d after End, got %d", expectedEnd, m.daemonLogOffset)
	}

	// Home key — jump to top
	updated, _ = m.handleDaemonLogsKey(tea.KeyMsg{Type: tea.KeyHome})
	m = updated.(Model)
	if m.daemonLogOffset != 0 {
		t.Errorf("expected offset 0 after Home, got %d", m.daemonLogOffset)
	}

	// PgDown then PgUp
	updated, _ = m.handleDaemonLogsKey(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(Model)
	afterPgDown := m.daemonLogOffset
	if afterPgDown != visible {
		t.Errorf("expected offset %d after PgDown, got %d", visible, afterPgDown)
	}
	updated, _ = m.handleDaemonLogsKey(tea.KeyMsg{Type: tea.KeyPgUp})
	m = updated.(Model)
	if m.daemonLogOffset != 0 {
		t.Errorf("expected offset 0 after PgUp, got %d", m.daemonLogOffset)
	}
}

func TestModel_DaemonLogsClose(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	runner := agent.NewRunner(nil)
	m := NewModel(database, runner, false)
	m.current = viewDaemonLogs
	m.width = 80
	m.height = 24
	m.daemonLogLines = []string{"line 1"}

	// Escape should close
	escMsg := tea.KeyMsg{Type: tea.KeyEscape}
	updated, _ := m.handleDaemonLogsKey(escMsg)
	m = updated.(Model)
	if m.current != viewTaskList {
		t.Errorf("expected viewTaskList after esc, got %d", m.current)
	}
	if m.activeTab != tabSettings {
		t.Error("expected settings tab after closing logs")
	}
	if m.daemonLogLines != nil {
		t.Error("expected daemonLogLines to be nil after close")
	}
}

func TestSettingsView_UXLogsRow(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetWarnings(nil)
	sv.SetProjects(nil)
	sv.SetBackends(nil)

	// Navigate past daemon logs to UX logs row
	sv.CursorDown() // daemon logs
	sv.CursorDown() // UX logs
	sel := sv.Selected()
	if sel == nil || sel.kind != settingsRowUXLogs {
		t.Fatal("expected cursor on UX logs row")
	}

	detail := sv.RenderDetail(60, 20)
	if !strings.Contains(detail, "UX Logs") {
		t.Error("expected 'UX Logs' in detail panel")
	}
	if !strings.Contains(detail, "enter") {
		t.Error("expected enter hint in detail panel")
	}
}

func TestModel_UXLogsView(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	runner := agent.NewRunner(nil)
	m := NewModel(database, runner, false)
	m.current = viewUXLogs
	m.width = 80
	m.height = 24
	m.uxLogLines = []string{
		"2026/03/16 10:00:00.000 startOrAttach: task=t1",
		"2026/03/16 10:00:01.000 handleAgentFinished: task=t1",
	}
	m.uxLogOffset = 0

	view := m.View()
	if !strings.Contains(view, "UX Logs") {
		t.Error("expected 'UX Logs' title")
	}
	if !strings.Contains(view, "startOrAttach") {
		t.Error("expected log content in view")
	}
}

func TestModel_UXLogsScroll(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	runner := agent.NewRunner(nil)
	m := NewModel(database, runner, false)
	m.current = viewUXLogs
	m.width = 80
	m.height = 24

	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("ux log line %d", i)
	}
	m.uxLogLines = lines
	m.uxLogOffset = 0

	// Scroll down
	updated, _ := m.handleUXLogsKey(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.uxLogOffset != 1 {
		t.Errorf("expected offset 1 after down, got %d", m.uxLogOffset)
	}

	// Scroll up
	updated, _ = m.handleUXLogsKey(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.uxLogOffset != 0 {
		t.Errorf("expected offset 0 after up, got %d", m.uxLogOffset)
	}

	// Page down
	updated, _ = m.handleUXLogsKey(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(Model)
	if m.uxLogOffset == 0 {
		t.Error("expected offset > 0 after page down")
	}
}

func TestModel_UXLogsClose(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	runner := agent.NewRunner(nil)
	m := NewModel(database, runner, false)
	m.current = viewUXLogs
	m.width = 80
	m.height = 24
	m.uxLogLines = []string{"line 1"}

	// Escape should close
	updated, _ := m.handleUXLogsKey(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(Model)
	if m.current != viewTaskList {
		t.Errorf("expected viewTaskList after esc, got %d", m.current)
	}
	if m.activeTab != tabSettings {
		t.Error("expected settings tab after closing UX logs")
	}
	if m.uxLogLines != nil {
		t.Error("expected uxLogLines to be nil after close")
	}
}

func TestModel_UXLogsMsg_Error(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	runner := agent.NewRunner(nil)
	m := NewModel(database, runner, false)
	m.width = 80
	m.height = 24

	updated, _ := m.Update(UXLogsMsg{Err: fmt.Errorf("open ux.log: no such file")})
	m = updated.(Model)

	if m.current == viewUXLogs {
		t.Error("should not switch to viewUXLogs on error")
	}
}

func TestModel_DaemonLogsMouseScroll(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	runner := agent.NewRunner(nil)
	m := NewModel(database, runner, false)
	m.current = viewDaemonLogs
	m.width = 80
	m.height = 24

	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("log line %d", i)
	}
	m.daemonLogLines = lines
	m.daemonLogOffset = 10

	// Mouse wheel down
	m.handleDaemonLogsMouse(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	if m.daemonLogOffset != 13 {
		t.Errorf("expected offset 13 after wheel down, got %d", m.daemonLogOffset)
	}

	// Mouse wheel up
	m.handleDaemonLogsMouse(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	if m.daemonLogOffset != 10 {
		t.Errorf("expected offset 10 after wheel up, got %d", m.daemonLogOffset)
	}
}


