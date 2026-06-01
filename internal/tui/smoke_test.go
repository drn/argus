package tui

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/gitutil"
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
	// afterDraw handles ONE thing: terminal resize → screen.Sync(). The
	// full pendingSync/forceRedraw scaffolding from the deleted era is
	// NOT here. wireApp swaps app.tapp so we re-register afterDraw.
	app.tapp.SetAfterDrawFunc(app.afterDraw)
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
	app := New(d, runner, true)

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
	app := New(d, runner, true)

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
	app := New(d, runner, true)

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
	app := New(d, runner, false)

	sim, stop := wireApp(t, app)
	defer stop()

	// Switch to each tab via numeric keys. After TabDAG was inserted between
	// TabTasks and TabSettings, the numeric mapping is 1=Tasks, 2=DAG,
	// 3=Settings.
	for _, tc := range []struct {
		key  rune
		want widget.Tab
	}{
		{'2', widget.TabDAG},
		{'3', widget.TabSettings},
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

func TestSmoke_HelpModalOpensOnQuestionKey(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	sim, stop := wireApp(t, app)
	defer stop()

	sim.InjectKey(tcell.KeyRune, '?', 0)
	syncUI(t, app.tapp)

	var open bool
	readUI(t, app.tapp, func() { open = app.helpModal != nil && app.mode == modeHelp })
	if !open {
		t.Fatal("? should open the help modal")
	}

	// Esc closes it and restores the task list mode.
	sim.InjectKey(tcell.KeyEscape, 0, 0)
	syncUI(t, app.tapp)

	var closed bool
	readUI(t, app.tapp, func() { closed = app.helpModal == nil && app.mode == modeTaskList })
	if !closed {
		t.Fatal("Esc should close the help modal")
	}
}

func TestSmoke_NewTaskFormPaste(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
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

	// Poll for the prompt to populate. syncUI's 50 ms eventSettle is enough
	// on a quiet machine but not reliably enough under -race on CI, where
	// draining 20 queued events through the tcell→tview boundary can take
	// longer than the fixed wait.
	var prompt string
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		syncUI(t, app.tapp)
		readUI(t, app.tapp, func() { prompt = string(form.prompt) })
		if prompt == "pasted prompt text" {
			break
		}
	}
	testutil.Equal(t, prompt, "pasted prompt text")
}

func TestSmoke_AgentViewEnterExit(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

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

// TestSmoke_AgentZenToggle verifies Ctrl+Z collapses the side panels to zero
// width (single-pane zoom) and toggles back to the 1:3:1 layout, and that
// exiting the agent view while zoomed restores the panels.
func TestSmoke_AgentZenToggle(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	task := &model.Task{
		ID:        "zen-1",
		Name:      "zen test",
		Status:    model.StatusPending,
		Project:   "p",
		CreatedAt: time.Now(),
	}
	testutil.NoError(t, d.Add(task))
	app.refreshTasks()

	sim, stop := wireApp(t, app)
	defer stop()

	// Enter agent view.
	sim.InjectKey(tcell.KeyEnter, 0, 0)
	syncUI(t, app.tapp)

	var zen bool
	var leftW, fileW, paneW int
	readUI(t, app.tapp, func() {
		zen = app.agentZen
		_, _, leftW, _ = app.agentLeftCol.GetRect()
		_, _, fileW, _ = app.filePanel.GetRect()
		_, _, paneW, _ = app.agentPane.GetRect()
	})
	testutil.Equal(t, zen, false)
	testutil.True(t, leftW > 0)
	testutil.True(t, fileW > 0)
	normalPaneW := paneW

	// Ctrl+Z → zoom: side panels collapse to zero width, pane widens.
	sim.InjectKey(tcell.KeyCtrlZ, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		zen = app.agentZen
		_, _, leftW, _ = app.agentLeftCol.GetRect()
		_, _, fileW, _ = app.filePanel.GetRect()
		_, _, paneW, _ = app.agentPane.GetRect()
	})
	testutil.Equal(t, zen, true)
	testutil.Equal(t, leftW, 0)
	testutil.Equal(t, fileW, 0)
	testutil.True(t, paneW > normalPaneW)

	// Ctrl+Z again → restore 1:3:1.
	sim.InjectKey(tcell.KeyCtrlZ, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		zen = app.agentZen
		_, _, leftW, _ = app.agentLeftCol.GetRect()
		_, _, fileW, _ = app.filePanel.GetRect()
	})
	testutil.Equal(t, zen, false)
	testutil.True(t, leftW > 0)
	testutil.True(t, fileW > 0)

	// Zoom again, then exit — exitAgentView must reset the zen flag so the
	// next agent view opens with panels visible.
	sim.InjectKey(tcell.KeyCtrlZ, 0, 0)
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyCtrlD, 0, 0) // exit (no live session)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { zen = app.agentZen })
	testutil.Equal(t, zen, false)

	// Re-enter: the restored 1:3:1 proportions lay out with visible panels.
	sim.InjectKey(tcell.KeyEnter, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		_, _, leftW, _ = app.agentLeftCol.GetRect()
		_, _, fileW, _ = app.filePanel.GetRect()
	})
	testutil.True(t, leftW > 0)
	testutil.True(t, fileW > 0)
}

// TestSmoke_AgentZenForcesTerminalFocus verifies that zooming while the file
// panel is focused snaps focus back to the terminal — the file panel is hidden
// in zen mode, so leaving focus there would swallow keys with no visible target.
func TestSmoke_AgentZenForcesTerminalFocus(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	task := &model.Task{
		ID:        "zen-focus-1",
		Name:      "zen focus test",
		Status:    model.StatusPending,
		Project:   "p",
		CreatedAt: time.Now(),
	}
	testutil.NoError(t, d.Add(task))
	app.refreshTasks()

	_, stop := wireApp(t, app)
	defer stop()

	readUI(t, app.tapp, func() {
		app.onTaskSelect(task, true)
		app.agentFocus = focusFiles
		app.updateFocusIndicators()
	})

	var focus agentFocus
	readUI(t, app.tapp, func() { focus = app.agentFocus })
	testutil.Equal(t, focus, focusFiles)

	readUI(t, app.tapp, func() { app.toggleAgentZen() })

	var zen bool
	readUI(t, app.tapp, func() {
		zen = app.agentZen
		focus = app.agentFocus
	})
	testutil.Equal(t, zen, true)
	testutil.Equal(t, focus, focusTerminal)
}

