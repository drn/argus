package tui

import (
	"errors"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/views"
	"github.com/gdamore/tcell/v2"
)

func TestReconnectBackoff(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 250 * time.Millisecond},
		{1, 500 * time.Millisecond},
		{2, time.Second},
		{3, 2 * time.Second},
		{10, 2 * time.Second}, // capped
		{-1, 250 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			testutil.Equal(t, reconnectBackoff(tc.attempt), tc.want)
		})
	}
}

func TestReconnectMessage_FlipsAfterGrace(t *testing.T) {
	// Before the grace period: optimistic.
	early := reconnectMessage(10*time.Second, 2)
	testutil.Contains(t, early, "Reconnecting…")
	// At/after the grace period: surfaces it is still trying.
	late := reconnectMessage(pluginReconnectGrace, 5)
	testutil.Contains(t, late, "Still trying")
}

// newReconnectApp wires an app with one registered plugin view + a fake
// connector factory, activates the view, and returns the app, sim, fake, and a
// stop func. The fake is shared across re-dials (the loop re-invokes the
// factory, which returns this same fake).
func newReconnectApp(t *testing.T) (*App, tcell.SimulationScreen, *fakePluginConnector, func()) {
	t.Helper()
	d := testDB(t)
	r := views.New(d)
	_, err := r.Register("", "Hera", "ctrl+l", "ws://127.0.0.1:5111/view")
	testutil.NoError(t, err)

	runner := agent.NewRunner(nil)
	app := New(d, runner, true)
	// Shrink the post-resume delayed resize re-send so the test doesn't wait
	// the 300ms production default (and so the re-send fires within the test).
	app.resumeResizeDelay = 5 * time.Millisecond
	fake := &fakePluginConnector{}
	app.pluginConnFactory = func(url string, onBytes func([]byte), onControl func([]byte), in <-chan []byte) pluginConnector {
		fake.onBytes = onBytes
		fake.onControl = onControl
		return fake
	}
	app.loadPluginViews()

	sim, stop := wireApp(t, app)

	sim.InjectKey(tcell.KeyCtrlL, 0, 0)
	syncUI(t, app.tapp)
	waitFor(t, time.Second, func() bool {
		return fake.dialed.Load() && fake.focusedCount.Load() == 1
	})
	return app, sim, fake, stop
}

func TestSmoke_PluginView_ReconnectShowsOverlayAndResumes(t *testing.T) {
	app, _, fake, stop := newReconnectApp(t)
	defer stop()

	// Make the redial fail so the overlay stays up long enough to observe.
	fake.setDialErr(errors.New("connection refused"))

	// Simulate the daemon dying — fire onClose from the test goroutine (never
	// inside a readUI closure: onPluginDisconnect calls QueueUpdateDraw and
	// would deadlock if nested).
	fake.fireClose(errors.New("EOF"))
	syncUI(t, app.tapp)

	// Reconnecting state is up: overlay page present, mode still plugin view.
	var reconnecting, hasOverlay bool
	var mode viewMode
	readUI(t, app.tapp, func() {
		reconnecting = app.activePlugin != nil && app.activePlugin.reconnecting
		hasOverlay = app.pages.HasPage(pluginReconnectPage)
		mode = app.mode
	})
	testutil.Equal(t, reconnecting, true)
	testutil.Equal(t, hasOverlay, true)
	testutil.Equal(t, mode, modePluginView)

	// While reconnecting, the plugin's bottom-bar hotkey hints are dropped (the
	// plugin no longer has the keyboard); the exit affordance lives on the overlay.
	var barHintCount int
	readUI(t, app.tapp, func() {
		_, _, hints := app.statusbar.PluginMode()
		barHintCount = len(hints)
	})
	testutil.Equal(t, barHintCount, 0)

	// Let the daemon "come back": redials now succeed → seamless resume.
	fake.setDialErr(nil)
	waitFor(t, 2*time.Second, func() bool { return fake.focusedCount.Load() >= 2 })

	var stillReconnecting, overlayGone bool
	var connSet bool
	readUI(t, app.tapp, func() {
		stillReconnecting = app.activePlugin.reconnecting
		overlayGone = !app.pages.HasPage(pluginReconnectPage)
		connSet = app.activePlugin.conn != nil
	})
	testutil.Equal(t, stillReconnecting, false)
	testutil.Equal(t, overlayGone, true)
	testutil.Equal(t, connSet, true)

	// Resume re-sent the resize+focus handshake: a second focus envelope.
	testutil.Equal(t, fake.focusedCount.Load() >= 2, true)
	if len(fake.resizeSnapshot()) < 2 {
		t.Fatalf("expected a fresh resize envelope on resume, got %d total", len(fake.resizeSnapshot()))
	}
}

