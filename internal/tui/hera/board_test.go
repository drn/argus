package hera

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/keymap"
	"github.com/gdamore/tcell/v2"
)

func TestBoardSelectionDoesNotStealView(t *testing.T) {
	p := NewHeraPage(nil)
	o := &heramodel.OrchView{ID: 1, Roles: []heramodel.RoleView{{RoleID: 2, Kind: db.HeraKindCoordinator, BoardUnread: 1, BoardItems: []*db.HeraBoardItem{{ID: 3, Subject: "Choose", Kind: "decision"}}}}}
	p.updateBoard(o)
	testutil.Equal(t, p.showBoard, true)
	p.showBoard = false
	p.updateBoard(o)
	testutil.Equal(t, p.showBoard, false)
	p.updateBoard(nil)
	p.SetBoardAuto(false)
	p.updateBoard(o)
	testutil.Equal(t, p.showBoard, false)
}

func TestBoardRefreshMailboxAndToggle(t *testing.T) {
	d := memDB(t)
	oid := seedOrch(t, d, "board")
	seedBoundRole(t, d, oid, "coord", db.HeraKindCoordinator, "t-coord")
	p := NewHeraPage(d)
	p.Refresh()
	testutil.Equal(t, selectOrchByName(p, "board"), true)
	testutil.Equal(t, p.showBoard, false)
	c := p.detailsOrch().CoordRole()
	i, err := d.PostHeraBoardItem(c.RoleID, "fyi", "Ready", "body")
	testutil.NoError(t, err)
	p.Refresh()
	testutil.Equal(t, p.showBoard, false)
	testutil.Equal(t, p.rail.coordMarker(oid), rune(0xf01ee))
	p.SetBoardAuto(false)
	testutil.Equal(t, p.rail.coordMarker(oid), heraIconCoord)
	testutil.Equal(t, p.boardTitle(), " Plan ")
	keys, _ := keymap.Build(config.Keybindings{HeraBoard: map[string]string{"toggle": "b"}})
	p.Keys = func() *keymap.Keymap { return keys }
	p.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'b', 0), noFocus)
	testutil.Equal(t, p.showBoard, true)
	testutil.NoError(t, d.ReadHeraBoardItem(c.RoleID, i.ID))
	p.Refresh()
	testutil.Equal(t, p.showBoard, true)
	p.SetBoardAuto(true)
	testutil.Equal(t, p.rail.coordMarker(oid), heraIconCoord)
	testutil.Equal(t, p.boardTitle(), " Plan · Board (0) ")
}

type boardReplySession struct {
	*fakeSession
	origin agentview.InputOrigin
	err    error
}

func TestBoardToggleDoesNotStealFilterTyping(t *testing.T) {
	p := planPage(t)
	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, '/', 0), noFocus)
	h(tcell.NewEventKey(tcell.KeyRune, 'p', 0), noFocus)
	testutil.Equal(t, p.rail.Filtering(), true)
	testutil.Equal(t, p.rail.Filter(), "p")
	testutil.Equal(t, p.showBoard, false)
}

func (s *boardReplySession) WriteInput(b []byte, o agentview.InputOrigin) (int, error) {
	s.origin = o
	if s.err != nil {
		return 0, s.err
	}
	s.wrote = append(s.wrote, b...)
	return len(b), nil
}

func TestBoardReplyFailuresAndOrigin(t *testing.T) {
	for _, tc := range []struct {
		name       string
		live, fail bool
	}{
		{"success", true, false}, {"dead", false, false}, {"write fails", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewHeraPage(memDB(t))
			s := &boardReplySession{fakeSession: &fakeSession{alive: tc.live}}
			if tc.fail {
				s.err = errors.New("write failed")
			}
			p.SetSessionResolver(func(string) agentview.TerminalAdapter { return s })
			var notice string
			p.OnInfo = func(v string) { notice = v }
			i := &db.HeraBoardItem{ID: 7, Subject: "Deploy?"}
			p.replyToBoard(i)
			if tc.live && !tc.fail {
				testutil.Equal(t, string(s.wrote), "\x1b[200~Re: Deploy? (#7)\n\n\x1b[201~")
				testutil.Equal(t, s.origin, agentview.OriginUser)
				testutil.Equal(t, p.focus.State(), FocusCoord)
			} else {
				testutil.Equal(t, p.focus.State(), FocusRail)
				testutil.Equal(t, notice != "", true)
			}
		})
	}
}

func TestBoardRenderingScrollAndBold(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(60, 10)
	b := newBoardView()
	b.items = []*db.HeraBoardItem{{ID: 1, Kind: "fyi", Subject: "Header", Body: strings.Repeat("long line\n", 30) + "Last line"}}
	b.draw(sim, 0, 0, 60, 10, " Board ", true)
	sim.Show()
	testutil.Contains(t, simScreenText(sim), "Header")
	_, style, _ := sim.Get(2, 1)
	_, _, attrs := style.Decompose()
	testutil.Equal(t, attrs&tcell.AttrBold != 0, true)
	b.expanded[1] = true
	b.offset = 30
	b.draw(sim, 0, 0, 60, 10, " Board ", true)
	sim.Show()
	testutil.Contains(t, simScreenText(sim), "Last line")
	b.items = append(b.items, &db.HeraBoardItem{ID: 2, Subject: "Second"})
	b.cursor = 1
	b.followCursor = true
	b.draw(sim, 0, 0, 60, 10, " Board ", true)
	sim.Show()
	testutil.Contains(t, simScreenText(sim), "Second")
	testutil.Equal(t, boardText("hi\x1b\x00there"), "hithere")
	b.items = nil
	b.draw(sim, 0, 0, 60, 10, " Board ", false)
	sim.Show()
	testutil.Contains(t, simScreenText(sim), "No open board items")
}

func TestBoardNavigationReadAndDismiss(t *testing.T) {
	p := NewHeraPage(nil)
	p.board.items = []*db.HeraBoardItem{{ID: 1, RoleID: 8, Subject: "One"}, {ID: 2, RoleID: 8, Subject: "Two"}}
	var read, dismiss int64
	p.OnBoardRead = func(_, id int64, _ time.Time) { read = id }
	p.OnBoardDismiss = func(_, id int64) { dismiss = id }
	p.handleBoardKey(tcell.NewEventKey(tcell.KeyDown, 0, 0), nil)
	p.handleBoardKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0), nil)
	testutil.Equal(t, read, int64(2))
	testutil.Equal(t, p.board.expanded[2], true)
	p.handleBoardKey(tcell.NewEventKey(tcell.KeyRune, 'x', 0), nil)
	testutil.Equal(t, dismiss, int64(2))
	p.handleBoardKey(tcell.NewEventKey(tcell.KeyUp, 0, 0), nil)
	testutil.Equal(t, p.board.selected().ID, int64(1))
	p.boardHeight = 10
	p.handleBoardKey(tcell.NewEventKey(tcell.KeyPgDn, 0, 0), nil)
	testutil.Equal(t, p.board.offset, 7)
	p.handleBoardKey(tcell.NewEventKey(tcell.KeyPgUp, 0, 0), nil)
	testutil.Equal(t, p.board.offset, 0)
	p.board.items = nil
	p.handleBoardKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0), nil)
	testutil.Nil(t, p.board.selected())
}
