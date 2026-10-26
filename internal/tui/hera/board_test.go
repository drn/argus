package hera

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/keymap"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/gdamore/tcell/v2"
)

func drawBoardRows(t *testing.T, b *boardView, width, height int) (tcell.SimulationScreen, []string) {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(width, height)
	b.draw(sim, 0, 0, width, height, " Board ", true, keymap.DefaultKeymap())
	sim.Show()
	var rows []string
	for y := 1; y < height-2; y++ {
		var row strings.Builder
		for x := 1; x < width-1; {
			r, _, width := sim.Get(x, y)
			row.WriteString(r)
			x += max(1, width)
		}
		rows = append(rows, strings.TrimRight(row.String(), " "))
	}
	return sim, rows
}

func TestBoardBodyWrapping(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		width      int
		want       []string
	}{
		{"words and blanks", "whole into Nothing account\n\nlast", 18, []string{"  whole into", "  Nothing", "  account", "", "  last"}},
		{"long token", "abcdefghijklmnop", 12, []string{"  abcdefgh", "  ijklmnop"}},
		{"wide runes", "ab 漢字 漢字", 12, []string{"  ab 漢字", "  漢字"}},
		{"dash bullet", "- whole into Nothing account", 18, []string{"  - whole into", "    Nothing", "    account"}},
		{"star bullet", "* whole into Nothing account", 18, []string{"  * whole into", "    Nothing", "    account"}},
		{"bold and literal syntax", "**My take:** `code` **open", 30, []string{"  My take: `code` **open"}},
		{"bold long token", "**abcdefghijklmnop**", 12, []string{"  abcdefgh", "  ijklmnop"}},
		{"normalized whitespace", "one\t **two**   three", 18, []string{"  one two three"}},
		{"empty bold markers", "****", 18, []string{"  ****"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &boardView{items: []*db.HeraBoardItem{{ID: 1, Kind: "fyi", Subject: "S", Body: tc.body, Read: true}}, expandedID: 1}
			_, rows := drawBoardRows(t, b, tc.width, 20)
			// Locate the first body row independently of header height.
			start := 0
			for start < len(rows) && !strings.HasPrefix(rows[start], "  ") {
				start++
			}
			testutil.Equal(t, start < len(rows), true)
			if start < len(rows) {
				testutil.DeepEqual(t, rows[start:start+len(tc.want)], tc.want)
			}
		})
	}
}

func TestBoardHeaderWrappingAndEllipsis(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		t.Run(fmt.Sprintf("expanded=%v", expanded), func(t *testing.T) {
			b := &boardView{items: []*db.HeraBoardItem{{ID: 1, Kind: "fyi", Subject: "one two three four five six"}}}
			if expanded {
				b.expandedID = 1
			}
			sim, rows := drawBoardRows(t, b, 28, 10)
			if expanded {
				testutil.Equal(t, rows[0], "▾ [fyi] #1 one two three •")
				testutil.Equal(t, rows[1], "           four five six")
				_, style, _ := sim.Get(12, 2)
				_, _, attrs := style.Decompose()
				testutil.Equal(t, attrs&tcell.AttrBold != 0, true)
				testutil.Equal(t, attrs&tcell.AttrReverse != 0, false)
			} else {
				testutil.Equal(t, rows[0], "▸ [fyi] #1 one two thre… •")
				testutil.Equal(t, rows[1], "")
			}
			_, style, _ := sim.Get(1, 1)
			_, _, attrs := style.Decompose()
			testutil.Equal(t, attrs&tcell.AttrBold != 0, true)
			testutil.Equal(t, attrs&tcell.AttrReverse != 0, true)
		})
	}
}

func TestBoardBoldAcrossWraps(t *testing.T) {
	b := &boardView{items: []*db.HeraBoardItem{{ID: 1, Kind: "fyi", Subject: "S", Body: "**whole into Nothing** plain", Read: true}}, expandedID: 1}
	sim, rows := drawBoardRows(t, b, 18, 10)
	testutil.DeepEqual(t, rows[1:3], []string{"  whole into", "  Nothing plain"})
	for _, tc := range []struct {
		x, y int
		bold bool
	}{{3, 2, true}, {3, 3, true}, {11, 3, false}} {
		t.Run(fmt.Sprintf("%d,%d", tc.x, tc.y), func(t *testing.T) {
			_, style, _ := sim.Get(tc.x, tc.y)
			_, _, attrs := style.Decompose()
			testutil.Equal(t, attrs&tcell.AttrBold != 0, tc.bold)
		})
	}
}

