package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/terminalpane"
	"github.com/drn/argus/internal/tui/views"
)

// pluginSurrenderApp builds an App parked in modePluginView with a minimal
// active mount (no real connector) so handleGlobalKey can be exercised
// directly. The returned clock pointer lets tests drive the failsafe window
// deterministically via app.nowFn.
func pluginSurrenderApp(t *testing.T) (*App, *time.Time) {
	t.Helper()
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	// Minimal mount — conn stays nil so deactivatePluginView's teardown is a
	// no-op beyond switching pages / focus, which buildUI has wired.
	m := &pluginViewMount{
		view:     &views.View{ID: 1, Title: "Test", CallbackURL: "ws://x"},
		pageName: "plugin-view:1",
	}
	app.activePlugin = m
	app.mode = modePluginView

	clock := time.Unix(1000, 0)
	app.nowFn = func() time.Time { return clock }
	return app, &clock
}

// TestHandleGlobalKey_PluginMode_EscForwarded asserts Esc is surrendered to the
// plugin (returned as an event) rather than deactivating the view.
func TestHandleGlobalKey_PluginMode_EscForwarded(t *testing.T) {
	app, _ := pluginSurrenderApp(t)
	ev := tcell.NewEventKey(tcell.KeyEscape, 0, 0)
	got := app.handleGlobalKey(ev)
	testutil.Equal(t, got == ev, true)
	testutil.Equal(t, app.mode, modePluginView)
	if app.activePlugin == nil {
		t.Fatal("Esc must not deactivate the plugin view")
	}
}

// TestHandleGlobalKey_PluginMode_CtrlCForwarded asserts Ctrl+C reaches the
// plugin and does not quit argus.
func TestHandleGlobalKey_PluginMode_CtrlCForwarded(t *testing.T) {
	app, _ := pluginSurrenderApp(t)
	ev := tcell.NewEventKey(tcell.KeyCtrlC, 0, 0)
	got := app.handleGlobalKey(ev)
	testutil.Equal(t, got == ev, true)
	testutil.Equal(t, app.mode, modePluginView)
}

// TestHandleGlobalKey_PluginMode_QuestionForwarded asserts `?` reaches the
// plugin and does not open argus's help overlay.
func TestHandleGlobalKey_PluginMode_QuestionForwarded(t *testing.T) {
	app, _ := pluginSurrenderApp(t)
	ev := tcell.NewEventKey(tcell.KeyRune, '?', 0)
	got := app.handleGlobalKey(ev)
	testutil.Equal(t, got == ev, true)
	testutil.Equal(t, app.mode, modePluginView)
	if app.helpModal != nil {
		t.Fatal("? must not open argus help in plugin mode")
	}
}

// TestHandleGlobalKey_PluginMode_TabSwitchNumberForwarded asserts a tab-switch
// number is surrendered (no argus navigation).
func TestHandleGlobalKey_PluginMode_TabSwitchNumberForwarded(t *testing.T) {
	app, _ := pluginSurrenderApp(t)
	ev := tcell.NewEventKey(tcell.KeyRune, '2', 0)
	got := app.handleGlobalKey(ev)
	testutil.Equal(t, got == ev, true)
	testutil.Equal(t, app.mode, modePluginView)
}

// TestHandleGlobalKey_PluginMode_FocusRailArrowForwarded asserts a modified
// arrow (argus's focus-rail key) is surrendered.
func TestHandleGlobalKey_PluginMode_FocusRailArrowForwarded(t *testing.T) {
	app, _ := pluginSurrenderApp(t)
	ev := tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl|tcell.ModAlt)
	got := app.handleGlobalKey(ev)
	testutil.Equal(t, got == ev, true)
	testutil.Equal(t, app.mode, modePluginView)
}

// TestHandleGlobalKey_PluginMode_SingleCtrlQForwarded asserts a lone Ctrl+Q is
// forwarded to the plugin (records the timestamp, does not return).
func TestHandleGlobalKey_PluginMode_SingleCtrlQForwarded(t *testing.T) {
	app, _ := pluginSurrenderApp(t)
	ev := tcell.NewEventKey(tcell.KeyCtrlQ, 0, 0)
	got := app.handleGlobalKey(ev)
	testutil.Equal(t, got == ev, true)
	testutil.Equal(t, app.mode, modePluginView)
	if app.activePlugin == nil {
		t.Fatal("a single Ctrl+Q must not deactivate")
	}
	if app.lastCtrlQ.IsZero() {
		t.Fatal("lastCtrlQ should be recorded after first Ctrl+Q")
	}
}

