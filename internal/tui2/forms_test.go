package tui2

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/config"
)

// --- ProjectForm tests ---

func TestProjectForm_New(t *testing.T) {
	pf := NewProjectForm()
	if pf.editMode {
		t.Error("should not be in edit mode")
	}
	if pf.Done() || pf.Canceled() {
		t.Error("should not be done or canceled initially")
	}
}

func TestProjectForm_LoadProject(t *testing.T) {
	pf := NewProjectForm()
	pf.LoadProject("test", config.Project{Path: "/tmp", Branch: "main", Backend: "claude"})
	if !pf.editMode {
		t.Error("should be in edit mode")
	}
	if string(pf.fields[pfFieldName]) != "test" {
		t.Errorf("name = %q, want test", string(pf.fields[pfFieldName]))
	}
	if pf.focused != pfFieldPath {
		t.Error("should focus path field in edit mode")
	}
}

func TestProjectForm_KeyNavigation(t *testing.T) {
	pf := NewProjectForm()
	// Tab to next field.
	pf.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	if pf.focused != 1 {
		t.Errorf("focused = %d, want 1", pf.focused)
	}
	// Back-tab.
	pf.HandleKey(tcell.NewEventKey(tcell.KeyBacktab, 0, 0))
	if pf.focused != 0 {
		t.Errorf("focused = %d, want 0", pf.focused)
	}
}

func TestProjectForm_TypeAndResult(t *testing.T) {
	pf := NewProjectForm()
	// Type a name.
	for _, r := range "myproj" {
		pf.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	// Enter → next field.
	pf.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	// Type a path.
	for _, r := range "/tmp/test" {
		pf.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	// Skip to done.
	pf.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0)) // → branch
	pf.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0)) // → backend
	pf.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0)) // → done

	if !pf.Done() {
		t.Error("should be done")
	}
	name, proj := pf.Result()
	if name != "myproj" {
		t.Errorf("name = %q", name)
	}
	if proj.Path != "/tmp/test" {
		t.Errorf("path = %q", proj.Path)
	}
}

func TestProjectForm_Escape(t *testing.T) {
	pf := NewProjectForm()
	pf.HandleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if !pf.Canceled() {
		t.Error("should be canceled")
	}
}

// --- BackendForm tests ---

func TestBackendForm_New(t *testing.T) {
	bf := NewBackendForm()
	if bf.editMode || bf.Done() || bf.Canceled() {
		t.Error("bad initial state")
	}
}

func TestBackendForm_LoadBackend(t *testing.T) {
	bf := NewBackendForm()
	bf.LoadBackend("claude", config.Backend{Command: "claude --dangerously-skip-permissions", PromptFlag: "--"})
	if !bf.editMode {
		t.Error("should be in edit mode")
	}
	if string(bf.fields[bfFieldCommand]) != "claude --dangerously-skip-permissions" {
		t.Error("command not loaded")
	}
}

func TestBackendForm_TypeAndSubmit(t *testing.T) {
	bf := NewBackendForm()
	for _, r := range "test-be" {
		bf.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	bf.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	for _, r := range "echo hello" {
		bf.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	bf.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0)) // → prompt flag
	bf.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0)) // → done

	if !bf.Done() {
		t.Error("should be done")
	}
	name, be := bf.Result()
	if name != "test-be" {
		t.Errorf("name = %q", name)
	}
	if be.Command != "echo hello" {
		t.Errorf("command = %q", be.Command)
	}
}

// --- Paste handler tests ---

func TestProjectForm_PasteHandler(t *testing.T) {
	pf := NewProjectForm()
	paste := pf.PasteHandler()

	t.Run("paste into focused field", func(t *testing.T) {
		paste("/home/user/project", func(p tview.Primitive) {})
		if got := string(pf.fields[pfFieldName]); got != "/home/user/project" {
			t.Errorf("field = %q, want /home/user/project", got)
		}
	})

	t.Run("paste skips read-only name in edit mode", func(t *testing.T) {
		pf2 := NewProjectForm()
		pf2.LoadProject("locked", config.Project{})
		pf2.focused = pfFieldName
		paste2 := pf2.PasteHandler()
		paste2("overwrite", func(p tview.Primitive) {})
		if got := string(pf2.fields[pfFieldName]); got != "locked" {
			t.Errorf("name changed to %q, should stay locked", got)
		}
	})
}

func TestBackendForm_PasteHandler(t *testing.T) {
	bf := NewBackendForm()
	bf.focused = bfFieldCommand
	paste := bf.PasteHandler()
	paste("claude --model opus", func(p tview.Primitive) {})
	if got := string(bf.fields[bfFieldCommand]); got != "claude --model opus" {
		t.Errorf("field = %q, want 'claude --model opus'", got)
	}
}

func TestRenameTaskForm_PasteHandler(t *testing.T) {
	rf := NewRenameTaskForm("old")
	paste := rf.PasteHandler()

	t.Run("paste appends at cursor", func(t *testing.T) {
		paste("-new-suffix", func(p tview.Primitive) {})
		if got := rf.Name(); got != "old-new-suffix" {
			t.Errorf("name = %q, want old-new-suffix", got)
		}
	})
}

// --- RenameTaskForm tests ---

func TestRenameTaskForm_New(t *testing.T) {
	rf := NewRenameTaskForm("old-name")
	if rf.Name() != "old-name" {
		t.Errorf("name = %q, want old-name", rf.Name())
	}
	if rf.cursor != 8 {
		t.Errorf("cursor = %d, want 8", rf.cursor)
	}
}

func TestRenameTaskForm_TypeAndSubmit(t *testing.T) {
	rf := NewRenameTaskForm("")
	for _, r := range "new-name" {
		rf.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	rf.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	if !rf.Done() {
		t.Error("should be done")
	}
	if rf.Name() != "new-name" {
		t.Errorf("name = %q", rf.Name())
	}
}

func TestRenameTaskForm_Backspace(t *testing.T) {
	rf := NewRenameTaskForm("abc")
	rf.HandleKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0))
	if rf.Name() != "ab" {
		t.Errorf("name = %q, want ab", rf.Name())
	}
}

func TestRenameTaskForm_Escape(t *testing.T) {
	rf := NewRenameTaskForm("test")
	rf.HandleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if !rf.Canceled() {
		t.Error("should be canceled")
	}
}
