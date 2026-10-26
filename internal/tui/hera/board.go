package hera

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/db"
	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/tui/keymap"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/tview"
)

type boardView struct {
	items        []*db.HeraBoardItem
	cursor       int
	offset       int
	expandedID   int64
	followCursor bool
}

func newBoardView() *boardView { return &boardView{} }

func (p *HeraPage) SetBoardAuto(enabled bool) {
	p.boardAuto = enabled
	p.rail.boardAuto = enabled
}

func (r *Rail) coordMarker(orchID int64) rune {
	if r.boardAuto {
		if o := r.model.OrchByID(orchID); o != nil {
			if c := o.CoordRole(); c != nil && c.BoardUnread > 0 {
				return '\U000f01ee'
			} // nf-md-email
		}
	}
	return heraIconCoord
}

func (p *HeraPage) updateBoard(o *heramodel.OrchView) {
	if o == nil {
		p.boardOrchID = 0
		p.showBoard = false
		p.board = newBoardView()
		return
	}
	c := o.CoordRole()
	if p.boardOrchID != o.ID {
		p.board = newBoardView()
		p.showBoard = p.boardAuto && c != nil && c.BoardUnread > 0
		p.boardOrchID = o.ID
		uxlog.Log("[board] selection orch=%d board=%v", o.ID, p.showBoard)
	}
	var selected int64
	if i := p.board.selected(); i != nil {
		selected = i.ID
	}
	p.board.items = nil
	if c != nil {
		p.board.items = c.BoardItems
	}
	expandedPresent := false
	for n, i := range p.board.items {
		if i.ID == selected {
			p.board.cursor = n
		}
		if i.ID == p.board.expandedID {
			expandedPresent = true
		}
	}
	if !expandedPresent && p.board.expandedID != 0 {
		uxlog.Log("[board] expansion cleared orch=%d item=%d: no longer open", o.ID, p.board.expandedID)
		p.board.expandedID = 0
	}
	p.board.cursor = max(0, min(p.board.cursor, len(p.board.items)-1))
}

func (b *boardView) selected() *db.HeraBoardItem {
	if b.cursor < 0 || b.cursor >= len(b.items) {
		return nil
	}
	return b.items[b.cursor]
}

func boardText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
}

func (p *HeraPage) boardTitle() string {
	if !p.boardAuto && !p.showBoard {
		return " Plan "
	}
	unread := 0
	for _, i := range p.board.items {
		if !i.Read {
			unread++
		}
	}
	return fmt.Sprintf(" Plan · Board (%d) ", unread)
}

func boardHint(keys *keymap.Keymap, focused bool) string {
	label := func(act keymap.Action) string {
		binding, _ := keys.BindingFor(keymap.CtxHeraBoard, act)
		return binding.String()
	}
	if !focused {
		return "⌘→ open board · " + label(keymap.ActBoardToggle) + " plan"
	}
	return fmt.Sprintf("%s/%s move · ⏎ expand · %s reply · %s dismiss · %s plan · ctrl+q back",
		label(keymap.ActBoardDown), label(keymap.ActBoardUp), label(keymap.ActBoardReply),
		label(keymap.ActBoardDismiss), label(keymap.ActBoardToggle))
}

