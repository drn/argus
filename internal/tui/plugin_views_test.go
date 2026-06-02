package tui

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/views"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
)

// fakePluginConnector is the stub the smoke test installs in place of a real
// views.Connector. It records every lifecycle call so the test can assert the
// dial / focus / resize / blur / close sequence without dialing a real
// WebSocket.
type fakePluginConnector struct {
	mu             sync.Mutex
	dialed         atomic.Bool
	resizes        [][2]int
	focusedCount   atomic.Int32
	blurredCount   atomic.Int32
	closedCount    atomic.Int32
	dialErr        error
	onBytes        func([]byte)
	onControl      func([]byte)
	bytesToReceive [][]byte
}

func (f *fakePluginConnector) Dial(ctx context.Context) error {
	if f.dialErr != nil {
		return f.dialErr
	}
	f.dialed.Store(true)
	for _, b := range f.bytesToReceive {
		if f.onBytes != nil {
			f.onBytes(b)
		}
	}
	return nil
}

func (f *fakePluginConnector) SendResize(cols, rows int) error {
	f.mu.Lock()
	f.resizes = append(f.resizes, [2]int{cols, rows})
	f.mu.Unlock()
	return nil
}

func (f *fakePluginConnector) SendFocus() error {
	f.focusedCount.Add(1)
	return nil
}

func (f *fakePluginConnector) SendBlur() error {
	f.blurredCount.Add(1)
	return nil
}

func (f *fakePluginConnector) Close() error {
	f.closedCount.Add(1)
	return nil
}

func TestSmoke_PluginView_HotkeyMountsAndEscExits(t *testing.T) {
	d := testDB(t)

	r := views.New(d)
	_, err := r.Register("", "Ludwig", "ctrl+l", "ws://127.0.0.1:5111/ws")
	testutil.NoError(t, err)

	runner := agent.NewRunner(nil)
	app := New(d, runner, true)

	fake := &fakePluginConnector{}
	app.pluginConnFactory = func(url string, onBytes func([]byte), onControl func([]byte), in <-chan []byte) pluginConnector {
		fake.onBytes = onBytes
		fake.onControl = onControl
		return fake
	}
	// Re-mount with the test factory in place. buildUI already called
	// loadPluginViews once with the default factory; rerun to wire the fake.
	app.loadPluginViews()

	sim, stop := wireApp(t, app)
	defer stop()

	// Sanity-check before injecting the hotkey.
	var mountCount int
	var hotkeyHit bool
	readUI(t, app.tapp, func() {
		mountCount = len(app.pluginMounts)
		_, hotkeyHit = app.pluginHotkeys[tcell.KeyCtrlL]
	})
	if mountCount == 0 {
		t.Fatalf("expected at least 1 plugin mount, got 0")
	}
	if !hotkeyHit {
		t.Fatalf("Ctrl+L not in pluginHotkeys map; map=%v", app.pluginHotkeys)
	}

	// Inject the hotkey — Ctrl+L from the task list.
	sim.InjectKey(tcell.KeyCtrlL, 0, 0)
	syncUI(t, app.tapp)

	var mode viewMode
	var activeURL string
	readUI(t, app.tapp, func() {
		mode = app.mode
		if app.activePlugin != nil {
			activeURL = app.activePlugin.view.CallbackURL
		}
	})
	testutil.Equal(t, mode, modePluginView)
	testutil.Equal(t, activeURL, "ws://127.0.0.1:5111/ws")

	// The connector should have been dialed + focus envelope sent.
	waitFor(t, 1*time.Second, func() bool {
		return fake.dialed.Load() && fake.focusedCount.Load() == 1
	})
	if got := fake.focusedCount.Load(); got != 1 {
		t.Fatalf("focus count = %d, want 1", got)
	}
	fake.mu.Lock()
	resizes := append([][2]int(nil), fake.resizes...)
	fake.mu.Unlock()
	if len(resizes) == 0 {
		t.Fatal("expected at least one resize envelope sent on activate")
	}

	// Full surrender: Esc does NOT exit — it forwards to the plugin.
	sim.InjectKey(tcell.KeyEscape, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modePluginView)
	if fake.blurredCount.Load() != 0 {
		t.Fatalf("blur count = %d, want 0 (Esc must not exit)", fake.blurredCount.Load())
	}

	// The double-Ctrl+Q failsafe is the one key argus reserves. Two fast
	// presses force-return to argus. handleGlobalKey reads app.nowFn for the
	// window check; drive it from a controllable clock so the two presses land
	// inside the 400ms window regardless of wall-clock scheduling.
	// clock is only ever touched on the tview goroutine (via readUI and via
	// nowFn, which handleGlobalKey calls on that same goroutine), so no race.
	clock := time.Unix(2000, 0)
	readUI(t, app.tapp, func() { app.nowFn = func() time.Time { return clock } })

	sim.InjectKey(tcell.KeyCtrlQ, 0, 0) // first — forwarded
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modePluginView)

	readUI(t, app.tapp, func() { clock = clock.Add(100 * time.Millisecond) })
	sim.InjectKey(tcell.KeyCtrlQ, 0, 0) // second within window — failsafe fires
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modeTaskList)
	if fake.blurredCount.Load() != 1 {
		t.Fatalf("blur count = %d, want 1 after failsafe", fake.blurredCount.Load())
	}
	if fake.closedCount.Load() == 0 {
		t.Fatal("connector.Close was not invoked on failsafe")
	}
}

// waitFor polls cond every 5ms until it returns true or the timeout fires.
// Mirrors the helper in internal/tui/views/connector_test.go.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

