package tui2

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
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
	pf.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0)) // → sandbox
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

func TestProjectForm_CtrlQ(t *testing.T) {
	pf := NewProjectForm()
	pf.HandleKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone))
	if !pf.Canceled() {
		t.Error("should be canceled")
	}
}

func TestProjectForm_BranchSelector(t *testing.T) {
	// syncBranchLoader simulates the async OnBranchFocus pattern used in
	// production (goroutine + QueueUpdateDraw) by calling SetBranchOptions
	// synchronously within the callback. This is safe in tests since there
	// is no tview event loop.
	syncBranchLoader := func(pf *ProjectForm, branches []string) {
		pf.OnBranchFocus = func(path string) {
			pf.SetBranchOptions(branches)
		}
	}

	t.Run("loads branches on tab to branch field", func(t *testing.T) {
		pf := NewProjectForm()
		syncBranchLoader(pf, []string{"upstream/master", "origin/main", "origin/feature"})
		// Type a path.
		pf.focused = pfFieldPath
		pf.fields[pfFieldPath] = []rune("/tmp/repo")
		// Tab to branch field.
		pf.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
		if pf.focused != pfFieldBranch {
			t.Fatalf("focused = %d, want %d", pf.focused, pfFieldBranch)
		}
		if len(pf.branchOptions) != 3 {
			t.Fatalf("branchOptions = %d, want 3", len(pf.branchOptions))
		}
		if pf.branchIdx != 0 {
			t.Errorf("branchIdx = %d, want 0", pf.branchIdx)
		}
	})

	t.Run("left/right cycles branch options", func(t *testing.T) {
		pf := NewProjectForm()
		syncBranchLoader(pf, []string{"origin/master", "origin/main", "origin/dev"})
		pf.focused = pfFieldPath
		pf.fields[pfFieldPath] = []rune("/repo")
		pf.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, 0)) // → branch

		// Right cycles forward.
		pf.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
		if pf.branchIdx != 1 {
			t.Errorf("after right: branchIdx = %d, want 1", pf.branchIdx)
		}
		// Left cycles back.
		pf.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
		if pf.branchIdx != 0 {
			t.Errorf("after left: branchIdx = %d, want 0", pf.branchIdx)
		}
		// Left wraps to end.
		pf.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
		if pf.branchIdx != 2 {
			t.Errorf("after wrap left: branchIdx = %d, want 2", pf.branchIdx)
		}
	})

	t.Run("result returns selected branch", func(t *testing.T) {
		pf := NewProjectForm()
		syncBranchLoader(pf, []string{"origin/master", "origin/main"})
		pf.fields[pfFieldName] = []rune("proj")
		pf.fields[pfFieldPath] = []rune("/repo")
		pf.focused = pfFieldPath
		pf.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, 0)) // → branch
		pf.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))

		_, proj := pf.Result()
		if proj.Branch != "origin/main" {
			t.Errorf("branch = %q, want origin/main", proj.Branch)
		}
	})

	t.Run("pre-selects existing branch on edit", func(t *testing.T) {
		pf := NewProjectForm()
		syncBranchLoader(pf, []string{"origin/master", "origin/main", "origin/dev"})
		pf.LoadProject("test", config.Project{Path: "/repo", Branch: "origin/main"})
		// Tab from path → branch.
		pf.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
		if pf.branchIdx != 1 {
			t.Errorf("branchIdx = %d, want 1 (origin/main)", pf.branchIdx)
		}
	})

	t.Run("no callback falls back to text input", func(t *testing.T) {
		pf := NewProjectForm()
		// No OnBranchFocus set.
		pf.focused = pfFieldPath
		pf.fields[pfFieldPath] = []rune("/repo")
		pf.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, 0)) // → branch
		if pf.branchIsSelector() {
			t.Error("should not be in selector mode without callback")
		}
		// Typing should still work.
		for _, r := range "main" {
			pf.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
		}
		_, proj := pf.Result()
		if proj.Branch != "main" {
			t.Errorf("branch = %q, want main", proj.Branch)
		}
	})

	t.Run("enter from path loads branches", func(t *testing.T) {
		pf := NewProjectForm()
		syncBranchLoader(pf, []string{"origin/main"})
		pf.focused = pfFieldPath
		pf.fields[pfFieldPath] = []rune("/repo")
		pf.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0)) // → branch
		if len(pf.branchOptions) != 1 {
			t.Errorf("branchOptions = %d, want 1", len(pf.branchOptions))
		}
	})

	t.Run("paste ignored in selector mode", func(t *testing.T) {
		pf := NewProjectForm()
		syncBranchLoader(pf, []string{"origin/main"})
		pf.focused = pfFieldPath
		pf.fields[pfFieldPath] = []rune("/repo")
		pf.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, 0)) // → branch
		paste := pf.PasteHandler()
		paste("garbage", func(p tview.Primitive) {})
		_, proj := pf.Result()
		if proj.Branch != "origin/main" {
			t.Errorf("branch = %q, want origin/main (paste should be ignored)", proj.Branch)
		}
	})

	t.Run("SetBranchOptions updates state", func(t *testing.T) {
		pf := NewProjectForm()
		pf.fields[pfFieldBranch] = []rune("origin/dev")
		pf.SetBranchOptions([]string{"origin/master", "origin/dev", "origin/main"})
		if pf.branchIdx != 1 {
			t.Errorf("branchIdx = %d, want 1 (pre-selected origin/dev)", pf.branchIdx)
		}
		if !pf.branchIsSelector() {
			t.Error("should be in selector mode after SetBranchOptions")
		}
	})
}