func TestBoardStyledBulletAndCombiningRunes(t *testing.T) {
	b := &boardView{items: []*db.HeraBoardItem{{ID: 1, Kind: "fyi", Subject: "S", Body: "- **漢字 one two** plain\n**e\u0301**\n- **abcdefghijklmno**\n**a** **b**", Read: true}}, expandedID: 1}
	sim, rows := drawBoardRows(t, b, 18, 15)
	testutil.DeepEqual(t, rows[1:7], []string{"  - 漢字 one two", "    plain", "  e\u0301", "  - abcdefghijkl", "    mno", "  a b"})
	for _, tc := range []struct {
		x, y int
		bold bool
	}{{5, 2, true}, {5, 3, false}, {3, 4, true}, {5, 6, true}, {5, 7, true}} {
		t.Run(fmt.Sprintf("%d,%d", tc.x, tc.y), func(t *testing.T) {
			_, style, _ := sim.Get(tc.x, tc.y)
			_, _, attrs := style.Decompose()
			testutil.Equal(t, attrs&tcell.AttrBold != 0, tc.bold)
		})
	}
}

func TestBoardBoldAcrossExplicitNewline(t *testing.T) {
	b := &boardView{items: []*db.HeraBoardItem{{ID: 1, Kind: "fyi", Subject: "S", Body: "**one\ntwo** plain", Read: true}}, expandedID: 1}
	sim, rows := drawBoardRows(t, b, 28, 10)
	testutil.DeepEqual(t, rows[1:3], []string{"  one", "  two plain"})
	_, style, _ := sim.Get(3, 3)
	_, _, attrs := style.Decompose()
	testutil.Equal(t, attrs&tcell.AttrBold != 0, true)
}

func TestBoardCursorFollowsWrappedHeader(t *testing.T) {
	b := &boardView{items: []*db.HeraBoardItem{
		{ID: 1, Kind: "fyi", Subject: strings.Repeat("long subject ", 8), Read: true},
		{ID: 2, Kind: "fyi", Subject: "Target", Read: true},
	}, expandedID: 1, cursor: 1, followCursor: true}
	_, rows := drawBoardRows(t, b, 28, 7)
	testutil.Contains(t, strings.Join(rows, "\n"), "#2 Target")
	testutil.Equal(t, b.offset > 0, true)
	b.cursor = 0
	b.followCursor = true
	_, rows = drawBoardRows(t, b, 28, 7)
	testutil.Contains(t, rows[0], "▾ [fyi] #1")
	testutil.Equal(t, b.offset, 0)
}

func TestBoardWideHeaderAndNarrowUnread(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		for _, width := range []int{3, 4, 16, 28} {
			t.Run(fmt.Sprintf("expanded=%v/width=%d", expanded, width), func(t *testing.T) {
				b := &boardView{items: []*db.HeraBoardItem{{ID: 1, Kind: "fyi", Subject: "漢字 subject 漢字 tail", Body: "漢字"}}}
				if expanded {
					b.expandedID = 1
				}
				sim, rows := drawBoardRows(t, b, width, 30)
				testutil.Contains(t, rows[0], "•")
				for y := 1; y < 29; y++ {
					r, _, _ := sim.Get(width-1, y)
					testutil.Equal(t, r, "│")
				}
				if expanded && width == 28 {
					testutil.Equal(t, rows[0], "▾ [fyi] #1 漢字 subject •")
					testutil.Equal(t, rows[1], "           漢字 tail")
				}
			})
		}
	}
}

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
	b.draw(sim, 0, 0, 60, 10, " Board ", true, keymap.DefaultKeymap())
	sim.Show()
	testutil.Contains(t, simScreenText(sim), "Header")
	_, style, _ := sim.Get(2, 1)
	_, _, attrs := style.Decompose()
	testutil.Equal(t, attrs&tcell.AttrBold != 0, true)
	b.expandedID = 1
	b.offset = 30
	b.draw(sim, 0, 0, 60, 10, " Board ", true, keymap.DefaultKeymap())
	sim.Show()
	testutil.Contains(t, simScreenText(sim), "Last line")
	b.items = append(b.items, &db.HeraBoardItem{ID: 2, Subject: "Second"})
	b.cursor = 1
	b.followCursor = true
	b.draw(sim, 0, 0, 60, 10, " Board ", true, keymap.DefaultKeymap())
	sim.Show()
	testutil.Contains(t, simScreenText(sim), "Second")
	testutil.Equal(t, boardText("hi\x1b\x00there"), "hithere")
	b.items = nil
	b.draw(sim, 0, 0, 60, 10, " Board ", false, keymap.DefaultKeymap())
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
	testutil.Equal(t, p.board.expandedID, int64(2))
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

