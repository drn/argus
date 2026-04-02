package tui2

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

func testSettingsView(t *testing.T) *SettingsView {
	t.Helper()
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	sv := NewSettingsView(database)
	sv.Refresh()
	return sv
}

func TestSettingsView_Empty(t *testing.T) {
	sv := testSettingsView(t)
	if len(sv.rows) == 0 {
		t.Error("should have section rows even with empty data")
	}
}

func TestSettingsView_Sections(t *testing.T) {
	sv := testSettingsView(t)
	sections := 0
	for _, row := range sv.rows {
		if row.kind == srSection {
			sections++
		}
	}
	// Status, Sandbox, Projects, Backends, Knowledge Base
	if sections < 4 {
		t.Errorf("expected at least 4 sections, got %d", sections)
	}
}

func TestSettingsView_CursorSkipsHeaders(t *testing.T) {
	sv := testSettingsView(t)
	// First row should be Status (section header), cursor should skip it.
	if sv.cursor < len(sv.rows) && sv.rows[sv.cursor].kind == srSection {
		t.Error("cursor should skip section headers")
	}
}

func TestSettingsView_Navigation(t *testing.T) {
	sv := testSettingsView(t)
	initial := sv.cursor
	sv.moveCursor(1)
	if sv.cursor == initial && len(sv.rows) > 2 {
		t.Error("cursor should move down")
	}
	sv.moveCursor(-1)
	// Should either return to initial or land on a non-header.
	if sv.cursor < len(sv.rows) && sv.rows[sv.cursor].kind == srSection {
		t.Error("cursor should not be on a section header after navigation")
	}
}

func TestSettingsView_CursorStaysOnFirstItem(t *testing.T) {
	sv := testSettingsView(t)
	// Move to the first selectable row.
	sv.cursor = 0
	sv.skipToSelectable(1)
	first := sv.cursor

	// Pressing up from the first selectable row should not move the cursor.
	sv.moveCursor(-1)
	if sv.cursor != first {
		t.Errorf("cursor moved from first selectable row %d to %d", first, sv.cursor)
	}
	if sv.rows[sv.cursor].kind == srSection {
		t.Error("cursor landed on a section header")
	}
}

func TestSettingsView_SetDaemonConnected(t *testing.T) {
	sv := testSettingsView(t)

	sv.SetDaemonConnected(false)
	if len(sv.warnings) == 0 {
		t.Error("should have a warning when not connected")
	}

	sv.SetDaemonConnected(true)
	if len(sv.warnings) != 0 {
		t.Error("should have no warnings when connected")
	}
}

