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

func testBackends() map[string]config.Backend {
	return map[string]config.Backend{
		"claude": {Command: "claude --dangerously-skip-permissions"},
		"codex":  {Command: "codex --dangerously-bypass-approvals-and-sandbox"},
	}
}

func TestNewTaskForm_ProjectListSorted(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")

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
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")

	if got := f.SelectedProject(); got != "alpha" {
		t.Errorf("SelectedProject() = %q, want %q", got, "alpha")
	}
}

func TestNewTaskForm_RightCyclesForward(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")
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
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")
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
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")

	// Starts on prompt
	if f.focused != fieldPrompt {
		t.Fatalf("expected initial focus on prompt, got %d", f.focused)
	}

	// Tab goes to project
	f.Update(tea.KeyMsg{Type: tea.KeyTab})
	if f.focused != fieldProject {
		t.Errorf("after tab: focused = %d, want %d", f.focused, fieldProject)
	}

	// Tab goes to backend
	f.Update(tea.KeyMsg{Type: tea.KeyTab})
	if f.focused != fieldBackend {
		t.Errorf("after tab x2: focused = %d, want %d", f.focused, fieldBackend)
	}

	// Tab goes back to prompt
	f.Update(tea.KeyMsg{Type: tea.KeyTab})
	if f.focused != fieldPrompt {
		t.Errorf("after tab x3: focused = %d, want %d", f.focused, fieldPrompt)
	}
}

func TestNewTaskForm_EnterOnProjectMovesToBackend(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")
	f.focused = fieldProject

	f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if f.focused != fieldBackend {
		t.Errorf("after enter on project: focused = %d, want %d", f.focused, fieldBackend)
	}
}

func TestNewTaskForm_TaskUsesBranchFromProject(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")
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
	f := NewNewTaskForm(theme, map[string]config.Project{}, "", testBackends(), "claude")

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
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")

	f.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !f.Canceled() {
		t.Error("expected Canceled() to be true after esc")
	}
}

func TestNewTaskForm_SubmitRequiresPrompt(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")

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
	f := NewNewTaskForm(theme, testProjects(), "bravo", testBackends(), "claude")
	if got := f.SelectedProject(); got != "bravo" {
		t.Errorf("SelectedProject() = %q, want %q", got, "bravo")
	}

	// Unknown project falls back to first
	f2 := NewNewTaskForm(theme, testProjects(), "unknown", testBackends(), "claude")
	if got := f2.SelectedProject(); got != "alpha" {
		t.Errorf("SelectedProject() with unknown default = %q, want %q", got, "alpha")
	}
}

func TestNewTaskForm_BackendSelectorDefaultsToDefault(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")

	if got := f.SelectedBackend(); got != "claude" {
		t.Errorf("SelectedBackend() = %q, want claude", got)
	}
	// Backend names should be claude, codex (sorted, no "(default)")
	if len(f.backendNames) != 2 {
		t.Fatalf("expected 2 backend names, got %d: %v", len(f.backendNames), f.backendNames)
	}
	if f.backendNames[0] != "claude" {
		t.Errorf("backendNames[0] = %q, want claude", f.backendNames[0])
	}
}

func TestNewTaskForm_BackendSelectorCycles(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")
	f.focused = fieldBackend

	// Right cycles: claude → codex → claude (wrap)
	f.Update(tea.KeyMsg{Type: tea.KeyRight})
	if got := f.SelectedBackend(); got != "codex" {
		t.Errorf("after right: SelectedBackend() = %q, want codex", got)
	}

	f.Update(tea.KeyMsg{Type: tea.KeyRight})
	if got := f.SelectedBackend(); got != "claude" {
		t.Errorf("after right x2 (wrap): SelectedBackend() = %q, want claude", got)
	}
}

