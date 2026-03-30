package tui2

import (
	"fmt"
	"time"

	"github.com/drn/argus/internal/spinner"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Tab identifies the active top-level tab.
type Tab int

const (
	TabTasks Tab = iota
	TabToDos
	TabReviews
	TabSettings
)

var tabLabels = [...]string{"Tasks", "To Dos", "Reviews", "Settings"}
var tabKeys = [...]string{"1", "2", "3", "4"}

// Powerline separator (right-facing filled chevron).
const powerlineSep = '\ue0b0'

// Colors matching the tmux status bar palette.
var (
	headerBaseBG     = tcell.Color236 // dark bar background (C3)
	headerActiveBG   = tcell.Color103 // lavender — active segment (C1)
	headerActiveFG   = tcell.Color236 // dark text on active
	headerInactiveBG = tcell.Color239 // medium gray — inactive segment (C2)
	headerInactiveFG = tcell.Color253 // light text on inactive
)

// Header renders the top tab bar with tmux-style powerline separators.
// It also supports a general-purpose notice area on the left side
// (spinner + short text) for async background operations.
type Header struct {
	*tview.Box
	activeTab Tab

	// Notice: general-purpose status indicator (left side of header).
	noticeText string // empty = no notice
}

// NewHeader creates a tab bar header.
func NewHeader() *Header {
	h := &Header{
		Box:       tview.NewBox(),
		activeTab: TabTasks,
	}
	return h
}

// SetTab changes the active tab.
func (h *Header) SetTab(t Tab) {
	h.activeTab = t
}

// ActiveTab returns the current tab.
func (h *Header) ActiveTab() Tab {
	return h.activeTab
}

// SetNotice sets the notice text displayed with a spinner on the left.
func (h *Header) SetNotice(text string) {
	h.noticeText = text
}

// ClearNotice removes the notice.
func (h *Header) ClearNotice() {
	h.noticeText = ""
}

// Notice returns the current notice text (empty if none).
func (h *Header) Notice() string {
	return h.noticeText
}

// Draw renders the tab bar with powerline-style segments, centered.
// If a notice is active, a spinner + text is drawn on the left.
func (h *Header) Draw(screen tcell.Screen) {
	h.Box.DrawForSubclass(screen, h)
	x, y, width, _ := h.GetInnerRect()
	if width <= 0 {
		return
	}

	// Fill entire row with base background
	baseStyle := tcell.StyleDefault.Background(headerBaseBG)
	for i := 0; i < width; i++ {
		screen.SetContent(x+i, y, ' ', nil, baseStyle)
	}

	// Draw notice on the left (spinner + text) if active.
	noticeEnd := x
	if h.noticeText != "" {
		sp := spinner.Get(spinner.StyleProgress)
		frame := int(time.Now().UnixMilli()/sp.TickInterval.Milliseconds()) % sp.FrameCount()
		spinnerRune := sp.Frame(frame)

		noticeStyle := tcell.StyleDefault.Background(headerBaseBG).Foreground(ColorInProgress)
		textStyle := tcell.StyleDefault.Background(headerBaseBG).Foreground(ColorNormal)

		col := x + 1 // 1 cell left padding
		screen.SetContent(col, y, spinnerRune, nil, noticeStyle)
		col++
		col++ // space after spinner
		for _, r := range h.noticeText {
			if col >= x+width {
				break
			}
			screen.SetContent(col, y, r, nil, textStyle)
			col++
		}
		noticeEnd = col + 1 // 1 cell padding after notice
	}

	// Compute total width of all tab segments to center them.
	// Each segment = 1 (open chevron) + len(text) + 1 (close chevron).
	totalWidth := 0
	for _, label := range tabLabels {
		text := fmt.Sprintf(" %s ", label)
		totalWidth += 1 + len(text) + 1 // open sep + text + close sep
	}

	col := x + (width-totalWidth)/2
	if col < noticeEnd {
		col = noticeEnd
	}

	// Draw tabs
	for i, label := range tabLabels {
		if col >= x+width {
			break
		}
		text := fmt.Sprintf(" %s ", label)
		if Tab(i) == h.activeTab {
			col = h.drawSegment(screen, col, y, x+width, text, headerActiveBG, headerActiveFG, true)
		} else {
			col = h.drawSegment(screen, col, y, x+width, text, headerInactiveBG, headerInactiveFG, false)
		}
	}
}

// drawSegment renders a powerline-style segment: opening chevron, text, closing chevron.
// Returns the column position after the segment.
func (h *Header) drawSegment(screen tcell.Screen, col, y, maxCol int, text string, bg, fg tcell.Color, bold bool) int {
	if col >= maxCol {
		return col
	}

	// Opening separator: transition from base → segment
	sepStyle := tcell.StyleDefault.Foreground(headerBaseBG).Background(bg)
	screen.SetContent(col, y, powerlineSep, nil, sepStyle)
	col++

	// Text
	textStyle := tcell.StyleDefault.Foreground(fg).Background(bg)
	if bold {
		textStyle = textStyle.Bold(true)
	}
	for _, r := range text {
		if col >= maxCol {
			return col
		}
		screen.SetContent(col, y, r, nil, textStyle)
		col++
	}

	// Closing separator: transition from segment → base
	if col < maxCol {
		sepStyle = tcell.StyleDefault.Foreground(bg).Background(headerBaseBG)
		screen.SetContent(col, y, powerlineSep, nil, sepStyle)
		col++
	}

	return col
}