func TestSettingsView_SelectedProject(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	database.SetProject("test-proj", config.Project{Path: "/tmp/test", Branch: "main"})
	sv := NewSettingsView(database)
	sv.Refresh()

	// Find a project row.
	found := false
	for i, row := range sv.rows {
		if row.kind == srProject && row.key == "test-proj" {
			sv.cursor = i
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no project row found")
	}

	pe := sv.SelectedProject()
	if pe == nil {
		t.Fatal("SelectedProject returned nil")
	}
	if pe.Name != "test-proj" {
		t.Errorf("project name = %q, want test-proj", pe.Name)
	}
}

func TestSettingsView_SelectedBackend(t *testing.T) {
	sv := testSettingsView(t)
	// Find a backend row (default backends should exist).
	for i, row := range sv.rows {
		if row.kind == srBackend {
			sv.cursor = i
			be := sv.SelectedBackend()
			if be == nil {
				t.Error("SelectedBackend returned nil on backend row")
			}
			return
		}
	}
	// It's OK if no backends exist in the test DB.
}

func TestSettingsView_SandboxToggle(t *testing.T) {
	sv := testSettingsView(t)
	initialEnabled := sv.sandboxEnabled

	// Find sandbox row and toggle.
	for i, row := range sv.rows {
		if row.kind == srSandbox {
			sv.cursor = i
			sv.handleEnter()
			break
		}
	}

	if sv.sandboxEnabled == initialEnabled {
		t.Error("sandbox should have toggled")
	}
}

func TestSettingsView_KBToggle(t *testing.T) {
	sv := testSettingsView(t)
	initialKB := sv.kbEnabled

	for i, row := range sv.rows {
		if row.kind == srKB {
			sv.cursor = i
			sv.handleEnter()
			break
		}
	}

	if sv.kbEnabled == initialKB {
		t.Error("KB should have toggled")
	}
}

func TestSettingsView_TodoProjectCycle(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	database.SetProject("alpha", config.Project{Path: "/a"})
	database.SetProject("beta", config.Project{Path: "/b"})
	sv := NewSettingsView(database)
	sv.Refresh()

	// Find the todo project row.
	todoIdx := -1
	for i, row := range sv.rows {
		if row.kind == srToDoProject {
			todoIdx = i
			break
		}
	}
	if todoIdx < 0 {
		t.Fatal("no todo project row found")
	}
	sv.cursor = todoIdx

	t.Run("starts empty", func(t *testing.T) {
		testutil.Equal(t, sv.todoProject, "")
	})

	t.Run("cycle forward to first project", func(t *testing.T) {
		sv.handleEnter()
		testutil.Equal(t, sv.todoProject, "alpha")
	})

	t.Run("cycle forward to second project", func(t *testing.T) {
		// Re-find row after rebuild.
		for i, row := range sv.rows {
			if row.kind == srToDoProject {
				sv.cursor = i
				break
			}
		}
		sv.handleEnter()
		testutil.Equal(t, sv.todoProject, "beta")
	})

	t.Run("cycle forward wraps to none", func(t *testing.T) {
		for i, row := range sv.rows {
			if row.kind == srToDoProject {
				sv.cursor = i
				break
			}
		}
		sv.handleEnter()
		testutil.Equal(t, sv.todoProject, "")
	})

	t.Run("cycle backward wraps to last project", func(t *testing.T) {
		for i, row := range sv.rows {
			if row.kind == srToDoProject {
				sv.cursor = i
				break
			}
		}
		sv.cycleTodoProject(-1)
		testutil.Equal(t, sv.todoProject, "beta")
	})

	t.Run("persists to database", func(t *testing.T) {
		cfg := database.Config()
		testutil.Equal(t, cfg.Defaults.TodoProject, "beta")
	})
}

func TestSettingsView_TodoProjectLeftRight(t *testing.T) {
	database, _ := db.OpenInMemory()
	database.SetProject("proj", config.Project{Path: "/p"})
	sv := NewSettingsView(database)
	sv.Refresh()

	for i, row := range sv.rows {
		if row.kind == srToDoProject {
			sv.cursor = i
			break
		}
	}

	// Right arrow cycles forward.
	ev := tcell.NewEventKey(tcell.KeyRight, 0, 0)
	handled := sv.HandleKey(ev)
	testutil.Equal(t, handled, true)
	testutil.Equal(t, sv.todoProject, "proj")

	// Left arrow cycles backward.
	ev = tcell.NewEventKey(tcell.KeyLeft, 0, 0)
	handled = sv.HandleKey(ev)
	testutil.Equal(t, handled, true)
	testutil.Equal(t, sv.todoProject, "")
}

func TestSettingsView_TodoProjectNoProjects(t *testing.T) {
	sv := testSettingsView(t)

	for i, row := range sv.rows {
		if row.kind == srToDoProject {
			sv.cursor = i
			break
		}
	}

	// Cycle should be a no-op with no projects.
	sv.cycleTodoProject(1)
	testutil.Equal(t, sv.todoProject, "")
}

func TestSettingsView_LogsSection(t *testing.T) {
	sv := testSettingsView(t)
	var logsRows []settingsRow
	for _, row := range sv.rows {
		if row.kind == srLogs {
			logsRows = append(logsRows, row)
		}
	}
	if len(logsRows) != 2 {
		t.Fatalf("expected 2 log rows, got %d", len(logsRows))
	}
	if logsRows[0].key != "ux" {
		t.Errorf("first log row key = %q, want ux", logsRows[0].key)
	}
	if logsRows[1].key != "daemon" {
		t.Errorf("second log row key = %q, want daemon", logsRows[1].key)
	}
}

func TestReadLogLines(t *testing.T) {
	// Non-existent file.
	lines := readLogLines("/nonexistent/path")
	if len(lines) != 1 || lines[0] != "(file not found)" {
		t.Errorf("expected '(file not found)', got %v", lines)
	}

	// Write a temp file with known content.
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		fmt.Fprintf(f, "line %d\n", i)
	}
	f.Close()

	lines = readLogLines(f.Name())
	if len(lines) != 20 {
		t.Fatalf("expected 20 lines, got %d", len(lines))
	}
	if lines[0] != "line 0" {
		t.Errorf("first line = %q, want 'line 0'", lines[0])
	}
	if lines[19] != "line 19" {
		t.Errorf("last line = %q, want 'line 19'", lines[19])
	}
}

