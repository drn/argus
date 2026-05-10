package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/modal"
	"github.com/drn/argus/internal/tui/widget"
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

func TestHandleSessionExitUI_SkipsTransitionWhenPendingRestart(t *testing.T) {
	// Regression test for the TUI-during-API-kick race: if a kick-restart is
	// in flight, handleSessionExitUI must not flip the row to InReview —
	// otherwise the resumed session runs with the wrong status. Replaces the
	// previous fix that synchronously RPC'd HasPendingRestart from the tview
	// main goroutine; pendingRestart now arrives as an arg captured by the
	// caller from a non-RPC source.
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	task := &model.Task{Name: "kick-deferred", Status: model.StatusInProgress, Worktree: t.TempDir()}
	testutil.NoError(t, d.Add(task))

	app.handleSessionExitUI(task.ID, true /* stopped */, true /* pendingRestart */)

	fresh, _ := d.Get(task.ID)
	if fresh == nil {
		t.Fatal("task disappeared")
	}
	if fresh.Status != model.StatusInProgress {
		t.Errorf("expected status InProgress when pendingRestart=true, got %s", fresh.Status)
	}

	// Same skip behavior when stopped=false (process exited naturally during
	// a kick window — rare but valid).
	task2 := &model.Task{Name: "kick-pending-natural", Status: model.StatusInProgress, Worktree: t.TempDir()}
	testutil.NoError(t, d.Add(task2))
	app.handleSessionExitUI(task2.ID, false /* stopped */, true /* pendingRestart */)
	fresh2, _ := d.Get(task2.ID)
	if fresh2.Status != model.StatusInProgress {
		t.Errorf("expected status InProgress when pendingRestart=true and stopped=false, got %s", fresh2.Status)
	}

	// Without pendingRestart, stopped=true → InReview.
	task3 := &model.Task{Name: "kick-not-deferred-stop", Status: model.StatusInProgress, Worktree: t.TempDir()}
	testutil.NoError(t, d.Add(task3))
	app.handleSessionExitUI(task3.ID, true /* stopped */, false /* pendingRestart */)
	fresh3, _ := d.Get(task3.ID)
	if fresh3.Status != model.StatusInReview {
		t.Errorf("expected status InReview when pendingRestart=false, got %s", fresh3.Status)
	}

	// Without pendingRestart, stopped=false → Complete.
	task4 := &model.Task{Name: "natural-complete", Status: model.StatusInProgress, Worktree: t.TempDir()}
	testutil.NoError(t, d.Add(task4))
	app.handleSessionExitUI(task4.ID, false /* stopped */, false /* pendingRestart */)
	fresh4, _ := d.Get(task4.ID)
	if fresh4.Status != model.StatusComplete {
		t.Errorf("expected status Complete when pendingRestart=false and stopped=false, got %s", fresh4.Status)
	}
}

func TestNew(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

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
	app := New(d, runner, false)

	app.switchTab(widget.TabReviews)
	if app.header.ActiveTab() != widget.TabReviews {
		t.Errorf("tab = %v, want widget.TabReviews", app.header.ActiveTab())
	}

	app.switchTab(widget.TabSettings)
	if app.header.ActiveTab() != widget.TabSettings {
		t.Errorf("tab = %v, want widget.TabSettings", app.header.ActiveTab())
	}

	app.switchTab(widget.TabTasks)
	if app.header.ActiveTab() != widget.TabTasks {
		t.Errorf("tab = %v, want widget.TabTasks", app.header.ActiveTab())
	}
}

func TestOnTaskSelect(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	task := &model.Task{
		ID:   "test-1",
		Name: "test task",
	}

	app.onTaskSelect(task, true)

	if app.mode != modeAgent {
		t.Errorf("mode = %v, want modeAgent", app.mode)
	}
	if app.agentState.TaskID != "test-1" {
		t.Errorf("agentState.TaskID = %q, want %q", app.agentState.TaskID, "test-1")
	}
}