func TestNewTaskForm_TaskIncludesBackend(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")
	f.focused = fieldBackend

	// Select codex (starts at claude, one right moves to codex)
	f.Update(tea.KeyMsg{Type: tea.KeyRight}) // codex

	f.focused = fieldPrompt
	f.promptInput.SetValue("fix the bug")

	task := f.Task()
	if task.Backend != "codex" {
		t.Errorf("task.Backend = %q, want codex", task.Backend)
	}
}

func TestNewTaskForm_TaskDefaultBackendIsPreselected(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")

	f.focused = fieldPrompt
	f.promptInput.SetValue("fix the bug")

	task := f.Task()
	if task.Backend != "claude" {
		t.Errorf("task.Backend = %q, want claude (pre-selected default)", task.Backend)
	}
}

func TestNewTaskForm_TextareaWrapsAndExpands(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")
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
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")
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

func TestFilterSkills(t *testing.T) {
	skills := []SkillItem{
		{Name: "bisect", Description: "Bisect commits"},
		{Name: "changelog", Description: "Generate changelog"},
		{Name: "debug", Description: "Debug issue"},
		{Name: "deploy", Description: "Deploy app"},
	}

	tests := []struct {
		prefix string
		want   []string
	}{
		{"", []string{"bisect", "changelog", "debug", "deploy"}},
		{"d", []string{"debug", "deploy"}},
		{"de", []string{"debug", "deploy"}},
		{"dep", []string{"deploy"}},
		{"ch", []string{"changelog"}},
		{"z", nil},
	}
	for _, tt := range tests {
		got := filterSkills(skills, tt.prefix)
		if len(got) != len(tt.want) {
			t.Errorf("filterSkills(%q): got %d results, want %d", tt.prefix, len(got), len(tt.want))
			continue
		}
		for i, s := range got {
			if s.Name != tt.want[i] {
				t.Errorf("filterSkills(%q)[%d] = %q, want %q", tt.prefix, i, s.Name, tt.want[i])
			}
		}
	}
}

func testFormWithSkills(skills []SkillItem) NewTaskForm {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")
	f.skills = skills
	f.SetSize(80, 40)
	return f
}

func testSkills() []SkillItem {
	return []SkillItem{
		{Name: "debug", Description: "Debug issue"},
		{Name: "deploy", Description: "Deploy app"},
		{Name: "deps", Description: "Audit dependencies"},
		{Name: "pr", Description: "Open a PR"},
	}
}

func TestNewTaskForm_AutocompleteOpensOnSlash(t *testing.T) {
	f := testFormWithSkills(testSkills())

	// Type "/" — should open autocomplete with all skills
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})

	if !f.acOpen {
		t.Error("acOpen should be true after typing '/'")
	}
	if len(f.acMatches) != 4 {
		t.Errorf("acMatches = %d, want 4", len(f.acMatches))
	}
}

func TestNewTaskForm_AutocompleteFiltersOnType(t *testing.T) {
	f := testFormWithSkills(testSkills())

	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})

	if !f.acOpen {
		t.Error("acOpen should be true after '/d'")
	}
	// debug, deploy, deps — 3 matches
	if len(f.acMatches) != 3 {
		t.Errorf("acMatches = %d, want 3", len(f.acMatches))
	}
}

func TestNewTaskForm_AutocompleteClosesOnSpace(t *testing.T) {
	f := testFormWithSkills(testSkills())

	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})

	if f.acOpen {
		t.Error("acOpen should be false after space")
	}
}

func TestNewTaskForm_AutocompleteClosesOnNoMatch(t *testing.T) {
	f := testFormWithSkills(testSkills())

	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})

	if f.acOpen {
		t.Error("acOpen should be false when no matches")
	}
}

func TestNewTaskForm_AutocompleteNavigationDown(t *testing.T) {
	f := testFormWithSkills(testSkills())

	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if f.acIdx != 0 {
		t.Fatalf("initial acIdx = %d, want 0", f.acIdx)
	}

	f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if f.acIdx != 1 {
		t.Errorf("after down: acIdx = %d, want 1", f.acIdx)
	}

	f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if f.acIdx != 2 {
		t.Errorf("after down x2: acIdx = %d, want 2", f.acIdx)
	}
}