func TestBoardHintRendering(t *testing.T) {
	for _, tc := range []struct {
		name           string
		focused, empty bool
		width, height  int
		bindings       map[string]string
		want           string
	}{
		{"unfocused empty", false, true, 100, 6, nil, "⌘→ open board · p plan"},
		{"focused empty", true, true, 100, 6, nil, "j/k move · ⏎ expand · r reply · x dismiss · p plan · ctrl+q back"},
		{"focused scrolling", true, false, 100, 6, nil, "j/k move · ⏎ expand · r reply · x dismiss · p plan · ctrl+q back"},
		{"narrow", true, false, 12, 6, nil, "j/k move ·"},
		{"one column", true, false, 3, 6, nil, "j"},
		{"wide key clipped", true, false, 3, 6, map[string]string{"down": "界"}, ""},
		{"literal bracket key", true, false, 100, 6, map[string]string{"reply": "["}, "j/k move · ⏎ expand · [ reply · x dismiss · p plan · ctrl+q back"},
		{"one interior row", false, false, 100, 3, nil, "⌘→ open board · p plan"},
		{"rebound focused", true, false, 100, 6, map[string]string{"down": "n", "up": "u", "reply": "a", "dismiss": "d", "toggle": "b"}, "n/u move · ⏎ expand · a reply · d dismiss · b plan · ctrl+q back"},
		{"rebound unfocused", false, true, 100, 6, map[string]string{"toggle": "b"}, "⌘→ open board · b plan"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim := tcell.NewSimulationScreen("UTF-8")
			testutil.NoError(t, sim.Init())
			t.Cleanup(sim.Fini)
			sim.SetSize(110, 10)
			keys, warnings := keymap.Build(config.Keybindings{HeraBoard: tc.bindings})
			testutil.Equal(t, len(warnings), 0)
			b := newBoardView()
			if !tc.empty {
				for id := int64(1); id <= 10; id++ {
					b.items = append(b.items, &db.HeraBoardItem{ID: id, Subject: "Item", Kind: "fyi"})
				}
				b.cursor = 9
				b.followCursor = true
			}
			b.draw(sim, 2, 1, tc.width, tc.height, " Board ", tc.focused, keys)
			sim.Show()
			var row strings.Builder
			for x := 3; x < 2+tc.width-1; x++ {
				r, style, _ := sim.Get(x, tc.height-1)
				row.WriteString(r)
				if x-3 < len([]rune(tc.want)) {
					testutil.Equal(t, style, theme.StyleDimmed)
				}
			}
			testutil.Equal(t, strings.TrimRight(row.String(), " "), tc.want)
			r, _, _ := sim.Get(2+tc.width-1, tc.height-1)
			testutil.Equal(t, r, "│")
			r, _, _ = sim.Get(2+tc.width, tc.height-1)
			testutil.Equal(t, r, " ")
			if !tc.empty && tc.height > 3 && tc.width > 12 {
				testutil.Contains(t, simScreenText(sim), "#10 Item")
			}
		})
	}
}

