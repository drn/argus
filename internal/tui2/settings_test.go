package tui2

import (
	"fmt"
	"os"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
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

func TestTailFile(t *testing.T) {
	// Non-existent file.
	lines := tailFile("/nonexistent/path", 10)
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

	// Tail last 5 lines.
	lines = tailFile(f.Name(), 5)
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(lines))
	}
	if lines[0] != "line 15" {
		t.Errorf("first tail line = %q, want 'line 15'", lines[0])
	}
	if lines[4] != "line 19" {
		t.Errorf("last tail line = %q, want 'line 19'", lines[4])
	}
}
