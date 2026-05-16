package tui

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// --- handleNewTaskKey: cancel, empty submit, no-project submit ---

func TestApp_HandleNewTaskKey_FormOpenedThenCancel(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("p", config.Project{Path: t.TempDir()})

	app.onNewTask()
	if app.newTaskForm == nil {
		t.Fatal("form should be open")
	}
	app.handleNewTaskKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleNewTaskKey_NoProjectPath(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	// Project with empty Path → triggers no-worktree branch.
	d.SetProject("p", config.Project{Path: ""})

	app.onNewTask()
	app.newTaskForm.focused = ntFieldPrompt
	app.newTaskForm.prompt = []rune("test prompt for the task")
	app.newTaskForm.cursorPos = len(app.newTaskForm.prompt)
	app.newTaskForm.projInput = []rune("p")
	app.newTaskForm.projCursorPos = 1

	// Enter submits the form when on prompt field with non-empty text.
	app.handleNewTaskKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	// Form should be closed; we entered agent view (no-project path adds task and goes to agent).
	if app.newTaskForm != nil {
		t.Error("form should be closed after submit")
	}
}

// --- handleBranchKey: cover delete, ctrl-u, ctrl-k, alt+b/f/d, alt-backspace ---

func TestNewTaskForm_HandleBranchKey_Edit(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {Path: "/tmp/p"}}, "p",
		map[string]config.Backend{"x": {}}, "x",
	)
	f.SetBranchOptions([]string{"origin/main", "origin/develop"})
	f.focused = ntFieldBranch
	f.branchInput = []rune("origin/main")
	f.branchCursorPos = len(f.branchInput)
	handler := f.InputHandler()

	// Backspace
	handler(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0), nil)
	if f.branchCursorPos != len(f.branchInput) {
		t.Errorf("backspace cursor wrong: %d vs %d", f.branchCursorPos, len(f.branchInput))
	}

	// Ctrl+A → home
	handler(tcell.NewEventKey(tcell.KeyCtrlA, 0, 0), nil)
	testutil.Equal(t, f.branchCursorPos, 0)

	// Ctrl+E → end
	handler(tcell.NewEventKey(tcell.KeyCtrlE, 0, 0), nil)
	testutil.Equal(t, f.branchCursorPos, len(f.branchInput))

	// Ctrl+U: kill from start to cursor — keep tail
	f.branchInput = []rune("abcdef")
	f.branchCursorPos = 3
	handler(tcell.NewEventKey(tcell.KeyCtrlU, 0, 0), nil)
	testutil.Equal(t, string(f.branchInput), "def")
	testutil.Equal(t, f.branchCursorPos, 0)

	// Ctrl+K: kill from cursor to end — keep head
	f.branchInput = []rune("abcdef")
	f.branchCursorPos = 3
	handler(tcell.NewEventKey(tcell.KeyCtrlK, 0, 0), nil)
	testutil.Equal(t, string(f.branchInput), "abc")

	// Delete (cursor in middle)
	f.branchInput = []rune("abcdef")
	f.branchCursorPos = 2
	handler(tcell.NewEventKey(tcell.KeyDelete, 0, 0), nil)
	testutil.Equal(t, string(f.branchInput), "abdef")

	// Left/Right
	f.branchCursorPos = 2
	handler(tcell.NewEventKey(tcell.KeyLeft, 0, 0), nil)
	testutil.Equal(t, f.branchCursorPos, 1)
	handler(tcell.NewEventKey(tcell.KeyRight, 0, 0), nil)
	testutil.Equal(t, f.branchCursorPos, 2)

	// Home
	handler(tcell.NewEventKey(tcell.KeyHome, 0, 0), nil)
	testutil.Equal(t, f.branchCursorPos, 0)

	// End
	handler(tcell.NewEventKey(tcell.KeyEnd, 0, 0), nil)
	testutil.Equal(t, f.branchCursorPos, len(f.branchInput))

	// Ctrl+W word delete
	f.branchInput = []rune("foo bar")
	f.branchCursorPos = 7
	handler(tcell.NewEventKey(tcell.KeyCtrlW, 0, 0), nil)
	if !contains(string(f.branchInput), "foo") {
		t.Errorf("ctrl+w should preserve 'foo': got %q", string(f.branchInput))
	}
}

