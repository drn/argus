package widget

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// AgentHeader renders a single-row header in the agent view showing the task name
// using the same powerline style as the root Header.
type AgentHeader struct {
	*tview.Box
	taskName      string
	clipboardHint bool // when true, render a "ctrl+y to copy" affordance
}

// NewAgentHeader creates an agent view header.
func NewAgentHeader() *AgentHeader {
	return &AgentHeader{
		Box: tview.NewBox(),
	}
}

// SetTaskName updates the displayed task name.
func (h *AgentHeader) SetTaskName(name string) {
	h.taskName = name
}

// SetClipboardHint toggles the agent-staged clipboard hint. When true, the
// header renders a small affordance reminding the user that ctrl+y will
// copy the pending payload to the OS clipboard.
func (h *AgentHeader) SetClipboardHint(show bool) {
	h.clipboardHint = show
}

// ClipboardHint returns whether the hint is currently shown. Test-only
// accessor; production code never reads it back.
func (h *AgentHeader) ClipboardHint() bool {
	return h.clipboardHint
}

// Draw renders the header with a powerline-style segment containing the task name.
func (h *AgentHeader) Draw(screen tcell.Screen) {
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

	if h.taskName == "" {
		return
	}

	text := " " + h.taskName + " "

	// Compute segment width: open chevron + text + close chevron
	segWidth := 1 + len(text) + 1
	col := x + (width-segWidth)/2
	if col < x {
		col = x
	}

	// Opening separator: base → segment
	if col < x+width {
		screen.SetContent(col, y, powerlineSep, nil,
			tcell.StyleDefault.Foreground(headerBaseBG).Background(headerActiveBG))
		col++
	}

	// Text
	textStyle := tcell.StyleDefault.Foreground(headerActiveFG).Background(headerActiveBG).Bold(true)
	for _, r := range text {
		if col >= x+width {
			break
		}
		screen.SetContent(col, y, r, nil, textStyle)
		col++
	}

	// Closing separator: segment → base
	if col < x+width {
		screen.SetContent(col, y, powerlineSep, nil,
			tcell.StyleDefault.Foreground(headerActiveBG).Background(headerBaseBG))
	}

	// Right-justified clipboard hint. Kept ASCII-only so each rune occupies
	// exactly one terminal cell — `runeWidth` then equals the visual cell
	// count, which is what the right-justify math needs. An earlier draft
	// used the 📋 emoji, which most terminals render at width 2 while
	// `range s` only yields one code point, leaving the hint placed one
	// cell too far right. Don't reintroduce wide characters here without
	// switching to a runewidth library.
	if h.clipboardHint {
		hint := " ctrl+y to copy "
		hintStart := x + width - runeWidth(hint)
		if hintStart < x {
			return
		}
		hintStyle := tcell.StyleDefault.Foreground(headerActiveFG).Background(headerBaseBG).Bold(true)
		c := hintStart
		for _, r := range hint {
			if c >= x+width {
				break
			}
			screen.SetContent(c, y, r, nil, hintStyle)
			c++
		}
	}
}

// runeWidth counts code points. Safe as a cell-count proxy ONLY when the
// caller passes ASCII-only input (see clipboard hint above). For arbitrary
// Unicode (CJK, emoji, combining marks) this would mis-count.
func runeWidth(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