// TestSmoke_ExitAgentViewResetsTab verifies that exiting agent view resets the
// header tab to widget.TabTasks. Without the reset, the global key handler
// routes up/down keys to the wrong tab's handler, breaking task list navigation.
func TestSmoke_ExitAgentViewResetsTab(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

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

	// Simulate entering agent view from the Settings tab.
	readUI(t, app.tapp, func() {
		app.header.SetTab(widget.TabSettings)
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
	app := New(d, runner, false)

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
	app := New(d, runner, false)

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
	app := New(d, runner, false)
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
// (tab switch, agent view enter/exit, modal open/close, Ctrl+L) all invoke
// forceRedraw. Guards against regression where the central
// `pages.SetChangedFunc` hook gets disconnected and we go back to per-callsite
// forceRedraw discipline (which got us into this mess once already).
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
	app := New(d, runner, false)

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

	// Every page mutation fires `pages.SetChangedFunc` which logs this string.
	const pagesChanged = "force redraw: pages changed"

	// Tab switch (1→2) fires forceRedraw via pages.SetChangedFunc.
	prev := strings.Count(readLog(), pagesChanged)
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)
	if strings.Count(readLog(), pagesChanged) <= prev {
		t.Errorf("tab switch did not fire pages-changed redraw")
	}

	// Ctrl+L triggers a Sync (one of only two places we Sync — user-
	// initiated refresh; one CSI 2J flash is the expected cost).
	sim.InjectKey(tcell.KeyCtrlL, 0, 0)
	syncUI(t, app.tapp)
	testutil.Contains(t, readLog(), "ctrl+l — Sync")

	// Back to Tasks so Enter can open the agent view.
	sim.InjectKey(tcell.KeyRune, '1', 0)
	syncUI(t, app.tapp)

	// Enter agent view fires pages-changed (SwitchToPage("agent")).
	prev = strings.Count(readLog(), pagesChanged)
	sim.InjectKey(tcell.KeyEnter, 0, 0)
	syncUI(t, app.tapp)
	if strings.Count(readLog(), pagesChanged) <= prev {
		t.Errorf("enter agent view did not fire pages-changed redraw")
	}

	// Exit agent view fires pages-changed (SwitchToPage("tasks")).
	prev = strings.Count(readLog(), pagesChanged)
	sim.InjectKey(tcell.KeyCtrlQ, 0, 0)
	syncUI(t, app.tapp)
	if strings.Count(readLog(), pagesChanged) <= prev {
		t.Errorf("exit agent view did not fire pages-changed redraw")
	}

	// switchTab(TabTasks) while in agent mode delegates to exitAgentView, which
	// calls SwitchToPage("tasks") exactly once. Asserting the count goes up by
	// exactly 1 catches a regression where the delegation grows a second
	// redundant Sync (the bug pattern that motivated the previous early-return
	// comment in switchTab). Drive switchTab directly on the tview goroutine
	// so we don't depend on session-start state from the Ctrl+Q exit above.
	readUI(t, app.tapp, func() {
		app.mode = modeAgent
		app.pages.SwitchToPage("agent")
	})
	syncUI(t, app.tapp)
	prev = strings.Count(readLog(), pagesChanged)
	readUI(t, app.tapp, func() { app.switchTab(widget.TabTasks) })
	syncUI(t, app.tapp)
	if delta := strings.Count(readLog(), pagesChanged) - prev; delta != 1 {
		t.Errorf("switchTab(TabTasks) from agent mode: expected 1 pages-changed redraw, got %d", delta)
	}

	// Modal open + close path: each AddPage / RemovePage / SwitchToPage
	// fires pages.SetChangedFunc, so opening then closing the new-task form
	// produces additional redraw entries. The previous bug was that opens
	// silently skipped Sync, leaving stale cells under tmux.
	d.SetProject("p", config.Project{Path: t.TempDir()})
	prev = strings.Count(readLog(), pagesChanged)
	sim.InjectKey(tcell.KeyRune, 'n', 0)
	syncUI(t, app.tapp)
	// Guard: if 'n' ever stops opening the form (binding change, missing
	// project requirement), the close assertion below would silently never
	// fire because Escape on no modal is a no-op.
	var hasModal bool
	readUI(t, app.tapp, func() { hasModal = app.pages.HasPage("newtask") })
	if !hasModal {
		t.Fatal("'n' did not open the new-task form — global binding may have changed")
	}
	if strings.Count(readLog(), pagesChanged) <= prev {
		t.Errorf("opening new-task form did not fire pages-changed redraw")
	}
	prev = strings.Count(readLog(), pagesChanged)
	sim.InjectKey(tcell.KeyEscape, 0, 0)
	syncUI(t, app.tapp)
	if strings.Count(readLog(), pagesChanged) <= prev {
		t.Errorf("closing new-task form did not fire pages-changed redraw")
	}
}

// TestSmoke_FilterToggleFiresRedraw guards the filter-mode branch-change
// wiring. Toggling `tl.filtering` swaps the bottom row between a task row and
// the filter input WITHOUT changing rowsSignature — so the row-composition
// signal alone won't catch it. The fix wires `setFiltering` to fire
// OnLayoutChange, which forceRedraws. Image 1 in the bug report (project
// header bleeding into a task row while `/nexus` filter was active) is
// exactly this class of shift.
func TestSmoke_FilterToggleFiresRedraw(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ux.log")
	if err := uxlog.Init(logPath); err != nil {
		t.Fatalf("uxlog.Init: %v", err)
	}
	defer uxlog.Close()

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.Add(&model.Task{ID: "f-1", Name: "filter test", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()}) //nolint:errcheck // test setup; failure surfaces in subsequent assertion
	app.refreshTasks()

	sim, stop := wireApp(t, app)
	defer stop()

	readLog := func() string {
		b, _ := os.ReadFile(logPath)
		return string(b)
	}
	// Distinct log reason from "tasklist rows changed" — filter toggle
	// reserves/releases the bottom row without shifting the row signature,
	// so it goes through OnFilterToggle, not OnLayoutChange.
	const filterToggled = "force redraw: tasklist filter toggled"

	// Open filter with `/`.
	prev := strings.Count(readLog(), filterToggled)
	sim.InjectKey(tcell.KeyRune, '/', 0)
	syncUI(t, app.tapp)
	if strings.Count(readLog(), filterToggled) <= prev {
		t.Errorf("'/' did not fire tasklist filter-toggle redraw")
	}

	// Confirm filter (Enter) — exits input mode, keeps filter text. Bottom-row
	// reservation lifts here, so a Sync is required.
	prev = strings.Count(readLog(), filterToggled)
	sim.InjectKey(tcell.KeyEnter, 0, 0)
	syncUI(t, app.tapp)
	if strings.Count(readLog(), filterToggled) <= prev {
		t.Errorf("Enter on active filter did not fire tasklist filter-toggle redraw")
	}

	// Re-enter filter, then Escape clears it (filtering=false + filter="").
	sim.InjectKey(tcell.KeyRune, '/', 0)
	syncUI(t, app.tapp)
	prev = strings.Count(readLog(), filterToggled)
	sim.InjectKey(tcell.KeyEscape, 0, 0)
	syncUI(t, app.tapp)
	if strings.Count(readLog(), filterToggled) <= prev {
		t.Errorf("Escape on filter did not fire tasklist filter-toggle redraw")
	}
}

// TestSmoke_FilePanelLayoutChangeFiresRedraw verifies the FilePanel's
// OnLayoutChange callback fires forceRedraw when the row composition changes
// (file list updates, directory expansion). The change-detection signature
// hashes path + status + indent so an in-place status flip (e.g. modified→
// deleted) is also caught.
func TestSmoke_FilePanelLayoutChangeFiresRedraw(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ux.log")
	if err := uxlog.Init(logPath); err != nil {
		t.Fatalf("uxlog.Init: %v", err)
	}
	defer uxlog.Close()

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	_, stop := wireApp(t, app)
	defer stop()

	readLog := func() string {
		b, _ := os.ReadFile(logPath)
		return string(b)
	}
	const filePanelChanged = "force redraw: filepanel rows changed"

	// Initial SetFiles populates rows from empty: row signature changes,
	// OnLayoutChange fires.
	prev := strings.Count(readLog(), filePanelChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.filePanel.SetFiles([]gitutil.ChangedFile{
			{Path: "a.go", Status: "M"},
			{Path: "b.go", Status: "A"},
		})
	})
	if strings.Count(readLog(), filePanelChanged) <= prev {
		t.Errorf("initial SetFiles did not fire filepanel layout change")
	}

	// Same file list, different status (in-place modified→deleted): signature
	// includes Status, so this fires.
	prev = strings.Count(readLog(), filePanelChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.filePanel.SetFiles([]gitutil.ChangedFile{
			{Path: "a.go", Status: "D"}, // M → D at same path
			{Path: "b.go", Status: "A"},
		})
	})
	if strings.Count(readLog(), filePanelChanged) <= prev {
		t.Errorf("status flip at same path did not fire filepanel layout change (signature must include Status)")
	}

	// Identical SetFiles call: signature unchanged, no fire.
	prev = strings.Count(readLog(), filePanelChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.filePanel.SetFiles([]gitutil.ChangedFile{
			{Path: "a.go", Status: "D"},
			{Path: "b.go", Status: "A"},
		})
	})
	if strings.Count(readLog(), filePanelChanged) != prev {
		t.Errorf("identical SetFiles must not fire filepanel layout change")
	}
}

