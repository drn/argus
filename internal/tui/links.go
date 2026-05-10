package tui

import (
	"os/exec"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/links"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
)

// Link is the link type used by the TUI's pickers. Aliased so tui code keeps
// reading naturally; the canonical definition lives in internal/links so the
// HTTP API can share it without dragging in tview.
type Link = links.Link

// ExtractLinks re-exports the shared extractor so callers in this package
// don't have to qualify it.
var ExtractLinks = links.Extract

// openURL opens the given URL in the default browser (macOS).
// Only http:// and https:// schemes are allowed to prevent opening
// file://, javascript:, or custom URI schemes from untrusted content.
//
// browserOpener is the package-level seam tests stub out so they don't
// actually launch a browser. The real implementation shells out to `open`
// (macOS); a future cross-platform port replaces this var directly.
var browserOpener = func(url string) error {
	return exec.Command("open", url).Start()
}

func openURL(url string) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		uxlog.Log("[links] rejected non-http URL: %s", url)
		return
	}
	if err := browserOpener(url); err != nil {
		uxlog.Log("[links] open URL failed: %v", err)
		return
	}
	uxlog.Log("[links] opened URL in browser: %s", url)
}

// ---------------------------------------------------------------------------
// LinkPickerModal — selection dialog for multiple links
// ---------------------------------------------------------------------------

// LinkPickerModal presents a list of links for the user to choose from.
type LinkPickerModal struct {
	*tview.Box
	links    []Link
	cursor   int
	selected bool
	canceled bool
}

// NewLinkPickerModal creates a link picker dialog.
func NewLinkPickerModal(items []Link) *LinkPickerModal {
	return &LinkPickerModal{
		Box:   tview.NewBox(),
		links: items,
	}
}

// Selected returns true if the user picked a link.
func (m *LinkPickerModal) Selected() bool { return m.selected }

// Canceled returns true if the user dismissed the modal.
func (m *LinkPickerModal) Canceled() bool { return m.canceled }

// SelectedLink returns the chosen link.
func (m *LinkPickerModal) SelectedLink() Link {
	if m.cursor >= 0 && m.cursor < len(m.links) {
		return m.links[m.cursor]
	}
	return Link{}
}

// PasteHandler is a no-op — the link picker has no text input, but all
// focused widgets must implement PasteHandler per project convention.
func (m *LinkPickerModal) PasteHandler() func(string, func(tview.Primitive)) {
	return m.WrapPasteHandler(func(_ string, _ func(tview.Primitive)) {})
}

// InputHandler handles key events for the link picker.
func (m *LinkPickerModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyEscape, tcell.KeyCtrlQ:
			m.canceled = true
		case tcell.KeyEnter:
			m.selected = true
		case tcell.KeyUp:
			if m.cursor > 0 {
				m.cursor--
			}
		case tcell.KeyDown:
			if m.cursor < len(m.links)-1 {
				m.cursor++
			}
		case tcell.KeyRune:
			switch event.Rune() {
			case 'j':
				if m.cursor < len(m.links)-1 {
					m.cursor++
				}
			case 'k':
				if m.cursor > 0 {
					m.cursor--
				}
			}
		}
	})
}

// Draw renders the link picker as a centered modal.
func (m *LinkPickerModal) Draw(screen tcell.Screen) {
	m.Box.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Compute modal dimensions
	maxLabelW := 0
	for _, l := range m.links {
		w := utf8.RuneCountInString(l.Label)
		if w > maxLabelW {
			maxLabelW = w
		}
	}

	// modal width: label + padding + border
	modalW := max(maxLabelW+6, 30)
	modalW = min(modalW, width-4)
	innerW := modalW - 4

	// Height: border(1) + title padding(1) + items + padding(1) + help(1) + border(1)
	maxVisible := max(min(len(m.links), height-6), 1)
	modalH := maxVisible + 5 // border + gap + items + gap + help + border
	if modalH > height {
		return
	}

	mx := x + (width-modalW)/2
	my := y + (height-modalH)/2

	// Clear modal area
	clearStyle := tcell.StyleDefault.Background(tcell.ColorDefault)
	for row := my; row < my+modalH; row++ {
		for col := mx; col < mx+modalW; col++ {
			screen.SetContent(col, row, ' ', nil, clearStyle)
		}
	}

	widget.DrawBorder(screen, mx, my, modalW, modalH, theme.StyleFocusedBorder)

	// Title
	title := " Open Link "
	titleX := mx + (modalW-utf8.RuneCountInString(title))/2
	titleStyle := tcell.StyleDefault.Foreground(theme.ColorTitle).Bold(true)
	for i, r := range title {
		screen.SetContent(titleX+i, my, r, nil, titleStyle)
	}

	innerX := mx + 2
	row := my + 2

	// Scrolling offset
	offset := 0
	if m.cursor >= maxVisible {
		offset = m.cursor - maxVisible + 1
	}

	for i := 0; i < maxVisible; i++ {
		idx := offset + i
		if idx >= len(m.links) {
			break
		}
		link := m.links[idx]
		isCursor := idx == m.cursor

		label := link.Label
		if utf8.RuneCountInString(label) > innerW {
			// Truncate
			runes := []rune(label)
			if innerW > 3 {
				label = string(runes[:innerW-1]) + "…"
			}
		}

		style := theme.StyleNormal
		if isCursor {
			style = theme.StyleSelected
		}
		widget.DrawText(screen, innerX, row+i, innerW, label, style)
	}

	// Help text
	helpRow := my + modalH - 2
	help := "↑/↓ select  Enter open  Esc cancel"
	widget.DrawText(screen, innerX, helpRow, innerW, help, theme.StyleDimmed)
}
