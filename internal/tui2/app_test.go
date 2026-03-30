package tui2

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/model"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func testDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestNew(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	if app.tapp == nil {
		t.Error("tview.Application should not be nil")
	}
	if app.header == nil {
		t.Error("header should not be nil")
	}
	if app.statusbar == nil {
		t.Error("statusbar should not be nil")
	}
	if app.tasklist == nil {
		t.Error("tasklist should not be nil")
	}
	if app.mode != modeTaskList {
		t.Errorf("initial mode = %v, want modeTaskList", app.mode)
	}
	if app.daemonConnected {
		t.Error("daemonConnected should be false")
	}
}

func TestSwitchTab(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	app.switchTab(TabReviews)
	if app.header.ActiveTab() != TabReviews {
		t.Errorf("tab = %v, want TabReviews", app.header.ActiveTab())
	}

	app.switchTab(TabSettings)
	if app.header.ActiveTab() != TabSettings {
		t.Errorf("tab = %v, want TabSettings", app.header.ActiveTab())
	}

	app.switchTab(TabTasks)
	if app.header.ActiveTab() != TabTasks {
		t.Errorf("tab = %v, want TabTasks", app.header.ActiveTab())
	}
}

func TestOnTaskSelect(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	task := &model.Task{
		ID:   "test-1",
		Name: "test task",
	}

	app.onTaskSelect(task)

	if app.mode != modeAgent {
		t.Errorf("mode = %v, want modeAgent", app.mode)
	}
	if app.agentState.TaskID != "test-1" {
		t.Errorf("agentState.TaskID = %q, want %q", app.agentState.TaskID, "test-1")
	}
}

func TestOnTaskSelectAutoResume(t *testing.T) {
	t.Run("no auto-resume without session ID", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false, false)

		task := &model.Task{
			ID:   "t-no-sid",
			Name: "no session id",
		}
		task.SetStatus(model.StatusInReview)

		app.onTaskSelect(task)

		// No session should have been started — runner has no sessions.
		if runner.Get("t-no-sid") != nil {
			t.Error("expected no session started for task without SessionID")
		}
	})

	t.Run("no auto-resume for completed task", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false, false)

		task := &model.Task{
			ID:        "t-complete",
			Name:      "completed task",
			SessionID: "sess-123",
		}
		task.SetStatus(model.StatusComplete)
		d.Add(task) //nolint:errcheck

		app.onTaskSelect(task)

		// Completed tasks should not auto-resume.
		got, _ := d.Get("t-complete")
		if got.Status != model.StatusComplete {
			t.Errorf("status = %v, want Complete", got.Status)
		}
	})

	t.Run("auto-resume for in-review task with session ID", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false, false)

		task := &model.Task{
			ID:        "t-resume",
			Name:      "resumable task",
			SessionID: "sess-456",
		}
		task.SetStatus(model.StatusInReview)
		d.Add(task) //nolint:errcheck

		app.onTaskSelect(task)

		// startSession was attempted — the runner.Start will fail (no real
		// backend binary), which reverts the task to Pending. Verify the
		// revert happened (proves auto-resume was triggered).
		got, _ := d.Get("t-resume")
		if got.Status != model.StatusPending {
			t.Errorf("status = %v, want Pending (reverted after failed start)", got.Status)
		}
	})

	t.Run("no auto-resume for archived task", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false, false)

		task := &model.Task{
			ID:        "t-archived",
			Name:      "archived task",
			SessionID: "sess-arc",
			Archived:  true,
		}
		task.SetStatus(model.StatusInReview)
		d.Add(task) //nolint:errcheck

		app.onTaskSelect(task)

		// Archived tasks should not auto-resume.
		got, _ := d.Get("t-archived")
		if got.Status != model.StatusInReview {
			t.Errorf("status = %v, want InReview (archived tasks should not auto-resume)", got.Status)
		}
	})

	t.Run("auto-resume for pending task with session ID", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false, false)

		task := &model.Task{
			ID:        "t-pending",
			Name:      "pending resumable",
			SessionID: "sess-789",
		}
		task.SetStatus(model.StatusPending)
		d.Add(task) //nolint:errcheck

		app.onTaskSelect(task)

		// startSession was attempted — verifies auto-resume triggers for
		// Pending tasks with a SessionID (daemon restart scenario).
		got, _ := d.Get("t-pending")
		// After failed start, task reverts to Pending with cleared SessionID.
		if got.SessionID != "" {
			t.Error("expected auto-resume attempt to clear SessionID on failure")
		}
	})
}

