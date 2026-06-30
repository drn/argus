package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/keymap"
	"github.com/drn/argus/internal/tui/modal"
)

// testDBWithConfig opens a real on-disk DB whose directory carries a
// config.toml, so the keybinding overlay flows through the production path
// (db.Config → FileLoader.Apply → keymap.Build). HOME is redirected so the
// agent/worktree guards never touch the real ~/.argus.
func testDBWithConfig(t *testing.T, toml string) *db.DB {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	if toml != "" {
		if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(toml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d, err := db.Open(filepath.Join(dir, "data.sql"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// TestSmoke_KeymapRebindTaskListNew proves the dispatch consults the live keymap
// end-to-end: with tasklist.new rebound to "x" in config.toml, pressing `x`
// opens the new-task form and the default `n` no longer does.
func TestSmoke_KeymapRebindTaskListNew(t *testing.T) {
	d := testDBWithConfig(t, "[keybindings.tasklist]\nnew = \"x\"\n")
	if err := d.Add(&model.Task{Name: "t1", Status: model.StatusInProgress, Project: "p"}); err != nil {
		t.Fatal(err)
	}
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()

	// Default key `n` must NOT open the form anymore.
	sim.InjectKey(tcell.KeyRune, 'n', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeTaskList) })

	// The rebound key `x` opens the new-task form.
	sim.InjectKey(tcell.KeyRune, 'x', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeNewTask) })
}

// TestSmoke_KeymapInvalidOverrideKeepsDefault proves a rejected override (a bare
// rune in the agent context) doesn't break dispatch: the default still resolves.
func TestSmoke_KeymapInvalidOverrideKeepsDefault(t *testing.T) {
	d := testDBWithConfig(t, "[keybindings.agent]\nlinks = \"z\"\n")
	app := New(d, agent.NewRunner(nil), false)
	// activeKeymap was primed in New; the bare-rune agent override is rejected,
	// so ctrl+l still resolves to the link-picker action.
	act, ok := app.activeKeymap().Resolve(keymap.CtxAgent, tcell.NewEventKey(tcell.KeyCtrlL, 0, 0))
	testutil.True(t, ok)
	testutil.Equal(t, act, keymap.ActAgentLinks)
}

// TestSmoke_CtrlZSwallowedWhenZoomRebound is the regression guard for the
// SIGTSTP invariant: even when agent.zoom is rebound away from ctrl+z, a literal
// ctrl+z must be consumed (never forwarded to the PTY as 0x1a). With zoom on
// ctrl+w, ctrl+z is inert (zen unchanged) and ctrl+w toggles zen.
func TestSmoke_CtrlZSwallowedWhenZoomRebound(t *testing.T) {
	d := testDBWithConfig(t, "[keybindings.agent]\nzoom = \"ctrl+w\"\n")
	task := &model.Task{ID: "zr-1", Name: "zoom rebind", Status: model.StatusPending, Project: "p"}
	testutil.NoError(t, d.Add(task))
	app := New(d, agent.NewRunner(nil), false)
	app.refreshTasks()
	sim, stop := wireApp(t, app)
	defer stop()

	// Enter agent view — defaults to zoomed (agentZen=true).
	sim.InjectKey(tcell.KeyEnter, 0, 0)
	syncUI(t, app.tapp)
	var zen bool
	readUI(t, app.tapp, func() { zen = app.agentZen })
	testutil.Equal(t, zen, true)

	// ctrl+z is now unbound (zoom moved to ctrl+w): it must be swallowed and NOT
	// toggle zen (and the dispatch must not forward 0x1a to the PTY).
	sim.InjectKey(tcell.KeyCtrlZ, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { zen = app.agentZen })
	testutil.Equal(t, zen, true) // unchanged — ctrl+z inert

	// The rebound key toggles zoom.
	sim.InjectKey(tcell.KeyCtrlW, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { zen = app.agentZen })
	testutil.Equal(t, zen, false)
}

// TestFilePanelKey_UnboundNonRuneFallsThrough guards the `default:` dispatch
// change: an unbound non-rune key (e.g. ctrl+g) must still fall through
// (return the event) so it isn't silently swallowed.
func TestFilePanelKey_UnboundNonRuneFallsThrough(t *testing.T) {
	app := New(testDB(t), agent.NewRunner(nil), false)
	got := app.handleFilePanelKey(tcell.NewEventKey(tcell.KeyCtrlG, 0, 0))
	testutil.NotNil(t, got) // unbound key propagates, not consumed
}

// TestHelpReflectsOverride proves the help overlay renders the overridden key.
func TestHelpReflectsOverride(t *testing.T) {
	km, warns := keymap.Build(config.Keybindings{TaskList: map[string]string{"new": "x"}})
	testutil.Equal(t, len(warns), 0)
	sections := modal.SectionsFromKeymap(km)
	var found string
	for _, sec := range sections {
		for _, b := range sec.Bindings {
			if b.Action == "new task" {
				found = b.Key
			}
		}
	}
	testutil.Equal(t, found, "x")
}
