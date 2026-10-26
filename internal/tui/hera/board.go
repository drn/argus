package hera

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

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
	"github.com/rivo/uniseg"
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
	p.rail.SetBoardAuto(enabled)
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

type boardLine struct {
	text           string
	bold, selected bool
	boldRunes      []bool
}

// boardBold removes only paired, nonempty bold markers. Other syntax stays literal.
func boardBold(s string) (string, []bool) {
	var text strings.Builder
	var bold []bool
	appendText := func(s string, strong bool) {
		text.WriteString(s)
		for range s {
			bold = append(bold, strong)
		}
	}
	for len(s) > 0 {
		start := strings.Index(s, "**")
		if start < 0 {
			appendText(s, false)
			break
		}
		end := strings.Index(s[start+2:], "**")
		if end <= 0 {
			appendText(s[:start+2], false)
			s = s[start+2:]
			continue
		}
		appendText(s[:start], false)
		appendText(s[start+2:start+2+end], true)
		s = s[start+4+end:]
	}
	return text.String(), bold
}

func boardBodyLines(body string, width int) []boardLine {
	var lines []boardLine
	indent := strings.Repeat(" ", min(2, max(0, width-1)))
	body, allBold := boardBold(strings.TrimRight(boardText(body), "\n"))
	offset := 0
	for _, paragraph := range strings.Split(body, "\n") {
		length := utf8.RuneCountInString(paragraph)
		bold := allBold[offset : offset+length]
		offset += length + 1
		bullet := ""
		if strings.HasPrefix(paragraph, "- ") || strings.HasPrefix(paragraph, "* ") {
			bullet, paragraph = paragraph[:2], paragraph[2:]
			bold = bold[2:]
		}
		source := []rune(paragraph)
		pos := 0
		padding := indent + strings.Repeat(" ", min(len(bullet), max(0, width-len(indent)-1)))
		for n, wrapped := range widget.WrapPreservingLines(paragraph, max(1, width-len(padding))) {
			prefix := padding
			if n == 0 && bullet != "" {
				prefix = indent + bullet[:len(padding)-len(indent)]
			}
			line := boardLine{text: prefix + wrapped, boldRunes: make([]bool, len(prefix))}
			// The shared wrapper normalizes whitespace; consume matching source
			// runes so styles survive word wraps and hard-broken tokens alike.
			for _, r := range wrapped {
				if r != ' ' {
					for pos < len(source) && source[pos] != r {
						pos++
					}
				}
				strong := pos < len(bold) && bold[pos]
				line.boldRunes = append(line.boldRunes, strong)
				if r == ' ' {
					for pos < len(source) && unicode.IsSpace(source[pos]) {
						pos++
					}
				} else if pos < len(source) {
					pos++
				}
			}
			lines = append(lines, line)
		}
	}
	return lines
}

func (l boardLine) draw(screen tcell.Screen, x, y, width int, focused bool) {
	col, index := 0, 0
	graphemes := uniseg.NewGraphemes(l.text)
	for graphemes.Next() {
		runes := graphemes.Runes()
		w := runewidth.StringWidth(graphemes.Str())
		if col+w > width {
			break
		}
		style := theme.StyleNormal.Bold(l.bold || (index < len(l.boldRunes) && l.boldRunes[index])).Reverse(l.selected && focused)
		if w > 0 {
			screen.SetContent(x+col, y, runes[0], runes[1:], style)
		}
		col += w
		index += len(runes)
	}
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
	var lines []boardLine
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
			if inner.W == 1 {
				unread = "•"
			}
		}
		prefix := boardText(fmt.Sprintf("%s [%s] #%d ", marker, i.Kind, i.ID))
		subject := boardText(i.Subject)
		if b.expandedID == i.ID {
			// Leave room for a wide subject rune even in very narrow panels.
			prefix = runewidth.Truncate(prefix, max(0, inner.W-runewidth.StringWidth(unread)-2), "")
			indent := strings.Repeat(" ", runewidth.StringWidth(prefix))
			markerOnly := unread != "" && inner.W-runewidth.StringWidth(unread) < 2
			wrapWidth := max(1, inner.W-len(indent)-runewidth.StringWidth(unread))
			if markerOnly {
				lines = append(lines, boardLine{text: unread, bold: true, selected: selected})
				wrapWidth = inner.W
			}
			for n, wrapped := range widget.WrapPreservingLines(subject, wrapWidth) {
				header := indent + wrapped
				if n == 0 && !markerOnly {
					header = prefix + wrapped + unread
				}
				lines = append(lines, boardLine{text: header, bold: true, selected: selected && n == 0 && !markerOnly})
			}
			lines = append(lines, boardBodyLines(i.Body, inner.W)...)
		} else {
			header := runewidth.Truncate(prefix+subject, max(0, inner.W-runewidth.StringWidth(unread)), "…") + unread
			if inner.W <= runewidth.StringWidth(unread) {
				header = unread
			}
			lines = append(lines, boardLine{text: header, bold: true, selected: selected})
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
		lines[n].draw(screen, inner.X, inner.Y+n-b.offset, inner.W, focused)
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
