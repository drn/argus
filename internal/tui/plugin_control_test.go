package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/views"
)

// activatePluginForTest registers one plugin view, wires a fake connector, and
// activates it via the Ctrl+L hotkey so app.activePlugin is set. Returns the
// app, the fake connector (with onControl captured), and a stop func.
func activatePluginForTest(t *testing.T) (*App, *fakePluginConnector, func()) {
	t.Helper()
	app, fake, _, stop := activatePluginForTestWithSim(t)
	return app, fake, stop
}

// activatePluginForTestWithSim is activatePluginForTest but also returns the
// SimulationScreen so tests can scrape rendered overlay content.
func activatePluginForTestWithSim(t *testing.T) (*App, *fakePluginConnector, tcell.SimulationScreen, func()) {
	t.Helper()
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

	sim.InjectKey(tcell.KeyCtrlL, 0, 0)
	syncUI(t, app.tapp)
	waitFor(t, time.Second, func() bool { return fake.dialed.Load() })

	var mode viewMode
	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modePluginView)
	if fake.onControl == nil {
		stop()
		t.Fatal("expected onControl to be captured by the fake connector")
	}
	return app, fake, sim, stop
}

func TestDispatchPluginControl_ReleaseDeactivates(t *testing.T) {
	app, fake, stop := activatePluginForTest(t)
	defer stop()

	// Simulate the read pump delivering a release frame.
	fake.onControl([]byte(`{"type":"release"}`))
	syncUI(t, app.tapp)

	var mode viewMode
	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modeTaskList)
	if fake.blurredCount.Load() != 1 {
		t.Fatalf("blur count = %d, want 1 after release", fake.blurredCount.Load())
	}
	if fake.closedCount.Load() == 0 {
		t.Fatal("connector.Close was not invoked on release")
	}
}

func TestDispatchPluginControl_HotkeysStoredOnMount(t *testing.T) {
	app, fake, stop := activatePluginForTest(t)
	defer stop()

	fake.onControl([]byte(`{"type":"hotkeys","items":[{"key":"^F","label":"next pane","bar":true},{"key":"^N","label":"new","bar":false}]}`))
	syncUI(t, app.tapp)

	var got []HotkeyItem
	readUI(t, app.tapp, func() {
		if app.activePlugin != nil {
			got = app.activePlugin.hotkeys
		}
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 hotkey items, got %d", len(got))
	}
	testutil.Equal(t, got[0].Key, "^F")
	testutil.Equal(t, got[0].Label, "next pane")
	testutil.Equal(t, got[0].Bar, true)
	testutil.Equal(t, got[1].Key, "^N")
	testutil.Equal(t, got[1].Bar, false)
	// Hotkeys must not deactivate.
	var mode viewMode
	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modePluginView)
}

func TestClampHotkeys_BoundsCountAndLengths(t *testing.T) {
	// Build an oversized dictionary: more items than the cap, with over-long
	// Key and Label strings.
	longKey := strings.Repeat("K", maxHotkeyKeyLen*4)
	longLabel := strings.Repeat("L", maxHotkeyLabelLen*4)
	items := make([]HotkeyItem, maxPluginHotkeys*3)
	for i := range items {
		items[i] = HotkeyItem{Key: longKey, Label: longLabel, Bar: true}
	}

	got := clampHotkeys(items)

	testutil.Equal(t, len(got), maxPluginHotkeys)
	testutil.Equal(t, len([]rune(got[0].Key)), maxHotkeyKeyLen)
	testutil.Equal(t, len([]rune(got[0].Label)), maxHotkeyLabelLen)
	// Truncated, not dropped — the bar flag survives.
	testutil.Equal(t, got[0].Bar, true)
}

func TestClampHotkeys_ShortDictionaryUnchanged(t *testing.T) {
	in := []HotkeyItem{{Key: "^F", Label: "next", Bar: true}}
	got := clampHotkeys(in)
	testutil.Equal(t, len(got), 1)
	testutil.Equal(t, got[0].Key, "^F")
	testutil.Equal(t, got[0].Label, "next")
}

func TestClampHotkeys_TruncatesOnRuneBoundary(t *testing.T) {
	// Multi-byte glyphs must not be split mid-encoding: truncation counts runes.
	in := []HotkeyItem{{Key: strings.Repeat("é", maxHotkeyKeyLen+5), Label: "x"}}
	got := clampHotkeys(in)
	testutil.Equal(t, len([]rune(got[0].Key)), maxHotkeyKeyLen)
	// Still valid UTF-8 (no split byte): every rune is 'é'.
	for _, r := range got[0].Key {
		testutil.Equal(t, r, 'é')
	}
}