// TestSmoke_AgentPaneBranchChangeFiresRedraw verifies the TerminalPane's
// OnBranchChange callback fires forceRedraw on Draw-branch swaps that don't
// go through Pages (SetPending toggles the pending banner ↔ "no session"
// text; EnterDiffMode/ExitDiffMode swap PTY render path with diff render).
// Each swap paints a different cell set in the same rect.
func TestSmoke_AgentPaneBranchChangeFiresRedraw(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ux.log")
	if err := uxlog.Init(logPath); err != nil {
		t.Fatalf("uxlog.Init: %v", err)
	}
	defer uxlog.Close()

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	_, stop := wireApp(t, app)
	defer stop()

	readLog := func() string {
		b, _ := os.ReadFile(logPath)
		return string(b)
	}
	const agentChanged = "force redraw: agentpane branch changed"

	// SetPending false→true flips the pending-banner branch. (Initial state
	// is pending=false, so this is a real transition.)
	prev := strings.Count(readLog(), agentChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.agentPane.SetPending(true)
	})
	if strings.Count(readLog(), agentChanged) <= prev {
		t.Errorf("SetPending(true) did not fire agentpane branch change")
	}

	// SetPending true→false: another transition.
	prev = strings.Count(readLog(), agentChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.agentPane.SetPending(false)
	})
	if strings.Count(readLog(), agentChanged) <= prev {
		t.Errorf("SetPending(false) did not fire agentpane branch change")
	}

	// No-op SetPending(false): already false, must not fire.
	prev = strings.Count(readLog(), agentChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.agentPane.SetPending(false)
	})
	if strings.Count(readLog(), agentChanged) != prev {
		t.Errorf("no-op SetPending must not fire agentpane branch change")
	}

	// EnterDiffMode swaps PTY render branch with the diff render branch.
	prev = strings.Count(readLog(), agentChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.agentPane.EnterDiffMode("diff --git a/a.go b/a.go\nindex 0..1\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n", "a.go")
	})
	if strings.Count(readLog(), agentChanged) <= prev {
		t.Errorf("EnterDiffMode did not fire agentpane branch change")
	}

	// ExitDiffMode swaps back. Must also fire (was-diff → not-diff).
	prev = strings.Count(readLog(), agentChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.agentPane.ExitDiffMode()
	})
	if strings.Count(readLog(), agentChanged) <= prev {
		t.Errorf("ExitDiffMode did not fire agentpane branch change")
	}
}

