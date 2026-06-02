package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/views"
)

// TestPluginHelp_HelpFrameShowsOverlayWithFullDict asserts a help control frame
// pops the overlay rendering every item in the pushed dictionary (including
// bar:false entries), titled with the plugin title.
func TestPluginHelp_HelpFrameShowsOverlayWithFullDict(t *testing.T) {
	app, fake, sim, stop := activatePluginForTestWithSim(t)
	defer stop()

	// Push a dictionary with a mix of bar:true and bar:false items.
	fake.onControl([]byte(`{"type":"hotkeys","items":[{"key":"^F","label":"next pane","bar":true},{"key":"r","label":"refresh","bar":false}]}`))
	syncUI(t, app.tapp)

	fake.onControl([]byte(`{"type":"help"}`))
	syncUI(t, app.tapp)

	var visible bool
	var mode viewMode
	var front string
	readUI(t, app.tapp, func() {
		visible = app.pluginHelpVisible
		mode = app.mode
		front, _ = app.pages.GetFrontPage()
	})
	testutil.Equal(t, visible, true)
	// Overlay does not change the surrender mode.
	testutil.Equal(t, mode, modePluginView)
	testutil.Equal(t, front, "pluginhelp")

	// Render and confirm the full dictionary shows, but no argus bindings.
	syncUI(t, app.tapp)
	if !previewScreenContains(sim, "next pane") {
		t.Error("overlay must list bar:true item 'next pane'")
	}
	if !previewScreenContains(sim, "refresh") {
		t.Error("overlay must list bar:false item 'refresh' (full dictionary)")
	}
	if !previewScreenContains(sim, "Ludwig") {
		t.Error("overlay title must include the plugin title 'Ludwig'")
	}
	if previewScreenContains(sim, "fork task") {
		t.Error("overlay must NOT show argus's own bindings")
	}
}

// TestPluginHelp_HelpFrameWithNoPriorDictRendersEmptyOverlay asserts that a help
// control frame with NO prior hotkeys dictionary still pops the overlay without
// panicking, showing the plugin title and an empty hotkey list.
func TestPluginHelp_HelpFrameWithNoPriorDictRendersEmptyOverlay(t *testing.T) {
	app, fake, sim, stop := activatePluginForTestWithSim(t)
	defer stop()

	// No hotkeys frame pushed — go straight to help.
	fake.onControl([]byte(`{"type":"help"}`))
	syncUI(t, app.tapp)

	var visible bool
	var mode viewMode
	var front string
	var n int
	readUI(t, app.tapp, func() {
		visible = app.pluginHelpVisible
		mode = app.mode
		front, _ = app.pages.GetFrontPage()
		if app.activePlugin != nil {
			n = len(app.activePlugin.hotkeys)
		}
	})
	testutil.Equal(t, visible, true)
	testutil.Equal(t, mode, modePluginView)
	testutil.Equal(t, front, "pluginhelp")
	testutil.Equal(t, n, 0) // empty dictionary

	// Render and confirm it does not panic and shows the plugin title.
	syncUI(t, app.tapp)
	if !previewScreenContains(sim, "Ludwig") {
		t.Error("overlay title must include the plugin title 'Ludwig' even with an empty dictionary")
	}
}

