package widget

import (
	"fmt"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// PluginHint is one bottom-bar hint a plugin contributes while it holds the
// keyboard. It is a widget-local mirror of the app's HotkeyItem (bar:true
// subset only) — defined here so widget never imports the tui package (that
// would be an import cycle). The app converts mount.hotkeys into these.
type PluginHint struct {
	Key   string
	Label string
}

// StatusBar renders the bottom status bar with task counts and keybinding hints.
type StatusBar struct {
	*tview.Box
	tasks     []*model.Task
	running   map[string]bool
	errMsg    string
	infoMsg   string
	activeTab Tab

	// Plugin-view state. When pluginActive is true, Draw renders the plugin's
	// bar hints plus a reserved, non-displaceable exit hint instead of the
	// tab-hints branch.
	pluginActive bool
	pluginTitle  string
	pluginHints  []PluginHint
}

// maxPluginBarHints caps how many plugin hints the bar will attempt to render
// so a misbehaving plugin can't flood the bar. The reserved exit hint is
// always rendered regardless of this cap.
const maxPluginBarHints = 8

// pluginExitHintKey / pluginExitHintLabel are the reserved "return to argus"
// affordance. Esc is surrendered to the plugin, so the only advertised exit is
// the double-Ctrl+Q failsafe.
const (
	pluginExitHintKey   = "^Q^Q"
	pluginExitHintLabel = "argus"
)

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

// SetPluginMode toggles the plugin-view bottom bar. When active, Draw renders
// the plugin's bar hints (already filtered to bar:true by the app) plus a
// reserved exit hint. When inactive, the normal tab-hints branch renders.
// The app calls this with active=false (and empty title/nil hints) on
// deactivate so nothing bleeds into the next plugin.
func (sb *StatusBar) SetPluginMode(active bool, pluginTitle string, hints []PluginHint) {
	sb.pluginActive = active
	sb.pluginTitle = pluginTitle
	sb.pluginHints = hints
}

// PluginMode reports the current plugin-view bar state. Used by the app's
// smoke tests to assert activate/re-push/deactivate wiring without scraping
// the rendered screen.
func (sb *StatusBar) PluginMode() (active bool, title string, hints []PluginHint) {
	return sb.pluginActive, sb.pluginTitle, sb.pluginHints
}

// SetError sets an error message to display.
func (sb *StatusBar) SetError(msg string) {
	sb.errMsg = msg
}

// ClearError clears the error message.
func (sb *StatusBar) ClearError() {
	sb.errMsg = ""
}

// SetInfo sets an informational (non-error) status message.
func (sb *StatusBar) SetInfo(msg string) {
	sb.infoMsg = msg
}

// ClearInfo clears the informational status message.
func (sb *StatusBar) ClearInfo() {
	sb.infoMsg = ""
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
		screen.SetContent(col, y, ' ', nil, theme.StyleStatusBar)
	}

	// Plugin-view mode renders an entirely different layout: an optional
	// "<plugin> has the keyboard" affordance on the left and the plugin's bar
	// hints + reserved exit hint on the right.
	if sb.pluginActive {
		sb.drawPluginMode(screen, x, y, width)
		return
	}

	// Left side: error, info, or task counts
	var left string
	if sb.errMsg != "" {
		left = " ! " + sb.errMsg
	} else if sb.infoMsg != "" {
		left = " " + sb.infoMsg
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
	leftStyle := theme.StyleStatusBar
	if sb.errMsg != "" {
		leftStyle = tcell.StyleDefault.Background(theme.ColorStatusBG).Foreground(theme.ColorError)
	} else if sb.infoMsg != "" {
		leftStyle = tcell.StyleDefault.Background(theme.ColorStatusBG).Foreground(theme.ColorDimmed)
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
	case TabSettings:
		hints = []hint{
			{"n", "new project"}, {"d", "del"},
			{"1", "tasks"}, {"2", "DAG"}, {"?", "help"}, {"q", "quit"},
		}
	case TabDAG:
		hints = []hint{
			{"l", "link"}, {"L", "unlink"}, {"h", "halt"}, {"RET", "open"},
			{"1", "tasks"}, {"3", "settings"}, {"?", "help"}, {"q", "quit"},
		}
	default:
		hints = []hint{
			{"n", "new"}, {"RET", "attach"}, {"s", "status"}, {"r", "rename"},
			{"^p", "PR"}, {"^f", "fork"}, {"^d", "del"}, {"^r", "prune"}, {"2", "DAG"}, {"3", "settings"},
			{"?", "help"}, {"q", "quit"},
		}
	}

	// Build right text and measure width
	var runs []styledRun
	keyStyle := tcell.StyleDefault.Background(theme.ColorStatusBG).Foreground(theme.ColorKeyHint)
	labelStyle := tcell.StyleDefault.Background(theme.ColorStatusBG).Foreground(theme.ColorKeyLabel)
	for i, h := range hints {
		if i > 0 {
			runs = append(runs, styledRun{"  ", theme.StyleStatusBar})
		}
		runs = append(runs, styledRun{h.key, keyStyle})
		runs = append(runs, styledRun{" " + h.label, labelStyle})
	}
	runs = append(runs, styledRun{" ", theme.StyleStatusBar})

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

// drawPluginMode renders the bottom bar while a plugin holds the keyboard.
//
// The reserved "return to argus" exit hint is rendered LAST / right-most and
// its width is reserved before any plugin hints are laid out, so plugin items
// can never occupy or push it off-screen. Plugin hints fill the space to the
// left of the reserved exit region and are truncated when they don't fit. The
// exit hint is never dropped or truncated.
func (sb *StatusBar) drawPluginMode(screen tcell.Screen, x, y, width int) {
	keyStyle := tcell.StyleDefault.Background(theme.ColorStatusBG).Foreground(theme.ColorKeyHint)
	labelStyle := tcell.StyleDefault.Background(theme.ColorStatusBG).Foreground(theme.ColorKeyLabel)

	// Reserved exit hint runs (rendered right-most). Always present.
	exitRuns := []styledRun{
		{pluginExitHintKey, keyStyle},
		{" " + pluginExitHintLabel + " ", labelStyle},
	}
	exitWidth := runsWidth(exitRuns)

	if len(sb.pluginHints) == 0 {
		// Fallback affordance: "▶ <plugin> has the keyboard".
		left := " ▶ " + sb.pluginTitle + " has the keyboard"
		leftStyle := tcell.StyleDefault.Background(theme.ColorStatusBG).Foreground(theme.ColorDimmed)
		col := x
		for _, r := range left {
			if col >= x+width-exitWidth {
				break
			}
			screen.SetContent(col, y, r, nil, leftStyle)
			col++
		}
	} else {
		// Build plugin hint runs, capped at maxPluginBarHints.
		hints := sb.pluginHints
		if len(hints) > maxPluginBarHints {
			hints = hints[:maxPluginBarHints]
		}
		var runs []styledRun
		for i, h := range hints {
			if i > 0 {
				runs = append(runs, styledRun{"  ", theme.StyleStatusBar})
			}
			runs = append(runs, styledRun{h.Key, keyStyle})
			runs = append(runs, styledRun{" " + h.Label, labelStyle})
		}
		runs = append(runs, styledRun{" ", theme.StyleStatusBar})

		// Plugin hints render left-aligned but must not intrude into the
		// reserved exit region: cap the drawable column at x+width-exitWidth.
		limit := x + width - exitWidth
		if limit < x {
			limit = x
		}
		col := x + 1 // small left margin
		for _, run := range runs {
			for _, r := range run.text {
				if col >= limit {
					break
				}
				screen.SetContent(col, y, r, nil, run.style)
				col++
			}
			if col >= limit {
				break
			}
		}
	}

	// Render the reserved exit hint flush to the right edge, unconditionally.
	rc := x + width - exitWidth
	if rc < x {
		rc = x
	}
	for _, run := range exitRuns {
		for _, r := range run.text {
			if rc >= x+width {
				break
			}
			screen.SetContent(rc, y, r, nil, run.style)
			rc++
		}
	}
}

// styledRun is a contiguous run of text sharing a single style.
type styledRun struct {
	text  string
	style tcell.Style
}

// runsWidth returns the total rune width of a slice of styled runs.
func runsWidth(runs []styledRun) int {
	w := 0
	for _, r := range runs {
		w += len([]rune(r.text))
	}
	return w
}
