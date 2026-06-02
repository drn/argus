package widget

import (
	"strconv"

	"github.com/drn/argus/internal/tui/theme"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// AttentionMaxRows caps how tall the bar will grow when many tasks are
// blocked on a user prompt. Past this, additional entries are summarised on
// the last line as "+N more".
const AttentionMaxRows = 5

// AttentionEntry is a single row in the attention bar.
type AttentionEntry struct {
	TaskName string
}

// AttentionBar is a bordered box that lists tasks blocked on a user prompt.
// It sits above the agent view's git status panel and grows vertically with
// the number of needs-input tasks. When the entry list is empty the bar
// reports zero desired height so its parent flex item can be collapsed.
type AttentionBar struct {
	*tview.Box
	entries []AttentionEntry

	// OnHeightChange fires when the bar's DesiredHeight() return value
	// changes. App wires this to resize the parent flex item so the bar
	// actually appears/disappears as the set of attention entries changes.
	OnHeightChange func()

	lastHeight int
}

// NewAttentionBar creates an empty attention bar.
func NewAttentionBar() *AttentionBar {
	return &AttentionBar{Box: tview.NewBox()}
}

// SetEntries replaces the bar's content. Fires OnHeightChange when the
// computed DesiredHeight differs from the previous one.
func (b *AttentionBar) SetEntries(entries []AttentionEntry) {
	b.entries = entries
	h := b.DesiredHeight()
	if h != b.lastHeight {
		b.lastHeight = h
		if b.OnHeightChange != nil {
			b.OnHeightChange()
		}
	}
}

// Entries returns the current entries. Test-only accessor.
func (b *AttentionBar) Entries() []AttentionEntry {
	return b.entries
}

// DesiredHeight returns the row count the bar wants to occupy:
//   - 0 when there are no entries (so the parent flex can collapse it)
//   - rows(entries, capped at AttentionMaxRows) + 2 for the border
func (b *AttentionBar) DesiredHeight() int {
	n := len(b.entries)
	if n == 0 {
		return 0
	}
	if n > AttentionMaxRows {
		n = AttentionMaxRows
	}
	return n + 2
}

// Draw renders the bordered list of attention entries. The icon and text
// use the in-review (blue) style to match the same status badge used in
// the task list, so users recognise it as the same "needs attention" cue.
func (b *AttentionBar) Draw(screen tcell.Screen) {
	b.Box.DrawForSubclass(screen, b)
	x, y, width, height := b.GetInnerRect()
	if width <= 0 || height <= 0 || len(b.entries) == 0 {
		return
	}

	inner := DrawBorderedPanel(screen, x, y, width, height, " Needs input ", theme.StyleInReview)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}

	nameStyle := theme.StyleInReview
	rows := len(b.entries)
	overflow := 0
	if rows > AttentionMaxRows {
		overflow = rows - (AttentionMaxRows - 1)
		rows = AttentionMaxRows - 1
	}
	row := inner.Y
	for i := 0; i < rows && row < inner.Y+inner.H; i++ {
		// Render the '?' icon in the needs-input (orange) style so it
		// matches the task-list badge, and the name in the in-review
		// (blue) panel style so the row reads as part of the panel.
		screen.SetContent(inner.X, row, theme.IconNeedsInput, nil, theme.StyleNeedsInput)
		if inner.W > 2 {
			DrawText(screen, inner.X+2, row, inner.W-2, truncateAttention(b.entries[i].TaskName, inner.W-2), nameStyle)
		}
		row++
	}
	if overflow > 0 && row < inner.Y+inner.H {
		line := "+ " + strconv.Itoa(overflow) + " more"
		DrawText(screen, inner.X, row, inner.W, truncateAttention(line, inner.W), theme.StyleDimmed)
	}
}

func truncateAttention(s string, maxW int) string {
	runes := []rune(s)
	if len(runes) <= maxW {
		return s
	}
	if maxW <= 1 {
		return string(runes[:maxW])
	}
	return string(runes[:maxW-1]) + "…"
}
