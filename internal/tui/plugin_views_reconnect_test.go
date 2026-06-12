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