func TestNewTaskForm_HandleBranchKey_AltMods(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {Path: "/tmp/p"}}, "p",
		map[string]config.Backend{"x": {}}, "x",
	)
	f.focused = ntFieldBranch
	f.branchInput = []rune("origin/main")
	f.branchCursorPos = len(f.branchInput)

	// Alt+B (word back)
	f.handleBranchKey(tcell.NewEventKey(tcell.KeyRune, 'b', tcell.ModAlt))
	// Alt+F (word forward)
	f.handleBranchKey(tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModAlt))
	// Alt+D (word delete forward)
	f.handleBranchKey(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModAlt))

	// Alt+Left/Right (word nav)
	f.branchCursorPos = len(f.branchInput)
	f.handleBranchKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModAlt))
	f.handleBranchKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModAlt))

	// Alt+Backspace (word delete back)
	f.branchInput = []rune("foo bar baz")
	f.branchCursorPos = len(f.branchInput)
	f.handleBranchKey(tcell.NewEventKey(tcell.KeyBackspace, 0, tcell.ModAlt))
}

func TestNewTaskForm_HandleBranchKey_UpDownNav(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {Path: "/tmp/p"}}, "p",
		map[string]config.Backend{"x": {}}, "x",
	)
	f.SetBranchOptions([]string{"origin/main", "origin/dev"})
	f.focused = ntFieldBranch

	// Down with AC closed → advance to backend
	f.branchACOpen = false
	f.handleBranchKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	testutil.Equal(t, f.focused, ntFieldBackend)

	// Up with AC closed → back to project
	f.focused = ntFieldBranch
	f.branchACOpen = false
	f.handleBranchKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	testutil.Equal(t, f.focused, ntFieldProject)

	// Down with AC open → moves AC selection
	f.focused = ntFieldBranch
	f.branchInput = nil
	f.branchCursorPos = 0
	f.updateBranchAC()
	if !f.branchACOpen {
		t.Fatal("AC should be open")
	}
	prevIdx := f.branchACIdx
	f.handleBranchKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	if f.branchACIdx == prevIdx {
		t.Error("Down with AC open should advance")
	}
	f.handleBranchKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
}

// --- handleProjectKey: edit ops (delete/ctrl-u/k, alt-mods) ---

func TestNewTaskForm_HandleProjectKey_Edit(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"alpha": {}, "beta": {}, "alphabet": {}}, "",
		map[string]config.Backend{"x": {}}, "x",
	)
	f.focused = ntFieldProject
	f.projInput = []rune("alpha")
	f.projCursorPos = len(f.projInput)
	handler := f.InputHandler()

	// Backspace
	handler(tcell.NewEventKey(tcell.KeyBackspace, 0, 0), nil)

	// Ctrl+A
	handler(tcell.NewEventKey(tcell.KeyCtrlA, 0, 0), nil)

	// Ctrl+E
	handler(tcell.NewEventKey(tcell.KeyCtrlE, 0, 0), nil)

	// Ctrl+U
	f.projInput = []rune("abcdef")
	f.projCursorPos = 3
	handler(tcell.NewEventKey(tcell.KeyCtrlU, 0, 0), nil)
	testutil.Equal(t, string(f.projInput), "def")

	// Ctrl+K
	f.projInput = []rune("abcdef")
	f.projCursorPos = 3
	handler(tcell.NewEventKey(tcell.KeyCtrlK, 0, 0), nil)
	testutil.Equal(t, string(f.projInput), "abc")

	// Delete (mid-cursor)
	f.projInput = []rune("abcdef")
	f.projCursorPos = 2
	handler(tcell.NewEventKey(tcell.KeyDelete, 0, 0), nil)
	testutil.Equal(t, string(f.projInput), "abdef")

	// Left/Right
	f.projCursorPos = 2
	handler(tcell.NewEventKey(tcell.KeyLeft, 0, 0), nil)
	handler(tcell.NewEventKey(tcell.KeyRight, 0, 0), nil)

	// Ctrl+W
	f.projInput = []rune("foo bar")
	f.projCursorPos = 7
	handler(tcell.NewEventKey(tcell.KeyCtrlW, 0, 0), nil)

	// Alt mods
	f.handleProjectKey(tcell.NewEventKey(tcell.KeyRune, 'b', tcell.ModAlt))
	f.handleProjectKey(tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModAlt))
	f.handleProjectKey(tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModAlt))
	f.handleProjectKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModAlt))
	f.handleProjectKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModAlt))

	// Alt+Backspace
	f.projInput = []rune("foo bar")
	f.projCursorPos = len(f.projInput)
	f.handleProjectKey(tcell.NewEventKey(tcell.KeyBackspace, 0, tcell.ModAlt))
}

