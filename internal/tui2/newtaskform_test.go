package tui2

import (
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestNewTaskForm_Creation(t *testing.T) {
	projects := map[string]config.Project{
		"alpha": {Path: "/tmp/alpha"},
		"beta":  {Path: "/tmp/beta"},
	}
	backends := map[string]config.Backend{
		"claude": {Command: "claude"},
		"codex":  {Command: "codex"},
	}

	f := NewNewTaskForm(projects, "beta", backends, "claude")

	if f.Done() || f.Canceled() {
		t.Error("form should not be done or canceled initially")
	}

	// Default project should be beta
	if f.SelectedProject() != "beta" {
		t.Errorf("default project = %q, want beta", f.SelectedProject())
	}

	// Backend should be claude
	task := f.Task()
	if task.Backend != "claude" {
		t.Errorf("default backend = %q, want claude", task.Backend)
	}
	if task.Status != model.StatusPending {
		t.Errorf("status = %v, want Pending", task.Status)
	}
}

func TestNewTaskForm_TabCycling(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p1": {}},
		"p1",
		map[string]config.Backend{"b1": {}},
		"b1",
	)

	if f.focused != ntFieldPrompt {
		t.Errorf("initial focus = %d, want %d", f.focused, ntFieldPrompt)
	}

	// Tab cycles through fields
	handler := f.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyTab, 0, 0), func(p tview.Primitive) {})
	if f.focused != ntFieldProject {
		t.Errorf("after tab: focus = %d, want %d", f.focused, ntFieldProject)
	}

	handler(tcell.NewEventKey(tcell.KeyTab, 0, 0), func(p tview.Primitive) {})
	if f.focused != ntFieldBackend {
		t.Errorf("after 2nd tab: focus = %d, want %d", f.focused, ntFieldBackend)
	}
}

func TestNewTaskForm_EscapeCancels(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{},
		"",
		map[string]config.Backend{},
		"",
	)

	handler := f.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(p tview.Primitive) {})

	if !f.Canceled() {
		t.Error("escape should cancel the form")
	}
}

func TestNewTaskForm_PromptInput(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}},
		"p",
		map[string]config.Backend{"b": {}},
		"b",
	)

	handler := f.InputHandler()
	// Type "hello"
	for _, r := range "hello" {
		handler(tcell.NewEventKey(tcell.KeyRune, r, 0), func(p tview.Primitive) {})
	}

	task := f.Task()
	if task.Name != "hello" {
		t.Errorf("task name = %q, want hello", task.Name)
	}
}

func TestNewTaskForm_EnterSubmits(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}},
		"p",
		map[string]config.Backend{"b": {}},
		"b",
	)

	handler := f.InputHandler()
	// Type something first
	handler(tcell.NewEventKey(tcell.KeyRune, 'x', 0), func(p tview.Primitive) {})
	// Submit
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) {})

	if !f.Done() {
		t.Error("enter with non-empty prompt should submit")
	}
}

func TestNewTaskForm_EnterEmptyNoSubmit(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}},
		"p",
		map[string]config.Backend{"b": {}},
		"b",
	)

	handler := f.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) {})

	if f.Done() {
		t.Error("enter with empty prompt should not submit")
	}
}

func TestItoa(t *testing.T) {
	if got := itoa(0); got != "0" {
		t.Errorf("itoa(0) = %q", got)
	}
	if got := itoa(42); got != "42" {
		t.Errorf("itoa(42) = %q", got)
	}
	if got := itoa(100); got != "100" {
		t.Errorf("itoa(100) = %q", got)
	}
}
