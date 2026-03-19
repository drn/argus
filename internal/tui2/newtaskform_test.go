package tui2

import (
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/skills"
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
	f.promptWidth = 10
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

func TestNewTaskForm_CircularNav(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
	f.promptWidth = 56
	handler := f.InputHandler()

	// Start on prompt, short text — down on last line wraps to project
	f.prompt = []rune("short")
	f.cursorPos = 3
	handler(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(p tview.Primitive) {})
	if f.focused != ntFieldProject {
		t.Errorf("down on last prompt line: focused=%d, want %d (project)", f.focused, ntFieldProject)
	}

	// Up on project wraps to prompt
	handler(tcell.NewEventKey(tcell.KeyUp, 0, 0), func(p tview.Primitive) {})
	if f.focused != ntFieldPrompt {
		t.Errorf("up on project: focused=%d, want %d (prompt)", f.focused, ntFieldPrompt)
	}
}

func TestNewTaskForm_SetErrorResetsDone(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
	handler := f.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRune, 'x', 0), func(p tview.Primitive) {})
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) {})
	if !f.Done() {
		t.Fatal("should be done after enter")
	}

	f.SetError("test error")
	if f.Done() {
		t.Error("SetError should reset done")
	}
	if f.errMsg != "test error" {
		t.Errorf("errMsg = %q, want %q", f.errMsg, "test error")
	}
}

func TestNewTaskForm_ScrollResetWhenFits(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
	f.promptWidth = 10

	// 15 chars, width 10 → 2 lines. visibleLines=2 means all content fits.
	f.prompt = make([]rune, 15)
	for i := range f.prompt {
		f.prompt[i] = 'x'
	}

	// Simulate stale scrollOffset=1 (the bug scenario)
	f.scrollOffset = 1
	f.cursorPos = 12 // on line 1
	f.ensureCursorVisible(2, 2)
	if f.scrollOffset != 0 {
		t.Errorf("scrollOffset = %d, want 0 (all content fits)", f.scrollOffset)
	}
}

func TestNewTaskForm_WordNav(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
	handler := f.InputHandler()

	// Type "hello world"
	for _, r := range "hello world" {
		handler(tcell.NewEventKey(tcell.KeyRune, r, 0), func(p tview.Primitive) {})
	}
	if f.cursorPos != 11 {
		t.Fatalf("cursor = %d, want 11", f.cursorPos)
	}

	// Alt+Left: jump to start of "world"
	handler(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModAlt), func(p tview.Primitive) {})
	if f.cursorPos != 6 {
		t.Errorf("alt+left: cursor = %d, want 6", f.cursorPos)
	}

	// Alt+Left again: jump to start of "hello"
	handler(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModAlt), func(p tview.Primitive) {})
	if f.cursorPos != 0 {
		t.Errorf("alt+left 2: cursor = %d, want 0", f.cursorPos)
	}

	// Alt+Right: jump to end of "hello"
	handler(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModAlt), func(p tview.Primitive) {})
	if f.cursorPos != 5 {
		t.Errorf("alt+right: cursor = %d, want 5", f.cursorPos)
	}
}

func TestNewTaskForm_WordDelete(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
	handler := f.InputHandler()

	// Type "hello world"
	for _, r := range "hello world" {
		handler(tcell.NewEventKey(tcell.KeyRune, r, 0), func(p tview.Primitive) {})
	}

	// Alt+Backspace: delete "world"
	handler(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModAlt), func(p tview.Primitive) {})
	got := string(f.prompt)
	if got != "hello " {
		t.Errorf("alt+backspace: prompt = %q, want %q", got, "hello ")
	}

	// Ctrl+W: delete "hello "
	handler(tcell.NewEventKey(tcell.KeyCtrlW, 0, 0), func(p tview.Primitive) {})
	got = string(f.prompt)
	if got != "" {
		t.Errorf("ctrl+w: prompt = %q, want empty", got)
	}
}