// --- handleDirInputKey: edit ops not yet covered ---

func TestQuickAddForm_HandleDirInputKey_Edit(t *testing.T) {
	f := NewQuickAddForm(nil)
	f.dirPath = []rune("abcdef")
	f.dirCursor = len(f.dirPath)

	// Ctrl+A
	f.handleDirInputKey(tcell.NewEventKey(tcell.KeyCtrlA, 0, 0))
	testutil.Equal(t, f.dirCursor, 0)

	// Ctrl+E
	f.handleDirInputKey(tcell.NewEventKey(tcell.KeyCtrlE, 0, 0))
	testutil.Equal(t, f.dirCursor, len(f.dirPath))

	// Ctrl+U: kill back from cursor
	f.dirPath = []rune("abcdef")
	f.dirCursor = 3
	f.handleDirInputKey(tcell.NewEventKey(tcell.KeyCtrlU, 0, 0))
	testutil.Equal(t, string(f.dirPath), "def")

	// Ctrl+K: kill forward from cursor
	f.dirPath = []rune("abcdef")
	f.dirCursor = 3
	f.handleDirInputKey(tcell.NewEventKey(tcell.KeyCtrlK, 0, 0))
	testutil.Equal(t, string(f.dirPath), "abc")

	// Delete
	f.dirPath = []rune("abcdef")
	f.dirCursor = 2
	f.handleDirInputKey(tcell.NewEventKey(tcell.KeyDelete, 0, 0))
	testutil.Equal(t, string(f.dirPath), "abdef")

	// Left/Right
	f.dirCursor = 2
	f.handleDirInputKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
	testutil.Equal(t, f.dirCursor, 1)
	f.handleDirInputKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	testutil.Equal(t, f.dirCursor, 2)

	// Home/End
	f.handleDirInputKey(tcell.NewEventKey(tcell.KeyHome, 0, 0))
	testutil.Equal(t, f.dirCursor, 0)
	f.handleDirInputKey(tcell.NewEventKey(tcell.KeyEnd, 0, 0))
	testutil.Equal(t, f.dirCursor, len(f.dirPath))

	// Backspace at start (no-op)
	f.dirCursor = 0
	f.handleDirInputKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0))

	// Backspace at end
	f.dirPath = []rune("abc")
	f.dirCursor = 3
	f.handleDirInputKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0))
	testutil.Equal(t, string(f.dirPath), "ab")

	// Delete past end (no-op)
	f.dirPath = []rune("a")
	f.dirCursor = 1
	f.handleDirInputKey(tcell.NewEventKey(tcell.KeyDelete, 0, 0))
	testutil.Equal(t, string(f.dirPath), "a")
}

func TestQuickAddForm_HandleDirInputKey_EnterScanning(t *testing.T) {
	f := NewQuickAddForm(nil)
	f.scanning = true
	// Enter while scanning → no-op
	f.handleDirInputKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Equal(t, f.scanning, true)
}

