package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/tui/keymap"
	"github.com/gdamore/tcell/v2"
)

// This exercises the actual app event loop and asynchronous DB callbacks; the
// only substitute is the PTY adapter, so no real agent or daemon is involved.
func TestSmoke_CoordinatorBoard(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	task := &model.Task{Name: "coord", Worktree: t.TempDir(), Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	o, err := d.CreateHeraOrchestrator("board", "")
	testutil.NoError(t, err)
	role, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{OrchestratorID: o.ID, Name: "coord", Kind: db.HeraKindCoordinator}, task.ID, task.Worktree)
	testutil.NoError(t, err)
	item, err := d.PostHeraBoardItem(role.ID, "decision", "Choose rollout", "Expanded body visible")
	testutil.NoError(t, err)
	sess := &recordingWriteSession{fakeKickSession: &fakeKickSession{alive: true}}
	runner := &bounceTestRunner{Runner: agent.NewRunner(nil), sessions: map[string]agent.SessionHandle{task.ID: sess}}
	app := New(d, runner, false)
	sim, stop := wireApp(t, app)
	defer stop()
	sim.SetSize(240, 50)
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.heraPage.SelectOrch(o.ID), true) })
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		w, h := sim.Size()
		testutil.Contains(t, screenText(sim, w, h), "⌘→ open board · p plan")
	})
	sim.InjectKey(tcell.KeyRight, 0, tcell.ModCtrl|tcell.ModAlt)
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyRight, 0, tcell.ModCtrl|tcell.ModAlt)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.heraPage.Machine().State(), hera.FocusAgent)
		testutil.Equal(t, app.tapp.GetFocus() == app.heraPage, true)
		w, h := sim.Size()
		testutil.Contains(t, screenText(sim, w, h), "Choose rollout")
		testutil.Contains(t, screenText(sim, w, h), "j/k move · ⏎ expand · r reply · x dismiss · p plan · ctrl+q back")
	})
	sim.InjectKey(tcell.KeyCtrlQ, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.heraPage.Machine().State(), hera.FocusRail)
		w, h := sim.Size()
		testutil.Contains(t, screenText(sim, w, h), "⌘→ open board · p plan")
	})
	keys, warnings := keymap.Build(config.Keybindings{HeraBoard: map[string]string{"reply": "a"}})
	testutil.Equal(t, len(warnings), 0)
	readUI(t, app.tapp, func() { app.heraPage.Keys = func() *keymap.Keymap { return keys } })
	sim.InjectKey(tcell.KeyRight, 0, tcell.ModCtrl|tcell.ModAlt)
	sim.InjectKey(tcell.KeyRight, 0, tcell.ModCtrl|tcell.ModAlt)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		w, h := sim.Size()
		testutil.Contains(t, screenText(sim, w, h), "j/k move · ⏎ expand · a reply · x dismiss · p plan · ctrl+q back")
	})
	sim.InjectKey(tcell.KeyEnter, 0, 0)
	syncUI(t, app.tapp)
	waitForCond(t, app.tapp, "board marked read", func() bool {
		items, e := d.ListHeraBoardItems(role.ID, false)
		return e == nil && len(items) == 1 && items[0].Read
	})
	readUI(t, app.tapp, func() { w, h := sim.Size(); testutil.Contains(t, screenText(sim, w, h), "Expanded body visible") })
	sim.InjectKey(tcell.KeyRune, 'a', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.heraPage.Machine().State(), hera.FocusCoord)
		testutil.Equal(t, len(sess.writes), 1)
		testutil.Equal(t, string(sess.writes[0]), fmt.Sprintf("\x1b[200~Re: Choose rollout (#%d)\n\n\x1b[201~", item.ID))
		testutil.Equal(t, sess.origins[0], agentview.OriginUser)
	})
	sim.InjectKey(tcell.KeyRight, 0, tcell.ModCtrl|tcell.ModAlt)
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyRune, 'x', 0)
	syncUI(t, app.tapp)
	waitForCond(t, app.tapp, "board dismissed", func() bool {
		items, e := d.ListHeraBoardItems(role.ID, true)
		return e == nil && len(items) == 1 && items[0].Resolved && items[0].ResolvedBy == "human"
	})
	msgs, err := d.HeraInbox(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 1)
	testutil.Contains(t, msgs[0].Body, "Aaron dropped")
	_, err = d.PostHeraBoardItem(role.ID, "fyi", "First accordion item", "First accordion body")
	testutil.NoError(t, err)
	_, err = d.PostHeraBoardItem(role.ID, "fyi", "Second accordion item", "Second accordion body")
	testutil.NoError(t, err)
	readUI(t, app.tapp, func() { app.heraPage.Refresh() })
	sim.InjectKey(tcell.KeyEnter, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		w, h := sim.Size()
		testutil.Contains(t, screenText(sim, w, h), "First accordion body")
	})
	sim.InjectKey(tcell.KeyRune, 'j', 0)
	sim.InjectKey(tcell.KeyEnter, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { app.heraPage.Refresh() })
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		w, h := sim.Size()
		text := screenText(sim, w, h)
		testutil.Equal(t, strings.Contains(text, "First accordion body"), false)
		testutil.Contains(t, text, "Second accordion body")
	})
	sim.InjectKey(tcell.KeyEnter, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		w, h := sim.Size()
		testutil.Equal(t, strings.Contains(screenText(sim, w, h), "Second accordion body"), false)
	})
}

func TestSmoke_CoordinatorBoardMutationError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	_, stop := wireApp(t, app)
	defer stop()
	go app.mutateHeraBoard(d, 999, 999, time.Time{}, true)
	waitForCond(t, app.tapp, "board error surfaced", func() bool { return strings.Contains(app.statusbar.Error(), "Board update failed") })
}