func TestOnTaskSelectAutoStart(t *testing.T) {
	t.Run("auto-start without session ID", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false)

		task := &model.Task{
			ID:   "t-no-sid",
			Name: "no session id",
		}
		task.SetStatus(model.StatusInReview)
		d.Add(task) //nolint:errcheck

		app.onTaskSelect(task, true)

		// Auto-start was attempted — the runner.Start will fail (no worktree),
		// which reverts the task to Pending. Proves auto-start was triggered
		// even without a SessionID.
		got, _ := d.Get("t-no-sid")
		if got.Status != model.StatusPending {
			t.Errorf("status = %v, want Pending (reverted after failed start)", got.Status)
		}
	})

	t.Run("no auto-start for completed task", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false)

		task := &model.Task{
			ID:        "t-complete",
			Name:      "completed task",
			SessionID: "sess-123",
		}
		task.SetStatus(model.StatusComplete)
		d.Add(task) //nolint:errcheck

		app.onTaskSelect(task, true)

		// Completed tasks should not auto-start.
		got, _ := d.Get("t-complete")
		if got.Status != model.StatusComplete {
			t.Errorf("status = %v, want Complete", got.Status)
		}
	})

	t.Run("auto-start for in-review task with session ID", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false)

		task := &model.Task{
			ID:        "t-resume",
			Name:      "resumable task",
			SessionID: "sess-456",
		}
		task.SetStatus(model.StatusInReview)
		d.Add(task) //nolint:errcheck

		app.onTaskSelect(task, true)

		// startSession was attempted — the runner.Start will fail (no
		// worktree), which reverts the task to Pending. Verify the revert
		// happened (proves auto-start was triggered).
		got, _ := d.Get("t-resume")
		if got.Status != model.StatusPending {
			t.Errorf("status = %v, want Pending (reverted after failed start)", got.Status)
		}
	})

	t.Run("no auto-start for archived task", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false)

		task := &model.Task{
			ID:        "t-archived",
			Name:      "archived task",
			SessionID: "sess-arc",
			Archived:  true,
		}
		task.SetStatus(model.StatusInReview)
		d.Add(task) //nolint:errcheck

		app.onTaskSelect(task, true)

		// Archived tasks should not auto-start.
		got, _ := d.Get("t-archived")
		if got.Status != model.StatusInReview {
			t.Errorf("status = %v, want InReview (archived tasks should not auto-start)", got.Status)
		}
	})

	t.Run("auto-start for pending task with session ID", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false)

		task := &model.Task{
			ID:        "t-pending",
			Name:      "pending resumable",
			SessionID: "sess-789",
		}
		task.SetStatus(model.StatusPending)
		d.Add(task) //nolint:errcheck

		app.onTaskSelect(task, true)

		// startSession was attempted — verifies auto-start triggers for
		// Pending tasks with a SessionID (daemon restart scenario).
		got, _ := d.Get("t-pending")
		// After failed start, task reverts to Pending with cleared SessionID.
		if got.SessionID != "" {
			t.Error("expected auto-start attempt to clear SessionID on failure")
		}
	})

	t.Run("no auto-start when autoStart is false", func(t *testing.T) {
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false)

		task := &model.Task{
			ID:        "t-navigate",
			Name:      "navigate target",
			SessionID: "sess-nav",
		}
		task.SetStatus(model.StatusInReview)
		d.Add(task) //nolint:errcheck

		app.onTaskSelect(task, false)

		// autoStart=false suppresses session start (used by navigateAgentTask).
		got, _ := d.Get("t-navigate")
		if got.Status != model.StatusInReview {
			t.Errorf("status = %v, want InReview (autoStart=false should not start)", got.Status)
		}
	})
}

