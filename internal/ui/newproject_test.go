package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewProjectForm_Defaults(t *testing.T) {
	f := NewNewProjectForm(DefaultTheme())
	if f.Done() {
		t.Error("new form should not be done")
	}
	if f.Canceled() {
		t.Error("new form should not be canceled")
	}
	if f.focused != projFieldName {
		t.Errorf("expected initial focus on name field, got %d", f.focused)
	}
}

func TestNewProjectForm_EscCancels(t *testing.T) {
	f := NewNewProjectForm(DefaultTheme())
	f.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !f.Canceled() {
		t.Error("esc should cancel the form")
	}
}

func TestNewProjectForm_TabNavigation(t *testing.T) {
	f := NewNewProjectForm(DefaultTheme())

	// Tab: name -> path
	f.Update(tea.KeyMsg{Type: tea.KeyTab})
	if f.focused != projFieldPath {
		t.Errorf("after tab: focused = %d, want %d", f.focused, projFieldPath)
	}

	// Tab: path -> branch
	f.Update(tea.KeyMsg{Type: tea.KeyTab})
	if f.focused != projFieldBranch {
		t.Errorf("after tab x2: focused = %d, want %d", f.focused, projFieldBranch)
	}

	// Tab: branch -> backend
	f.Update(tea.KeyMsg{Type: tea.KeyTab})
	if f.focused != projFieldBackend {
		t.Errorf("after tab x3: focused = %d, want %d", f.focused, projFieldBackend)
	}

	// Tab: backend -> wraps to name
	f.Update(tea.KeyMsg{Type: tea.KeyTab})
	if f.focused != projFieldName {
		t.Errorf("after tab x4: focused = %d, want %d (wrap)", f.focused, projFieldName)
	}
}

func TestNewProjectForm_ShiftTabNavigation(t *testing.T) {
	f := NewNewProjectForm(DefaultTheme())

	// Shift+tab: name -> wraps to backend
	f.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if f.focused != projFieldBackend {
		t.Errorf("after shift+tab: focused = %d, want %d", f.focused, projFieldBackend)
	}

	// Shift+tab: backend -> branch
	f.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if f.focused != projFieldBranch {
		t.Errorf("after shift+tab x2: focused = %d, want %d", f.focused, projFieldBranch)
	}
}

func TestNewProjectForm_EnterSubmits(t *testing.T) {
	f := NewNewProjectForm(DefaultTheme())

	// Set required fields
	f.inputs[projFieldName].SetValue("myproject")
	f.inputs[projFieldPath].SetValue("/tmp/myproject")

	// Move to last field (backend)
	f.focused = projFieldBackend

	f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !f.Done() {
		t.Error("enter on last field with name+path should submit")
	}
}

func TestNewProjectForm_EnterWithoutNameDoesNotSubmit(t *testing.T) {
	f := NewNewProjectForm(DefaultTheme())

	// Only set path, not name
	f.inputs[projFieldPath].SetValue("/tmp/myproject")
	f.focused = projFieldBackend

	f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if f.Done() {
		t.Error("enter without name should not submit")
	}
}

func TestNewProjectForm_EnterWithoutPathDoesNotSubmit(t *testing.T) {
	f := NewNewProjectForm(DefaultTheme())

	// Only set name, not path
	f.inputs[projFieldName].SetValue("myproject")
	f.focused = projFieldBackend

	f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if f.Done() {
		t.Error("enter without path should not submit")
	}
}

func TestNewProjectForm_EnterOnNonLastFieldAdvances(t *testing.T) {
	f := NewNewProjectForm(DefaultTheme())
	f.focused = projFieldName

	f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if f.focused != projFieldPath {
		t.Errorf("enter on name should advance to path, got %d", f.focused)
	}
}