func TestExitAgentView(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	app.mode = modeAgent
	app.exitAgentView()

	if app.mode != modeTaskList {
		t.Errorf("mode = %v, want modeTaskList", app.mode)
	}
}

func TestTcellKeyToBytes(t *testing.T) {
	tests := []struct {
		name string
		key  tcell.Key
		rune rune
		mod  tcell.ModMask
		want []byte
	}{
		{"enter", tcell.KeyEnter, 0, 0, []byte{'\r'}},
		{"tab", tcell.KeyTab, 0, 0, []byte{'\t'}},
		{"shift-tab", tcell.KeyBacktab, 0, 0, []byte("\x1b[Z")},
		{"backspace", tcell.KeyBackspace2, 0, 0, []byte{0x7f}},
		{"up", tcell.KeyUp, 0, 0, []byte("\x1b[A")},
		{"down", tcell.KeyDown, 0, 0, []byte("\x1b[B")},
		{"right", tcell.KeyRight, 0, 0, []byte("\x1b[C")},
		{"left", tcell.KeyLeft, 0, 0, []byte("\x1b[D")},
		{"ctrl-c", tcell.KeyCtrlC, 0, 0, []byte{0x03}},
		{"ctrl-d", tcell.KeyCtrlD, 0, 0, []byte{0x04}},
		{"escape", tcell.KeyEscape, 0, 0, []byte{0x1b}},
		{"rune-a", tcell.KeyRune, 'a', 0, []byte("a")},
		{"rune-alt-a", tcell.KeyRune, 'a', tcell.ModAlt, []byte{0x1b, 'a'}},
		{"delete", tcell.KeyDelete, 0, 0, []byte("\x1b[3~")},
		// Alt+arrow keys for word navigation
		{"alt-left", tcell.KeyLeft, 0, tcell.ModAlt, []byte("\x1b[1;3D")},
		{"alt-right", tcell.KeyRight, 0, tcell.ModAlt, []byte("\x1b[1;3C")},
		{"alt-up", tcell.KeyUp, 0, tcell.ModAlt, []byte("\x1b[1;3A")},
		{"alt-down", tcell.KeyDown, 0, tcell.ModAlt, []byte("\x1b[1;3B")},
		{"alt-backspace", tcell.KeyBackspace2, 0, tcell.ModAlt, []byte{0x1b, 0x7f}},
		{"alt-delete", tcell.KeyDelete, 0, tcell.ModAlt, []byte{0x1b, 0x7f}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := tcell.NewEventKey(tt.key, tt.rune, tt.mod)
			got := tcellKeyToBytes(ev)
			if string(got) != string(tt.want) {
				t.Errorf("tcellKeyToBytes(%v) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestArrowTabNavigation(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	// Start on Tasks tab
	if app.header.ActiveTab() != TabTasks {
		t.Fatalf("initial tab = %v, want TabTasks", app.header.ActiveTab())
	}

	// Right arrow → To Dos
	ev := tcell.NewEventKey(tcell.KeyRight, 0, 0)
	result := app.handleGlobalKey(ev)
	if result != nil {
		t.Error("right arrow should be consumed (return nil)")
	}
	if app.header.ActiveTab() != TabToDos {
		t.Errorf("tab = %v, want TabToDos", app.header.ActiveTab())
	}

	// Right arrow → Reviews
	result = app.handleGlobalKey(ev)
	if app.header.ActiveTab() != TabReviews {
		t.Errorf("tab = %v, want TabReviews", app.header.ActiveTab())
	}

	// Right arrow → Settings
	result = app.handleGlobalKey(ev)
	if app.header.ActiveTab() != TabSettings {
		t.Errorf("tab = %v, want TabSettings", app.header.ActiveTab())
	}

	// Right arrow at Settings — stays on Settings (no wrap)
	result = app.handleGlobalKey(ev)
	if app.header.ActiveTab() != TabSettings {
		t.Errorf("tab = %v, want TabSettings (no wrap)", app.header.ActiveTab())
	}

	// Left arrow → Reviews
	ev = tcell.NewEventKey(tcell.KeyLeft, 0, 0)
	result = app.handleGlobalKey(ev)
	if result != nil {
		t.Error("left arrow should be consumed")
	}
	if app.header.ActiveTab() != TabReviews {
		t.Errorf("tab = %v, want TabReviews", app.header.ActiveTab())
	}

	// Left arrow → To Dos
	result = app.handleGlobalKey(ev)
	if app.header.ActiveTab() != TabToDos {
		t.Errorf("tab = %v, want TabToDos", app.header.ActiveTab())
	}

	// Left arrow → Tasks
	result = app.handleGlobalKey(ev)
	if app.header.ActiveTab() != TabTasks {
		t.Errorf("tab = %v, want TabTasks", app.header.ActiveTab())
	}

	// Left arrow at Tasks — stays on Tasks (no wrap)
	result = app.handleGlobalKey(ev)
	if app.header.ActiveTab() != TabTasks {
		t.Errorf("tab = %v, want TabTasks (no wrap)", app.header.ActiveTab())
	}
}

func TestCtrlCForwardsToAgentPTY(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	// Start a real process so we have a live session.
	task := &model.Task{
		ID:       "ctrl-c-test",
		Name:     "ctrl-c-test",
		Status:   model.StatusInProgress,
		Worktree: t.TempDir(),
		Backend:  "test",
	}
	cfg := config.DefaultConfig()
	cfg.Backends["test"] = config.Backend{Command: "sleep 30"}
	sess, err := runner.Start(task, cfg, 24, 80, false)
	if err != nil {
		t.Fatalf("runner.Start: %v", err)
	}
	defer runner.Stop(task.ID)

	// Enter agent mode with the session wired up
	app.mode = modeAgent
	app.agentState.Reset(task.ID, task.Name)
	app.agentPane.SetSession(sess)

	if !sess.Alive() {
		t.Fatal("session should be alive")
	}

	// ctrl+c in agent mode with live session should be consumed (forwarded to PTY)
	// and NOT stop the app.
	ev := tcell.NewEventKey(tcell.KeyCtrlC, 0, 0)
	result := app.handleGlobalKey(ev)
	if result != nil {
		t.Error("ctrl+c in agent mode with live session should be consumed")
	}
	if app.mode != modeAgent {
		t.Errorf("mode = %v, want modeAgent after ctrl+c with live session", app.mode)
	}
}

func TestCtrlCNoopInAgentViewDeadSession(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	// Agent mode with no session — ctrl+c should be consumed but not exit
	app.mode = modeAgent
	app.agentState.Reset("t1", "test")

	ev := tcell.NewEventKey(tcell.KeyCtrlC, 0, 0)
	result := app.handleGlobalKey(ev)
	if result != nil {
		t.Error("ctrl+c in agent mode with dead session should be consumed")
	}
	if app.mode != modeAgent {
		t.Errorf("mode = %v, want modeAgent after ctrl+c with no session", app.mode)
	}
}

func TestCtrlDExitsAgentViewWhenSessionDead(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	app.mode = modeAgent
	app.agentState.Reset("t1", "test")

	// No session running — ctrl+d should exit agent view
	ev := tcell.NewEventKey(tcell.KeyCtrlD, 0, 0)
	app.handleAgentKey(ev)

	if app.mode != modeTaskList {
		t.Errorf("mode = %v, want modeTaskList after ctrl+d with no session", app.mode)
	}
}

func TestEscapeStaysInAgentView(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	app.mode = modeAgent
	app.agentState.Reset("t1", "test")
	app.agentFocus = focusTerminal

	// No session running — escape should be consumed, NOT exit agent view
	ev := tcell.NewEventKey(tcell.KeyEscape, 0, 0)
	result := app.handleAgentKey(ev)

	if app.mode != modeAgent {
		t.Errorf("mode = %v, want modeAgent after escape with no session", app.mode)
	}
	if result != nil {
		t.Error("escape should return nil (consumed), not pass through to tview")
	}
}

func TestFilePanelKeyRouting(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	// Enter agent mode with file panel focused
	app.mode = modeAgent
	app.agentState.Reset("t1", "test")
	app.agentFocus = focusFiles
	app.filePanel.SetFocused(true)

	// Set the file panel rect so CursorDown can compute visible rows
	app.filePanel.SetRect(0, 0, 40, 20)

	// Populate files
	files := []gitutil.ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "A", Path: "b.go"},
		{Status: "D", Path: "c.go"},
	}
	app.filePanel.SetFiles(files)

	// Verify initial state
	if f := app.filePanel.SelectedFile(); f == nil || f.Path != "a.go" {
		t.Fatalf("initial selected file = %v, want a.go", f)
	}

	// Press Down arrow — should move cursor to b.go
	ev := tcell.NewEventKey(tcell.KeyDown, 0, 0)
	result := app.handleGlobalKey(ev)
	if result != nil {
		t.Error("Down arrow in file panel should be consumed (return nil)")
	}
	if f := app.filePanel.SelectedFile(); f == nil || f.Path != "b.go" {
		t.Errorf("after Down: selected = %v, want b.go", f)
	}

	// Press Up arrow — should move cursor back to a.go
	ev = tcell.NewEventKey(tcell.KeyUp, 0, 0)
	result = app.handleGlobalKey(ev)
	if result != nil {
		t.Error("Up arrow in file panel should be consumed (return nil)")
	}
	if f := app.filePanel.SelectedFile(); f == nil || f.Path != "a.go" {
		t.Errorf("after Up: selected = %v, want a.go", f)
	}
}

func TestDiffModeArrowsNavigateFiles(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	// Enter agent mode
	app.mode = modeAgent
	app.agentState.Reset("t1", "test")
	app.agentFocus = focusTerminal
	app.filePanel.SetRect(60, 0, 40, 20)

	// Populate files
	files := []gitutil.ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "A", Path: "b.go"},
		{Status: "D", Path: "c.go"},
	}
	app.filePanel.SetFiles(files)

	// Enter diff mode (simulate viewing a.go's diff)
	app.agentPane.EnterDiffMode("+line1\n-line2\n context", "a.go")
	if !app.agentPane.InDiffMode() {
		t.Fatal("should be in diff mode")
	}

	// Verify cursor starts on a.go
	if f := app.filePanel.SelectedFile(); f == nil || f.Path != "a.go" {
		t.Fatalf("initial = %v, want a.go", f)
	}

	// Press Down arrow — should move file cursor to b.go (not scroll diff)
	ev := tcell.NewEventKey(tcell.KeyDown, 0, 0)
	result := app.handleGlobalKey(ev)
	if result != nil {
		t.Error("Down in diff mode should be consumed")
	}
	if f := app.filePanel.SelectedFile(); f == nil || f.Path != "b.go" {
		t.Errorf("after Down: selected = %v, want b.go", f)
	}

	// Press Up arrow — should move file cursor back to a.go
	ev = tcell.NewEventKey(tcell.KeyUp, 0, 0)
	result = app.handleGlobalKey(ev)
	if result != nil {
		t.Error("Up in diff mode should be consumed")
	}
	if f := app.filePanel.SelectedFile(); f == nil || f.Path != "a.go" {
		t.Errorf("after Up: selected = %v, want a.go", f)
	}
}

