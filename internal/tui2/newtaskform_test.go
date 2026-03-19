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

func TestNewTaskForm_WrapPrompt(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)

	// Empty prompt → single empty line
	lines := f.wrapPrompt(10)
	if len(lines) != 1 {
		t.Errorf("empty prompt: got %d lines, want 1", len(lines))
	}

	// Short prompt that fits in one line
	f.prompt = []rune("hello")
	f.cursorPos = 5
	lines = f.wrapPrompt(10)
	if len(lines) != 1 {
		t.Errorf("short prompt: got %d lines, want 1", len(lines))
	}
	if lines[0].start != 0 || lines[0].length != 5 {
		t.Errorf("short prompt line = {%d, %d}, want {0, 5}", lines[0].start, lines[0].length)
	}

	// Prompt that wraps to 3 lines (25 chars, width 10)
	f.prompt = []rune("abcdefghijklmnopqrstuvwxy")
	f.cursorPos = 25
	lines = f.wrapPrompt(10)
	if len(lines) != 3 {
		t.Errorf("25-char prompt w=10: got %d lines, want 3", len(lines))
	}
	if lines[0].start != 0 || lines[0].length != 10 {
		t.Errorf("line 0 = {%d, %d}, want {0, 10}", lines[0].start, lines[0].length)
	}
	if lines[1].start != 10 || lines[1].length != 10 {
		t.Errorf("line 1 = {%d, %d}, want {10, 10}", lines[1].start, lines[1].length)
	}
	if lines[2].start != 20 || lines[2].length != 5 {
		t.Errorf("line 2 = {%d, %d}, want {20, 5}", lines[2].start, lines[2].length)
	}
}

func TestNewTaskForm_CursorWrappedPos(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
	f.prompt = []rune("abcdefghijklmnop") // 16 chars

	tests := []struct {
		cursorPos int
		width     int
		wantLine  int
		wantCol   int
	}{
		{0, 10, 0, 0},
		{5, 10, 0, 5},
		{10, 10, 1, 0},
		{15, 10, 1, 5},
	}

	for _, tt := range tests {
		f.cursorPos = tt.cursorPos
		line, col := f.cursorWrappedPos(tt.width)
		if line != tt.wantLine || col != tt.wantCol {
			t.Errorf("cursorPos=%d width=%d: got (%d,%d), want (%d,%d)",
				tt.cursorPos, tt.width, line, col, tt.wantLine, tt.wantCol)
		}
	}
}

func TestNewTaskForm_CursorUpDown(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
	// Set cached prompt width (normally set by Draw)
	f.promptWidth = 56

	// 120 chars at width 56 → wraps. Cursor at pos 59 → line 1, col 3
	f.prompt = make([]rune, 120)
	for i := range f.prompt {
		f.prompt[i] = 'a'
	}
	f.cursorPos = 59 // on second wrapped line (line 1), col 3

	// Move up should go to first line, same column
	moved := f.moveCursorUp()
	if !moved {
		t.Error("moveCursorUp should return true when not on first line")
	}
	if f.cursorPos != 3 {
		t.Errorf("after move up: cursorPos = %d, want 3", f.cursorPos)
	}

	// Move up again — already on first line, should return false
	moved = f.moveCursorUp()
	if moved {
		t.Error("moveCursorUp should return false when on first line")
	}

	// Move down should go back to second line
	f.moveCursorDown()
	if f.cursorPos != 59 {
		t.Errorf("after move down: cursorPos = %d, want 59", f.cursorPos)
	}
}

func TestNewTaskForm_EnsureCursorVisible(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)

	// 100 chars, width 10 → 10 lines, visible 5
	f.prompt = make([]rune, 100)
	for i := range f.prompt {
		f.prompt[i] = 'x'
	}

	// Cursor at end (line 9), visible 5
	f.cursorPos = 95
	f.scrollOffset = 0
	f.ensureCursorVisible(10, 5)
	if f.scrollOffset != 5 {
		t.Errorf("scrollOffset = %d, want 5", f.scrollOffset)
	}

	// Cursor at start (line 0), scrollOffset was 5
	f.cursorPos = 3
	f.ensureCursorVisible(10, 5)
	if f.scrollOffset != 0 {
		t.Errorf("scrollOffset = %d, want 0", f.scrollOffset)
	}
}

func TestNewTaskForm_UpArrowLeavesPrompt(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
	f.promptWidth = 56

	// Short prompt — cursor on first line, up arrow should leave prompt field
	f.prompt = []rune("short")
	f.cursorPos = 3
	handler := f.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyUp, 0, 0), func(p tview.Primitive) {})
	if f.focused != ntFieldBackend {
		t.Errorf("up on first line should move to backend, got focused=%d", f.focused)
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