// TestSmoke_TaskGitPanelBranchChangeFiresRedraw verifies GitPanel fires
// forceRedraw when its rendered branch changes — Loading→loaded, empty-state
// ↔ Files-present, and section presence flips (statusLines / diffLines /
// branchLines empty ↔ non-empty). The taskGitPanel mutates on every cursor
// move between tasks with different worktrees, so this is the most
// frequently-fired hook on the task list page.
func TestSmoke_TaskGitPanelBranchChangeFiresRedraw(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ux.log")
	if err := uxlog.Init(logPath); err != nil {
		t.Fatalf("uxlog.Init: %v", err)
	}
	defer uxlog.Close()

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	_, stop := wireApp(t, app)
	defer stop()

	readLog := func() string {
		b, _ := os.ReadFile(logPath)
		return string(b)
	}
	const taskGitChanged = "force redraw: task git panel branch changed"

	// !loaded → loaded with sections populated. Shape flips.
	prev := strings.Count(readLog(), taskGitChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.taskGitPanel.SetStatus("M file.go", "+1 -0", "ahead 1")
	})
	if strings.Count(readLog(), taskGitChanged) <= prev {
		t.Errorf("first SetStatus must fire task git panel branch change (Loading→loaded)")
	}

	// Same shape — re-publishing the same content doesn't flip any
	// presence bit. Must not fire.
	prev = strings.Count(readLog(), taskGitChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.taskGitPanel.SetStatus("M file.go", "+1 -0", "ahead 1")
	})
	if strings.Count(readLog(), taskGitChanged) != prev {
		t.Errorf("identical SetStatus must not fire task git panel branch change")
	}

	// Drop the diff section. Shape flips (diffLines non-empty → empty).
	prev = strings.Count(readLog(), taskGitChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.taskGitPanel.SetStatus("M file.go", "", "ahead 1")
	})
	if strings.Count(readLog(), taskGitChanged) <= prev {
		t.Errorf("dropping the diff section must fire task git panel branch change")
	}

	// Clear back to !loaded. Shape flips.
	prev = strings.Count(readLog(), taskGitChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.taskGitPanel.Clear()
	})
	if strings.Count(readLog(), taskGitChanged) <= prev {
		t.Errorf("Clear must fire task git panel branch change (loaded→Loading)")
	}
}

// TestSmoke_TaskPreviewBranchChangeFiresRedraw verifies TaskPreviewPanel
// fires forceRedraw when its rendered branch changes — task ID change
// (clears cells, swaps to centered "Loading..."), cells nil↔non-nil, and
// status-message changes in the centered placeholder branch.
func TestSmoke_TaskPreviewBranchChangeFiresRedraw(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ux.log")
	if err := uxlog.Init(logPath); err != nil {
		t.Fatalf("uxlog.Init: %v", err)
	}
	defer uxlog.Close()

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	_, stop := wireApp(t, app)
	defer stop()

	readLog := func() string {
		b, _ := os.ReadFile(logPath)
		return string(b)
	}
	const previewChanged = "force redraw: task preview branch changed"

	// SetTaskID change: "" → "tp-1". statusMsg width changes
	// ("No task selected" → "Loading..."). Shape flips.
	prev := strings.Count(readLog(), previewChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.taskPreview.SetTaskID("tp-1")
	})
	if strings.Count(readLog(), previewChanged) <= prev {
		t.Errorf("SetTaskID(\"tp-1\") must fire preview branch change")
	}

	// Same task ID — no-op, must not fire.
	prev = strings.Count(readLog(), previewChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.taskPreview.SetTaskID("tp-1")
	})
	if strings.Count(readLog(), previewChanged) != prev {
		t.Errorf("repeated SetTaskID with same id must not fire preview branch change")
	}

	// SetStatus with a different placeholder message: cells already nil but
	// statusMsg width changes. Shape flips.
	prev = strings.Count(readLog(), previewChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.taskPreview.SetStatus("No active agent")
	})
	if strings.Count(readLog(), previewChanged) <= prev {
		t.Errorf("SetStatus with new message must fire preview branch change")
	}

	// RefreshOutput with empty raw: cells stay nil but statusMsg width
	// changes ("No active agent" → "Waiting for output..."). Shape flips.
	prev = strings.Count(readLog(), previewChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.taskPreview.RefreshOutput(nil, 80, 24, 80, 24)
	})
	if strings.Count(readLog(), previewChanged) <= prev {
		t.Errorf("RefreshOutput(empty) must fire preview branch change (statusMsg width changed)")
	}

	// RefreshOutput with content: cells transitions nil → grid. Shape flips.
	prev = strings.Count(readLog(), previewChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.taskPreview.RefreshOutput([]byte("hello world\n"), 80, 24, 80, 24)
	})
	if strings.Count(readLog(), previewChanged) <= prev {
		t.Errorf("RefreshOutput(content) must fire preview branch change (cells nil→grid)")
	}

	// Repeat RefreshOutput with same dimensions and grid output: shape
	// unchanged (cellsNil=false, cols/rows unchanged), must not fire.
	prev = strings.Count(readLog(), previewChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.taskPreview.RefreshOutput([]byte("hello world 2\n"), 80, 24, 80, 24)
	})
	if strings.Count(readLog(), previewChanged) != prev {
		t.Errorf("repeated RefreshOutput at same dims must not fire preview branch change")
	}
}