func TestDispatchPluginControl_OversizedHotkeysClampedOnStore(t *testing.T) {
	app, fake, stop := activatePluginForTest(t)
	defer stop()

	// Build a JSON dictionary that blows past every cap.
	longLabel := strings.Repeat("Z", maxHotkeyLabelLen*2)
	var b strings.Builder
	b.WriteString(`{"type":"hotkeys","items":[`)
	for i := 0; i < maxPluginHotkeys*2; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"key":"^F","label":"`)
		b.WriteString(longLabel)
		b.WriteString(`","bar":true}`)
	}
	b.WriteString(`]}`)

	fake.onControl([]byte(b.String()))
	syncUI(t, app.tapp)

	var stored []HotkeyItem
	readUI(t, app.tapp, func() {
		if app.activePlugin != nil {
			stored = app.activePlugin.hotkeys
		}
	})
	testutil.Equal(t, len(stored), maxPluginHotkeys)
	testutil.Equal(t, len([]rune(stored[0].Label)), maxHotkeyLabelLen)
}

func TestDispatchPluginControl_HelpTriggersStub(t *testing.T) {
	app, fake, stop := activatePluginForTest(t)
	defer stop()

	var before bool
	readUI(t, app.tapp, func() { before = app.pluginHelpRequested })
	testutil.Equal(t, before, false)

	fake.onControl([]byte(`{"type":"help"}`))
	syncUI(t, app.tapp)

	var after bool
	var mode viewMode
	readUI(t, app.tapp, func() {
		after = app.pluginHelpRequested
		mode = app.mode
	})
	testutil.Equal(t, after, true)
	// Help must not deactivate the view.
	testutil.Equal(t, mode, modePluginView)
}

func TestDispatchPluginControl_UnknownTypeIgnored(t *testing.T) {
	app, fake, stop := activatePluginForTest(t)
	defer stop()

	fake.onControl([]byte(`{"type":"teleport"}`))
	syncUI(t, app.tapp)

	var mode viewMode
	var help bool
	readUI(t, app.tapp, func() {
		mode = app.mode
		help = app.pluginHelpRequested
	})
	testutil.Equal(t, mode, modePluginView) // unchanged
	testutil.Equal(t, help, false)
	testutil.Equal(t, fake.blurredCount.Load(), int32(0))
}

func TestDispatchPluginControl_MalformedJSONIgnored(t *testing.T) {
	app, fake, stop := activatePluginForTest(t)
	defer stop()

	fake.onControl([]byte(`not json {{{`)) // must not panic
	syncUI(t, app.tapp)

	var mode viewMode
	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modePluginView)
	testutil.Equal(t, fake.blurredCount.Load(), int32(0))
}

func TestDispatchPluginControl_HelpDroppedWhenMountStale(t *testing.T) {
	app, _, stop := activatePluginForTest(t)
	defer stop()

	var staleMount *pluginViewMount
	readUI(t, app.tapp, func() { staleMount = app.activePlugin })
	readUI(t, app.tapp, func() { app.deactivatePluginView() })

	// A help frame for the stale mount must be dropped, leaving the flag unset.
	app.dispatchPluginControl(staleMount, []byte(`{"type":"help"}`))
	syncUI(t, app.tapp)

	var help bool
	readUI(t, app.tapp, func() { help = app.pluginHelpRequested })
	testutil.Equal(t, help, false)
}

// TestDispatchPluginControl_ReleaseDroppedWhenMountStale models a late release
// from plugin A arriving after plugin B has taken the ball: the release frame
// carries A's mount, but app.activePlugin is now B. The guard must ignore the
// stale release rather than deactivate the live plugin B.
func TestDispatchPluginControl_ReleaseDroppedWhenMountStale(t *testing.T) {
	app, _, stop := activatePluginForTest(t)
	defer stop()

	// Capture the active mount (plugin A).
	var staleMount *pluginViewMount
	readUI(t, app.tapp, func() { staleMount = app.activePlugin })

	// Make a different mount (plugin B) the current active plugin.
	otherMount := &pluginViewMount{
		view:     &views.View{ID: 99, Title: "Other", CallbackURL: "ws://other"},
		pageName: "plugin-view:99",
	}
	readUI(t, app.tapp, func() { app.activePlugin = otherMount })

	// A late release frame for plugin A's stale mount must be ignored.
	app.dispatchPluginControl(staleMount, []byte(`{"type":"release"}`))
	syncUI(t, app.tapp)

	var active *pluginViewMount
	var mode viewMode
	readUI(t, app.tapp, func() {
		active = app.activePlugin
		mode = app.mode
	})
	if active != otherMount {
		t.Fatal("stale release must not deactivate the currently-active plugin")
	}
	testutil.Equal(t, mode, modePluginView)
}

func TestDispatchPluginControl_HotkeysDroppedWhenMountStale(t *testing.T) {
	app, _, stop := activatePluginForTest(t)
	defer stop()

	// Capture the (now-active) mount, then deactivate so it is stale.
	var staleMount *pluginViewMount
	readUI(t, app.tapp, func() { staleMount = app.activePlugin })
	readUI(t, app.tapp, func() { app.deactivatePluginView() })

	// A hotkeys frame for the stale mount must be dropped, not stored.
	app.dispatchPluginControl(staleMount, []byte(`{"type":"hotkeys","items":[{"key":"^F","label":"x","bar":true}]}`))
	syncUI(t, app.tapp)

	var n int
	readUI(t, app.tapp, func() { n = len(staleMount.hotkeys) })
	testutil.Equal(t, n, 0)
}
