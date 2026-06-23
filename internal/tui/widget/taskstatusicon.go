package widget

import (
	"time"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/gdamore/tcell/v2"
)

// TaskStatusInputs carries the in_progress sub-state signals that pick a task's
// status glyph. They mirror the per-task maps drawTaskRow consults so the task
// list and the task switcher render an identical indicator column.
type TaskStatusInputs struct {
	NeedsInput    bool // agent rendered a blocking prompt — highest precedence
	IdleUnvisited bool // idle and not viewed since going idle (moon with stars)
	Running       bool // a live session is attached
	Idle          bool // session present but quiet
}

// TaskStatusIcon resolves a task's status glyph + style — the SINGLE source of
// truth for the indicator column shared by the task list (drawTaskRow) and the
// task switcher modal, so the two surfaces can never drift. `frame` is the
// current spinner animation frame (the actively-running in_progress case
// animates via SpinnerFrame). The in_progress precedence matches drawTaskRow:
// needs-input → idle-unvisited → idle/absent → actively-running spinner.
func TaskStatusIcon(status model.Status, in TaskStatusInputs, frame int) (rune, tcell.Style) {
	switch status {
	case model.StatusPending:
		return '○', theme.StylePending
	case model.StatusInProgress:
		switch {
		case in.NeedsInput:
			return theme.IconNeedsInput, theme.StyleNeedsInput
		case in.IdleUnvisited:
			return theme.IconMoonStars, theme.StyleInReview
		case !in.Running || in.Idle:
			return theme.IconMoonOutline, theme.StyleInReview
		default:
			return SpinnerFrame(frame), theme.StyleInProgress
		}
	case model.StatusInReview:
		return theme.IconReview, theme.StyleInReview
	case model.StatusComplete:
		return '✓', theme.StyleComplete
	default:
		return '○', theme.StylePending
	}
}

// CurrentSpinnerFrame returns the spinner frame for the current wall-clock time,
// matching the task list's time-based animation (updateSpinnerFrame) so spinners
// stay in phase across surfaces that lack their own animation tick.
func CurrentSpinnerFrame() int {
	interval := SpinnerTickInterval()
	if interval <= 0 {
		return 0
	}
	return int(time.Now().UnixMilli()/interval.Milliseconds()) % SpinnerFrameCount()
}
