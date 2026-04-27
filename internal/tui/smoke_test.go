package tui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// --- Test helpers for SimulationScreen-based integration tests ---
//
// These tests run a real tview event loop against a SimulationScreen.
// They NEVER connect to a live daemon, touch ~/.argus/, or spawn processes.
// All state is in-memory (db.OpenInMemory, agent.NewRunner(nil)).

// uiTimeout is the maximum time to wait for tview event loop operations.
const uiTimeout = 2 * time.Second

// eventSettle is the time to let injected events propagate from the
// SimulationScreen's event queue into tview's event loop. SimulationScreen
// delivers events via a channel that tview polls in a separate goroutine,
// so injected events aren't instantly visible to QueueUpdate callbacks.
const eventSettle = 50 * time.Millisecond

// pasteCapture is a minimal tview.Primitive that records paste events.
type pasteCapture struct {
	*tview.Box
	mu     sync.Mutex
	pasted string
}

func (p *pasteCapture) PasteHandler() func(string, func(tview.Primitive)) {
	return p.WrapPasteHandler(func(text string, setFocus func(tview.Primitive)) {
		p.mu.Lock()
		p.pasted = text
		p.mu.Unlock()
	})
}

func (p *pasteCapture) getPasted() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pasted
}

// mouseCapture is a minimal tview.Primitive that records mouse events.
type mouseCapture struct {
	*tview.Box
	mu  sync.Mutex
	got bool
}

func (m *mouseCapture) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return m.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(tview.Primitive)) (bool, tview.Primitive) {
		m.mu.Lock()
		m.got = true
		m.mu.Unlock()
		return true, nil
	})
}

func (m *mouseCapture) gotMouse() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.got
}

// simApp creates a tview.Application wired to a SimulationScreen wrapped in
// lazyScreen. Returns the app, sim screen, and lazyScreen. The caller must
// call app.Stop() to shut down (which also calls sim.Fini()).
func simApp(t *testing.T) (*tview.Application, tcell.SimulationScreen, *lazyScreen) {
	t.Helper()
	app := tview.NewApplication()
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(80, 24)
	ls := &lazyScreen{Screen: sim}
	app.SetScreen(ls)
	// Critical: EnableMouse/EnablePaste AFTER SetScreen.
	app.EnableMouse(true)
	app.EnablePaste(true)
	return app, sim, ls
}

// runApp starts the tview event loop in a goroutine and returns a function
// to stop it and wait for shutdown.
func runApp(t *testing.T, app *tview.Application) func() {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.Run() //nolint:errcheck
	}()
	// Wait for the event loop to be alive.
	syncUI(t, app)
	return func() {
		app.Stop()
		select {
		case <-done:
		case <-time.After(uiTimeout):
			t.Fatal("tview event loop did not stop within timeout")
		}
	}
}

// syncUI waits for injected events to propagate through the tview event loop.
// It sleeps briefly to let SimulationScreen deliver events, then executes a
// QueueUpdate round-trip to confirm tview has processed them.
func syncUI(t *testing.T, app *tview.Application) {
	t.Helper()
	time.Sleep(eventSettle)
	ch := make(chan struct{})
	app.QueueUpdate(func() { close(ch) })
	select {
	case <-ch:
	case <-time.After(uiTimeout):
		t.Fatal("timed out waiting for tview event loop")
	}
}

// readUI executes fn on the tview goroutine and waits for it to complete.
// Use this to safely read tview state without data races.
func readUI(t *testing.T, app *tview.Application, fn func()) {
	t.Helper()
	ch := make(chan struct{})
	app.QueueUpdate(func() {
		fn()
		close(ch)
	})
	select {
	case <-ch:
	case <-time.After(uiTimeout):
		t.Fatal("timed out reading UI state")
	}
}

// wireApp replaces an App's tview.Application with a SimulationScreen-backed
// one for testing. Sets app.screen to match production wiring (Run sets
// app.screen). Returns the sim screen and stop function. This does NOT
// start a daemon, connect to sockets, or touch ~/.argus/.
func wireApp(t *testing.T, app *App) (tcell.SimulationScreen, func()) {
	t.Helper()
	tApp, sim, ls := simApp(t)
	app.tapp = tApp
	app.screen = ls // match production wiring (Run sets app.screen)
	app.tapp.SetInputCapture(app.handleGlobalKey)
	app.tapp.SetRoot(app.root, true)
	stop := runApp(t, tApp)
	return sim, stop
}