func TestSettingsView_LogScroll(t *testing.T) {
	sv := testSettingsView(t)

	// Find a log row.
	for i, row := range sv.rows {
		if row.kind == srLogs {
			sv.cursor = i
			break
		}
	}

	// Simulate loading some lines.
	sv.logLines = make([]string, 100)
	for i := range sv.logLines {
		sv.logLines[i] = fmt.Sprintf("line %d", i)
	}
	sv.logKey = sv.SelectedRow().key
	sv.logScrollOff = 50

	// Scroll up.
	sv.HandleMouse(tview.MouseScrollUp)
	if sv.logScrollOff != 49 {
		t.Errorf("scroll up: offset = %d, want 49", sv.logScrollOff)
	}

	// Scroll down.
	sv.HandleMouse(tview.MouseScrollDown)
	if sv.logScrollOff != 50 {
		t.Errorf("scroll down: offset = %d, want 50", sv.logScrollOff)
	}

	// Scroll up at 0 stays at 0.
	sv.logScrollOff = 0
	sv.HandleMouse(tview.MouseScrollUp)
	if sv.logScrollOff != 0 {
		t.Errorf("scroll up at 0: offset = %d, want 0", sv.logScrollOff)
	}
}

func TestSettingsView_DaemonRestart(t *testing.T) {
	sv := testSettingsView(t)

	// Not connected — no daemon row.
	sv.SetDaemonConnected(false)
	for _, row := range sv.rows {
		if row.kind == srDaemon {
			t.Fatal("daemon row should not appear when not connected")
		}
	}

	// Connected — daemon row should appear.
	sv.SetDaemonConnected(true)
	found := false
	for _, row := range sv.rows {
		if row.kind == srDaemon {
			found = true
			if row.label != "  Restart Daemon" {
				t.Errorf("daemon row label = %q, want '  Restart Daemon'", row.label)
			}
		}
	}
	if !found {
		t.Fatal("daemon row should appear when connected")
	}

	// Enter on daemon row fires callback.
	called := false
	sv.OnRestartDaemon = func() { called = true }
	for i, row := range sv.rows {
		if row.kind == srDaemon {
			sv.cursor = i
			break
		}
	}
	sv.handleEnter()
	if !called {
		t.Error("OnRestartDaemon should be called on enter")
	}
	if !sv.daemonRestarting {
		t.Error("daemonRestarting should be true after enter")
	}

	// While restarting, label changes and enter is a no-op.
	called = false
	sv.handleEnter()
	if called {
		t.Error("OnRestartDaemon should not fire while restarting")
	}
	for _, row := range sv.rows {
		if row.kind == srDaemon && row.label != "  Restarting..." {
			t.Errorf("daemon row label during restart = %q, want '  Restarting...'", row.label)
		}
	}

	// Clear restarting state.
	sv.SetDaemonRestarting(false)
	if sv.daemonRestarting {
		t.Error("daemonRestarting should be false after SetDaemonRestarting(false)")
	}
}

func TestSettingsView_APIRestartHint(t *testing.T) {
	sv := testSettingsView(t)

	// After first Refresh, boot state is recorded.
	testutil.Equal(t, sv.apiBootRecorded, true)
	testutil.Equal(t, sv.apiEnabledAtBoot, false) // default is disabled

	apiLabel := func() string {
		for _, row := range sv.rows {
			if row.kind == srAPI {
				return row.label
			}
		}
		return ""
	}

	t.Run("no hint when state matches boot", func(t *testing.T) {
		testutil.Equal(t, apiLabel(), "  Disabled")
	})

	t.Run("hint appears after toggle", func(t *testing.T) {
		// Toggle API on.
		for i, row := range sv.rows {
			if row.kind == srAPI {
				sv.cursor = i
				sv.handleEnter()
				break
			}
		}
		testutil.Contains(t, apiLabel(), "(restart required)")
	})

	t.Run("hint disappears after double toggle", func(t *testing.T) {
		for i, row := range sv.rows {
			if row.kind == srAPI {
				sv.cursor = i
				sv.handleEnter()
				break
			}
		}
		label := apiLabel()
		if strings.Contains(label, "(restart required)") {
			t.Errorf("hint should disappear after toggling back, got %q", label)
		}
	})

	t.Run("hint clears after daemon restart completes", func(t *testing.T) {
		// Toggle API on again to show hint.
		for i, row := range sv.rows {
			if row.kind == srAPI {
				sv.cursor = i
				sv.handleEnter()
				break
			}
		}
		testutil.Contains(t, apiLabel(), "(restart required)")

		// Simulate daemon restart completion (covers both manual and auto paths).
		sv.SetDaemonRestarting(false)
		testutil.Equal(t, sv.apiBootRecorded, false)

		// Next Refresh re-anchors boot state — hint should clear.
		sv.Refresh()
		testutil.Equal(t, sv.apiBootRecorded, true)
		testutil.Equal(t, sv.apiEnabledAtBoot, true) // now matches toggled state
		label := apiLabel()
		if strings.Contains(label, "(restart required)") {
			t.Errorf("hint should clear after restart + refresh, got %q", label)
		}
	})
}