// TestSmoke_PluginView_ReconnectAfterResume pins the multi-bounce path: after a
// successful resume adopts the new connection, a SECOND disconnect on that
// connection re-enters the reconnect loop and resumes again. hera can bounce
// more than once, so the onClose wiring must survive across resumes.
func TestSmoke_PluginView_ReconnectAfterResume(t *testing.T) {
	app, _, fake, stop := newReconnectApp(t)
	defer stop()

	// First bounce → reconnect → resume.
	fake.fireClose(errors.New("EOF #1"))
	syncUI(t, app.tapp)
	waitFor(t, 2*time.Second, func() bool { return fake.focusedCount.Load() >= 2 })
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.activePlugin.reconnecting, false)
	})

	// Second bounce on the freshly adopted connection must re-enter reconnect.
	fake.setDialErr(errors.New("refused again"))
	fake.fireClose(errors.New("EOF #2"))
	syncUI(t, app.tapp)
	var reconnecting, hasOverlay bool
	readUI(t, app.tapp, func() {
		reconnecting = app.activePlugin != nil && app.activePlugin.reconnecting
		hasOverlay = app.pages.HasPage(pluginReconnectPage)
	})
	testutil.Equal(t, reconnecting, true)
	testutil.Equal(t, hasOverlay, true)

	// Daemon returns again → second resume.
	fake.setDialErr(nil)
	waitFor(t, 2*time.Second, func() bool { return fake.focusedCount.Load() >= 3 })
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.activePlugin.reconnecting, false)
		testutil.Equal(t, app.pages.HasPage(pluginReconnectPage), false)
	})
}

// TestSmoke_PluginView_ResumeReSendsResizeAfterDelay pins the fix for the
// warm-restart first-frame race: after a successful resume, argus re-sends the
// resize envelope once more a short delay later (defeating a plugin that
// rendered before applying the initial resize and would otherwise stay tiny
// until a manual resize). Asserts an EXTRA resize lands after the resume's
// immediate one.
func TestSmoke_PluginView_ResumeReSendsResizeAfterDelay(t *testing.T) {
	app, _, fake, stop := newReconnectApp(t)
	defer stop()

	// Drop and immediately let the redial succeed → resume.
	fake.fireClose(errors.New("EOF"))
	syncUI(t, app.tapp)
	waitFor(t, 2*time.Second, func() bool { return fake.focusedCount.Load() >= 2 })

	// Three resizes total: initial activation, the resume's immediate re-emit,
	// and the delayed post-resume re-send (resumeResizeDelay=5ms in
	// newReconnectApp). The delayed one is QueueUpdateDraw'd, so drain on poll.
	waitFor(t, 2*time.Second, func() bool {
		syncUI(t, app.tapp)
		return len(fake.resizeSnapshot()) >= 3
	})
	last := fake.resizeSnapshot()
	// Every re-send matches the live viewport — never a tiny/zero size.
	for i, got := range last {
		if got[0] <= 2 || got[1] <= 2 {
			t.Fatalf("resize %d had bad dims %dx%d", i, got[0], got[1])
		}
	}
}