// TestHandleGlobalKey_PluginMode_DoubleCtrlQWithinWindowDeactivates asserts a
// fast double Ctrl+Q fires the failsafe and force-returns to argus.
func TestHandleGlobalKey_PluginMode_DoubleCtrlQWithinWindowDeactivates(t *testing.T) {
	app, clock := pluginSurrenderApp(t)

	first := app.handleGlobalKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, 0))
	testutil.Equal(t, first != nil, true) // forwarded

	*clock = clock.Add(200 * time.Millisecond) // within 400ms window
	second := app.handleGlobalKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, 0))
	testutil.Nil(t, second) // intercepted, not forwarded
	testutil.Equal(t, app.mode, modeTaskList)
	if app.activePlugin != nil {
		t.Fatal("double Ctrl+Q within window must deactivate")
	}
}

// TestHandleGlobalKey_PluginMode_DoubleCtrlQOutsideWindowDoesNotDeactivate
// asserts two Ctrl+Q presses spaced beyond the window do NOT trip the failsafe.
func TestHandleGlobalKey_PluginMode_DoubleCtrlQOutsideWindowDoesNotDeactivate(t *testing.T) {
	app, clock := pluginSurrenderApp(t)

	first := app.handleGlobalKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, 0))
	testutil.Equal(t, first != nil, true)

	*clock = clock.Add(500 * time.Millisecond) // outside 400ms window
	second := app.handleGlobalKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, 0))
	testutil.Equal(t, second != nil, true) // still forwarded
	testutil.Equal(t, app.mode, modePluginView)
	if app.activePlugin == nil {
		t.Fatal("two Ctrl+Q outside the window must not deactivate")
	}
}

// TestHandleGlobalKey_PluginMode_DoubleCtrlQAtExactWindowBoundaryDeactivates
// pins the documented `<=` semantics of the failsafe window: two Ctrl+Q presses
// separated by EXACTLY pluginFailsafeWindow (400ms) count as inside the window
// (inclusive boundary) and force-return control to argus.
func TestHandleGlobalKey_PluginMode_DoubleCtrlQAtExactWindowBoundaryDeactivates(t *testing.T) {
	app, clock := pluginSurrenderApp(t)

	first := app.handleGlobalKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, 0))
	testutil.Equal(t, first != nil, true) // forwarded

	*clock = clock.Add(pluginFailsafeWindow) // exactly at the boundary (inclusive)
	second := app.handleGlobalKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, 0))
	testutil.Nil(t, second) // intercepted: boundary counts as inside
	testutil.Equal(t, app.mode, modeTaskList)
	if app.activePlugin != nil {
		t.Fatal("double Ctrl+Q exactly at the window boundary must deactivate (<= semantics)")
	}
}

// TestActivatePluginView_ResetsLastCtrlQ asserts the failsafe timestamp is
// cleared on activation so a stale press can't trip the failsafe in a new view.
func TestActivatePluginView_ResetsLastCtrlQ(t *testing.T) {
	app, _ := pluginSurrenderApp(t)
	// Simulate a stale timestamp left from prior use, and a different mount.
	app.lastCtrlQ = time.Unix(500, 0)
	app.activePlugin = nil
	app.mode = modeTaskList
	app.pluginConnFactory = func(url string, onBytes func([]byte), onControl func([]byte), in <-chan []byte) pluginConnector {
		return &fakePluginConnector{}
	}
	bytesIn := make(chan []byte, 1)
	keysOut := make(chan []byte, 1)
	pane := terminalpane.New(bytesIn)
	pane.SetInputBack(keysOut)
	m := &pluginViewMount{
		view:     &views.View{ID: 2, Title: "Other", CallbackURL: "ws://y"},
		pane:     pane,
		pageName: "plugin-view:2",
		bytesIn:  bytesIn,
		keysOut:  keysOut,
	}
	app.pages.AddPage(m.pageName, pane, true, false)
	app.activatePluginView(m)
	if !app.lastCtrlQ.IsZero() {
		t.Fatal("activatePluginView must reset lastCtrlQ to zero")
	}
}