func TestSettingsView_LogScrollResetOnCursorMove(t *testing.T) {
	sv := testSettingsView(t)

	// Find a log row and set scroll state.
	for i, row := range sv.rows {
		if row.kind == srLogs {
			sv.cursor = i
			sv.logScrollOff = 42
			sv.logKey = row.key
			sv.logLines = []string{"test"}
			break
		}
	}

	// Move cursor away — should reset scroll.
	sv.moveCursor(1)
	if sv.logScrollOff != 0 {
		t.Errorf("scroll offset not reset after cursor move: %d", sv.logScrollOff)
	}
	if sv.logKey != "" {
		t.Errorf("logKey not cleared: %q", sv.logKey)
	}
}

func TestSettingsView_NewProjectCallback(t *testing.T) {
	database, _ := db.OpenInMemory()
	database.SetProject("test-proj", config.Project{Path: "/tmp/test", Branch: "main"})
	sv := NewSettingsView(database)
	sv.Refresh()

	// Move cursor to a project row.
	for i, row := range sv.rows {
		if row.kind == srProject {
			sv.cursor = i
			break
		}
	}

	called := false
	sv.OnNewProject = func() { called = true }

	ev := tcell.NewEventKey(tcell.KeyRune, 'n', 0)
	handled := sv.HandleKey(ev)
	if !handled {
		t.Error("'n' key should be handled on project row")
	}
	if !called {
		t.Error("OnNewProject callback not fired")
	}
}

func TestSettingsView_EditProjectCallback(t *testing.T) {
	database, _ := db.OpenInMemory()
	database.SetProject("test-proj", config.Project{Path: "/tmp/test", Branch: "main"})
	sv := NewSettingsView(database)
	sv.Refresh()

	// Move cursor to a project row.
	for i, row := range sv.rows {
		if row.kind == srProject && row.key == "test-proj" {
			sv.cursor = i
			break
		}
	}

	var gotName string
	sv.OnEditProject = func(name string, p config.Project) { gotName = name }

	ev := tcell.NewEventKey(tcell.KeyRune, 'e', 0)
	handled := sv.HandleKey(ev)
	if !handled {
		t.Error("'e' key should be handled on project row")
	}
	if gotName != "test-proj" {
		t.Errorf("OnEditProject got name %q, want test-proj", gotName)
	}
}

func TestSettingsView_NewBackendCallback(t *testing.T) {
	sv := testSettingsView(t)

	// Move cursor to a backend row.
	for i, row := range sv.rows {
		if row.kind == srBackend {
			sv.cursor = i
			break
		}
	}

	called := false
	sv.OnNewBackend = func() { called = true }

	ev := tcell.NewEventKey(tcell.KeyRune, 'n', 0)
	handled := sv.HandleKey(ev)
	if !handled {
		t.Error("'n' key should be handled on backend row")
	}
	if !called {
		t.Error("OnNewBackend callback not fired")
	}
}

func TestSettingsView_EditBackendCallback(t *testing.T) {
	sv := testSettingsView(t)

	// Move cursor to a backend row.
	for i, row := range sv.rows {
		if row.kind == srBackend {
			sv.cursor = i
			break
		}
	}

	var gotName string
	sv.OnEditBackend = func(name string, b config.Backend) { gotName = name }

	ev := tcell.NewEventKey(tcell.KeyRune, 'e', 0)
	handled := sv.HandleKey(ev)
	if !handled {
		t.Error("'e' key should be handled on backend row")
	}
	if gotName == "" {
		t.Error("OnEditBackend callback not fired or got empty name")
	}
}

