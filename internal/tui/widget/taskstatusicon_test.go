package widget

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/gdamore/tcell/v2"
)

// TestTaskStatusIcon pins the shared classifier that drives both the task list's
// indicator column (drawTaskRow) and the task switcher modal, so the two can
// never drift. The in_progress precedence mirrors drawTaskRow: needs-input →
// idle-unvisited → idle/absent → actively-running spinner.
func TestTaskStatusIcon(t *testing.T) {
	const frame = 0
	cases := []struct {
		name      string
		status    model.Status
		in        TaskStatusInputs
		wantGlyph rune
		wantStyle tcell.Style
	}{
		{"pending", model.StatusPending, TaskStatusInputs{}, '○', theme.StylePending},
		{"in_review", model.StatusInReview, TaskStatusInputs{}, theme.IconReview, theme.StyleInReview},
		{"complete", model.StatusComplete, TaskStatusInputs{}, '✓', theme.StyleComplete},
		{"in_progress needs-input wins", model.StatusInProgress, TaskStatusInputs{NeedsInput: true, IdleUnvisited: true, Running: true}, theme.IconNeedsInput, theme.StyleNeedsInput},
		{"in_progress idle-unvisited", model.StatusInProgress, TaskStatusInputs{IdleUnvisited: true, Running: true}, theme.IconMoonStars, theme.StyleInReview},
		{"in_progress idle", model.StatusInProgress, TaskStatusInputs{Running: true, Idle: true}, theme.IconMoonOutline, theme.StyleInReview},
		{"in_progress not running", model.StatusInProgress, TaskStatusInputs{}, theme.IconMoonOutline, theme.StyleInReview},
		{"in_progress active → spinner", model.StatusInProgress, TaskStatusInputs{Running: true}, SpinnerFrame(frame), theme.StyleInProgress},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			glyph, style := TaskStatusIcon(tc.status, tc.in, frame)
			testutil.Equal(t, glyph, tc.wantGlyph)
			testutil.Equal(t, style, tc.wantStyle)
		})
	}
}

// TestCurrentSpinnerFrame stays within the active spinner's frame range so the
// switcher never indexes out of bounds.
func TestCurrentSpinnerFrame(t *testing.T) {
	f := CurrentSpinnerFrame()
	if f < 0 || f >= SpinnerFrameCount() {
		t.Errorf("CurrentSpinnerFrame() = %d, out of range [0, %d)", f, SpinnerFrameCount())
	}
}