func TestFilePanelMouseFocus(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	// Enter agent mode with terminal focused (default)
	app.mode = modeAgent
	app.agentState.Reset("t1", "test")
	app.agentFocus = focusTerminal

	// Set up file panel with rect and files
	app.filePanel.SetRect(60, 0, 40, 20)
	files := []gitutil.ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "A", Path: "b.go"},
	}
	app.filePanel.SetFiles(files)

	// Simulate clicking on the file panel — OnClick should switch agentFocus
	if app.filePanel.OnClick == nil {
		t.Fatal("OnClick callback not wired")
	}
	app.filePanel.OnClick()

	if app.agentFocus != focusFiles {
		t.Errorf("after click: agentFocus = %v, want focusFiles", app.agentFocus)
	}
	if !app.filePanel.focused {
		t.Error("after click: file panel should be focused")
	}

	// Now Up/Down should navigate files (key routing test)
	ev := tcell.NewEventKey(tcell.KeyDown, 0, 0)
	result := app.handleGlobalKey(ev)
	if result != nil {
		t.Error("Down arrow after mouse focus should be consumed")
	}
	if f := app.filePanel.SelectedFile(); f == nil || f.Path != "b.go" {
		t.Errorf("after click+Down: selected = %v, want b.go", f)
	}

	// Click on terminal pane should switch focus back
	if app.agentPane.OnClick == nil {
		t.Fatal("TerminalPane OnClick not wired")
	}
	app.agentPane.OnClick()

	if app.agentFocus != focusTerminal {
		t.Errorf("after terminal click: agentFocus = %v, want focusTerminal", app.agentFocus)
	}
}