// --- handleRestartDaemonKey: Restart path (Y) ---

func TestApp_HandleRestartDaemonKey_RestartChosen(t *testing.T) {
	// HOME redirect keeps any path that slips past the restartDaemonFn
	// override away from the real ~/.argus/argusd symlink and the live
	// daemon socket — defense in depth.
	t.Setenv("HOME", t.TempDir())

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	// Override restartDaemonFn so the goroutine does not fork the test
	// binary as a fake daemon. Counting invocations also proves the
	// callback fires on the Restart branch.
	var calls atomic.Int32
	done := make(chan struct{}, 1)
	app.restartDaemonFn = func() {
		calls.Add(1)
		select {
		case done <- struct{}{}:
		default:
		}
	}

	app.openRestartDaemonPrompt()
	// Press 'y' to confirm restart. The modal interprets Enter as the default
	// (Restart), so press Enter.
	app.handleRestartDaemonKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	// daemonRestarting should be true (a goroutine is restarting it).
	app.mu.Lock()
	restarting := app.daemonRestarting
	app.mu.Unlock()
	if !restarting {
		t.Error("daemonRestarting should be true after choosing restart")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("restartDaemonFn was not invoked after choosing Restart")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("restartDaemonFn invocations = %d, want 1", got)
	}
}

func TestApp_HandleRestartDaemonKey_NilModal(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	// Modal is nil — should be a no-op.
	app.handleRestartDaemonKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)
}

// (Tests for the removed pendingNarrowRestart / reapStaleNarrowRestart
// fields were dropped here when master moved that responsibility into the
// daemon's KickRerender path. The new behavior is covered by
// TestRunner_KickRerender_* in internal/agent/runner_test.go.)

// --- onTick: with no daemon connection, exercises early healthCheck branch ---

func TestApp_OnTick_NoDaemon(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	// Wire a sim screen so QueueUpdateDraw doesn't block forever.
	_, stop := wireApp(t, app)
	defer stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.onTick()
	}()
	select {
	case <-done:
	case <-time.After(uiTimeout):
		t.Fatal("onTick blocked")
	}
}

func TestApp_OnTick_PreviewActive(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	// Add a task and select it in the preview.
	task := &model.Task{
		ID: "preview-tick", Project: "p", Name: "pt",
		Status: model.StatusPending, CreatedAt: time.Now(),
	}
	d.Add(task)
	app.refreshTasks()

	_, stop := wireApp(t, app)
	defer stop()

	readUI(t, app.tapp, func() {
		app.taskPreview.SetTaskID("preview-tick")
		app.taskPreview.SetRect(0, 0, 60, 20)
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.onTick()
	}()
	select {
	case <-done:
	case <-time.After(uiTimeout):
		t.Fatal("onTick blocked with preview")
	}
}

// (Tests for the removed maybeKickNarrowRerender helper were dropped here
// when master moved that responsibility into the daemon's KickRerender path.)

// --- openQuickAddForm and handleQuickAddKey: full flow ---

func TestApp_OpenQuickAddForm(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.openQuickAddForm()
	if app.quickAddForm == nil {
		t.Fatal("quickAddForm should be set")
	}
	testutil.Equal(t, app.mode, modeQuickAdd)

	// Cancel via handler.
	app.handleQuickAddKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleQuickAddKey_DoneSavesProjects(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.openQuickAddForm()
	// Set up phase 1 with selected repos.
	app.quickAddForm.repos = []repoCandidate{
		{name: "repo-a", path: "/tmp/repo-a", selected: true},
	}
	app.quickAddForm.phase = 1

	// Submit (Enter while in phase 1 fires Done if any selected).
	app.handleQuickAddKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)

	projects, _ := d.Projects()
	if _, ok := projects["repo-a"]; !ok {
		t.Error("repo-a should be saved as project")
	}
}

// --- helper ---

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