// TestSmoke_AgentGitPanelBranchChangeFiresRedraw mirrors the taskGitPanel
// test against the agent-view gitPanel instance (separate wiring at
// app.go:306, separate uxlog reason). Without a dedicated test, future
// refactors could silently drop the agent-view callback while leaving the
// task-list callback intact and the existing test passing.
func TestSmoke_AgentGitPanelBranchChangeFiresRedraw(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ux.log")
	if err := uxlog.Init(logPath); err != nil {
		t.Fatalf("uxlog.Init: %v", err)
	}
	defer uxlog.Close()

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	_, stop := wireApp(t, app)
	defer stop()

	readLog := func() string {
		b, _ := os.ReadFile(logPath)
		return string(b)
	}
	const agentGitChanged = "force redraw: agent git panel branch changed"

	// !loaded → loaded with sections: shape flips.
	prev := strings.Count(readLog(), agentGitChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.gitPanel.SetStatus("M file.go", "+1 -0", "ahead 1")
	})
	if strings.Count(readLog(), agentGitChanged) <= prev {
		t.Errorf("first SetStatus on agent gitPanel must fire branch change")
	}

	// loaded → !loaded: shape flips.
	prev = strings.Count(readLog(), agentGitChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.gitPanel.Clear()
	})
	if strings.Count(readLog(), agentGitChanged) <= prev {
		t.Errorf("Clear on agent gitPanel must fire branch change")
	}
}

// TestSmoke_TaskDetailBranchChangeFiresRedraw verifies TaskDetailPanel
// fires forceRedraw when its rendered shape changes — task transition
// (different conditional rows render), running-flag flip (changes the
// status row width), and status transitions. Cursor moves between tasks
// on the task list page route through this widget every time.
func TestSmoke_TaskDetailBranchChangeFiresRedraw(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ux.log")
	if err := uxlog.Init(logPath); err != nil {
		t.Fatalf("uxlog.Init: %v", err)
	}
	defer uxlog.Close()

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	_, stop := wireApp(t, app)
	defer stop()

	readLog := func() string {
		b, _ := os.ReadFile(logPath)
		return string(b)
	}
	const detailChanged = "force redraw: task detail branch changed"

	taskA := &model.Task{
		ID: "td-a", Name: "alpha", Status: model.StatusPending,
		Project: "p", Branch: "b", CreatedAt: time.Now(),
	}
	taskB := &model.Task{
		ID: "td-b", Name: "beta", Status: model.StatusInProgress,
		Project: "p", CreatedAt: time.Now(), Prompt: "do thing",
	}

	// nil → taskA: sentinel lastShape guarantees the first SetTask fires.
	prev := strings.Count(readLog(), detailChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.taskDetail.SetTask(taskA, false)
	})
	if strings.Count(readLog(), detailChanged) <= prev {
		t.Errorf("first SetTask(taskA) must fire detail branch change")
	}

	// Same task, same running flag: shape unchanged, must not fire.
	prev = strings.Count(readLog(), detailChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.taskDetail.SetTask(taskA, false)
	})
	if strings.Count(readLog(), detailChanged) != prev {
		t.Errorf("repeated SetTask with identical task/running must not fire detail branch change")
	}

	// Running flag flip on same task: status row changes width ("(idle)" ↔
	// "(running)"). Shape flips. (Status is Pending here so the suffix
	// branch in Draw isn't entered, but the running bit is still hashed
	// — we test it later under InProgress.)
	prev = strings.Count(readLog(), detailChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.taskDetail.SetTask(taskA, true)
	})
	if strings.Count(readLog(), detailChanged) <= prev {
		t.Errorf("running flag flip must fire detail branch change")
	}

	// taskA → taskB: different task ID, different conditional rows render
	// (B has no Branch, has Prompt). Shape flips.
	prev = strings.Count(readLog(), detailChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.taskDetail.SetTask(taskB, false)
	})
	if strings.Count(readLog(), detailChanged) <= prev {
		t.Errorf("task switch must fire detail branch change")
	}

	// taskB → nil: swap to the "No task selected" branch.
	prev = strings.Count(readLog(), detailChanged)
	app.tapp.QueueUpdateDraw(func() {
		app.taskDetail.SetTask(nil, false)
	})
	if strings.Count(readLog(), detailChanged) <= prev {
		t.Errorf("SetTask(nil) must fire detail branch change")
	}
}

