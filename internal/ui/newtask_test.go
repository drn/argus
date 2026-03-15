package ui

import (
	"strings"
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
	f := NewNewTaskForm(theme, testProjects(), "")

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
	f := NewNewTaskForm(theme, testProjects(), "")

	if got := f.SelectedProject(); got != "alpha" {
		t.Errorf("SelectedProject() = %q, want %q", got, "alpha")
	}
}

func TestNewTaskForm_RightCyclesForward(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "")
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
	f := NewNewTaskForm(theme, testProjects(), "")
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
	f := NewNewTaskForm(theme, testProjects(), "")

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
	f := NewNewTaskForm(theme, testProjects(), "")
	f.focused = fieldProject

	f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if f.focused != fieldPrompt {
		t.Errorf("after enter on project: focused = %d, want %d", f.focused, fieldPrompt)
	}
}

func TestNewTaskForm_TaskUsesBranchFromProject(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "")
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
	f := NewNewTaskForm(theme, map[string]config.Project{}, "")

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
	f := NewNewTaskForm(theme, testProjects(), "")

	f.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !f.Canceled() {
		t.Error("expected Canceled() to be true after esc")
	}
}

func TestNewTaskForm_SubmitRequiresPrompt(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "")

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

func TestNewTaskForm_DefaultProject(t *testing.T) {
	theme := DefaultTheme()

	// Default to "bravo" — should be index 1 in sorted list [alpha, bravo, charlie]
	f := NewNewTaskForm(theme, testProjects(), "bravo")
	if got := f.SelectedProject(); got != "bravo" {
		t.Errorf("SelectedProject() = %q, want %q", got, "bravo")
	}

	// Unknown project falls back to first
	f2 := NewNewTaskForm(theme, testProjects(), "unknown")
	if got := f2.SelectedProject(); got != "alpha" {
		t.Errorf("SelectedProject() with unknown default = %q, want %q", got, "alpha")
	}
}

func TestNewTaskForm_TextareaWrapsAndExpands(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "")
	f.SetSize(80, 40)

	// Textarea should start at height 1
	view := f.promptInput.View()
	lines := strings.Split(view, "\n")
	// With height=1 and no content, should be minimal
	if len(lines) > 3 {
		t.Errorf("empty textarea should be small, got %d lines", len(lines))
	}

	// Set a multi-line value with explicit newlines
	f.promptInput.SetValue("line one\nline two\nline three")
	// Trigger an update so auto-resize kicks in
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})

	lineCount := f.promptInput.LineCount()
	if lineCount < 3 {
		t.Errorf("expected at least 3 lines for multi-line input, got %d", lineCount)
	}

	// View should render without issue
	fullView := f.View()
	if fullView == "" {
		t.Error("View() returned empty string")
	}
	if !strings.Contains(fullView, "New Task") {
		t.Error("View() missing 'New Task' title")
	}
}

// TestNewTaskForm_WordWrapHeightTransitions tests that height updates correctly
// when typing real words that trigger word wrapping (not character wrapping).
// Verifies heights increase monotonically and all content stays visible.
func TestNewTaskForm_WordWrapHeightTransitions(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "")
	f.SetSize(80, 40)

	words := strings.Fields("implement a new feature that allows users to search through their task history and filter by project name and status")

	prevHeight := 1
	totalChars := 0
	for _, word := range words {
		if totalChars > 0 {
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
			totalChars++
		}
		for _, ch := range word {
			f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
			totalChars++
		}

		h := f.promptInput.Height()
		if h != prevHeight {
			if h < prevHeight {
				t.Errorf("height decreased from %d to %d at %d chars", prevHeight, h, totalChars)
			}
			if h > prevHeight+1 {
				t.Errorf("height jumped from %d to %d (skipped) at %d chars", prevHeight, h, totalChars)
			}
			prevHeight = h
		}
	}

	// Every word in the value should appear in the view (no clipping)
	view := f.promptInput.View()
	for _, word := range strings.Fields(f.promptInput.Value()) {
		if !strings.Contains(view, word) {
			t.Errorf("word %q not found in view — content may be clipped", word)
		}
	}
}

func TestNewTaskForm_VisualLineCount(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "")
	f.SetSize(80, 40) // modal width ~32, input width ~28

	// Empty value = 1 visual line
	f.promptInput.SetValue("")
	if got := f.visualLineCount(); got != 1 {
		t.Errorf("empty: visualLineCount() = %d, want 1", got)
	}

	// Short text that fits in one line
	f.promptInput.SetValue("hello")
	if got := f.visualLineCount(); got != 1 {
		t.Errorf("short: visualLineCount() = %d, want 1", got)
	}

	// Long text that should soft-wrap
	inputWidth := f.promptInput.Width()
	longText := strings.Repeat("a", inputWidth*2+5)
	f.promptInput.SetValue(longText)
	got := f.visualLineCount()
	if got != 3 {
		t.Errorf("long text (%d chars, width %d): visualLineCount() = %d, want 3", len(longText), inputWidth, got)
	}

	// Hard newlines (pasted text) — returns maxPromptLines to let textarea scroll
	mixedText := strings.Repeat("b", inputWidth+1) + "\nshort"
	f.promptInput.SetValue(mixedText)
	got = f.visualLineCount()
	if got != maxPromptLines {
		t.Errorf("mixed: visualLineCount() = %d, want %d (maxPromptLines for pasted multi-line)", got, maxPromptLines)
	}
}