// --- BackendForm tests ---

func TestBackendForm_New(t *testing.T) {
	bf := NewBackendForm()
	if bf.editMode || bf.Done() || bf.Canceled() {
		t.Error("bad initial state")
	}
}

func TestBackendForm_CtrlQ(t *testing.T) {
	bf := NewBackendForm()
	bf.HandleKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone))
	if !bf.Canceled() {
		t.Error("should be canceled")
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

func TestRenameTaskForm_CtrlQ(t *testing.T) {
	rf := NewRenameTaskForm("test")
	rf.HandleKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone))
	if !rf.Canceled() {
		t.Error("should be canceled")
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

func TestRenameTaskForm_WordNavigation(t *testing.T) {
	t.Run("alt+left jumps word left", func(t *testing.T) {
		rf := NewRenameTaskForm("hello world")
		// cursor at end (11)
		rf.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModAlt))
		testutil.Equal(t, rf.cursor, 6) // before "world"
	})

	t.Run("alt+right jumps word right", func(t *testing.T) {
		rf := NewRenameTaskForm("hello world")
		rf.cursor = 0
		rf.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModAlt))
		testutil.Equal(t, rf.cursor, 5) // after "hello"
	})

	t.Run("alt+backspace deletes word left", func(t *testing.T) {
		rf := NewRenameTaskForm("hello world")
		rf.HandleKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModAlt))
		testutil.Equal(t, rf.Name(), "hello ")
		testutil.Equal(t, rf.cursor, 6)
	})

	t.Run("alt+b jumps word left", func(t *testing.T) {
		rf := NewRenameTaskForm("hello world")
		rf.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'b', tcell.ModAlt))
		testutil.Equal(t, rf.cursor, 6)
	})

	t.Run("alt+f jumps word right", func(t *testing.T) {
		rf := NewRenameTaskForm("hello world")
		rf.cursor = 0
		rf.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModAlt))
		testutil.Equal(t, rf.cursor, 5)
	})

	t.Run("alt+d deletes word right", func(t *testing.T) {
		rf := NewRenameTaskForm("hello world")
		rf.cursor = 0
		rf.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModAlt))
		testutil.Equal(t, rf.Name(), " world")
		testutil.Equal(t, rf.cursor, 0)
	})

	t.Run("ctrl+a moves to start", func(t *testing.T) {
		rf := NewRenameTaskForm("hello")
		rf.HandleKey(tcell.NewEventKey(tcell.KeyCtrlA, 0, 0))
		testutil.Equal(t, rf.cursor, 0)
	})

	t.Run("ctrl+e moves to end", func(t *testing.T) {
		rf := NewRenameTaskForm("hello")
		rf.cursor = 0
		rf.HandleKey(tcell.NewEventKey(tcell.KeyCtrlE, 0, 0))
		testutil.Equal(t, rf.cursor, 5)
	})

	t.Run("ctrl+w deletes word left", func(t *testing.T) {
		rf := NewRenameTaskForm("hello world")
		rf.HandleKey(tcell.NewEventKey(tcell.KeyCtrlW, 0, 0))
		testutil.Equal(t, rf.Name(), "hello ")
	})

	t.Run("ctrl+u deletes to start", func(t *testing.T) {
		rf := NewRenameTaskForm("hello world")
		rf.cursor = 5
		rf.HandleKey(tcell.NewEventKey(tcell.KeyCtrlU, 0, 0))
		testutil.Equal(t, rf.Name(), " world")
		testutil.Equal(t, rf.cursor, 0)
	})

	t.Run("ctrl+k deletes to end", func(t *testing.T) {
		rf := NewRenameTaskForm("hello world")
		rf.cursor = 5
		rf.HandleKey(tcell.NewEventKey(tcell.KeyCtrlK, 0, 0))
		testutil.Equal(t, rf.Name(), "hello")
		testutil.Equal(t, rf.cursor, 5)
	})

	t.Run("delete key removes char at cursor", func(t *testing.T) {
		rf := NewRenameTaskForm("hello")
		rf.cursor = 0
		rf.HandleKey(tcell.NewEventKey(tcell.KeyDelete, 0, 0))
		testutil.Equal(t, rf.Name(), "ello")
		testutil.Equal(t, rf.cursor, 0)
	})
}