func TestNewTaskForm_AltBF(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
	handler := f.InputHandler()

	for _, r := range "foo bar" {
		handler(tcell.NewEventKey(tcell.KeyRune, r, 0), func(p tview.Primitive) {})
	}

	// Alt+B: jump word left
	handler(tcell.NewEventKey(tcell.KeyRune, 'b', tcell.ModAlt), func(p tview.Primitive) {})
	if f.cursorPos != 4 {
		t.Errorf("alt+b: cursor = %d, want 4", f.cursorPos)
	}

	// Alt+F: jump word right
	handler(tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModAlt), func(p tview.Primitive) {})
	if f.cursorPos != 7 {
		t.Errorf("alt+f: cursor = %d, want 7", f.cursorPos)
	}
}

func TestNewTaskForm_Autocomplete(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {Command: "claude"}}, "b",
	)
	// Inject test skills
	f.skills = []skills.SkillItem{
		{Name: "commit", Description: "Create a commit"},
		{Name: "review", Description: "Review PR"},
		{Name: "test", Description: "Run tests"},
	}
	handler := f.InputHandler()

	// Type "/" — should open autocomplete with all 3 skills
	handler(tcell.NewEventKey(tcell.KeyRune, '/', 0), func(p tview.Primitive) {})
	if !f.acOpen {
		t.Error("autocomplete should open on /")
	}
	if len(f.acMatches) != 3 {
		t.Errorf("matches = %d, want 3", len(f.acMatches))
	}

	// Type "co" — should filter to "commit"
	handler(tcell.NewEventKey(tcell.KeyRune, 'c', 0), func(p tview.Primitive) {})
	handler(tcell.NewEventKey(tcell.KeyRune, 'o', 0), func(p tview.Primitive) {})
	if len(f.acMatches) != 1 {
		t.Errorf("matches after /co = %d, want 1", len(f.acMatches))
	}
	if f.acMatches[0].Name != "commit" {
		t.Errorf("match = %q, want commit", f.acMatches[0].Name)
	}

	// Enter selects the autocomplete item
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) {})
	if f.acOpen {
		t.Error("autocomplete should close on enter")
	}
	got := string(f.prompt)
	if got != "/commit " {
		t.Errorf("prompt = %q, want %q", got, "/commit ")
	}
	if f.Done() {
		t.Error("should not submit when selecting autocomplete")
	}
}

func TestNewTaskForm_ACEscCloses(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {Command: "claude"}}, "b",
	)
	f.skills = []skills.SkillItem{
		{Name: "commit", Description: "Create a commit"},
	}
	handler := f.InputHandler()

	// Type "/" to open autocomplete
	handler(tcell.NewEventKey(tcell.KeyRune, '/', 0), func(p tview.Primitive) {})
	if !f.acOpen {
		t.Fatal("autocomplete should be open")
	}

	// Esc should close autocomplete, NOT cancel form
	handler(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(p tview.Primitive) {})
	if f.acOpen {
		t.Error("esc should close autocomplete")
	}
	if f.Canceled() {
		t.Error("esc should not cancel form when autocomplete was open")
	}
}

func TestNewTaskForm_ACNavigation(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {Command: "claude"}}, "b",
	)
	f.skills = []skills.SkillItem{
		{Name: "alpha", Description: "a"},
		{Name: "beta", Description: "b"},
		{Name: "gamma", Description: "g"},
	}
	handler := f.InputHandler()

	// Type "/" to open autocomplete
	handler(tcell.NewEventKey(tcell.KeyRune, '/', 0), func(p tview.Primitive) {})
	if f.acIdx != 0 {
		t.Fatalf("initial acIdx = %d, want 0", f.acIdx)
	}

	// Down → index 1
	handler(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(p tview.Primitive) {})
	if f.acIdx != 1 {
		t.Errorf("after down: acIdx = %d, want 1", f.acIdx)
	}

	// Up → back to 0
	handler(tcell.NewEventKey(tcell.KeyUp, 0, 0), func(p tview.Primitive) {})
	if f.acIdx != 0 {
		t.Errorf("after up: acIdx = %d, want 0", f.acIdx)
	}

	// Up wraps to last item
	handler(tcell.NewEventKey(tcell.KeyUp, 0, 0), func(p tview.Primitive) {})
	if f.acIdx != 2 {
		t.Errorf("after wrap up: acIdx = %d, want 2", f.acIdx)
	}
}

