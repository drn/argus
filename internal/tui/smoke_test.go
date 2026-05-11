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
	// afterDraw is the production hook that consumes the pendingSync flag and
	// runs screen.Sync(). buildUI wires it on the original tview.Application;
	// re-wire it here since wireApp swapped app.tapp.
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

	// Switch to each tab via numeric keys.
	for _, tc := range []struct {
		key  rune
		want widget.Tab
	}{
		{'2', widget.TabSettings},
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
	syncUI(t, app.tapp)

	var prompt string
	readUI(t, app.tapp, func() { prompt = string(form.prompt) })
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

	// Ctrl+L always logs explicitly (user-initiated refresh).
	sim.InjectKey(tcell.KeyCtrlL, 0, 0)
	syncUI(t, app.tapp)
	testutil.Contains(t, readLog(), "force redraw: ctrl+l")

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

// TestSmoke_AfterDrawSyncsOnPendingFlag verifies the architectural change:
// forceRedraw sets a flag, afterDraw consumes it. Drives one update cycle
// that fires several forceRedraw calls and asserts the flag clears (idempotent
// — multiple sets collapse to one consumed flag). QueueUpdateDraw blocks
// until BOTH f() and a.draw() complete; afterDraw runs inside a.draw(), so
// when the call returns we're guaranteed afterDraw has run.
func TestSmoke_AfterDrawSyncsOnPendingFlag(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.Add(&model.Task{ID: "ad-1", Name: "afterdraw", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()}) //nolint:errcheck // test setup; failure surfaces in subsequent assertion
	app.refreshTasks()

	_, stop := wireApp(t, app)
	defer stop()

	// Set the flag inside QueueUpdateDraw — afterDraw fires before this returns.
	app.tapp.QueueUpdateDraw(func() {
		app.forceRedraw("test single")
	})
	if app.pendingSync.Load() {
		t.Errorf("pendingSync should have been consumed by afterDraw")
	}

	// Multiple forceRedraw calls in one update collapse to one consumed flag.
	app.tapp.QueueUpdateDraw(func() {
		app.forceRedraw("test a")
		app.forceRedraw("test b")
		app.forceRedraw("test c")
	})
	if app.pendingSync.Load() {
		t.Errorf("multiple forceRedraw calls in one event must collapse to one consumed flag, got pendingSync still set")
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