// ---------- 1. SimulationScreen integration tests for tview setup ----------

func TestEnablePasteAfterSetScreen(t *testing.T) {
	app, sim, _ := simApp(t)

	w := &pasteCapture{Box: tview.NewBox()}
	app.SetRoot(w, true)
	stop := runApp(t, app)
	defer stop()

	// Inject bracketed paste: start → keys → end.
	sim.PostEvent(tcell.NewEventPaste(true))
	sim.InjectKey(tcell.KeyRune, 'X', 0)
	sim.InjectKey(tcell.KeyRune, 'Y', 0)
	sim.PostEvent(tcell.NewEventPaste(false))

	syncUI(t, app)
	testutil.Equal(t, w.getPasted(), "XY")
}

// Note: A negative test (EnablePaste before SetScreen) is not possible with
// SimulationScreen because PostEvent injects EventPaste directly into the
// event queue, bypassing the real terminal's bracket paste mode. In a real
// terminal, the broken ordering means the terminal never sends bracket paste
// escape sequences, so paste arrives as individual keystrokes.

func TestEnableMouseAfterSetScreen(t *testing.T) {
	// Verify mouse events are delivered when EnableMouse is called
	// after SetScreen (same ordering issue as paste).
	app, sim, _ := simApp(t)

	w := &mouseCapture{Box: tview.NewBox()}
	app.SetRoot(w, true)
	stop := runApp(t, app)
	defer stop()

	sim.InjectMouse(5, 5, tcell.Button1, 0)
	syncUI(t, app)

	if !w.gotMouse() {
		t.Error("mouse event not received — EnableMouse may not be applied to screen")
	}
}

func TestLazyScreen_EnableDisableDoesNotPanic(t *testing.T) {
	// Verify that lazyScreen's embedding correctly forwards EnablePaste,
	// DisablePaste, EnableMouse, DisableMouse to the underlying screen
	// without panic. SimulationScreen's paste/mouse fields are unexported
	// so we can only verify the calls don't crash.
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	defer sim.Fini()

	ls := &lazyScreen{Screen: sim}
	ls.EnablePaste()
	ls.DisablePaste()
	ls.EnableMouse()
	ls.DisableMouse()
}

// ---------- 2. App smoke tests for major UI paths ----------

func TestSmoke_RestartDaemonPrompt_OpensAndSkips(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, true, false)

	sim, stop := wireApp(t, app)
	defer stop()

	// Open the modal on the tview goroutine (mimics what Run() does when
	// SetDaemonStale was called before the event loop started).
	readUI(t, app.tapp, func() { app.openRestartDaemonPrompt() })

	var mode viewMode
	var hasModal bool
	readUI(t, app.tapp, func() {
		mode = app.mode
		hasModal = app.restartDaemonModal != nil
	})
	if mode != modeRestartDaemonPrompt || !hasModal {
		t.Fatalf("modal not open: mode=%v hasModal=%v", mode, hasModal)
	}

	// Pressing Esc should choose Skip and dismiss.
	sim.InjectKey(tcell.KeyEscape, 0, 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() {
		mode = app.mode
		hasModal = app.restartDaemonModal != nil
	})
	if mode != modeTaskList {
		t.Errorf("mode after skip = %v, want modeTaskList", mode)
	}
	if hasModal {
		t.Error("restartDaemonModal should be cleared after skip")
	}
}

func TestSetDaemonStale_StoresFlag(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, true, false)

	if app.daemonStale {
		t.Error("daemonStale should default to false")
	}
	app.SetDaemonStale(true)
	if !app.daemonStale {
		t.Error("SetDaemonStale(true) should set the flag")
	}
}