func TestNewTaskForm_ACSpaceCloses(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {Command: "claude"}}, "b",
	)
	f.skills = []skills.SkillItem{
		{Name: "commit", Description: "Create a commit"},
	}
	handler := f.InputHandler()

	handler(tcell.NewEventKey(tcell.KeyRune, '/', 0), func(p tview.Primitive) {})
	handler(tcell.NewEventKey(tcell.KeyRune, 'c', 0), func(p tview.Primitive) {})
	if !f.acOpen {
		t.Fatal("autocomplete should be open")
	}

	// Space should close autocomplete (user typing args after skill name)
	handler(tcell.NewEventKey(tcell.KeyRune, ' ', 0), func(p tview.Primitive) {})
	if f.acOpen {
		t.Error("space should close autocomplete")
	}
}

func TestNewTaskForm_AltD(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
	handler := f.InputHandler()

	for _, r := range "hello world" {
		handler(tcell.NewEventKey(tcell.KeyRune, r, 0), func(p tview.Primitive) {})
	}

	// Move cursor to start
	handler(tcell.NewEventKey(tcell.KeyHome, 0, 0), func(p tview.Primitive) {})

	// Alt+D: delete word right ("hello")
	handler(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModAlt), func(p tview.Primitive) {})
	got := string(f.prompt)
	if got != " world" {
		t.Errorf("alt+d: prompt = %q, want %q", got, " world")
	}
	if f.cursorPos != 0 {
		t.Errorf("alt+d: cursor = %d, want 0", f.cursorPos)
	}
}

func TestNewTaskForm_ACDownWrap(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {Command: "claude"}}, "b",
	)
	f.skills = []skills.SkillItem{
		{Name: "alpha", Description: "a"},
		{Name: "beta", Description: "b"},
	}
	handler := f.InputHandler()

	handler(tcell.NewEventKey(tcell.KeyRune, '/', 0), func(p tview.Primitive) {})
	if f.acIdx != 0 {
		t.Fatalf("initial acIdx = %d, want 0", f.acIdx)
	}

	// Down → 1
	handler(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(p tview.Primitive) {})
	if f.acIdx != 1 {
		t.Errorf("after down: acIdx = %d, want 1", f.acIdx)
	}

	// Down wraps to 0
	handler(tcell.NewEventKey(tcell.KeyDown, 0, 0), func(p tview.Primitive) {})
	if f.acIdx != 0 {
		t.Errorf("after wrap down: acIdx = %d, want 0", f.acIdx)
	}
}

func TestNewTaskForm_EnterOnSelector(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
	handler := f.InputHandler()

	// Tab to project
	handler(tcell.NewEventKey(tcell.KeyTab, 0, 0), func(p tview.Primitive) {})
	if f.focused != ntFieldProject {
		t.Fatalf("focused = %d, want project", f.focused)
	}

	// Enter on project → backend
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) {})
	if f.focused != ntFieldBackend {
		t.Errorf("enter on project: focused = %d, want backend", f.focused)
	}

	// Enter on backend → prompt
	handler(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) {})
	if f.focused != ntFieldPrompt {
		t.Errorf("enter on backend: focused = %d, want prompt", f.focused)
	}
}

func TestNewTaskForm_CtrlWClosesAC(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {Command: "claude"}}, "b",
	)
	f.skills = []skills.SkillItem{
		{Name: "commit", Description: "Create a commit"},
	}
	handler := f.InputHandler()

	// Type "/com" — AC open
	for _, r := range "/com" {
		handler(tcell.NewEventKey(tcell.KeyRune, r, 0), func(p tview.Primitive) {})
	}
	if !f.acOpen {
		t.Fatal("AC should be open after /com")
	}

	// Ctrl+W deletes "com" (word chars), leaving "/"
	handler(tcell.NewEventKey(tcell.KeyCtrlW, 0, 0), func(p tview.Primitive) {})
	got := string(f.prompt)
	if got != "/" {
		t.Errorf("after ctrl+w: prompt = %q, want %q", got, "/")
	}
	// AC should still be open (just "/" with all skills matching)
	if !f.acOpen {
		t.Error("AC should remain open with just trigger char")
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