func TestSmoke_PluginView_EscExitsDuringReconnect(t *testing.T) {
	app, sim, fake, stop := newReconnectApp(t)
	defer stop()

	// Keep redials failing so we stay in the reconnecting state.
	fake.setDialErr(errors.New("refused"))
	fake.fireClose(errors.New("EOF"))
	syncUI(t, app.tapp)

	var reconnecting bool
	readUI(t, app.tapp, func() {
		reconnecting = app.activePlugin != nil && app.activePlugin.reconnecting
	})
	testutil.Equal(t, reconnecting, true)

	// A single Esc exits to the task list while reconnecting.
	sim.InjectKey(tcell.KeyEscape, 0, 0)
	syncUI(t, app.tapp)

	var mode viewMode
	var active bool
	var hasOverlay bool
	readUI(t, app.tapp, func() {
		mode = app.mode
		active = app.activePlugin != nil
		hasOverlay = app.pages.HasPage(pluginReconnectPage)
	})
	testutil.Equal(t, mode, modeTaskList)
	testutil.Equal(t, active, false)
	testutil.Equal(t, hasOverlay, false)
}

func TestSmoke_PluginView_DoubleCtrlQExitsDuringReconnect(t *testing.T) {
	app, sim, fake, stop := newReconnectApp(t)
	defer stop()

	fake.setDialErr(errors.New("refused"))
	fake.fireClose(errors.New("EOF"))
	syncUI(t, app.tapp)

	// Drive the failsafe window with a controllable clock.
	clock := time.Unix(3000, 0)
	readUI(t, app.tapp, func() { app.nowFn = func() time.Time { return clock } })

	sim.InjectKey(tcell.KeyCtrlQ, 0, 0) // first — recorded
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { clock = clock.Add(100 * time.Millisecond) })
	sim.InjectKey(tcell.KeyCtrlQ, 0, 0) // second within window — failsafe
	syncUI(t, app.tapp)

	var mode viewMode
	var hasOverlay bool
	readUI(t, app.tapp, func() {
		mode = app.mode
		hasOverlay = app.pages.HasPage(pluginReconnectPage)
	})
	testutil.Equal(t, mode, modeTaskList)
	testutil.Equal(t, hasOverlay, false)
}

func TestOnPluginDisconnect_IgnoredWhenInactive(t *testing.T) {
	d := testDB(t)
	r := views.New(d)
	_, err := r.Register("", "Hera", "ctrl+l", "ws://127.0.0.1:5111/view")
	testutil.NoError(t, err)
	runner := agent.NewRunner(nil)
	app := New(d, runner, true)
	fake := &fakePluginConnector{}
	app.pluginConnFactory = func(url string, onBytes func([]byte), onControl func([]byte), in <-chan []byte) pluginConnector {
		fake.onBytes = onBytes
		fake.onControl = onControl
		return fake
	}
	app.loadPluginViews()
	sim, stop := wireApp(t, app)
	defer stop()

	sim.InjectKey(tcell.KeyCtrlL, 0, 0)
	syncUI(t, app.tapp)
	waitFor(t, time.Second, func() bool { return fake.dialed.Load() })

	// Deactivate, THEN fire a stale disconnect — it must not start a reconnect.
	readUI(t, app.tapp, func() { app.deactivatePluginView() })
	syncUI(t, app.tapp)
	fake.fireClose(errors.New("late EOF"))
	syncUI(t, app.tapp)

	var hasOverlay bool
	var mode viewMode
	readUI(t, app.tapp, func() {
		hasOverlay = app.pages.HasPage(pluginReconnectPage)
		mode = app.mode
	})
	testutil.Equal(t, hasOverlay, false)
	testutil.Equal(t, mode, modeTaskList)
}

// TestStopPluginReconnect_SafeWhenNotReconnecting pins that the teardown helper
// is a clean no-op for a mount that never entered the reconnecting state.
func TestStopPluginReconnect_SafeWhenNotReconnecting(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, true)
	m := &pluginViewMount{}
	app.stopPluginReconnect(m) // must not panic
	testutil.Equal(t, m.reconnecting, false)
}