func TestExitAgentView(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

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
	app := New(d, runner, false)

	// Start on Tasks tab
	if app.header.ActiveTab() != widget.TabTasks {
		t.Fatalf("initial tab = %v, want widget.TabTasks", app.header.ActiveTab())
	}

	// Right arrow → Reviews
	ev := tcell.NewEventKey(tcell.KeyRight, 0, 0)
	result := app.handleGlobalKey(ev)
	if result != nil {
		t.Error("right arrow should be consumed (return nil)")
	}
	if app.header.ActiveTab() != widget.TabReviews {
		t.Errorf("tab = %v, want widget.TabReviews", app.header.ActiveTab())
	}

	// Right arrow → Settings
	result = app.handleGlobalKey(ev)
	if app.header.ActiveTab() != widget.TabSettings {
		t.Errorf("tab = %v, want widget.TabSettings", app.header.ActiveTab())
	}

	// Right arrow at Settings — stays on Settings (no wrap)
	result = app.handleGlobalKey(ev)
	if app.header.ActiveTab() != widget.TabSettings {
		t.Errorf("tab = %v, want widget.TabSettings (no wrap)", app.header.ActiveTab())
	}

	// Left arrow → Reviews
	ev = tcell.NewEventKey(tcell.KeyLeft, 0, 0)
	result = app.handleGlobalKey(ev)
	if result != nil {
		t.Error("left arrow should be consumed")
	}
	if app.header.ActiveTab() != widget.TabReviews {
		t.Errorf("tab = %v, want widget.TabReviews", app.header.ActiveTab())
	}

	// Left arrow → Tasks
	result = app.handleGlobalKey(ev)
	if app.header.ActiveTab() != widget.TabTasks {
		t.Errorf("tab = %v, want widget.TabTasks", app.header.ActiveTab())
	}

	// Left arrow at Tasks — stays on Tasks (no wrap)
	result = app.handleGlobalKey(ev)
	if app.header.ActiveTab() != widget.TabTasks {
		t.Errorf("tab = %v, want widget.TabTasks (no wrap)", app.header.ActiveTab())
	}
}

func TestCtrlCForwardsToAgentPTY(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

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
	app := New(d, runner, false)

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
	app := New(d, runner, false)

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
	app := New(d, runner, false)

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

func TestCtrlLOpensLinkPicker(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.mode = modeAgent
	app.agentState.Reset("t1", "test")

	result := app.handleAgentKey(tcell.NewEventKey(tcell.KeyCtrlL, 0, tcell.ModNone))
	if result != nil {
		t.Error("Ctrl+L should return nil (consumed)")
	}
}

func TestFilePanelKeyRouting(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

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
	app := New(d, runner, false)

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
	app := New(d, runner, false)

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
	if !app.filePanel.Focused() {
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
	app := New(d, runner, false)

	app.mode = modeAgent
	app.agentState.Reset("t1", "test")

	// Right arrow should NOT switch tabs in agent mode
	ev := tcell.NewEventKey(tcell.KeyRight, 0, 0)
	app.handleGlobalKey(ev)
	if app.header.ActiveTab() != widget.TabTasks {
		t.Errorf("tab changed in agent mode: %v", app.header.ActiveTab())
	}
}

// ptySizeForPanel is tested inline below.

func TestRefreshTasks(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

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
		m := modal.NewConfirmDeleteModal(task)
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
		m := modal.NewConfirmDeleteModal(task)

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
		m := modal.NewConfirmDeleteModal(task)

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
		m := modal.NewConfirmDeleteModal(task)
		if m.Task().ID != "t1" {
			t.Errorf("Task().ID = %q, want %q", m.Task().ID, "t1")
		}
	})
}

func TestOpenConfirmDelete(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

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
	app := New(d, runner, false)

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
	app := New(d, runner, false)

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
	tasks, _ := d.Tasks()
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks in DB, got %d", len(tasks))
	}
}

func TestRefreshTasksLocal(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

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
	app := New(d, runner, false)

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
	app := New(d, runner, false)

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
	app := New(d, runner, false)
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
	app := New(d, runner, false)
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

	// The header notice should show 1 total, not 2.
	// Before the fix, the worktree was counted once as a pruned task
	// AND once as an orphan (because PruneCompleted deletes the DB
	// record before WorktreePaths runs).
	notice := app.header.Notice()
	if notice == "" {
		t.Fatal("expected header notice to be shown")
	}
	if !strings.Contains(notice, "0/1") {
		t.Errorf("header notice = %q, want progress showing total of 1 (not double-counted)", notice)
	}
}