func TestRailBoardMailboxStyle(t *testing.T) {
	for _, selected := range []bool{false, true} {
		for _, bridging := range []bool{false, true} {
			for _, unread := range []bool{false, true} {
				t.Run(fmt.Sprintf("selected=%v/bridging=%v/unread=%v", selected, bridging, unread), func(t *testing.T) {
					r := NewRail()
					r.boardAuto = true
					m := twoOrchModel()
					if unread {
						m.Active[0].Roles[0].BoardUnread = 1
					}
					r.SetModel(m)
					sim := tcell.NewSimulationScreen("UTF-8")
					testutil.NoError(t, sim.Init())
					t.Cleanup(sim.Fini)
					sim.SetSize(60, 4)
					if bridging {
						r.drawRoleRow(sim, 0, 0, 60, railRow{role: &m.Active[0].Roles[1], collOrchID: 1}, selected, theme.StyleNormal)
					} else {
						r.drawOrchRow(sim, 0, 0, 60, railRow{orch: &m.Active[0]}, selected)
					}
					sim.Show()
					glyph, style, _ := sim.Get(4, 0)
					want := theme.StyleProject
					if selected {
						want = theme.StyleSelected
					}
					if unread {
						testutil.Equal(t, glyph, string(rune(0xf01ee)))
						want = want.Foreground(theme.ColorInProgress)
					} else {
						testutil.Equal(t, glyph, string(heraIconCoord))
					}
					testutil.Equal(t, style, want)
					_, textStyle, _ := sim.Get(6, 0)
					wantText := theme.StyleProject
					if bridging {
						wantText = theme.StyleNormal
					}
					if selected {
						wantText = theme.StyleSelected
					}
					testutil.Equal(t, textStyle, wantText)
				})
			}
		}
	}
}

func TestBoardAccordion(t *testing.T) {
	p := NewHeraPage(nil)
	p.board.items = []*db.HeraBoardItem{{ID: 1, RoleID: 8, Subject: "First", Body: strings.Repeat("First body\n", 30)}, {ID: 2, RoleID: 8, Subject: "Second", Body: strings.Repeat("Second body\n", 20)}}
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(100, 12)
	assertBodies := func(first, second bool) {
		t.Helper()
		p.board.draw(sim, 0, 0, 100, 12, " Board ", true, p.keys())
		sim.Show()
		text := simScreenText(sim)
		testutil.Equal(t, strings.Contains(text, "First body"), first)
		testutil.Equal(t, strings.Contains(text, "Second body"), second)
	}
	var read []int64
	p.OnBoardRead = func(_, id int64, _ time.Time) { read = append(read, id) }
	enter := tcell.NewEventKey(tcell.KeyEnter, 0, 0)
	p.handleBoardKey(enter, nil)
	testutil.Equal(t, p.board.expandedID, int64(1))
	assertBodies(true, false)
	for _, ev := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyRune, 'j', 0),
		tcell.NewEventKey(tcell.KeyRune, 'k', 0),
		tcell.NewEventKey(tcell.KeyDown, 0, 0),
		tcell.NewEventKey(tcell.KeyUp, 0, 0),
	} {
		p.handleBoardKey(ev, nil)
		testutil.Equal(t, p.board.expandedID, int64(1))
	}
	p.handleBoardKey(tcell.NewEventKey(tcell.KeyDown, 0, 0), nil)
	assertBodies(true, false)
	p.handleBoardKey(enter, nil)
	testutil.Equal(t, p.board.expandedID, int64(2))
	assertBodies(false, true)
	testutil.Contains(t, simScreenText(sim), "#2 Second")
	testutil.DeepEqual(t, read, []int64{1, 2})
	p.handleBoardKey(enter, nil)
	testutil.Equal(t, p.board.expandedID, int64(0))
	assertBodies(false, false)
	testutil.DeepEqual(t, read, []int64{1, 2})
}

func TestBoardAccordionRefresh(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []*db.HeraBoardItem
		want  int64
	}{
		{"retained and reordered", []*db.HeraBoardItem{{ID: 2, Body: "updated"}, {ID: 1}}, 2},
		{"resolved or removed", []*db.HeraBoardItem{{ID: 1}}, 0},
		{"all removed", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewHeraPage(nil)
			o := &heramodel.OrchView{ID: 1, Roles: []heramodel.RoleView{{RoleID: 8, Kind: db.HeraKindCoordinator, BoardItems: []*db.HeraBoardItem{{ID: 1}, {ID: 2}}}}}
			p.updateBoard(o)
			p.board.cursor = 1
			p.handleBoardKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0), nil)
			o.Roles[0].BoardItems = tc.items
			p.updateBoard(o)
			testutil.Equal(t, p.board.expandedID, tc.want)
			o.Roles[0].BoardItems = []*db.HeraBoardItem{{ID: 1}, {ID: 2}}
			p.updateBoard(o)
			testutil.Equal(t, p.board.expandedID, tc.want)
		})
	}
}
