package tui

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
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

func TestSettingsView_RailNavigation(t *testing.T) {
	sv := testSettingsView(t)
	sv.setFocus(focusRail)
	sv.setCategory(catSystem)

	// Down arrow moves to next category.
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	testutil.Equal(t, got, true)
	testutil.Equal(t, sv.category, catSandbox)

	// Up arrow moves back.
	got = sv.HandleKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	testutil.Equal(t, got, true)
	testutil.Equal(t, sv.category, catSystem)

	// Up at top is a no-op (returns false to allow tab nav).
	got = sv.HandleKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	testutil.Equal(t, got, false)
	testutil.Equal(t, sv.category, catSystem)

	// Right arrow moves focus into the pane.
	testutil.Equal(t, sv.focus, focusRail)
	got = sv.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	testutil.Equal(t, got, true)
	testutil.Equal(t, sv.focus, focusPane)
}

func TestSettingsView_RailNavigationBottomBound(t *testing.T) {
	sv := testSettingsView(t)
	sv.setFocus(focusRail)
	// Park on the last category.
	sv.setCategory(allCategories[len(allCategories)-1])

	// Down at the bottom must return false (allows tab nav to propagate).
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	testutil.Equal(t, got, false)
	testutil.Equal(t, sv.category, allCategories[len(allCategories)-1])
}

func TestSettingsView_VimFocusAliases(t *testing.T) {
	sv := testSettingsView(t)
	// Pane → rail via 'h'.
	sv.setFocus(focusPane)
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'h', 0))
	testutil.Equal(t, got, true)
	testutil.Equal(t, sv.focus, focusRail)
	// 'h' in rail is a no-op (returns false, lets it bubble).
	got = sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'h', 0))
	testutil.Equal(t, got, false)

	// Rail → pane via 'l'.
	got = sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'l', 0))
	testutil.Equal(t, got, true)
	testutil.Equal(t, sv.focus, focusPane)
	// 'l' in pane is a no-op.
	got = sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'l', 0))
	testutil.Equal(t, got, false)
}

func TestSettingsView_EmptyBackendsHasPlaceholder(t *testing.T) {
	d := testDB(t)
	// Iterate whatever the DB seeded — no hardcoded backend names, so this
	// stays valid if defaults change.
	backends, err := d.Backends()
	if err != nil {
		t.Fatal(err)
	}
	for name := range backends {
		if err := d.DeleteBackend(name); err != nil {
			t.Fatal(err)
		}
	}
	sv := NewSettingsView(d)
	sv.Refresh()
	sv.setCategory(catBackends)
	if len(sv.rows) != 1 {
		t.Fatalf("empty backends should yield 1 placeholder row, got %d", len(sv.rows))
	}
	testutil.Contains(t, sv.rows[0].label, "no backends")
}

func TestSettingsView_HandleClickRail(t *testing.T) {
	sv := testSettingsView(t)
	sv.SetRect(0, 0, 100, 30)
	sv.setCategory(catSystem)
	sv.setFocus(focusPane)

	// Click on the third row of the rail — that's the Projects category
	// (index 2: System(0), Sandbox(1), Projects(2)). The rail's first
	// interior row is at y=1.
	sv.HandleClick(2, 3) // x=2 is inside rail (width 100 → rail 20)
	testutil.Equal(t, sv.category, catProjects)
	testutil.Equal(t, sv.focus, focusRail)
}

func TestSettingsView_HandleClickPane(t *testing.T) {
	sv := testSettingsView(t)
	sv.SetRect(0, 0, 100, 30)
	sv.setFocus(focusRail)

	// Click well inside the right pane area.
	sv.HandleClick(50, 15)
	testutil.Equal(t, sv.focus, focusPane)
}

func TestSettingsView_HandleClickOutside(t *testing.T) {
	sv := testSettingsView(t)
	sv.SetRect(10, 10, 50, 20)
	sv.setFocus(focusPane)
	// Click well outside the rect.
	sv.HandleClick(0, 0)
	testutil.Equal(t, sv.focus, focusPane) // unchanged
}

func TestSettingsView_Categories(t *testing.T) {
	// All 9 categories must be addressable. Visiting each must populate
	// at least one row (auto-start row is platform-gated, but every other
	// category seeds at least one row).
	sv := testSettingsView(t)
	for _, c := range allCategories {
		sv.setCategory(c)
		// Sandbox / KB / API toggles always have ≥1 row. Projects/Schedules
		// fall back to a placeholder row when empty. Backends has seeded
		// defaults from the DB. System always has the OK/warning row.
		if len(sv.rows) == 0 {
			t.Errorf("category %s produced no rows", c.Label())
		}
	}
}