// Regression test for the startup deadlock fixed in 67eda38. Run() opens the
// daemon-stale prompt directly because tview v0.42's QueueUpdate is
// synchronous (sends on `updates`, then blocks on a per-call done channel
// until the event loop runs the closure). The contract this test pins:
// openRestartDaemonPrompt itself must remain safe to call without an event
// loop running, because Run() calls it directly before tapp.Run(). If
// someone modifies openRestartDaemonPrompt to internally use QueueUpdate /
// QueueUpdateDraw, this test will time out.
//
// Note: this test does NOT cover the case of Run() itself re-wrapping the
// call in QueueUpdateDraw — that regression is guarded by the explicit
// comment in app.go and the gotcha entry in ui-threading.md, plus would
// require a Run()-with-sim-screen harness we don't have.
func TestSmoke_OpenRestartDaemonPromptBeforeRunDoesNotBlock(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, true, false)

	tApp, _, ls := simApp(t)
	app.tapp = tApp
	app.screen = ls
	app.tapp.SetInputCapture(app.handleGlobalKey)
	app.tapp.SetRoot(app.root, true)
	// Deliberately NOT starting the event loop — this mimics the window
	// inside Run() between SetScreen and tapp.Run().

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.openRestartDaemonPrompt()
	}()

	select {
	case <-done:
	case <-time.After(uiTimeout):
		t.Fatal("openRestartDaemonPrompt blocked before tapp.Run() — likely re-introduced QueueUpdateDraw deadlock")
	}

	if app.mode != modeRestartDaemonPrompt {
		t.Errorf("mode = %v, want modeRestartDaemonPrompt", app.mode)
	}
	if app.restartDaemonModal == nil {
		t.Error("restartDaemonModal should be set")
	}
}

func TestSmoke_TabSwitching(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	sim, stop := wireApp(t, app)
	defer stop()

	// Switch to each tab via numeric keys.
	for _, tc := range []struct {
		key  rune
		want widget.Tab
	}{
		{'2', widget.TabToDos},
		{'3', widget.TabReviews},
		{'4', widget.TabSettings},
		{'1', widget.TabTasks},
	} {
		sim.InjectKey(tcell.KeyRune, tc.key, 0)
		syncUI(t, app.tapp)
		// Read tab state on the tview goroutine to avoid data races.
		var got widget.Tab
		readUI(t, app.tapp, func() { got = app.header.ActiveTab() })
		if got != tc.want {
			t.Errorf("key %c: tab = %d, want %d", tc.key, got, tc.want)
		}
	}
}

func TestSmoke_NewTaskFormPaste(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)
	// Ensure there's a project and backend for the form.
	d.SetProject("test", config.Project{Path: t.TempDir()})

	sim, stop := wireApp(t, app)
	defer stop()

	// Open new task form via 'n' key.
	sim.InjectKey(tcell.KeyRune, 'n', 0)
	syncUI(t, app.tapp)

	var form *NewTaskForm
	readUI(t, app.tapp, func() { form = app.newTaskForm })

	if form == nil {
		t.Fatal("new task form should be open after 'n' key")
	}

	// Paste into the prompt field.
	sim.PostEvent(tcell.NewEventPaste(true))
	for _, r := range "pasted prompt text" {
		sim.InjectKey(tcell.KeyRune, r, 0)
	}
	sim.PostEvent(tcell.NewEventPaste(false))
	syncUI(t, app.tapp)

	var prompt string
	readUI(t, app.tapp, func() { prompt = string(form.prompt) })
	testutil.Equal(t, prompt, "pasted prompt text")
}

func TestSmoke_AgentViewEnterExit(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	task := &model.Task{
		ID:        "smoke-1",
		Name:      "smoke test",
		Status:    model.StatusPending,
		Project:   "p",
		CreatedAt: time.Now(),
	}
	d.Add(task)
	// refreshTasks populates the task list with cursor on the first (only) task.
	app.refreshTasks()

	sim, stop := wireApp(t, app)
	defer stop()

	// Enter agent view via Enter on the task.
	sim.InjectKey(tcell.KeyEnter, 0, 0)
	syncUI(t, app.tapp)

	var mode viewMode
	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modeAgent)

	// Exit via Ctrl+D (no live session).
	sim.InjectKey(tcell.KeyCtrlD, 0, 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modeTaskList)
}