func TestArrowsIgnoredInAgentMode(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	app.mode = modeAgent
	app.agentState.Reset("t1", "test")

	// Right arrow should NOT switch tabs in agent mode
	ev := tcell.NewEventKey(tcell.KeyRight, 0, 0)
	app.handleGlobalKey(ev)
	if app.header.ActiveTab() != TabTasks {
		t.Errorf("tab changed in agent mode: %v", app.header.ActiveTab())
	}
}

// ptySizeForPanel is tested inline below.

func TestRefreshTasks(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	// Add a task
	task := &model.Task{
		ID:        "t1",
		Name:      "task one",
		Status:    model.StatusPending,
		Project:   "proj",
		CreatedAt: time.Now(),
	}
	d.Add(task)

	app.refreshTasks()

	if len(app.tasks) != 1 {
		t.Errorf("len(tasks) = %d, want 1", len(app.tasks))
	}
	if !app.tasklist.HasTasks() {
		t.Error("tasklist should have tasks")
	}
}

func TestConfirmDeleteModal(t *testing.T) {
	task := &model.Task{
		ID:       "t1",
		Name:     "test task",
		Worktree: "/some/path",
		Branch:   "argus/test-task",
	}

	t.Run("cancel", func(t *testing.T) {
		m := NewConfirmDeleteModal(task)
		if m.Confirmed() || m.Canceled() {
			t.Error("modal should not be confirmed or canceled initially")
		}

		// Press Esc
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(p tview.Primitive) {})

		if !m.Canceled() {
			t.Error("modal should be canceled after Esc")
		}
		if m.Confirmed() {
			t.Error("modal should not be confirmed after Esc")
		}
	})

	t.Run("ctrl+q cancels", func(t *testing.T) {
		m := NewConfirmDeleteModal(task)

		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone), func(p tview.Primitive) {})

		if !m.Canceled() {
			t.Error("modal should be canceled after Ctrl+Q")
		}
		if m.Confirmed() {
			t.Error("modal should not be confirmed after Ctrl+Q")
		}
	})

	t.Run("confirm", func(t *testing.T) {
		m := NewConfirmDeleteModal(task)

		// Press Enter
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) {})

		if !m.Confirmed() {
			t.Error("modal should be confirmed after Enter")
		}
		if m.Canceled() {
			t.Error("modal should not be canceled after Enter")
		}
	})

	t.Run("task preserved", func(t *testing.T) {
		m := NewConfirmDeleteModal(task)
		if m.Task().ID != "t1" {
			t.Errorf("Task().ID = %q, want %q", m.Task().ID, "t1")
		}
	})
}

