package widget

import (
	"strconv"

	"github.com/drn/argus/internal/tui/theme"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// attentionSummaryHeight is the fixed drawn height of the summary box when it
// has a non-zero count: one text line plus the two border rows.
const attentionSummaryHeight = 3

// AttentionSummary is a fixed one-line bordered box that reports how many tasks
// elsewhere are blocked on a user prompt, e.g. "2 tasks need input". Unlike
// AttentionBar (which lists task names and grows with the count), it reports a
// bare count — used in the Hera view, where the listed tasks are not actionable
// from this tab so naming them would be misleading. When the count is zero the
// box reports zero desired height so its parent can collapse it.
type AttentionSummary struct {
	*tview.Box
	count int
}

// NewAttentionSummary creates an empty (zero-count, hidden) summary box.
func NewAttentionSummary() *AttentionSummary {
	return &AttentionSummary{Box: tview.NewBox()}
}

// SetCount records the number of tasks needing input. Negative values are
// clamped to zero (treated as "nothing to show").
func (s *AttentionSummary) SetCount(n int) {
	if n < 0 {
		n = 0
	}
	s.count = n
}

// Count returns the current count. Test-only accessor.
func (s *AttentionSummary) Count() int { return s.count }

// DesiredHeight returns attentionSummaryHeight when there is something to show
// and 0 otherwise (so the parent flex/layout can collapse it).
func (s *AttentionSummary) DesiredHeight() int {
	if s.count <= 0 {
		return 0
	}
	return attentionSummaryHeight
}

// Draw renders the bordered count line. The border + text use the in-review
// (blue) style to match the agent-view attention bar, so the operator reads it
// as the same "needs attention" family.
func (s *AttentionSummary) Draw(screen tcell.Screen) {
	s.DrawForSubclass(screen, s)
	x, y, width, height := s.GetInnerRect()
	if width <= 0 || height <= 0 || s.count <= 0 {
		return
	}

	inner := DrawBorderedPanel(screen, x, y, width, height, " Needs input ", theme.StyleInReview)
	if inner.W <= 1 || inner.H <= 0 {
		return
	}
	// One cell of left padding so the text reads "│ 2 tasks need input", not
	// flush against the border "│2 tasks need input" (BUG-004).
	DrawText(screen, inner.X+1, inner.Y, inner.W-1, attentionSummaryText(s.count), theme.StyleInReview)
}

// attentionSummaryText builds the pluralised count line.
func attentionSummaryText(n int) string {
	if n == 1 {
		return "1 task needs input"
	}
	return strconv.Itoa(n) + " tasks need input"
}