// TestSmoke_ExitAgentViewResetsTab verifies that exiting agent view resets the
// header tab to widget.TabTasks. This is critical when the agent was entered from a
// non-Tasks tab (e.g. ToDos): without the reset, the global key handler routes
// up/down keys to the wrong tab's handler, breaking task list navigation.
func TestSmoke_ExitAgentViewResetsTab(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	task := &model.Task{
		ID:        "tab-reset-1",
		Name:      "tab reset test",
		Status:    model.StatusPending,
		Project:   "p",
		CreatedAt: time.Now(),
	}
	d.Add(task)
	app.refreshTasks()

	_, stop := wireApp(t, app)
	defer stop()

	// Simulate entering agent view from the ToDos tab: set header to widget.TabToDos,
	// then enter agent view.
	readUI(t, app.tapp, func() {
		app.header.SetTab(widget.TabToDos)
		app.onTaskSelect(task, true)
	})

	var mode viewMode
	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modeAgent)

	// Exit agent view (Ctrl+D with no session).
	readUI(t, app.tapp, func() {
		app.exitAgentView()
	})

	var tab widget.Tab
	readUI(t, app.tapp, func() {
		mode = app.mode
		tab = app.header.ActiveTab()
	})
	testutil.Equal(t, mode, modeTaskList)
	testutil.Equal(t, tab, widget.TabTasks)
}

func TestSmoke_LinkPickerFocusRestore(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	_, stop := wireApp(t, app)
	defer stop()

	// Open and close the link picker modal on the tview goroutine.
	links := []Link{
		{Label: "Example", URL: "https://example.com"},
		{Label: "Other", URL: "https://other.com"},
	}
	readUI(t, app.tapp, func() {
		app.openLinkPickerModal(links)
	})

	var mode viewMode
	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modeLinkPicker)

	// Close modal — should restore focus to tasklist.
	readUI(t, app.tapp, func() {
		app.closeLinkPickerModal()
	})

	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modeTaskList)

	// Verify focus was restored to the tasklist widget.
	var focused tview.Primitive
	readUI(t, app.tapp, func() { focused = app.tapp.GetFocus() })
	if focused != app.tasklist {
		t.Error("focus should be on tasklist after link picker close, but it is not")
	}
}

func TestSmoke_FuzzyLinkPickerLifecycle(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	sim, stop := wireApp(t, app)
	defer stop()

	links := []Link{
		{Label: "GitHub", URL: "https://github.com/foo"},
		{Label: "Docs", URL: "https://docs.example.com"},
	}

	// Open fuzzy link picker from agent mode context.
	readUI(t, app.tapp, func() {
		app.mode = modeAgent
		app.openFuzzyLinkPickerModal(links)
	})

	var mode viewMode
	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modeFuzzyLinkPicker)

	// Close via Escape through the event loop.
	sim.InjectKey(tcell.KeyEscape, 0, 0)
	time.Sleep(50 * time.Millisecond)

	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modeAgent)
}

func TestSmoke_NewTaskFormEscape(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)
	d.SetProject("test", config.Project{Path: t.TempDir()})

	sim, stop := wireApp(t, app)
	defer stop()

	// Open and close the new task form.
	sim.InjectKey(tcell.KeyRune, 'n', 0)
	syncUI(t, app.tapp)

	var isNewTask bool
	readUI(t, app.tapp, func() { isNewTask = app.mode == modeNewTask })
	testutil.Equal(t, isNewTask, true)

	sim.InjectKey(tcell.KeyEscape, 0, 0)
	syncUI(t, app.tapp)

	var isTaskList bool
	readUI(t, app.tapp, func() { isTaskList = app.mode == modeTaskList })
	testutil.Equal(t, isTaskList, true)
}