func TestSmoke_PluginView_BottomBarLifecycle(t *testing.T) {
	d := testDB(t)
	r := views.New(d)
	_, err := r.Register("", "Ludwig", "ctrl+l", "ws://127.0.0.1:5111/ws")
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

	// Activate the plugin view → bar enters plugin mode with the plugin title.
	sim.InjectKey(tcell.KeyCtrlL, 0, 0)
	syncUI(t, app.tapp)

	var active bool
	var title string
	var hints []widget.PluginHint
	readUI(t, app.tapp, func() {
		active, title, hints = app.statusbar.PluginMode()
	})
	testutil.Equal(t, active, true)
	testutil.Equal(t, title, "Ludwig")
	testutil.Equal(t, len(hints), 0)

	// A hotkeys re-push refreshes the bar live. Only bar:true items render.
	// dispatchPluginControl decodes on the calling goroutine then defers the
	// tview mutation through QueueUpdateDraw, mirroring the read-pump path, so
	// it is called from the test goroutine (not inside a readUI closure, which
	// would nest QueueUpdate calls).
	var mount0 *pluginViewMount
	readUI(t, app.tapp, func() { mount0 = app.activePlugin })
	app.dispatchPluginControl(mount0, []byte(
		`{"type":"hotkeys","items":[`+
			`{"key":"j","label":"down","bar":true},`+
			`{"key":"k","label":"up","bar":true},`+
			`{"key":"x","label":"hidden","bar":false}]}`))
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		active, title, hints = app.statusbar.PluginMode()
	})
	testutil.Equal(t, active, true)
	testutil.Equal(t, len(hints), 2) // bar:false filtered out
	testutil.Equal(t, hints[0].Key, "j")
	testutil.Equal(t, hints[1].Key, "k")

	// Deactivate → bar leaves plugin mode; mount.hotkeys + pluginHelpRequested
	// are cleared so nothing bleeds into the next plugin.
	var mount *pluginViewMount
	readUI(t, app.tapp, func() {
		mount = app.activePlugin
		app.pluginHelpRequested = true // simulate a prior help request
		app.deactivatePluginView()
	})
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		active, title, hints = app.statusbar.PluginMode()
	})
	testutil.Equal(t, active, false)
	testutil.Equal(t, title, "")
	testutil.Equal(t, len(hints), 0)
	if mount.hotkeys != nil {
		t.Fatalf("mount.hotkeys not cleared on deactivate: %v", mount.hotkeys)
	}
	if app.pluginHelpRequested {
		t.Fatal("pluginHelpRequested not cleared on deactivate")
	}
}

func TestSmoke_PluginView_InvalidHotkeySkipped(t *testing.T) {
	d := testDB(t)
	r := views.New(d)
	// Bogus hotkey — parser rejects "alt+l".
	_, err := r.Register("", "Bogus", "alt+l", "ws://127.0.0.1:5111/ws")
	testutil.NoError(t, err)

	runner := agent.NewRunner(nil)
	app := New(d, runner, true)

	// loadPluginViews should have skipped the bogus hotkey, leaving no
	// mounts and no entries in the hotkey map.
	if len(app.pluginMounts) != 0 {
		t.Fatalf("expected 0 mounts, got %d", len(app.pluginMounts))
	}
	if len(app.pluginHotkeys) != 0 {
		t.Fatalf("expected empty hotkey map, got %v", app.pluginHotkeys)
	}
}

func TestSmoke_PluginView_RemoteModeIsNoOp(t *testing.T) {
	// Remote-TUI mode has a.db that isn't *db.DB. loadPluginViews must
	// short-circuit cleanly and leave the mount slice / hotkey map empty.
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, true)

	// Sanity: with no registered views, mounts are empty.
	if len(app.pluginMounts) != 0 {
		t.Fatalf("expected empty mounts on fresh app, got %d", len(app.pluginMounts))
	}
}

func TestDefaultPluginConnectorFactory_ReturnsNonNil(t *testing.T) {
	in := make(chan []byte)
	c := defaultPluginConnectorFactory("ws://127.0.0.1:1", nil, nil, in)
	if c == nil {
		t.Fatal("expected non-nil connector")
	}
	// Don't Dial — port 1 is unreachable. Close should be a clean no-op.
	testutil.NoError(t, c.Close())
}

func TestResizePluginViewIfActive_NoOpWhenInactive(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, true)
	app.resizePluginViewIfActive() // must not panic
}

func TestDeactivatePluginView_NoOpWhenInactive(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, true)
	app.deactivatePluginView() // must not panic
}

func TestPluginViewportSize_FallbacksWhenNoScreen(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, true)
	// No screen → 80x24 default.
	cols, rows := app.pluginViewportSize()
	testutil.Equal(t, cols, 80)
	testutil.Equal(t, rows, 24)
}

func TestActivatePluginView_ReactivationResendsResize(t *testing.T) {
	d := testDB(t)
	r := views.New(d)
	_, err := r.Register("", "Ludwig", "ctrl+l", "ws://127.0.0.1:5111/ws")
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
	waitFor(t, 1*time.Second, func() bool { return fake.dialed.Load() })

	fake.mu.Lock()
	firstCount := len(fake.resizes)
	fake.mu.Unlock()

	// Re-activate the same view; should re-send resize without re-dialing.
	readUI(t, app.tapp, func() { app.activatePluginView(app.pluginMounts[0]) })

	fake.mu.Lock()
	secondCount := len(fake.resizes)
	fake.mu.Unlock()
	if secondCount <= firstCount {
		t.Fatalf("resize count did not grow on re-activation: %d → %d", firstCount, secondCount)
	}
}