func TestOpenConfirmDelete(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	task := &model.Task{
		ID:        "t1",
		Name:      "test task",
		Status:    model.StatusPending,
		Project:   "proj",
		CreatedAt: time.Now(),
	}
	d.Add(task)
	app.refreshTasks()

	app.openConfirmDelete(task)

	if app.mode != modeConfirmDelete {
		t.Errorf("mode = %v, want modeConfirmDelete", app.mode)
	}
	if app.confirmDeleteModal == nil {
		t.Error("confirmDeleteModal should not be nil")
	}
}

func TestCloseConfirmDelete(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	task := &model.Task{
		ID:        "t1",
		Name:      "test task",
		Status:    model.StatusPending,
		Project:   "proj",
		CreatedAt: time.Now(),
	}
	d.Add(task)
	app.refreshTasks()

	// Open then close
	app.openConfirmDelete(task)
	app.closeConfirmDelete()

	if app.mode != modeTaskList {
		t.Errorf("mode = %v, want modeTaskList", app.mode)
	}
	if app.confirmDeleteModal != nil {
		t.Error("confirmDeleteModal should be nil after close")
	}
}

func TestDeleteTask(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	task := &model.Task{
		ID:        "t1",
		Name:      "test task",
		Status:    model.StatusPending,
		Project:   "proj",
		CreatedAt: time.Now(),
	}
	d.Add(task)
	app.refreshTasks()

	if len(app.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(app.tasks))
	}

	app.deleteTask(task)

	if len(app.tasks) != 0 {
		t.Errorf("expected 0 tasks after delete, got %d", len(app.tasks))
	}

	// Verify task is gone from DB
	tasks := d.Tasks()
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks in DB, got %d", len(tasks))
	}
}