// TestPluginHelp_NextKeyDismisses asserts that while the overlay is visible the
// next key is consumed to dismiss it and returns to the plugin view.
func TestPluginHelp_NextKeyDismisses(t *testing.T) {
	app, fake, stop := activatePluginForTest(t)
	defer stop()

	fake.onControl([]byte(`{"type":"hotkeys","items":[{"key":"^F","label":"next pane","bar":true}]}`))
	syncUI(t, app.tapp)
	fake.onControl([]byte(`{"type":"help"}`))
	syncUI(t, app.tapp)

	var visible bool
	readUI(t, app.tapp, func() { visible = app.pluginHelpVisible })
	testutil.Equal(t, visible, true)

	// The next key dismisses the overlay (consumed by argus, not forwarded).
	var got *tcell.EventKey
	readUI(t, app.tapp, func() {
		got = app.handleGlobalKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	})
	testutil.Nil(t, got) // consumed, not forwarded to the plugin

	var nowVisible bool
	var mode viewMode
	var front string
	readUI(t, app.tapp, func() {
		nowVisible = app.pluginHelpVisible
		mode = app.mode
		front, _ = app.pages.GetFrontPage()
	})
	testutil.Equal(t, nowVisible, false)
	testutil.Equal(t, mode, modePluginView) // back to plugin view, still surrendered
	testutil.Equal(t, front, "plugin-view:1")

	// Bottom bar is restored for the plugin.
	var active bool
	var title string
	readUI(t, app.tapp, func() { active, title, _ = app.statusbar.PluginMode() })
	testutil.Equal(t, active, true)
	testutil.Equal(t, title, "Ludwig")
}

// TestPluginHelp_AfterDismissKeysForwardAgain asserts that once dismissed,
// argus is back to full surrender (the next key reaches the plugin).
func TestPluginHelp_AfterDismissKeysForwardAgain(t *testing.T) {
	app, fake, stop := activatePluginForTest(t)
	defer stop()

	fake.onControl([]byte(`{"type":"help"}`))
	syncUI(t, app.tapp)

	// First key dismisses.
	readUI(t, app.tapp, func() {
		app.handleGlobalKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	})
	// Second key is now forwarded (surrender restored).
	var got *tcell.EventKey
	readUI(t, app.tapp, func() {
		got = app.handleGlobalKey(tcell.NewEventKey(tcell.KeyRune, 'y', tcell.ModNone))
	})
	if got == nil {
		t.Fatal("after dismissal, keys must forward to the plugin again")
	}
}

// TestPluginHelp_QuestionNotReserved asserts argus does NOT reserve `?`: in
// plugin mode (overlay not visible) a `?` is forwarded to the plugin and does
// NOT open argus's own help.
func TestPluginHelp_QuestionNotReserved(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	m := &pluginViewMount{
		view:     &views.View{ID: 1, Title: "Test", CallbackURL: "ws://x"},
		pageName: "plugin-view:1",
	}
	app.activePlugin = m
	app.mode = modePluginView

	ev := tcell.NewEventKey(tcell.KeyRune, '?', 0)
	got := app.handleGlobalKey(ev)
	testutil.Equal(t, got == ev, true) // forwarded
	testutil.Equal(t, app.mode, modePluginView)
	if app.helpModal != nil {
		t.Fatal("? must not open argus help in plugin mode")
	}
	if app.pluginHelpVisible {
		t.Fatal("? alone must not open the plugin help overlay (plugin triggers it via control frame)")
	}
}

// TestPluginHelp_DismissNoOpWhenNotVisible asserts dismissPluginHelp is a safe
// no-op when no overlay is showing (defensive guard).
func TestPluginHelp_DismissNoOpWhenNotVisible(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.pluginHelpVisible = false
	app.dismissPluginHelp() // must not panic, must stay false
	testutil.Equal(t, app.pluginHelpVisible, false)
}

// TestPluginHelp_DeactivateClearsOverlay asserts the overlay state is cleared on
// deactivate so nothing lingers into the next plugin.
func TestPluginHelp_DeactivateClearsOverlay(t *testing.T) {
	app, fake, stop := activatePluginForTest(t)
	defer stop()

	fake.onControl([]byte(`{"type":"help"}`))
	syncUI(t, app.tapp)

	var visible bool
	readUI(t, app.tapp, func() { visible = app.pluginHelpVisible })
	testutil.Equal(t, visible, true)

	readUI(t, app.tapp, func() { app.deactivatePluginView() })

	var nowVisible bool
	var hasPage bool
	readUI(t, app.tapp, func() {
		nowVisible = app.pluginHelpVisible
		hasPage = app.pages.HasPage("pluginhelp")
	})
	testutil.Equal(t, nowVisible, false)
	testutil.Equal(t, hasPage, false)
}
