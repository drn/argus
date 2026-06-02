package tui

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/terminalpane"
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
	resizeErr      error
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
	defer f.mu.Unlock()
	if f.resizeErr != nil {
		return f.resizeErr
	}
	f.resizes = append(f.resizes, [2]int{cols, rows})
	return nil
}

func (f *fakePluginConnector) setResizeErr(err error) {
	f.mu.Lock()
	f.resizeErr = err
	f.mu.Unlock()
}

func (f *fakePluginConnector) resizeSnapshot() [][2]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]int(nil), f.resizes...)
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

func TestReconcilePluginViewSize_NoOpWhenInactive(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, true)
	app.reconcilePluginViewSize() // must not panic
}

// TestPluginViewportSize_PreLayoutUsesScreenChrome pins the fix for the
// pre-layout garbage envelope: a never-drawn pane's Box rect is the tview
// default (15x10), and the viewport must NOT derive from it (13x8). Before
// the first layout pass the size falls back to screen-minus-chrome.
func TestPluginViewportSize_PreLayoutUsesScreenChrome(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, true)

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(100, 30)
	app.screen = &lazyScreen{Screen: sim}

	// A mount whose pane has never been laid out — its rect is the Box default.
	bytesIn := make(chan []byte, 1)
	app.activePlugin = &pluginViewMount{pane: terminalpane.New(bytesIn), pageName: "plugin-view:test"}

	cols, rows := app.pluginViewportSize()
	testutil.Equal(t, cols, 100-pluginViewColOverhead)
	testutil.Equal(t, rows, 30-pluginViewRowOverhead)
}

// TestSmoke_PluginView_FirstResizeEnvelopeIsPostLayout pins the end-to-end
// contract: every resize envelope sent after activation matches the pane's
// real post-layout inner rect — never the 13x8 pre-layout Box default — and
// repeated draws with an unchanged size do not re-send duplicates.
func TestSmoke_PluginView_FirstResizeEnvelopeIsPostLayout(t *testing.T) {
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
	waitFor(t, 1*time.Second, func() bool { return len(fake.resizeSnapshot()) > 0 })

	// Kick extra draws — the reconciler must dedupe an unchanged size.
	app.tapp.QueueUpdateDraw(func() {})
	syncUI(t, app.tapp)
	app.tapp.QueueUpdateDraw(func() {})
	syncUI(t, app.tapp)

	// The envelope must match the pane's real post-layout inner rect.
	var want [2]int
	readUI(t, app.tapp, func() {
		_, _, w, h := app.pluginMounts[0].pane.GetRect()
		want = [2]int{w - 2, h - 2}
	})
	if want == [2]int{13, 8} {
		t.Fatal("pane rect is still the pre-layout Box default; test setup broken")
	}
	resizes := fake.resizeSnapshot()
	for i, rz := range resizes {
		if rz != want {
			t.Fatalf("envelope %d = %dx%d, want %dx%d (pre-layout garbage must never be sent)", i, rz[0], rz[1], want[0], want[1])
		}
	}
	testutil.Equal(t, len(resizes), 1)

	// Focus follows the first successful resize, exactly once.
	testutil.Equal(t, fake.focusedCount.Load(), int32(1))
}

// TestReconcilePluginViewSize_ResendsOnDriftAndRetriesOnError pins the
// reconciliation contract: a last-sent size that differs from the computed
// viewport is corrected on the next reconcile (no terminal resize needed),
// and a failed send leaves last-sent unchanged so the envelope is retried.
func TestReconcilePluginViewSize_ResendsOnDriftAndRetriesOnError(t *testing.T) {
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
	waitFor(t, 1*time.Second, func() bool { return len(fake.resizeSnapshot()) > 0 })

	// The reconciler's target is the pane's real post-layout inner rect.
	var want [2]int
	readUI(t, app.tapp, func() {
		_, _, w, h := app.pluginMounts[0].pane.GetRect()
		want = [2]int{w - 2, h - 2}
	})

	// Simulate drift: pretend a stale size was the last envelope delivered.
	readUI(t, app.tapp, func() {
		app.activePlugin.lastSentCols, app.activePlugin.lastSentRows = 13, 8
		app.reconcilePluginViewSize()
	})
	resizes := fake.resizeSnapshot()
	if last := resizes[len(resizes)-1]; last != want {
		t.Fatalf("drift not corrected: last envelope = %dx%d, want %dx%d", last[0], last[1], want[0], want[1])
	}

	// Failed send: last-sent stays stale so the next reconcile retries.
	fake.setResizeErr(errors.New("boom"))
	readUI(t, app.tapp, func() {
		app.activePlugin.lastSentCols, app.activePlugin.lastSentRows = 13, 8
		app.reconcilePluginViewSize()
		testutil.Equal(t, app.activePlugin.lastSentCols, 13)
		testutil.Equal(t, app.activePlugin.lastSentRows, 8)
	})
	fake.setResizeErr(nil)
	before := len(fake.resizeSnapshot())
	readUI(t, app.tapp, func() { app.reconcilePluginViewSize() })
	resizes = fake.resizeSnapshot()
	if len(resizes) != before+1 {
		t.Fatalf("retry after error did not send: %d → %d envelopes", before, len(resizes))
	}
	if last := resizes[len(resizes)-1]; last != want {
		t.Fatalf("retry envelope = %dx%d, want %dx%d", last[0], last[1], want[0], want[1])
	}
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
	// Wait for the dial to complete AND the initial post-layout envelope to
	// land, so the re-send below is unambiguously caused by the re-activation.
	waitFor(t, 1*time.Second, func() bool {
		return fake.dialed.Load() && len(fake.resizeSnapshot()) > 0
	})

	firstCount := len(fake.resizeSnapshot())

	// Re-activate the same view; should re-send resize without re-dialing.
	readUI(t, app.tapp, func() { app.activatePluginView(app.pluginMounts[0]) })

	secondCount := len(fake.resizeSnapshot())
	if secondCount <= firstCount {
		t.Fatalf("resize count did not grow on re-activation: %d → %d", firstCount, secondCount)
	}
}
