package tui2

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/drn/argus/internal/model"
)

// StatusBar renders the bottom status bar with task counts and keybinding hints.
type StatusBar struct {
	*tview.Box
	tasks      []*model.Task
	running    map[string]bool
	errMsg     string
	activeTab  Tab
}

// NewStatusBar creates a status bar.
func NewStatusBar() *StatusBar {
	sb := &StatusBar{
		Box:     tview.NewBox(),
		running: make(map[string]bool),
	}
	return sb
}

// SetTasks updates the task list for stat counting.
func (sb *StatusBar) SetTasks(tasks []*model.Task) {
	sb.tasks = tasks
}

// SetRunning updates the set of running task IDs.
func (sb *StatusBar) SetRunning(ids []string) {
	sb.running = make(map[string]bool, len(ids))
	for _, id := range ids {
		sb.running[id] = true
	}
}

// SetTab updates which tab is active (changes hint display).
func (sb *StatusBar) SetTab(t Tab) {
	sb.activeTab = t
}

// SetError sets an error message to display.
func (sb *StatusBar) SetError(msg string) {
	sb.errMsg = msg
}

// ClearError clears the error message.
func (sb *StatusBar) ClearError() {
	sb.errMsg = ""
}

// Draw renders the status bar.
func (sb *StatusBar) Draw(screen tcell.Screen) {
	sb.Box.DrawForSubclass(screen, sb)
	x, y, width, _ := sb.GetInnerRect()
	if width <= 0 {
		return
	}

	// Fill background
	for col := x; col < x+width; col++ {
		screen.SetContent(col, y, ' ', nil, StyleStatusBar)
	}

	// Left side: error or task counts
	var left string
	if sb.errMsg != "" {
		left = " ! " + sb.errMsg
	} else {
		active, pending, complete := 0, 0, 0
		for _, t := range sb.tasks {
			switch t.Status {
			case model.StatusInProgress:
				if sb.running[t.ID] {
					active++
				}
			case model.StatusPending:
				pending++
			case model.StatusComplete:
				complete++
			}
		}
		left = fmt.Sprintf(" %d active  %d pending  %d done", active, pending, complete)
	}

	// Draw left text
	leftStyle := StyleStatusBar
	if sb.errMsg != "" {
		leftStyle = tcell.StyleDefault.Background(ColorStatusBG).Foreground(ColorError)
	}
	col := x
	for _, r := range left {
		if col >= x+width {
			break
		}
		screen.SetContent(col, y, r, nil, leftStyle)
		col++
	}

	// Right side: keybinding hints
	type hint struct{ key, label string }
	var hints []hint
	switch sb.activeTab {
	case TabToDos:
		hints = []hint{
			{"↑↓", "navigate"}, {"RET", "launch"}, {"R", "refresh"},
			{"1", "tasks"}, {"3", "reviews"}, {"4", "settings"},
			{"q", "quit"},
		}
	case TabReviews:
		hints = []hint{
			{"↑↓", "navigate"}, {"RET", "select"}, {"c", "comment"},
			{"a", "approve"}, {"r", "req changes"}, {"R", "refresh"},
			{"1", "tasks"}, {"4", "settings"}, {"q", "quit"},
		}
	case TabSettings:
		hints = []hint{
			{"n", "new project"}, {"d", "del"},
			{"1", "tasks"}, {"?", "help"}, {"q", "quit"},
		}
	default:
		hints = []hint{
			{"n", "new"}, {"RET", "attach"}, {"s", "status"},
			{"^f", "fork"}, {"^d", "del"}, {"^r", "prune"}, {"2", "todos"}, {"3", "reviews"}, {"4", "settings"},
			{"?", "help"}, {"q", "quit"},
		}
	}

	// Build right text and measure width
	type styledRun struct {
		text  string
		style tcell.Style
	}
	var runs []styledRun
	keyStyle := tcell.StyleDefault.Background(ColorStatusBG).Foreground(ColorKeyHint)
	labelStyle := tcell.StyleDefault.Background(ColorStatusBG).Foreground(ColorKeyLabel)
	for i, h := range hints {
		if i > 0 {
			runs = append(runs, styledRun{"  ", StyleStatusBar})
		}
		runs = append(runs, styledRun{h.key, keyStyle})
		runs = append(runs, styledRun{" " + h.label, labelStyle})
	}
	runs = append(runs, styledRun{" ", StyleStatusBar})

	rightWidth := 0
	for _, r := range runs {
		rightWidth += len([]rune(r.text))
	}

	// Draw right-aligned
	rightStart := x + width - rightWidth
	if rightStart < col {
		rightStart = col
	}
	rc := rightStart
	for _, run := range runs {
		for _, r := range run.text {
			if rc >= x+width {
				break
			}
			screen.SetContent(rc, y, r, nil, run.style)
			rc++
		}
	}
}