func TestSettingsView_NKeyOnNonProjectRow(t *testing.T) {
	sv := testSettingsView(t)

	// Cursor should be on a non-project row (e.g., warning/status).
	sv.OnNewProject = func() { t.Error("OnNewProject should not fire on non-project row") }
	sv.OnNewBackend = func() { t.Error("OnNewBackend should not fire on non-backend row") }

	ev := tcell.NewEventKey(tcell.KeyRune, 'n', 0)
	handled := sv.HandleKey(ev)
	if handled {
		t.Error("'n' should not be handled on non-project/backend row")
	}
}

func TestSettingsView_ProjectDetail_SandboxInherit(t *testing.T) {
	database, _ := db.OpenInMemory()
	database.SetProject("proj", config.Project{Path: "/tmp/proj"})
	sv := NewSettingsView(database)
	sv.Refresh()

	pe := findProjectEntry(t, sv, "proj")
	testutil.Nil(t, pe.Project.Sandbox.Enabled)
}

func TestSettingsView_ProjectDetail_SandboxEnabled(t *testing.T) {
	database, _ := db.OpenInMemory()
	v := true
	database.SetProject("proj", config.Project{
		Path:    "/tmp/proj",
		Sandbox: config.ProjectSandboxConfig{Enabled: &v},
	})
	sv := NewSettingsView(database)
	sv.Refresh()

	pe := findProjectEntry(t, sv, "proj")
	if pe.Project.Sandbox.Enabled == nil {
		t.Fatal("expected Sandbox.Enabled to be non-nil")
	}
	testutil.Equal(t, *pe.Project.Sandbox.Enabled, true)
}

func TestSettingsView_ProjectDetail_SandboxDisabled(t *testing.T) {
	database, _ := db.OpenInMemory()
	v := false
	database.SetProject("proj", config.Project{
		Path:    "/tmp/proj",
		Sandbox: config.ProjectSandboxConfig{Enabled: &v},
	})
	sv := NewSettingsView(database)
	sv.Refresh()

	pe := findProjectEntry(t, sv, "proj")
	if pe.Project.Sandbox.Enabled == nil {
		t.Fatal("expected Sandbox.Enabled to be non-nil")
	}
	testutil.Equal(t, *pe.Project.Sandbox.Enabled, false)
}

func TestSettingsView_ProjectDetail_SandboxRoundTrip(t *testing.T) {
	database, _ := db.OpenInMemory()
	v := true
	database.SetProject("proj", config.Project{
		Path: "/tmp/proj",
		Sandbox: config.ProjectSandboxConfig{
			Enabled:    &v,
			DenyRead:   []string{"/secret"},
			ExtraWrite: []string{"/tmp/build"},
		},
	})
	sv := NewSettingsView(database)
	sv.Refresh()

	pe := findProjectEntry(t, sv, "proj")
	testutil.DeepEqual(t, pe.Project.Sandbox.DenyRead, []string{"/secret"})
	testutil.DeepEqual(t, pe.Project.Sandbox.ExtraWrite, []string{"/tmp/build"})
}

func TestSettingsView_ReviewPromptEdit(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	sv := NewSettingsView(database)
	sv.Refresh()

	// Default value.
	testutil.Equal(t, sv.reviewPrompt, "/review")

	// Find review prompt row.
	rpIdx := -1
	for i, row := range sv.rows {
		if row.kind == srReviewPrompt {
			rpIdx = i
			break
		}
	}
	if rpIdx < 0 {
		t.Fatal("no review prompt row found")
	}
	sv.cursor = rpIdx

	t.Run("enter starts editing", func(t *testing.T) {
		sv.handleEnter()
		testutil.Equal(t, sv.editingPrompt, true)
		testutil.Equal(t, sv.editPromptBuf, "/review")
	})

	t.Run("typing appends to buffer", func(t *testing.T) {
		sv.handleEditPromptKey(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone))
		sv.handleEditPromptKey(tcell.NewEventKey(tcell.KeyRune, '-', tcell.ModNone))
		sv.handleEditPromptKey(tcell.NewEventKey(tcell.KeyRune, '-', tcell.ModNone))
		testutil.Equal(t, sv.editPromptBuf, "/review --")
	})

	t.Run("backspace removes last rune", func(t *testing.T) {
		sv.handleEditPromptKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))
		testutil.Equal(t, sv.editPromptBuf, "/review -")
	})

	t.Run("escape cancels without saving", func(t *testing.T) {
		sv.handleEditPromptKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
		testutil.Equal(t, sv.editingPrompt, false)
		testutil.Equal(t, sv.reviewPrompt, "/review") // unchanged
	})

	t.Run("enter saves and persists", func(t *testing.T) {
		// Re-find row after rebuild.
		for i, row := range sv.rows {
			if row.kind == srReviewPrompt {
				sv.cursor = i
				break
			}
		}
		sv.handleEnter() // start editing
		sv.editPromptBuf = "/cortex:review"
		sv.handleEditPromptKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
		testutil.Equal(t, sv.editingPrompt, false)
		testutil.Equal(t, sv.reviewPrompt, "/cortex:review")

		// Verify persisted to DB.
		cfg := database.Config()
		testutil.Equal(t, cfg.Defaults.ReviewPrompt, "/cortex:review")
	})
}