func (b *boardView) draw(screen tcell.Screen, x, y, w, h int, title string, focused bool, keys *keymap.Keymap) {
	border := theme.StyleBorder
	if focused {
		border = theme.StyleFocusedBorder
	}
	inner := widget.DrawBorderedPanel(screen, x, y, w, h, title, border)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}
	// Reserve the footer before layout and scrolling, including empty boards.
	hint := runewidth.Truncate(boardHint(keys, focused), inner.W, "")
	tview.Print(screen, tview.Escape(hint), inner.X, inner.Y+inner.H-1, inner.W, tview.AlignLeft, theme.ColorDimmed)
	inner.H--
	if inner.H == 0 {
		return
	}
	type line struct {
		text           string
		bold, selected bool
	}
	var lines []line
	selectedStart := 0
	for n, i := range b.items {
		selected := n == b.cursor
		if selected {
			selectedStart = len(lines)
		}
		marker := "▸"
		if b.expandedID == i.ID {
			marker = "▾"
		}
		unread := ""
		if !i.Read {
			unread = " •"
		}
		header := fmt.Sprintf("%s [%s] #%d %s%s", marker, i.Kind, i.ID, i.Subject, unread)
		lines = append(lines, line{boardText(header), true, selected})
		if b.expandedID == i.ID {
			for _, part := range strings.Split(boardText(i.Body), "\n") {
				for _, wrapped := range strings.Split(runewidth.Wrap(part, max(1, inner.W-2)), "\n") {
					lines = append(lines, line{"  " + wrapped, false, false})
				}
			}
		}
	}
	if len(lines) == 0 {
		widget.DrawText(screen, inner.X, inner.Y, inner.W, "No open board items", theme.StyleDimmed)
		return
	}
	if b.followCursor {
		if selectedStart < b.offset {
			b.offset = selectedStart
		}
		if selectedStart >= b.offset+inner.H {
			b.offset = selectedStart - inner.H + 1
		}
		b.followCursor = false
	}
	b.offset = max(0, min(b.offset, max(0, len(lines)-inner.H)))
	for n := b.offset; n < len(lines) && n-b.offset < inner.H; n++ {
		st := theme.StyleNormal
		if lines[n].bold {
			st = st.Bold(true)
		}
		if lines[n].selected && focused {
			st = st.Reverse(true)
		}
		widget.DrawText(screen, inner.X, inner.Y+n-b.offset, inner.W, lines[n].text, st)
	}
}

func (p *HeraPage) handleBoardKey(event *tcell.EventKey, setFocus func(tview.Primitive)) {
	act, _ := p.keys().Resolve(keymap.CtxHeraBoard, event)
	switch {
	case event.Key() == tcell.KeyDown || act == keymap.ActBoardDown:
		p.board.cursor = min(len(p.board.items)-1, p.board.cursor+1)
		p.board.followCursor = true
	case event.Key() == tcell.KeyUp || act == keymap.ActBoardUp:
		p.board.cursor = max(0, p.board.cursor-1)
		p.board.followCursor = true
	case event.Key() == tcell.KeyPgDn:
		p.board.offset += max(1, p.boardHeight-3)
	case event.Key() == tcell.KeyPgUp:
		p.board.offset = max(0, p.board.offset-max(1, p.boardHeight-3))
	default:
		i := p.board.selected()
		if i == nil {
			return
		}
		switch {
		case event.Key() == tcell.KeyEnter:
			p.board.followCursor = true
			if p.board.expandedID == i.ID {
				p.board.expandedID = 0
			} else {
				p.board.expandedID = i.ID
				if p.OnBoardRead != nil {
					p.OnBoardRead(i.RoleID, i.ID, i.UpdatedAt)
				}
			}
			uxlog.Log("[board] expansion orch=%d item=%d expanded=%v", p.boardOrchID, i.ID, p.board.expandedID == i.ID)
		case act == keymap.ActBoardReply:
			p.replyToBoard(i)
		case act == keymap.ActBoardDismiss:
			if p.OnBoardDismiss != nil {
				p.OnBoardDismiss(i.RoleID, i.ID)
			}
		}
	}
}

func (p *HeraPage) replyToBoard(i *db.HeraBoardItem) {
	var sess agentview.TerminalAdapter
	if p.resolve != nil {
		sess = p.resolve(p.coordBound)
	}
	if sess == nil || !sess.Alive() {
		uxlog.Log("[board] reply skipped role=%d item=%d: no live coordinator", i.RoleID, i.ID)
		if p.OnInfo != nil {
			p.OnInfo("Restart the coordinator before replying")
		}
		return
	}
	data := []byte(fmt.Sprintf("\x1b[200~Re: %s (#%d)\n\n\x1b[201~", i.Subject, i.ID))
	n, err := sess.WriteInput(data, agentview.OriginUser)
	if err != nil || n != len(data) {
		uxlog.Log("[board] reply failed role=%d item=%d bytes=%d/%d: %v", i.RoleID, i.ID, n, len(data), err)
		if p.OnInfo != nil {
			p.OnInfo("Board reply paste failed; check the coordinator composer")
		}
		return
	}
	p.coordPane.ResetScroll()
	p.focus.SetRegion(FocusCoord)
	uxlog.Log("[board] reply composed role=%d item=%d", i.RoleID, i.ID)
}