func TestNewTaskForm_AutocompleteNavigationWrap(t *testing.T) {
	f := testFormWithSkills(testSkills())

	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})

	// Wrap down past last item (4 skills)
	for i := 0; i < 4; i++ {
		f.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if f.acIdx != 0 {
		t.Errorf("after 4 downs (wrap): acIdx = %d, want 0", f.acIdx)
	}

	// Wrap up past first item
	f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if f.acIdx != 3 {
		t.Errorf("after up from 0 (wrap): acIdx = %d, want 3", f.acIdx)
	}
}

func TestNewTaskForm_AutocompleteSelectOnEnter(t *testing.T) {
	f := testFormWithSkills(testSkills())

	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	f.Update(tea.KeyMsg{Type: tea.KeyDown}) // select deploy (index 1)

	f.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Should close autocomplete
	if f.acOpen {
		t.Error("acOpen should be false after enter")
	}
	// Should not submit form (enter with autocomplete just selects)
	if f.Done() {
		t.Error("Done() should be false — enter on autocomplete selects, not submits")
	}
	// Value should be the selected skill name
	val := f.promptInput.Value()
	if !strings.HasPrefix(val, "/deploy") {
		t.Errorf("prompt value = %q, want '/deploy ...'", val)
	}
}

func TestNewTaskForm_AutocompleteEscClosesDropdown(t *testing.T) {
	f := testFormWithSkills(testSkills())

	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !f.acOpen {
		t.Fatal("acOpen should be true")
	}

	f.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if f.acOpen {
		t.Error("acOpen should be false after esc")
	}
	// Form should NOT be canceled — esc on autocomplete only closes it
	if f.Canceled() {
		t.Error("form should not be canceled when esc closes autocomplete")
	}
}

func TestNewTaskForm_AutocompleteEscTwiceCancels(t *testing.T) {
	f := testFormWithSkills(testSkills())

	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	f.Update(tea.KeyMsg{Type: tea.KeyEsc}) // close autocomplete
	f.Update(tea.KeyMsg{Type: tea.KeyEsc}) // cancel form

	if !f.Canceled() {
		t.Error("form should be canceled after second esc")
	}
}

func TestNewTaskForm_AutocompleteViewRendersDropdown(t *testing.T) {
	f := testFormWithSkills(testSkills())
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})

	view := f.View()
	if !strings.Contains(view, "/pr") {
		t.Errorf("View() missing '/pr' in autocomplete: %q", view)
	}
}

func TestNewTaskForm_AutocompleteScrolling(t *testing.T) {
	// Create more skills than acMaxVisible (6)
	skills := []SkillItem{
		{Name: "a1", Description: "A1"},
		{Name: "a2", Description: "A2"},
		{Name: "a3", Description: "A3"},
		{Name: "a4", Description: "A4"},
		{Name: "a5", Description: "A5"},
		{Name: "a6", Description: "A6"},
		{Name: "a7", Description: "A7"},
	}
	f := testFormWithSkills(skills)

	f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if len(f.acMatches) != 7 {
		t.Fatalf("acMatches = %d, want 7", len(f.acMatches))
	}

	// Scroll down past visible window
	for i := 0; i < acMaxVisible; i++ {
		f.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	// acIdx = 6, acScroll should have moved
	if f.acIdx != acMaxVisible {
		t.Errorf("acIdx = %d, want %d", f.acIdx, acMaxVisible)
	}
	if f.acScroll == 0 {
		t.Error("acScroll should be > 0 after scrolling past visible window")
	}
}

func TestNewTaskForm_VisualLineCount(t *testing.T) {
	theme := DefaultTheme()
	f := NewNewTaskForm(theme, testProjects(), "", testBackends(), "claude")
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