func TestCtrlRPrunesCompleted(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
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
	app := New(d, runner, false)

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
	app := New(d, runner, false)

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

// Covers the happy path (InProgress flipped) and idempotency on rows already
// in a terminal state. The database.Tasks() error path is not exercised
// directly — propagation is straight pass-through and the helper has no
// other behavior on top of it.
func TestReconcileStaleSessionsFlipsInProgress(t *testing.T) {
	d := testDB(t)

	d.Add(&model.Task{ID: "t1", Name: "was-running", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t2", Name: "was-pending", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()})
	d.Add(&model.Task{ID: "t3", Name: "was-review", Status: model.StatusInReview, Project: "p", CreatedAt: time.Now()})

	n, err := agent.ReconcileStaleSessions(d)
	if err != nil {
		t.Fatalf("ReconcileStaleSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}

	tasks, _ := d.Tasks()
	for _, task := range tasks {
		switch task.ID {
		case "t1":
			if task.Status != model.StatusInReview {
				t.Errorf("task %q: got %s, want in_review", task.Name, task.Status)
			}
		case "t2":
			if task.Status != model.StatusPending {
				t.Errorf("task %q: got %s, want pending (untouched)", task.Name, task.Status)
			}
		case "t3":
			if task.Status != model.StatusInReview {
				t.Errorf("task %q: got %s, want in_review (untouched)", task.Name, task.Status)
			}
		}
	}
}

// TestReconcileSkipsOnStaleStartGen and TestReconcileWorksWhenStartGenUnchanged
// replicate the startGen guard logic from onTick's QueueUpdateDraw callback
// inline. This is intentional — onTick involves a tick goroutine + RPC +
// QueueUpdateDraw pipeline that isn't unit-testable. If the guard condition
// in onTick changes, these tests must be updated in lockstep.
func TestReconcileSkipsOnStaleStartGen(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.daemonConnected = true

	d.Add(&model.Task{ID: "t1", Name: "just-started", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})

	// Simulate the race: tick captures startGen=0, then startSession bumps it.
	startGen := app.startGen.Load()
	app.startGen.Add(1) // simulates startSession

	// Stale runningIDs (empty — captured before session existed).
	runningIDs := []string{}

	// Simulate what onTick's QueueUpdateDraw callback does:
	// startGen changed → pass nil to skip reconciliation.
	if app.startGen.Load() != startGen {
		runningIDs = nil
	}
	app.refreshTasksWithIDs(runningIDs, []string{})

	for _, task := range app.tasks {
		if task.ID == "t1" {
			// Should NOT be reconciled — startGen mismatch skipped it.
			testutil.Equal(t, task.Status, model.StatusInProgress)
		}
	}
}

// TestRefreshTasksAsyncStartGenGuard replicates the startGen guard in
// refreshTasksAsync. Before the fix, refreshTasksAsync had no guard — a
// session exit calling refreshTasksAsync while a new task was starting would
// capture stale runningIDs and reconcile the new task to Complete.
func TestRefreshTasksAsyncStartGenGuard(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.daemonConnected = true

	d.Add(&model.Task{ID: "t1", Name: "just-started", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})

	// Simulate: refreshTasksAsync captures startGen before RPC...
	startGen := app.startGen.Load()

	// ...then startSession bumps it while the RPC is in-flight.
	app.startGen.Add(1)

	// RPC returns stale empty runningIDs (new session not yet registered).
	runningIDs := []string{}

	// Simulate what refreshTasksAsync's QueueUpdateDraw callback now does:
	if app.startGen.Load() != startGen {
		runningIDs = nil
	}
	app.refreshTasksWithIDs(runningIDs, []string{})

	for _, task := range app.tasks {
		if task.ID == "t1" {
			testutil.Equal(t, task.Status, model.StatusInProgress)
		}
	}
}

func TestReconcileWorksWhenStartGenUnchanged(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.daemonConnected = true

	d.Add(&model.Task{ID: "t1", Name: "stale-task", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})

	// No startGen change — runningIDs are fresh and trustworthy.
	// (No guard needed; startGen unchanged means reconciliation proceeds normally.)
	app.refreshTasksWithIDs([]string{}, []string{})

	for _, task := range app.tasks {
		if task.ID == "t1" {
			testutil.Equal(t, task.Status, model.StatusComplete)
		}
	}
}

// TestReconcileGracePeriodProtectsRecentStarts verifies that tasks started
// within the last 5 seconds are not reconciled to Complete even if they are
// not in the running set. This protects against restart cascade races where
// ListSessions returns stale data.
func TestReconcileGracePeriodProtectsRecentStarts(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.daemonConnected = true

	d.Add(&model.Task{ID: "t1", Name: "recently-started", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})

	// Simulate startSession recording the start time.
	app.recentStarts["t1"] = time.Now()

	// Empty running set — session not yet visible to ListSessions.
	app.refreshTasksWithIDs([]string{}, []string{})

	// Task should be protected by grace period.
	for _, task := range app.tasks {
		if task.ID == "t1" {
			testutil.Equal(t, task.Status, model.StatusInProgress)
		}
	}
}

// TestReconcileGracePeriodExpiresAfterTimeout verifies that the grace period
// expires and allows reconciliation after the timeout.
func TestReconcileGracePeriodExpiresAfterTimeout(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.daemonConnected = true

	d.Add(&model.Task{ID: "t1", Name: "old-start", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()})

	// Set start time in the past (beyond grace period).
	app.recentStarts["t1"] = time.Now().Add(-10 * time.Second)

	app.refreshTasksWithIDs([]string{}, []string{})

	for _, task := range app.tasks {
		if task.ID == "t1" {
			testutil.Equal(t, task.Status, model.StatusComplete)
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
		if got := agent.IsWorktreeSubdir(tt.path); got != tt.want {
			t.Errorf("agent.IsWorktreeSubdir(%q) = %v, want %v", tt.path, got, tt.want)
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

func TestPTYSizeFromHostTerm(t *testing.T) {
	cases := []struct {
		name              string
		tw, th            int
		err               error
		wantRows, wantCol uint16
	}{
		{"typical wide", 320, 100, nil, 95, 190},
		{"standard 80x24", 80, 24, nil, 19, 46},
		// 50-col host: 50*3/5-2 = 28 ⇒ no clamp.
		{"narrow 50x20", 50, 20, nil, 15, 28},
		// Pathological tiny host triggers both clamps.
		{"tiny clamps both floors", 30, 8, nil, 5, 20},
		// Real-world reproduction of the original bug. Anything works as long
		// as it isn't 20x8 — the PTY size that left Claude rendering narrow.
		{"realistic iTerm2 split", 200, 60, nil, 55, 118},
		// Unusable signals: function must yield 0,0 so callers fall back.
		{"err short-circuits", 320, 100, errFakeNoTTY, 0, 0},
		{"zero width", 0, 100, nil, 0, 0},
		{"zero height", 320, 0, nil, 0, 0},
		{"negative", -1, -1, nil, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRows, gotCols := ptySizeFromHostTerm(tc.tw, tc.th, tc.err)
			testutil.Equal(t, gotRows, tc.wantRows)
			testutil.Equal(t, gotCols, tc.wantCol)
		})
	}
}

func TestPTYSizeFromPaneRect(t *testing.T) {
	cases := []struct {
		name              string
		pw, ph            int
		wantRows, wantCol uint16
	}{
		// The bug: tview's NewBox returns 15x10 before Flex lays it out.
		// Reading that as authoritative produced a 20x8 PTY.
		{"tview Box default rejected", 15, 10, 0, 0},
		// Anything at-or-below the threshold falls through too.
		{"30x10 still rejected", 30, 10, 0, 0},
		{"20x8 (pre-fix output) rejected", 20, 8, 0, 0},
		// Genuinely laid-out panes pass.
		{"laid-out wide pane", 192, 84, 82, 190},
		{"31x11 (just above floor)", 31, 11, 9, 29},
		// Zero / negative are noise.
		{"zero rejected", 0, 0, 0, 0},
		{"negative rejected", -1, -1, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRows, gotCols := ptySizeFromPaneRect(tc.pw, tc.ph)
			testutil.Equal(t, gotRows, tc.wantRows)
			testutil.Equal(t, gotCols, tc.wantCol)
		})
	}
}

// errFakeNoTTY stands in for term.GetSize's "inappropriate ioctl for device"
// error when stdout isn't a TTY.
var errFakeNoTTY = &fakeErr{msg: "inappropriate ioctl for device"}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }
