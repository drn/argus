package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/drn/argus/internal/config"
)

func testProjects() map[string]config.Project {
	return map[string]config.Project{
		"alpha":   {Path: "/tmp/alpha", Branch: "main"},
		"bravo":   {Path: "/tmp/bravo", Branch: "develop"},
		"charlie": {Path: "/tmp/charlie"},
	}
}

func TestNewTaskForm_ProjectListSorted(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects())

	if len(f.projectNames) != 3 {
		t.Fatalf("expected 3 project names, got %d", len(f.projectNames))
	}
	expected := []string{"alpha", "bravo", "charlie"}
	for i, name := range expected {
		if f.projectNames[i] != name {
			t.Errorf("projectNames[%d] = %q, want %q", i, f.projectNames[i], name)
		}
	}
}

func TestNewTaskForm_DefaultSelectsFirst(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects())

	if got := f.SelectedProject(); got != "alpha" {
		t.Errorf("SelectedProject() = %q, want %q", got, "alpha")
	}
}

func TestNewTaskForm_RightCyclesForward(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects())
	f.focused = fieldProject

	f.Update(tea.KeyMsg{Type: tea.KeyRight})
	if got := f.SelectedProject(); got != "bravo" {
		t.Errorf("after right: SelectedProject() = %q, want %q", got, "bravo")
	}

	f.Update(tea.KeyMsg{Type: tea.KeyRight})
	if got := f.SelectedProject(); got != "charlie" {
		t.Errorf("after right x2: SelectedProject() = %q, want %q", got, "charlie")
	}

	// Wraps around
	f.Update(tea.KeyMsg{Type: tea.KeyRight})
	if got := f.SelectedProject(); got != "alpha" {
		t.Errorf("after right x3 (wrap): SelectedProject() = %q, want %q", got, "alpha")
	}
}

func TestNewTaskForm_LeftCyclesBackward(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects())
	f.focused = fieldProject

	// Wraps to last
	f.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if got := f.SelectedProject(); got != "charlie" {
		t.Errorf("after left (wrap): SelectedProject() = %q, want %q", got, "charlie")
	}

	f.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if got := f.SelectedProject(); got != "bravo" {
		t.Errorf("after left x2: SelectedProject() = %q, want %q", got, "bravo")
	}
}

func TestNewTaskForm_TabNavigatesBetweenFields(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects())

	// Starts on prompt
	if f.focused != fieldPrompt {
		t.Fatalf("expected initial focus on prompt, got %d", f.focused)
	}

	// Tab goes to project
	f.Update(tea.KeyMsg{Type: tea.KeyTab})
	if f.focused != fieldProject {
		t.Errorf("after tab: focused = %d, want %d", f.focused, fieldProject)
	}

	// Tab goes back to prompt
	f.Update(tea.KeyMsg{Type: tea.KeyTab})
	if f.focused != fieldPrompt {
		t.Errorf("after tab x2: focused = %d, want %d", f.focused, fieldPrompt)
	}
}

func TestNewTaskForm_EnterOnProjectMovesToPrompt(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects())
	f.focused = fieldProject

	f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if f.focused != fieldPrompt {
		t.Errorf("after enter on project: focused = %d, want %d", f.focused, fieldPrompt)
	}
}

func TestNewTaskForm_TaskUsesBranchFromProject(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects())
	f.focused = fieldProject

	// Select bravo (has branch "develop")
	f.Update(tea.KeyMsg{Type: tea.KeyRight})

	// Set prompt
	f.focused = fieldPrompt
	f.promptInput.SetValue("fix the bug")

	task := f.Task()
	if task.Project != "bravo" {
		t.Errorf("task.Project = %q, want %q", task.Project, "bravo")
	}
	if task.Branch != "develop" {
		t.Errorf("task.Branch = %q, want %q", task.Branch, "develop")
	}
}

func TestNewTaskForm_EmptyProjects(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, map[string]config.Project{})

	if got := f.SelectedProject(); got != "" {
		t.Errorf("SelectedProject() with no projects = %q, want empty", got)
	}

	// Left/right should not panic
	f.focused = fieldProject
	f.Update(tea.KeyMsg{Type: tea.KeyRight})
	f.Update(tea.KeyMsg{Type: tea.KeyLeft})

	if got := f.SelectedProject(); got != "" {
		t.Errorf("SelectedProject() still empty = %q, want empty", got)
	}
}

func TestNewTaskForm_EscCancels(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects())

	f.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !f.Canceled() {
		t.Error("expected Canceled() to be true after esc")
	}
}

func TestNewTaskForm_SubmitRequiresPrompt(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects())

	// Enter with empty prompt should not submit
	f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if f.Done() {
		t.Error("should not be done with empty prompt")
	}

	// Set prompt and submit
	f.promptInput.SetValue("do something")
	f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !f.Done() {
		t.Error("expected Done() to be true after enter with prompt")
	}
}