func TestSmoke_ClickNonInteractivePanelKeepsFocus(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

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

// TestSmoke_SettingsCategorySwapFiresRedraw guards the per-category branch
// switch on the settings page. The right pane renders entirely different
// content per category — without a forceRedraw, tcell's per-cell diff
// leaves the prior category's text under the new one (e.g. project rows
// bleeding through into the Sandbox detail).
func TestSmoke_SettingsCategorySwapFiresRedraw(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ux.log")
	if err := uxlog.Init(logPath); err != nil {
		t.Fatalf("uxlog.Init: %v", err)
	}
	defer uxlog.Close()

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	_, stop := wireApp(t, app)
	defer stop()

	readLog := func() string {
		b, _ := os.ReadFile(logPath)
		return string(b)
	}
	const settingsBranch = "force redraw: settings branch changed"

	// Category swap — moving from catSystem to catSandbox is a real change
	// and must fire the callback.
	prev := strings.Count(readLog(), settingsBranch)
	app.tapp.QueueUpdateDraw(func() {
		app.settings.setCategory(catSandbox)
	})
	syncUI(t, app.tapp)
	if strings.Count(readLog(), settingsBranch) <= prev {
		t.Errorf("setCategory(catSandbox) did not fire settings branch change")
	}

	// Focus swap — from focusPane (default) to focusRail must also fire,
	// because the border highlight is different cells in the same rect.
	prev = strings.Count(readLog(), settingsBranch)
	app.tapp.QueueUpdateDraw(func() {
		app.settings.setFocus(focusRail)
	})
	syncUI(t, app.tapp)
	if strings.Count(readLog(), settingsBranch) <= prev {
		t.Errorf("setFocus(focusRail) did not fire settings branch change")
	}

	// Same-value setFocus must NOT fire — guards against churn redraws.
	prev = strings.Count(readLog(), settingsBranch)
	app.tapp.QueueUpdateDraw(func() {
		app.settings.setFocus(focusRail)
	})
	syncUI(t, app.tapp)
	if strings.Count(readLog(), settingsBranch) > prev {
		t.Errorf("setFocus to same value should not fire branch change")
	}
}

// TestSmoke_SettingsPageMouseClickKeepsFocus verifies clicks inside the
// settings page never strand keyboard input on a non-interactive child.
// The SettingsPage MouseHandler always redirects focus back to sp itself
// (which delegates input to SettingsView), so tview's default focus-steal
// on click never wins.
func TestSmoke_SettingsPageMouseClickKeepsFocus(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	sim, stop := wireApp(t, app)
	defer stop()

	// Switch to the settings tab.
	app.tapp.QueueUpdateDraw(func() { app.switchTab(widget.TabSettings) })
	syncUI(t, app.tapp)

	var focused tview.Primitive
	readUI(t, app.tapp, func() { focused = app.tapp.GetFocus() })
	if focused != app.settingsPage {
		t.Fatalf("expected initial focus on settingsPage, got %T", focused)
	}

	// Click in the middle of the settings rect — outside the rail.
	sim.InjectMouse(60, 10, tcell.Button1, 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() { focused = app.tapp.GetFocus() })
	if focused != app.settingsPage {
		t.Errorf("click on settings pane stole focus from settingsPage (got %T)", focused)
	}

	// Click on the rail — focus must still stay on settingsPage so HandleKey
	// continues to route through the settings view.
	sim.InjectMouse(2, 4, tcell.Button1, 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() { focused = app.tapp.GetFocus() })
	if focused != app.settingsPage {
		t.Errorf("click on rail stole focus from settingsPage (got %T)", focused)
	}
}

// TestSmoke_SettingsPagePasteRouting verifies that bracket-paste events
// posted to the screen reach the SettingsView paste handler when the
// settings tab is focused. Without SettingsPage.PasteHandler forwarding
// to sv.PasteHandler, tview's default Box paste handler swallows the
// pasted text silently — only visible when the user is mid-edit on a
// vault or source-path row.
func TestSmoke_SettingsPagePasteRouting(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	sim, stop := wireApp(t, app)
	defer stop()

	// Switch to settings, enter Knowledge Base category, start editing
	// the Metis vault path.
	app.tapp.QueueUpdateDraw(func() {
		app.switchTab(widget.TabSettings)
		app.settings.setCategory(catKnowledgeBase)
		// Park cursor on the vault row and trigger inline editing.
		for i, r := range app.settings.rows {
			if r.kind == srVaultPath && r.key == vaultKeyMetis {
				app.settings.cursor = i
				break
			}
		}
		app.settings.editingVault = vaultKeyMetis
		app.settings.editVaultBuf = ""
	})
	syncUI(t, app.tapp)

	// Inject bracketed paste of a path fragment.
	_ = sim.PostEvent(tcell.NewEventPaste(true))
	sim.InjectKey(tcell.KeyRune, '/', 0)
	sim.InjectKey(tcell.KeyRune, 'v', 0)
	sim.InjectKey(tcell.KeyRune, 'a', 0)
	sim.InjectKey(tcell.KeyRune, 'u', 0)
	sim.InjectKey(tcell.KeyRune, 'l', 0)
	sim.InjectKey(tcell.KeyRune, 't', 0)
	_ = sim.PostEvent(tcell.NewEventPaste(false))
	syncUI(t, app.tapp)

	var got string
	readUI(t, app.tapp, func() { got = app.settings.editVaultBuf })
	if got != "/vault" {
		t.Errorf("paste did not reach vault editor: editVaultBuf = %q, want %q", got, "/vault")
	}
}

// TestSmoke_DAGTabRendersAndFiresRedraw exercises the new DAG tab end-to-end:
// switching to the tab populates the widget, the OnBranchChange callback
// fires forceRedraw on the snapshot install, and the cursor can be moved by
// the inner widget's input handler.
func TestSmoke_DAGTabRendersAndFiresRedraw(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ux.log")
	if err := uxlog.Init(logPath); err != nil {
		t.Fatalf("uxlog.Init: %v", err)
	}
	defer uxlog.Close()

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	// Two linked tasks so the DAG has edges to render.
	_ = d.Add(&model.Task{ID: "p1", Name: "parent", Status: model.StatusComplete, CreatedAt: time.Now()})
	_ = d.Add(&model.Task{ID: "c1", Name: "child", Status: model.StatusInProgress, DependsOn: []string{"p1"}, CreatedAt: time.Now()})
	app.refreshTasks()

	sim, stop := wireApp(t, app)
	defer stop()

	readLog := func() string {
		b, _ := os.ReadFile(logPath)
		return string(b)
	}

	// Switch to the DAG tab via the global Right arrow.
	prior := strings.Count(readLog(), "force redraw: dag branch changed")
	sim.InjectKey(tcell.KeyRight, 0, 0)
	syncUI(t, app.tapp)

	// DAG tab active + page switched.
	readUI(t, app.tapp, func() {
		if app.header.ActiveTab() != widget.TabDAG {
			t.Errorf("expected TabDAG active, got %v", app.header.ActiveTab())
		}
		page, _ := app.pages.GetFrontPage()
		if page != "dag" {
			t.Errorf("expected dag page, got %q", page)
		}
	})

	// branch-change must have fired (snapshot installed → forceRedraw).
	if strings.Count(readLog(), "force redraw: dag branch changed") <= prior {
		t.Errorf("expected dag branch-change forceRedraw after tab switch; log so far:\n%s", readLog())
	}

	// Cursor lives on a real node.
	readUI(t, app.tapp, func() {
		cur := app.dagWidget.CurrentTask()
		if cur != "p1" && cur != "c1" {
			t.Errorf("dag cursor = %q, want p1 or c1", cur)
		}
	})
}

// TestSmoke_NumericTabKeysRouteCorrectly guards the keybinding bug where
// `case '2'` continued to route to TabSettings after TabDAG was inserted
// between Tasks and Settings, so the statusbar advertised "2=DAG, 3=settings"
// but actual keystrokes landed on the wrong tabs. This test exercises the
// exact path the user takes — number-key shortcut — rather than the arrow-key
// path the original DAG smoke test happened to use.
func TestSmoke_NumericTabKeysRouteCorrectly(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	sim, stop := wireApp(t, app)
	defer stop()

	// Start on Tasks.
	readUI(t, app.tapp, func() {
		if app.header.ActiveTab() != widget.TabTasks {
			t.Fatalf("initial tab = %v, want TabTasks", app.header.ActiveTab())
		}
	})

	// `2` → DAG.
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		if app.header.ActiveTab() != widget.TabDAG {
			t.Errorf("'2' routed to %v, want TabDAG", app.header.ActiveTab())
		}
	})

	// `1` → back to Tasks.
	sim.InjectKey(tcell.KeyRune, '1', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		if app.header.ActiveTab() != widget.TabTasks {
			t.Errorf("'1' routed to %v, want TabTasks", app.header.ActiveTab())
		}
	})

	// `3` → Settings.
	sim.InjectKey(tcell.KeyRune, '3', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		if app.header.ActiveTab() != widget.TabSettings {
			t.Errorf("'3' routed to %v, want TabSettings", app.header.ActiveTab())
		}
	})
}