func TestNewProjectForm_ProjectEntry(t *testing.T) {
	f := NewNewProjectForm(DefaultTheme())
	f.inputs[projFieldName].SetValue("testproj")
	f.inputs[projFieldPath].SetValue("/home/user/testproj")
	f.inputs[projFieldBranch].SetValue("develop")
	f.inputs[projFieldBackend].SetValue("claude")

	name, proj := f.ProjectEntry()
	if name != "testproj" {
		t.Errorf("name = %q, want 'testproj'", name)
	}
	if proj.Path != "/home/user/testproj" {
		t.Errorf("path = %q, want '/home/user/testproj'", proj.Path)
	}
	if proj.Branch != "develop" {
		t.Errorf("branch = %q, want 'develop'", proj.Branch)
	}
	if proj.Backend != "claude" {
		t.Errorf("backend = %q, want 'claude'", proj.Backend)
	}
}

func TestNewProjectForm_DefaultBranch(t *testing.T) {
	f := NewNewProjectForm(DefaultTheme())
	if f.inputs[projFieldBranch].Value() != "master" {
		t.Errorf("default branch = %q, want 'master'", f.inputs[projFieldBranch].Value())
	}
}

func TestNewProjectForm_SetSize(t *testing.T) {
	f := NewNewProjectForm(DefaultTheme())
	f.SetSize(120, 40)
	if f.width != 120 || f.height != 40 {
		t.Errorf("SetSize(120,40) gave width=%d height=%d", f.width, f.height)
	}
	// Inputs should have non-zero width
	for i, input := range f.inputs {
		if input.Width <= 0 {
			t.Errorf("input[%d] width should be positive after SetSize, got %d", i, input.Width)
		}
	}
}

func TestNewProjectForm_ModalWidth(t *testing.T) {
	f := NewNewProjectForm(DefaultTheme())

	// Small width: should be at least 50
	f.width = 60
	if w := f.modalWidth(); w < 50 {
		t.Errorf("modalWidth() with width=60 = %d, want >= 50", w)
	}

	// Large width: should be capped at 80
	f.width = 300
	if w := f.modalWidth(); w > 80 {
		t.Errorf("modalWidth() with width=300 = %d, want <= 80", w)
	}
}

func TestNewProjectForm_DetectBranchMsg(t *testing.T) {
	f := NewNewProjectForm(DefaultTheme())

	// Default is "master" — detectBranchMsg should override it.
	f.Update(detectBranchMsg{branch: "origin/main"})
	if got := f.inputs[projFieldBranch].Value(); got != "origin/main" {
		t.Errorf("branch after detectBranchMsg = %q, want %q", got, "origin/main")
	}
}

func TestNewProjectForm_DetectBranchMsg_NoOverrideCustom(t *testing.T) {
	f := NewNewProjectForm(DefaultTheme())

	// User manually set a custom branch — should NOT be overridden.
	f.inputs[projFieldBranch].SetValue("develop")
	f.Update(detectBranchMsg{branch: "origin/main"})
	if got := f.inputs[projFieldBranch].Value(); got != "develop" {
		t.Errorf("branch should remain %q, got %q", "develop", got)
	}
}

func TestDetectRemoteDefaultBranch_BadPath(t *testing.T) {
	// Non-existent directory should return empty string, not panic.
	result := detectRemoteDefaultBranch("/nonexistent/path")
	if result != "" {
		t.Errorf("expected empty string for bad path, got %q", result)
	}
}

func TestNewProjectForm_View(t *testing.T) {
	f := NewNewProjectForm(DefaultTheme())
	f.SetSize(100, 30)

	view := f.View()
	if !strings.Contains(view, "New Project") {
		t.Error("view should contain 'New Project' title")
	}
	if !strings.Contains(view, "Name:") {
		t.Error("view should contain 'Name:' label")
	}
	if !strings.Contains(view, "Path:") {
		t.Error("view should contain 'Path:' label")
	}
	if !strings.Contains(view, "esc") {
		t.Error("view should contain esc key hint")
	}
}