func TestSettingsView_CursorClampsToRows(t *testing.T) {
	sv := testSettingsView(t)
	if len(sv.rows) > 0 && (sv.cursor < 0 || sv.cursor >= len(sv.rows)) {
		t.Errorf("cursor %d out of range for %d rows", sv.cursor, len(sv.rows))
	}
}

func TestSettingsView_Navigation(t *testing.T) {
	sv := testSettingsView(t)
	// Switch to a category that always has multiple rows.
	sv.setCategory(catLogs)
	sv.cursor = 0
	sv.moveCursor(1)
	if sv.cursor != 1 {
		t.Errorf("cursor should advance to 1, got %d", sv.cursor)
	}
	sv.moveCursor(-1)
	if sv.cursor != 0 {
		t.Errorf("cursor should return to 0, got %d", sv.cursor)
	}
}

func TestSettingsView_CursorStaysOnFirstItem(t *testing.T) {
	sv := testSettingsView(t)
	sv.setCategory(catLogs)
	sv.cursor = 0
	sv.moveCursor(-1)
	if sv.cursor != 0 {
		t.Errorf("cursor should clamp at 0, got %d", sv.cursor)
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
	sv.setCategory(catProjects)

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
	sv.setCategory(catBackends)
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
}

func TestSettingsView_SandboxToggle(t *testing.T) {
	sv := testSettingsView(t)
	sv.setCategory(catSandbox)
	initialEnabled := sv.sandboxEnabled

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
	sv.setCategory(catKnowledgeBase)
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

func TestSettingsView_LogsSection(t *testing.T) {
	sv := testSettingsView(t)
	sv.setCategory(catLogs)
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
	sv.setCategory(catLogs)

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
	sv.setCategory(catSystem)

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
			if row.label != "Restart Daemon" {
				t.Errorf("daemon row label = %q, want 'Restart Daemon'", row.label)
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
		if row.kind == srDaemon && row.label != "Restarting..." {
			t.Errorf("daemon row label during restart = %q, want 'Restarting...'", row.label)
		}
	}

	// Clear restarting state.
	sv.SetDaemonRestarting(false)
	if sv.daemonRestarting {
		t.Error("daemonRestarting should be false after SetDaemonRestarting(false)")
	}
}

func TestSettingsView_UpdateArgusRow(t *testing.T) {
	sv := testSettingsView(t)
	sv.SetDaemonConnected(true)
	sv.setCategory(catSystem)

	hasUpdate, hasSource := false, false
	for _, row := range sv.rows {
		if row.kind == srUpdateArgus {
			hasUpdate = true
		}
		if row.kind == srSourcePath {
			hasSource = true
		}
	}
	if !hasUpdate || !hasSource {
		t.Fatalf("expected both srUpdateArgus and srSourcePath rows; update=%v source=%v", hasUpdate, hasSource)
	}

	// Disconnect — neither row should appear.
	sv.SetDaemonConnected(false)
	for _, row := range sv.rows {
		if row.kind == srUpdateArgus || row.kind == srSourcePath {
			t.Errorf("row %v should not appear when daemon disconnected", row.kind)
		}
	}
}

func TestSettingsView_UpdateArgusEnter(t *testing.T) {
	sv := testSettingsView(t)
	sv.SetDaemonConnected(true)
	sv.setCategory(catSystem)
	called := false
	sv.OnUpdateArgus = func() { called = true }

	for i, row := range sv.rows {
		if row.kind == srUpdateArgus {
			sv.cursor = i
			break
		}
	}
	sv.handleEnter()
	if !called {
		t.Error("OnUpdateArgus should fire on enter")
	}
	if !sv.updating {
		t.Error("updating flag should be set")
	}

	// While updating, second enter is a no-op.
	called = false
	sv.handleEnter()
	if called {
		t.Error("OnUpdateArgus should not fire while updating")
	}

	// SetUpdateResult clears the flag and persists status/output for the detail panel.
	sv.SetUpdateResult("install output", "Update succeeded")
	if sv.updating {
		t.Error("updating flag should be cleared")
	}
	if sv.updateOutput != "install output" || sv.updateStatus != "Update succeeded" {
		t.Errorf("unexpected post-update state: out=%q status=%q", sv.updateOutput, sv.updateStatus)
	}
}

func TestSettingsView_SourcePathEdit(t *testing.T) {
	sv := testSettingsView(t)
	sv.SetDaemonConnected(true)
	sv.setCategory(catSystem)

	for i, row := range sv.rows {
		if row.kind == srSourcePath {
			sv.cursor = i
			break
		}
	}
	sv.handleEnter()
	if !sv.editingSource {
		t.Fatal("expected editingSource to be true after enter")
	}
	for _, r := range "/tmp/argus" {
		sv.handleEditSourceKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	sv.handleEditSourceKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if sv.editingSource {
		t.Error("editingSource should be false after enter")
	}
	if sv.argusSourcePath != "/tmp/argus" {
		t.Errorf("argusSourcePath = %q, want /tmp/argus", sv.argusSourcePath)
	}
	cfg := sv.database.Config()
	if cfg.Argus.SourcePath != "/tmp/argus" {
		t.Errorf("persisted SourcePath = %q, want /tmp/argus", cfg.Argus.SourcePath)
	}
}

func TestSettingsView_APIRestartHint(t *testing.T) {
	sv := testSettingsView(t)
	sv.setCategory(catRemoteAPI)

	// After first Refresh, boot state is recorded.
	testutil.Equal(t, sv.apiBootRecorded, true)
	testutil.Equal(t, sv.apiEnabledAtBoot, false) // default is disabled

	apiLabel := func() string {
		sv.setCategory(catRemoteAPI)
		for _, row := range sv.rows {
			if row.kind == srAPI {
				return row.label
			}
		}
		return ""
	}

	t.Run("no hint when state matches boot", func(t *testing.T) {
		testutil.Equal(t, apiLabel(), "Disabled")
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
	sv.setCategory(catLogs)

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
	sv.setCategory(catProjects)

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
	sv.setCategory(catProjects)

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

func TestSettingsView_NewBackendIsHardcoded(t *testing.T) {
	// Backends are hardcoded — 'n' on a backend row must NOT trigger any
	// callback (there is no add path).
	sv := testSettingsView(t)
	sv.setCategory(catBackends)

	for i, row := range sv.rows {
		if row.kind == srBackend {
			sv.cursor = i
			break
		}
	}

	ev := tcell.NewEventKey(tcell.KeyRune, 'n', 0)
	if handled := sv.HandleKey(ev); handled {
		t.Error("'n' key on backend row should be a no-op (backends are hardcoded)")
	}
}

func TestSettingsView_EditBackendIsHardcoded(t *testing.T) {
	// Backends are hardcoded — 'e' on a backend row must NOT trigger any
	// callback.
	sv := testSettingsView(t)
	sv.setCategory(catBackends)

	for i, row := range sv.rows {
		if row.kind == srBackend {
			sv.cursor = i
			break
		}
	}

	ev := tcell.NewEventKey(tcell.KeyRune, 'e', 0)
	if handled := sv.HandleKey(ev); handled {
		t.Error("'e' key on backend row should be a no-op (backends are hardcoded)")
	}
}

func TestSettingsView_NKeyOnNonListCategory(t *testing.T) {
	sv := testSettingsView(t)
	sv.setCategory(catSystem)
	sv.OnNewProject = func() { t.Error("OnNewProject should not fire outside Projects") }

	ev := tcell.NewEventKey(tcell.KeyRune, 'n', 0)
	handled := sv.HandleKey(ev)
	if handled {
		t.Error("'n' should not be handled outside list categories")
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

func TestSettingsView_VaultPathEdit(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	sv := NewSettingsView(database)
	sv.Refresh()
	sv.setCategory(catKnowledgeBase)

	// Verify default path is populated from DB seed.
	if sv.metisVaultPath == "" {
		t.Fatal("metisVaultPath should be populated from DB seed")
	}

	// Verify vault path row exists.
	metisIdx := -1
	for i, row := range sv.rows {
		if row.kind == srVaultPath && row.key == vaultKeyMetis {
			metisIdx = i
		}
	}
	if metisIdx < 0 {
		t.Fatal("no metis vault path row found")
	}

	t.Run("enter starts editing metis", func(t *testing.T) {
		sv.cursor = metisIdx
		sv.handleEnter()
		testutil.Equal(t, sv.editingVault, vaultKeyMetis)
		testutil.Equal(t, sv.editVaultBuf, sv.metisVaultPath)
		testutil.Equal(t, sv.IsEditing(), true)
	})

	t.Run("escape cancels without saving", func(t *testing.T) {
		origPath := sv.metisVaultPath
		sv.editVaultBuf = "/some/other/path"
		sv.handleEditVaultKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
		testutil.Equal(t, sv.editingVault, "")
		testutil.Equal(t, sv.metisVaultPath, origPath) // unchanged
		testutil.Equal(t, sv.IsEditing(), false)
	})

	t.Run("typing appends to buffer", func(t *testing.T) {
		// Re-find row after rebuild.
		for i, row := range sv.rows {
			if row.kind == srVaultPath && row.key == vaultKeyMetis {
				sv.cursor = i
				break
			}
		}
		sv.handleEnter()
		sv.editVaultBuf = "/new"
		sv.handleEditVaultKey(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone))
		sv.handleEditVaultKey(tcell.NewEventKey(tcell.KeyRune, 'p', tcell.ModNone))
		testutil.Equal(t, sv.editVaultBuf, "/new/p")
	})

	t.Run("backspace removes last rune", func(t *testing.T) {
		sv.handleEditVaultKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))
		testutil.Equal(t, sv.editVaultBuf, "/new/")
	})

	t.Run("enter saves metis and persists", func(t *testing.T) {
		sv.editVaultBuf = "/custom/metis/vault"
		sv.handleEditVaultKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
		testutil.Equal(t, sv.editingVault, "")
		testutil.Equal(t, sv.metisVaultPath, "/custom/metis/vault")

		cfg := database.Config()
		testutil.Equal(t, cfg.KB.MetisVaultPath, "/custom/metis/vault")
	})

	t.Run("vault editing blocks global keys", func(t *testing.T) {
		for i, row := range sv.rows {
			if row.kind == srVaultPath && row.key == vaultKeyMetis {
				sv.cursor = i
				break
			}
		}
		sv.handleEnter()
		testutil.Equal(t, sv.IsEditing(), true)

		// 'q' should be captured as a rune, not trigger quit.
		handled := sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone))
		testutil.Equal(t, handled, true)
		testutil.Contains(t, sv.editVaultBuf, "q")

		sv.handleEditVaultKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	})
}

func TestSettingsView_VaultPathRestartHint(t *testing.T) {
	database, _ := db.OpenInMemory()
	sv := NewSettingsView(database)
	sv.Refresh()
	sv.setCategory(catKnowledgeBase)

	vaultLabel := func(key string) string {
		sv.setCategory(catKnowledgeBase)
		for _, row := range sv.rows {
			if row.kind == srVaultPath && row.key == key {
				return row.label
			}
		}
		return ""
	}

	t.Run("no hint initially", func(t *testing.T) {
		label := vaultLabel(vaultKeyMetis)
		if strings.Contains(label, "(restart required)") {
			t.Errorf("should not show restart hint initially, got %q", label)
		}
	})

	t.Run("hint appears after edit", func(t *testing.T) {
		for i, row := range sv.rows {
			if row.kind == srVaultPath && row.key == vaultKeyMetis {
				sv.cursor = i
				break
			}
		}
		sv.handleEnter()
		sv.editVaultBuf = "/changed/path"
		sv.handleEditVaultKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
		testutil.Contains(t, vaultLabel(vaultKeyMetis), "(restart required)")
	})

	t.Run("hint clears after daemon restart", func(t *testing.T) {
		sv.SetDaemonRestarting(false)
		testutil.Equal(t, sv.vaultBootRecorded, false)
		sv.Refresh()
		label := vaultLabel(vaultKeyMetis)
		if strings.Contains(label, "(restart required)") {
			t.Errorf("hint should clear after restart + refresh, got %q", label)
		}
	})
}

func TestSettingsView_VaultPathCycle(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	sv := NewSettingsView(database)
	sv.Refresh()
	sv.setCategory(catKnowledgeBase)

	sv.discoveredVaults = []string{"/vaults/Alpha", "/vaults/Beta", "/vaults/Metis"}

	metisIdx := -1
	for i, row := range sv.rows {
		if row.kind == srVaultPath && row.key == vaultKeyMetis {
			metisIdx = i
		}
	}
	if metisIdx < 0 {
		t.Fatal("vault path row not found")
	}

	t.Run("right arrow cycles metis to first discovered vault", func(t *testing.T) {
		sv.cursor = metisIdx
		sv.cycleVaultPath(1)
		testutil.Equal(t, sv.metisVaultPath, "/vaults/Alpha")

		cfg := database.Config()
		testutil.Equal(t, cfg.KB.MetisVaultPath, "/vaults/Alpha")
	})

	t.Run("right arrow cycles metis forward", func(t *testing.T) {
		sv.cycleVaultPath(1)
		testutil.Equal(t, sv.metisVaultPath, "/vaults/Beta")
	})

	t.Run("left arrow cycles metis backward", func(t *testing.T) {
		sv.cycleVaultPath(-1)
		testutil.Equal(t, sv.metisVaultPath, "/vaults/Alpha")
	})

	t.Run("wraps around at end", func(t *testing.T) {
		sv.metisVaultPath = "/vaults/Metis"
		sv.cycleVaultPath(1)
		testutil.Equal(t, sv.metisVaultPath, "/vaults/Alpha")
	})

	t.Run("wraps around at start", func(t *testing.T) {
		sv.metisVaultPath = "/vaults/Alpha"
		sv.cycleVaultPath(-1)
		testutil.Equal(t, sv.metisVaultPath, "/vaults/Metis")
	})
}

func TestSettingsView_VaultPathCycleNoVaults(t *testing.T) {
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	sv := NewSettingsView(database)
	sv.Refresh()
	sv.setCategory(catKnowledgeBase)

	origMetis := sv.metisVaultPath
	sv.discoveredVaults = nil

	for i, row := range sv.rows {
		if row.kind == srVaultPath && row.key == vaultKeyMetis {
			sv.cursor = i
			break
		}
	}

	// Left/Right should be no-ops.
	sv.cycleVaultPath(1)
	testutil.Equal(t, sv.metisVaultPath, origMetis)

	sv.cycleVaultPath(-1)
	testutil.Equal(t, sv.metisVaultPath, origMetis)
}

func TestSettingsView_VaultPathAutocomplete(t *testing.T) {
	root := setupACDirs(t, "MetisVault", "ArgusVault", "OtherDir")

	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	sv := NewSettingsView(database)
	sv.Refresh()
	sv.setCategory(catKnowledgeBase)

	for i, row := range sv.rows {
		if row.kind == srVaultPath && row.key == vaultKeyMetis {
			sv.cursor = i
			break
		}
	}
	sv.handleEnter()
	testutil.Equal(t, sv.editingVault, vaultKeyMetis)

	t.Run("typing path opens autocomplete", func(t *testing.T) {
		sv.editVaultBuf = root + "/"
		sv.vaultAC.Update(sv.editVaultBuf)
		testutil.Equal(t, sv.vaultAC.Open(), true)
		testutil.Equal(t, len(sv.vaultAC.matches), 3)
	})

	t.Run("typing prefix filters", func(t *testing.T) {
		sv.editVaultBuf = root + "/M"
		sv.vaultAC.Update(sv.editVaultBuf)
		testutil.Equal(t, sv.vaultAC.Open(), true)
		testutil.Equal(t, len(sv.vaultAC.matches), 1)
		testutil.Contains(t, sv.vaultAC.matches[0], "MetisVault")
	})

	t.Run("tab accepts autocomplete", func(t *testing.T) {
		sv.editVaultBuf = root + "/M"
		sv.vaultAC.Update(sv.editVaultBuf)
		sv.handleEditVaultKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
		testutil.Contains(t, sv.editVaultBuf, "MetisVault/")
	})

	t.Run("tab on closed triggers and accepts", func(t *testing.T) {
		sv.editVaultBuf = root + "/A"
		sv.vaultAC.Close()
		testutil.Equal(t, sv.vaultAC.Open(), false)
		sv.handleEditVaultKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
		testutil.Contains(t, sv.editVaultBuf, "ArgusVault/")
	})

	t.Run("down/up navigates", func(t *testing.T) {
		sv.editVaultBuf = root + "/"
		sv.vaultAC.Update(sv.editVaultBuf)
		testutil.Equal(t, sv.vaultAC.idx, 0)

		sv.handleEditVaultKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
		testutil.Equal(t, sv.vaultAC.idx, 1)

		sv.handleEditVaultKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
		testutil.Equal(t, sv.vaultAC.idx, 0)
	})

	t.Run("escape closes autocomplete first", func(t *testing.T) {
		sv.editVaultBuf = root + "/"
		sv.vaultAC.Update(sv.editVaultBuf)
		testutil.Equal(t, sv.vaultAC.Open(), true)

		sv.handleEditVaultKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
		testutil.Equal(t, sv.vaultAC.Open(), false)
		testutil.Equal(t, sv.editingVault, vaultKeyMetis) // still editing
	})

	t.Run("enter accepts autocomplete instead of saving", func(t *testing.T) {
		sv.editVaultBuf = root + "/O"
		sv.vaultAC.Update(sv.editVaultBuf)
		testutil.Equal(t, sv.vaultAC.Open(), true)

		sv.handleEditVaultKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
		testutil.Contains(t, sv.editVaultBuf, "OtherDir/")
		testutil.Equal(t, sv.editingVault, vaultKeyMetis) // still editing, not saved
	})

	t.Run("paste triggers autocomplete", func(t *testing.T) {
		sv.editVaultBuf = ""
		sv.vaultAC.Close()

		paste := sv.PasteHandler()
		paste(root+"/", func(p tview.Primitive) {})

		testutil.Equal(t, sv.editVaultBuf, root+"/")
		testutil.Equal(t, sv.vaultAC.Open(), true)
		testutil.Equal(t, len(sv.vaultAC.matches), 3)
	})

	// Clean up editing state.
	sv.handleEditVaultKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	sv.handleEditVaultKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
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

func TestSettingsView_DeleteProjectCallback(t *testing.T) {
	database, _ := db.OpenInMemory()
	database.SetProject("test-proj", config.Project{Path: "/tmp/test", Branch: "main"})
	sv := NewSettingsView(database)
	sv.Refresh()
	sv.setCategory(catProjects)

	for i, row := range sv.rows {
		if row.kind == srProject && row.key == "test-proj" {
			sv.cursor = i
			break
		}
	}

	var gotName string
	sv.OnDeleteProject = func(name string) { gotName = name }

	ev := tcell.NewEventKey(tcell.KeyRune, 'd', 0)
	handled := sv.HandleKey(ev)
	testutil.Equal(t, handled, true)
	testutil.Equal(t, gotName, "test-proj")
}

func TestSettingsView_DeleteProjectRoundTrip(t *testing.T) {
	database, _ := db.OpenInMemory()
	database.SetProject("proj-a", config.Project{Path: "/tmp/a", Branch: "main"})
	database.SetProject("proj-b", config.Project{Path: "/tmp/b", Branch: "main"})
	sv := NewSettingsView(database)
	sv.Refresh()
	sv.setCategory(catProjects)
	testutil.Equal(t, len(sv.projects), 2)

	sv.OnDeleteProject = func(name string) {
		database.DeleteProject(name)
		sv.Refresh()
	}

	for i, row := range sv.rows {
		if row.kind == srProject && row.key == "proj-a" {
			sv.cursor = i
			break
		}
	}

	ev := tcell.NewEventKey(tcell.KeyRune, 'd', 0)
	sv.HandleKey(ev)

	// After refresh, only proj-b should remain.
	testutil.Equal(t, len(sv.projects), 1)
	testutil.Equal(t, sv.projects[0].Name, "proj-b")
}

func TestSettingsView_DKeyOnBackendSetsDefault(t *testing.T) {
	sv := testSettingsView(t)
	sv.setCategory(catBackends)

	for i, row := range sv.rows {
		if row.kind == srBackend && row.key != sv.defaultBackend {
			sv.cursor = i
			break
		}
	}
	be := sv.SelectedBackend()
	if be == nil {
		t.Fatal("test setup: expected at least one non-default backend")
	}
	oldDefault := sv.defaultBackend

	ev := tcell.NewEventKey(tcell.KeyRune, 'd', 0)
	handled := sv.HandleKey(ev)
	testutil.Equal(t, handled, true)
	if sv.defaultBackend == oldDefault {
		t.Error("expected default backend to change")
	}
}

func TestSettingsView_DKeyOnNonListCategory(t *testing.T) {
	sv := testSettingsView(t)
	sv.setCategory(catSandbox) // Sandbox row doesn't accept 'd'.
	sv.cursor = 0
	ev := tcell.NewEventKey(tcell.KeyRune, 'd', 0)
	handled := sv.HandleKey(ev)
	testutil.Equal(t, handled, false)
}

func TestSettingsView_ScrollClampOnResize(t *testing.T) {
	// Build a Projects category with enough projects to force pane scroll.
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		if err := database.SetProject(fmt.Sprintf("p%02d", i), config.Project{Path: "/tmp/p", Branch: "main"}); err != nil {
			t.Fatal(err)
		}
	}
	sv := NewSettingsView(database)
	sv.Refresh()
	sv.setCategory(catProjects)
	sv.cursor = len(sv.rows) - 1

	screen := tcell.NewSimulationScreen("")
	_ = screen.Init()
	// Small viewport: forces scroll within the right pane items list.
	screen.SetSize(80, 14)
	sv.SetRect(0, 0, 80, 14)
	sv.Draw(screen)
	testutil.Equal(t, sv.scrollOff > 0, true)

	// Grow the viewport — though the items list cap is 8, scrollOff still
	// needs to clamp when there's plenty of room. Reset cursor to 0 so the
	// scroll math has no reason to keep the offset elevated.
	sv.cursor = 0
	screen.SetSize(80, 40)
	sv.SetRect(0, 0, 80, 40)
	sv.Draw(screen)
	testutil.Equal(t, sv.scrollOff, 0)
}

// TestSettingsView_AutoStartRow verifies the auto-start LaunchAgent row appears
// in the Status section on macOS regardless of daemon-connection state. The
// LaunchAgent operates on launchd config and is meaningful even when the
// daemon is offline, so the row is intentionally NOT gated on daemonConnected.
func TestSettingsView_AutoStartRow(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("LaunchAgent only available on darwin")
	}
	for _, daemonConnected := range []bool{true, false} {
		t.Run(fmt.Sprintf("connected=%v", daemonConnected), func(t *testing.T) {
			sv := testSettingsView(t)
			sv.SetDaemonConnected(daemonConnected)
			sv.setCategory(catSystem)

			found := false
			for _, row := range sv.rows {
				if row.kind == srAutoStart {
					found = true
					if !strings.Contains(row.label, "Auto-start at login") {
						t.Errorf("auto-start row label = %q, want to contain 'Auto-start at login'", row.label)
					}
				}
			}
			if !found {
				t.Fatal("expected auto-start row in System category on darwin")
			}
		})
	}
}

// TestSettingsView_AutoStartToggleDispatchesCallback confirms that pressing
// Enter on the auto-start row marks the row busy and fires OnToggleAutoStart
// without performing any launchctl work synchronously on the UI thread.
func TestSettingsView_AutoStartToggleDispatchesCallback(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("LaunchAgent only available on darwin")
	}
	sv := testSettingsView(t)
	sv.setCategory(catSystem)
	var got struct {
		called    bool
		installed bool
	}
	sv.OnToggleAutoStart = func(installed bool) {
		got.called = true
		got.installed = installed
	}

	for i, row := range sv.rows {
		if row.kind == srAutoStart {
			sv.cursor = i
			break
		}
	}
	consumed := sv.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	testutil.Equal(t, consumed, true)
	testutil.Equal(t, got.called, true)
	testutil.Equal(t, sv.autoStartBusy, true)
}

func TestSettings_SetTasksAllStatuses(t *testing.T) {
	sv := makeSettings(t)
	sv.setTasks([]*model.Task{
		{ID: "1", Project: "p", Status: model.StatusPending},
		{ID: "2", Project: "p", Status: model.StatusInProgress},
		{ID: "3", Project: "p", Status: model.StatusInReview},
		{ID: "4", Project: "p", Status: model.StatusComplete},
	})
	c := sv.taskCounts["p"]
	testutil.Equal(t, c.pending, 1)
	testutil.Equal(t, c.inProgress, 1)
	testutil.Equal(t, c.inReview, 1)
	testutil.Equal(t, c.complete, 1)
}

func TestSettings_HandleEdit_Schedule(t *testing.T) {
	d := testDB(t)
	s := &model.ScheduledTask{ID: "id", Name: "x", Project: "p", Prompt: "x", Schedule: "@daily"}
	d.AddSchedule(s)
	sv := NewSettingsView(d)
	sv.Refresh()
	sv.setCategory(catSchedules)
	for i, row := range sv.rows {
		if row.kind == srSchedule && row.key == "id" {
			sv.cursor = i
			break
		}
	}
	called := false
	sv.OnEditSchedule = func(*model.ScheduledTask) { called = true }
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'e', 0))
	testutil.Equal(t, got, true)
	testutil.Equal(t, called, true)
}