// TestSmoke_ClickDAGPageDoesNotStealFocus enforces the CLAUDE.md page-wrapper
// rule: clicking on any non-interactive area inside the DAG page must keep
// focus on the page wrapper, which forwards InputHandler to the inner
// widget. Without this contract, tview's default Box.MouseHandler can park
// focus on a primitive with no InputHandler and silently drop keystrokes.
// Pattern mirrors TestSmoke_TaskPageClickKeepsFocus / SettingsPage's click
// test.
func TestSmoke_ClickDAGPageDoesNotStealFocus(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	_ = d.Add(&model.Task{ID: "click-1", Name: "click test", Status: model.StatusPending, CreatedAt: time.Now()})
	app.refreshTasks()

	sim, stop := wireApp(t, app)
	defer stop()

	// Switch to DAG tab.
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)

	var focused tview.Primitive
	readUI(t, app.tapp, func() { focused = app.tapp.GetFocus() })
	if focused != app.dagPage {
		t.Fatalf("expected initial focus on dagPage, got %T", focused)
	}

	// Click on a position likely to land on the page wrapper's border row.
	sim.InjectMouse(0, 1, tcell.Button1, 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() { focused = app.tapp.GetFocus() })
	if focused != app.dagPage {
		t.Errorf("click on dag page wrapper border stole focus from dagPage (got %T)", focused)
	}

	// Click inside the widget area too — focus must STILL be on the page
	// wrapper so the wrapper's InputHandler routes keystrokes to the inner
	// widget (the wrapper has an InputHandler that forwards; the inner
	// widget alone would still receive keys but the wrapper-focused path
	// is what the rest of the page contract assumes).
	sim.InjectMouse(20, 10, tcell.Button1, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { focused = app.tapp.GetFocus() })
	if focused != app.dagPage {
		t.Errorf("click inside dag node area stole focus from dagPage (got %T)", focused)
	}
}

// TestSmoke_FocusRegainTriggersSync exercises the focus-regain → Sync chain:
// a *tcell.EventFocus(true) posted to the SimulationScreen must reach
// lazyScreen.PollEvent, fire onFocusGained, and produce a "focus regained
// — Sync" uxlog entry. This is one of only two places we Sync — repair-
// screen-damage cases per gdamore's intent (tmux pane may have been
// repainted from a stale backing while we were unfocused). One CSI 2J
// flash on a rare event is the right tradeoff for guaranteed correctness.
func TestSmoke_FocusRegainTriggersSync(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ux.log")
	if err := uxlog.Init(logPath); err != nil {
		t.Fatalf("uxlog.Init: %v", err)
	}
	defer uxlog.Close()

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	sim, stop := wireApp(t, app)
	defer stop()
	// Production wires this inside Run(); wireApp bypasses Run() so we
	// install the same callback (log + Sync) explicitly.
	app.screen.onFocusGained = func() {
		uxlog.Log("[tui] focus regained — Sync")
		app.screen.Sync()
	}

	sim.PostEvent(tcell.NewEventFocus(true))
	syncUI(t, app.tapp)

	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	testutil.Contains(t, string(b), "focus regained — Sync")
}

// TestSmoke_FocusLossDoesNotTriggerSync guards the negative edge: a focus
// loss event must NOT trigger Sync. lazyScreen.PollEvent filters on
// EventFocus.Focused == true; firing on loss too would burn a Sync every
// time the user clicked away from the window.
func TestSmoke_FocusLossDoesNotTriggerSync(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ux.log")
	if err := uxlog.Init(logPath); err != nil {
		t.Fatalf("uxlog.Init: %v", err)
	}
	defer uxlog.Close()

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	sim, stop := wireApp(t, app)
	defer stop()
	app.screen.onFocusGained = func() {
		uxlog.Log("[tui] focus regained — Sync")
		app.screen.Sync()
	}

	// Snapshot the log AFTER any wireApp-induced redraws so the assertion
	// only inspects what posting the focus-loss event added.
	preSize := int64(0)
	if fi, err := os.Stat(logPath); err == nil {
		preSize = fi.Size()
	}

	sim.PostEvent(tcell.NewEventFocus(false))
	syncUI(t, app.tapp)

	f, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()
	if _, err := f.Seek(preSize, 0); err != nil {
		t.Fatalf("seek log: %v", err)
	}
	tail, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read log tail: %v", err)
	}
	if strings.Contains(string(tail), "focus regained — Sync") {
		t.Fatalf("focus loss must not fire focus-regained Sync; tail:\n%s", string(tail))
	}
}