// TestSmoke_ForceRedrawOnTransitions verifies that layout-changing transitions
// (tab switch, agent view enter/exit, Ctrl+L) all invoke forceRedraw.
// Guards against regression where a new transition path forgets to force a
// tcell Sync, re-introducing ghost cells in Alacritty+tmux.
func TestSmoke_ForceRedrawOnTransitions(t *testing.T) {
	// Point uxlog at a temp file so we can inspect the "[tui] force redraw"
	// entries produced by forceRedraw.
	logPath := filepath.Join(t.TempDir(), "ux.log")
	if err := uxlog.Init(logPath); err != nil {
		t.Fatalf("uxlog.Init: %v", err)
	}
	defer uxlog.Close()

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	task := &model.Task{
		ID:        "redraw-1",
		Name:      "redraw test",
		Status:    model.StatusPending,
		Project:   "p",
		CreatedAt: time.Now(),
	}
	d.Add(task)
	app.refreshTasks()

	sim, stop := wireApp(t, app)
	defer stop()

	readLog := func() string {
		b, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		return string(b)
	}

	// Tab switch (1→2) fires forceRedraw.
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)
	testutil.Contains(t, readLog(), "force redraw: tab switch")

	// Ctrl+L in non-agent mode fires forceRedraw.
	sim.InjectKey(tcell.KeyCtrlL, 0, 0)
	syncUI(t, app.tapp)
	testutil.Contains(t, readLog(), "force redraw: ctrl+l")

	// Back to Tasks so Enter can open the agent view.
	sim.InjectKey(tcell.KeyRune, '1', 0)
	syncUI(t, app.tapp)

	// Enter agent view fires forceRedraw.
	sim.InjectKey(tcell.KeyEnter, 0, 0)
	syncUI(t, app.tapp)
	testutil.Contains(t, readLog(), "force redraw: enter agent view")

	// Exit agent view fires forceRedraw.
	sim.InjectKey(tcell.KeyCtrlQ, 0, 0)
	syncUI(t, app.tapp)
	testutil.Contains(t, readLog(), "force redraw: exit agent view")

	// Sanity: only one "exit agent view" entry — confirms no double-Sync
	// from switchTab → exitAgentView paths.
	if count := strings.Count(readLog(), "force redraw: exit agent view"); count != 1 {
		t.Errorf("expected 1 exit-agent-view redraw entry, got %d", count)
	}
}

func TestSmoke_WaitingReviewToggle(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	task := &model.Task{
		ID:        "wr-1",
		Name:      "wr smoke",
		Status:    model.StatusInReview,
		Project:   "p",
		CreatedAt: time.Now(),
	}
	if err := d.Add(task); err != nil {
		t.Fatal(err)
	}
	app.refreshTasks()

	sim, stop := wireApp(t, app)
	defer stop()

	// Press 'w' — task should be flagged WaitingReview, persisted to DB,
	// and the tasklist should still be focused.
	sim.InjectKey(tcell.KeyRune, 'w', 0)
	syncUI(t, app.tapp)

	got, err := d.Get("wr-1")
	testutil.NoError(t, err)
	if !got.WaitingReview {
		t.Error("'w' key did not flag task as WaitingReview in DB")
	}

	var focused tview.Primitive
	readUI(t, app.tapp, func() { focused = app.tapp.GetFocus() })
	if focused != app.tasklist {
		t.Error("focus should remain on tasklist after 'w'")
	}
}

func TestSmoke_ClickNonInteractivePanelKeepsFocus(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false, false)

	task := &model.Task{
		ID:        "click-1",
		Name:      "click test",
		Status:    model.StatusPending,
		Project:   "p",
		CreatedAt: time.Now(),
	}
	d.Add(task)
	app.refreshTasks()

	sim, stop := wireApp(t, app)
	defer stop()

	// Verify initial focus is on the task list.
	var focused tview.Primitive
	readUI(t, app.tapp, func() { focused = app.tapp.GetFocus() })
	if focused != app.tasklist {
		t.Fatal("expected initial focus on tasklist")
	}

	// Click on the center panel area (preview/git panel) — coordinates in the
	// non-interactive region of the 80x24 screen. The 3-column layout with
	// proportions 1:3:1 puts the center panel around x=16..64.
	sim.InjectMouse(40, 12, tcell.Button1, 0)
	syncUI(t, app.tapp)

	// Focus must remain on the task list, not stolen by the clicked panel.
	readUI(t, app.tapp, func() { focused = app.tapp.GetFocus() })
	if focused != app.tasklist {
		t.Error("clicking non-interactive panel stole focus from tasklist")
	}

	// Also click on the detail panel (rightmost column, ~x=70).
	sim.InjectMouse(70, 12, tcell.Button1, 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() { focused = app.tapp.GetFocus() })
	if focused != app.tasklist {
		t.Error("clicking detail panel stole focus from tasklist")
	}
}