func TestRefreshTasksLocal(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	d.Add(&model.Task{ID: "t1", Name: "task1", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t2", Name: "task2", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()})
	app.refreshTasks()

	if len(app.tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(app.tasks))
	}

	// Delete from DB, then use refreshTasksLocal (no RPC)
	d.Delete("t1")
	app.refreshTasksLocal()

	if len(app.tasks) != 1 {
		t.Errorf("expected 1 task after local refresh, got %d", len(app.tasks))
	}
	if app.tasks[0].ID != "t2" {
		t.Errorf("expected t2, got %s", app.tasks[0].ID)
	}
}

func TestCtrlDOpensConfirmDelete(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	task := &model.Task{
		ID:        "t1",
		Name:      "test task",
		Status:    model.StatusPending,
		Project:   "proj",
		CreatedAt: time.Now(),
	}
	d.Add(task)
	app.refreshTasks()

	// Ctrl+D on task list should open confirm modal
	ev := tcell.NewEventKey(tcell.KeyCtrlD, 0, 0)
	result := app.handleGlobalKey(ev)

	if result != nil {
		t.Error("Ctrl+D should be consumed (return nil)")
	}
	if app.mode != modeConfirmDelete {
		t.Errorf("mode = %v, want modeConfirmDelete", app.mode)
	}
}

func TestCtrlDDoesNotDeleteInAgentMode(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	app.mode = modeAgent
	app.agentState.Reset("t1", "test")

	// Ctrl+D in agent mode with no session exits agent view (not delete modal)
	ev := tcell.NewEventKey(tcell.KeyCtrlD, 0, 0)
	app.handleGlobalKey(ev)

	// Should return to task list, NOT open confirm delete modal
	if app.mode == modeConfirmDelete {
		t.Error("Ctrl+D in agent mode should not open delete modal")
	}
}

func TestPruneCompletedTasks(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)
	app.wtRoot = t.TempDir() // isolate from real worktrees

	// Add tasks with various statuses
	d.Add(&model.Task{ID: "t1", Name: "pending", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t2", Name: "done1", Status: model.StatusComplete, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t3", Name: "in-progress", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t4", Name: "done2", Status: model.StatusComplete, Project: "p", CreatedAt: time.Now()})
	app.refreshTasks()

	if len(app.tasks) != 4 {
		t.Fatalf("expected 4 tasks, got %d", len(app.tasks))
	}

	app.pruneCompletedTasks()

	if len(app.tasks) != 2 {
		t.Errorf("expected 2 tasks after prune, got %d", len(app.tasks))
	}

	// Only non-complete tasks should remain
	for _, task := range app.tasks {
		if task.Status == model.StatusComplete {
			t.Errorf("completed task %q should have been pruned", task.Name)
		}
	}
}

func TestPruneDoesNotDoubleCountWorktrees(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)
	wtRoot := t.TempDir()
	app.wtRoot = wtRoot

	// Create a worktree directory on disk for the completed task.
	wtPath := filepath.Join(wtRoot, "p", "done-task")
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}

	d.Add(&model.Task{
		ID: "t1", Name: "done-task", Status: model.StatusComplete,
		Project: "p", Worktree: wtPath, CreatedAt: time.Now(),
	})
	d.Add(&model.Task{
		ID: "t2", Name: "active", Status: model.StatusPending,
		Project: "p", CreatedAt: time.Now(),
	})
	app.refreshTasks()

	app.pruneCompletedTasks()

	// The prune modal total should be 1 (the completed task), not 2.
	// Before the fix, the worktree was counted once as a pruned task
	// AND once as an orphan (because PruneCompleted deletes the DB
	// record before WorktreePaths runs).
	if app.pruneModal == nil {
		t.Fatal("expected prune modal to be shown")
	}
	if app.pruneModal.total != 1 {
		t.Errorf("prune modal total = %d, want 1 (worktree double-counted as orphan)", app.pruneModal.total)
	}
}

func TestCtrlRPrunesCompleted(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)
	app.wtRoot = t.TempDir() // isolate from real worktrees

	d.Add(&model.Task{ID: "t1", Name: "pending", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t2", Name: "done", Status: model.StatusComplete, Project: "p", CreatedAt: time.Now()})
	app.refreshTasks()

	ev := tcell.NewEventKey(tcell.KeyCtrlR, 0, 0)
	result := app.handleGlobalKey(ev)

	if result != nil {
		t.Error("Ctrl+R should be consumed (return nil)")
	}
	if len(app.tasks) != 1 {
		t.Errorf("expected 1 task after Ctrl+R prune, got %d", len(app.tasks))
	}
}

func TestReconcileSkipsOnNilRunning(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	// Simulate daemon mode
	app.daemonConnected = true

	d.Add(&model.Task{ID: "t1", Name: "active-agent", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t2", Name: "also-active", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})

	// Pass nil runningIDs (simulates RPC failure) — should NOT reconcile
	app.refreshTasksWithIDs(nil, nil)

	for _, task := range app.tasks {
		if task.Status == model.StatusComplete {
			t.Errorf("task %q was wrongly reconciled to Complete on nil runningIDs", task.Name)
		}
	}
}

func TestReconcileWorksOnEmptyRunning(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	// Simulate daemon mode
	app.daemonConnected = true

	d.Add(&model.Task{ID: "t1", Name: "stale-task", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})

	// Pass empty non-nil runningIDs (daemon confirmed nothing running) — should reconcile
	app.refreshTasksWithIDs([]string{}, []string{})

	found := false
	for _, task := range app.tasks {
		if task.ID == "t1" && task.Status == model.StatusComplete {
			found = true
		}
	}
	if !found {
		t.Error("stale task should have been reconciled to Complete with empty (non-nil) runningIDs")
	}
}

func TestReconcileFreshDaemonUsesInReview(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, true) // daemonFreshStart = true

	app.daemonConnected = true

	d.Add(&model.Task{ID: "t1", Name: "was-running", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t2", Name: "was-pending", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()})

	// Fresh daemon has no sessions — InProgress tasks should become InReview, not Complete.
	app.refreshTasksWithIDs([]string{}, []string{})

	for _, task := range app.tasks {
		switch task.ID {
		case "t1":
			if task.Status != model.StatusInReview {
				t.Errorf("task %q: got status %s, want in_review (fresh daemon start)", task.Name, task.Status)
			}
		case "t2":
			if task.Status != model.StatusPending {
				t.Errorf("task %q: got status %s, want pending (should not be affected)", task.Name, task.Status)
			}
		}
	}

	// After first reconciliation, daemonFreshStart should be cleared.
	if app.daemonFreshStart {
		t.Error("daemonFreshStart should be cleared after first reconciliation")
	}
}

func TestReconcileFreshDaemonClearsFlag(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, true) // daemonFreshStart = true

	app.daemonConnected = true

	d.Add(&model.Task{ID: "t1", Name: "task-a", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})

	// First call: fresh start → InReview
	app.refreshTasksWithIDs([]string{}, []string{})

	// Add a new InProgress task after daemon is warmed up
	d.Add(&model.Task{ID: "t2", Name: "task-b", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})

	// Second call: flag cleared → should use Complete
	app.refreshTasksWithIDs([]string{}, []string{})

	for _, task := range app.tasks {
		switch task.ID {
		case "t1":
			// t1 was reconciled to InReview on the first call and should stay there.
			if task.Status != model.StatusInReview {
				t.Errorf("task %q: got status %s, want in_review (should remain from first reconciliation)", task.Name, task.Status)
			}
		case "t2":
			// t2 was added after the flag cleared — should use normal Complete path.
			if task.Status != model.StatusComplete {
				t.Errorf("task %q: got status %s, want complete (flag should be cleared after first reconciliation)", task.Name, task.Status)
			}
		}
	}
}

func TestWorktreeSubdir(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/Users/foo/.argus/worktrees/proj/task", true},
		{"/Users/foo/.claude/worktrees/proj/task", true},
		{"/Users/foo/projects/repo", false},
		{"/tmp/foo", false},
	}
	for _, tt := range tests {
		if got := isWorktreeSubdir(tt.path); got != tt.want {
			t.Errorf("isWorktreeSubdir(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestPRURLRegex(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://github.com/acme/widgets/pull/42", "https://github.com/acme/widgets/pull/42"},
		{"Created PR https://github.com/acme/widgets/pull/42\n", "https://github.com/acme/widgets/pull/42"},
		{"no url here", ""},
		// OSC 8 hyperlink: URL appears twice — take last match
		{"\x1b]8;;https://github.com/a/b/pull/1\x1b\\https://github.com/a/b/pull/1\x1b]8;;\x1b\\", "https://github.com/a/b/pull/1"},
		// Multiple PRs: take last
		{"https://github.com/a/b/pull/1 then https://github.com/a/b/pull/2", "https://github.com/a/b/pull/2"},
	}
	for _, tt := range tests {
		matches := prURLRe.FindAllString(tt.input, -1)
		got := ""
		if len(matches) > 0 {
			got = matches[len(matches)-1]
		}
		if got != tt.want {
			t.Errorf("prURLRe(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestScanAndStorePRURL(t *testing.T) {
	d := testDB(t)

	task := &model.Task{
		ID:      "pr-scan-1",
		Name:    "test",
		Project: "proj",
		Status:  model.StatusInProgress,
	}
	d.Add(task) //nolint:errcheck

	// Simulate what scanAndStorePRURL does (without needing a running tview app).
	output := []byte("Created https://github.com/acme/repo/pull/99\nDone.")
	matches := prURLRe.FindAll(output, -1)
	if len(matches) == 0 {
		t.Fatal("prURLRe should match PR URL in output")
	}
	url := string(matches[len(matches)-1])
	if url != "https://github.com/acme/repo/pull/99" {
		t.Errorf("matched URL = %q, want https://github.com/acme/repo/pull/99", url)
	}

	// Persist to DB (same as scanAndStorePRURL does).
	got, _ := d.Get("pr-scan-1")
	got.PRURL = url
	d.Update(got) //nolint:errcheck

	got2, _ := d.Get("pr-scan-1")
	if got2.PRURL != "https://github.com/acme/repo/pull/99" {
		t.Errorf("DB PRURL = %q, want https://github.com/acme/repo/pull/99", got2.PRURL)
	}

	// No match case.
	noURLOutput := []byte("no github link here")
	if matches := prURLRe.FindAll(noURLOutput, -1); len(matches) != 0 {
		t.Errorf("should not match in %q", noURLOutput)
	}
}