// TestSmoke_AttentionBarShowsOtherNeedsInputTasks verifies the agent view's
// attention bar populates from needsInputIDs, excludes the currently-viewed
// task, and collapses back to zero height once no other tasks are blocked.
func TestSmoke_AttentionBarShowsOtherNeedsInputTasks(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	viewed := &model.Task{ID: "view-1", Name: "viewed", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}
	other := &model.Task{ID: "other-1", Name: "needs-help", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}
	d.Add(viewed)
	d.Add(other)
	app.refreshTasks()

	_, stop := wireApp(t, app)
	defer stop()

	// Pretend both tasks were blocked on a prompt, then enter the viewed task.
	readUI(t, app.tapp, func() {
		app.needsInputIDs = []string{viewed.ID, other.ID}
		app.updateAttentionBar()
		app.onTaskSelect(viewed, false)
	})

	var entries []widget.AttentionEntry
	var height int
	readUI(t, app.tapp, func() {
		entries = app.attentionBar.Entries()
		height = app.attentionBar.DesiredHeight()
	})
	if len(entries) != 1 || entries[0].TaskName != "needs-help" {
		t.Fatalf("attention bar should contain the OTHER task only; got %#v", entries)
	}
	if height != 3 { // 1 entry + 2 border rows
		t.Fatalf("desired height = %d, want 3 for one entry", height)
	}

	// Clear the other task from needsInputIDs and refresh — bar should collapse.
	readUI(t, app.tapp, func() {
		app.needsInputIDs = nil
		app.updateAttentionBar()
	})

	readUI(t, app.tapp, func() {
		entries = app.attentionBar.Entries()
		height = app.attentionBar.DesiredHeight()
	})
	if len(entries) != 0 {
		t.Fatalf("attention bar should be empty after clearing other; got %#v", entries)
	}
	if height != 0 {
		t.Fatalf("desired height after clearing = %d, want 0", height)
	}
}

// TestRefreshTasks_NeedsInputSticky verifies that once a task is detected as
// needing input, it remains in needsInputIDs across ticks where it temporarily
// drops out of idleIDs. Claude's prompt UI emits periodic animation bytes
// (cursor blink, spinner) that bump the session's lastOutput without
// representing real progress; before the sticky pass, the attention bar
// would flicker off in that gap and appear briefly each time the task
// crossed back through the 3 s idle threshold.
func TestRefreshTasks_NeedsInputSticky(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	viewed := &model.Task{ID: "view-1", Name: "viewed", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}
	other := &model.Task{ID: "other-1", Name: "needs-help", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}
	if err := d.Add(viewed); err != nil {
		t.Fatalf("db.Add viewed: %v", err)
	}
	if err := d.Add(other); err != nil {
		t.Fatalf("db.Add other: %v", err)
	}

	_, stop := wireApp(t, app)
	defer stop()

	// Write a log file for the other task containing the needs-input marker.
	// Path A ('❯ 1.' with literal space) is what survives ANSI strip in the
	// AskUserQuestion overlay.
	logPath := agent.SessionLogPath(other.ID)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	withMarker := []byte("Do you want me to proceed?\n❯ 1. Yes\n  2. No\n")
	if err := os.WriteFile(logPath, withMarker, 0o600); err != nil {
		t.Fatalf("write log with marker: %v", err)
	}

	snapshotIDs := func() []string {
		var out []string
		readUI(t, app.tapp, func() {
			out = append(out, app.needsInputIDs...)
		})
		return out
	}

	// Tick 1: other is idle → detected via the normal path.
	readUI(t, app.tapp, func() {
		app.refreshTasksWithIDs([]string{viewed.ID, other.ID}, []string{other.ID})
	})
	if got := snapshotIDs(); !containsString(got, other.ID) {
		t.Fatalf("tick 1: expected %q in needsInputIDs, got %v", other.ID, got)
	}

	// Tick 2: Claude emitted an animation byte → other is no longer in
	// idleIDs. Sticky pass must keep it because the marker is still on disk.
	readUI(t, app.tapp, func() {
		app.refreshTasksWithIDs([]string{viewed.ID, other.ID}, nil)
	})
	if got := snapshotIDs(); !containsString(got, other.ID) {
		t.Fatalf("tick 2 (sticky): expected %q to persist in needsInputIDs even when not idle, got %v", other.ID, got)
	}

	// Tick 3: agent moved past the question (user responded elsewhere) — log
	// no longer contains the marker. Sticky pass must drop the task.
	if err := os.WriteFile(logPath, []byte("agent is working on something else\n"), 0o600); err != nil {
		t.Fatalf("overwrite log: %v", err)
	}
	readUI(t, app.tapp, func() {
		app.refreshTasksWithIDs([]string{viewed.ID, other.ID}, nil)
	})
	if got := snapshotIDs(); containsString(got, other.ID) {
		t.Fatalf("tick 3: expected %q to drop after marker cleared, got %v", other.ID, got)
	}

	// Tick 4: task no longer running — sticky must drop it even if the
	// marker is still in the log tail (rewrite it back to ensure that's the
	// scenario the test is exercising).
	if err := os.WriteFile(logPath, withMarker, 0o600); err != nil {
		t.Fatalf("rewrite log with marker: %v", err)
	}
	// Re-detect via a fresh tick where it IS idle so the sticky branch has
	// something to carry forward.
	readUI(t, app.tapp, func() {
		app.refreshTasksWithIDs([]string{viewed.ID, other.ID}, []string{other.ID})
	})
	if got := snapshotIDs(); !containsString(got, other.ID) {
		t.Fatalf("tick 4 setup: expected %q in needsInputIDs, got %v", other.ID, got)
	}
	// Now exclude other from runningIDs entirely.
	readUI(t, app.tapp, func() {
		app.refreshTasksWithIDs([]string{viewed.ID}, nil)
	})
	if got := snapshotIDs(); containsString(got, other.ID) {
		t.Fatalf("tick 4: expected %q to drop when no longer running, got %v", other.ID, got)
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