func TestSettingsView_AutoStartToggle(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	sv := NewSettingsView(database)
	sv.Refresh()

	// Find KB row.
	kbIdx := -1
	for i, row := range sv.rows {
		if row.kind == srKB {
			kbIdx = i
			break
		}
	}
	if kbIdx < 0 {
		t.Fatal("no KB row found")
	}
	sv.cursor = kbIdx

	t.Run("initially off", func(t *testing.T) {
		testutil.Equal(t, sv.autoStartTodos, false)
	})

	t.Run("a key toggles on", func(t *testing.T) {
		ev := tcell.NewEventKey(tcell.KeyRune, 'a', 0)
		handled := sv.HandleKey(ev)
		testutil.Equal(t, handled, true)
		testutil.Equal(t, sv.autoStartTodos, true)
	})

	t.Run("auto-enables task sync", func(t *testing.T) {
		testutil.Equal(t, sv.kbTaskSync, true)
	})

	t.Run("persists to database", func(t *testing.T) {
		cfg := database.Config()
		testutil.Equal(t, cfg.KB.AutoStartTodos, true)
		testutil.Equal(t, cfg.KB.AutoCreateTasks, true)
	})

	t.Run("a key toggles off and disables task sync", func(t *testing.T) {
		// Re-find KB row after rebuild.
		for i, row := range sv.rows {
			if row.kind == srKB {
				sv.cursor = i
				break
			}
		}
		ev := tcell.NewEventKey(tcell.KeyRune, 'a', 0)
		sv.HandleKey(ev)
		testutil.Equal(t, sv.autoStartTodos, false)
		testutil.Equal(t, sv.kbTaskSync, false)

		cfg := database.Config()
		testutil.Equal(t, cfg.KB.AutoStartTodos, false)
		testutil.Equal(t, cfg.KB.AutoCreateTasks, false)
	})

	t.Run("a key no-op on non-KB row", func(t *testing.T) {
		sv.cursor = 0
		sv.skipToSelectable(1) // land on first selectable (not KB)
		ev := tcell.NewEventKey(tcell.KeyRune, 'a', 0)
		handled := sv.HandleKey(ev)
		// Should not be handled if not on KB row.
		if sv.rows[sv.cursor].kind != srKB {
			testutil.Equal(t, handled, false)
		}
	})

	t.Run("default interval is 120", func(t *testing.T) {
		testutil.Equal(t, sv.autoStartInterval, 120)
	})
}

// findProjectEntry locates a project in the settings view by name.
func findProjectEntry(t *testing.T, sv *SettingsView, name string) *projectEntry {
	t.Helper()
	for i := range sv.projects {
		if sv.projects[i].Name == name {
			return &sv.projects[i]
		}
	}
	t.Fatalf("project %q not found in settings view", name)
	return nil
}

func TestSettingsView_ScrollClampOnResize(t *testing.T) {
	sv := testSettingsView(t)

	// Simulate a small viewport where scrolling is needed: move cursor to last row.
	sv.cursor = len(sv.rows) - 1
	sv.skipToSelectable(-1)

	// Draw in a small viewport so scrollOff advances.
	screen := tcell.NewSimulationScreen("")
	_ = screen.Init()
	screen.SetSize(60, 10) // only 10 rows total, border eats 2 → 8 inner rows
	sv.SetRect(0, 0, 60, 10)
	sv.Draw(screen)
	testutil.Equal(t, sv.scrollOff > 0, true) // should have scrolled

	// Now "maximize": grow the viewport so all rows fit.
	bigH := len(sv.rows) + 4 // inner = bigH-2, plenty for all rows
	screen.SetSize(60, bigH)
	sv.SetRect(0, 0, 60, bigH)
	sv.Draw(screen)

	// scrollOff must clamp to 0 since all rows fit.
	testutil.Equal(t, sv.scrollOff, 0)
}
