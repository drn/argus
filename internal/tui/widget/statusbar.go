package widget

import (
	"fmt"
	"time"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// StatusNoticeTTL is how long a transient notice set via SetError / SetInfo
// stays visible before the status bar auto-reverts to its default task counts
// (BUG-030). The revert is realized lazily in Draw — the app's unconditional
// ~1s onTick QueueUpdateDraw repaints the bar within a tick of expiry, so no
// notice can linger forever on a static screen. Never force the repaint with
// screen.Sync() (see gotchas/ui-threading.md).
const StatusNoticeTTL = 15 * time.Second

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

	// Transient-notice expiry (BUG-030). errMsg/infoMsg auto-revert to the
	// default task counts once now() passes the matching expiresAt. Zero value
	// means "no pending expiry". now is injectable so tests exercise expiry
	// without a real 15s sleep; it defaults to time.Now.
	errExpiresAt  time.Time
	infoExpiresAt time.Time
	now           func() time.Time

	// heraFocus tracks which Hera region has focus (0=rail, 1=coord, 2=agent),
	// matching hera.Focus iota order. Used to pick focus-aware hint sets while
	// TabHera is active. Zero value (FocusRail) is the correct default.
	heraFocus int

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
const maxPluginBarHints = 20

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
		now:     time.Now,
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

// SetHeraFocus updates which Hera region holds focus, switching the hint set
// shown while TabHera is active. f matches hera.Focus iota: 0=rail, 1=coord,
// 2=agent. The caller converts before passing so widget never imports hera.
func (sb *StatusBar) SetHeraFocus(f int) {
	sb.heraFocus = f
}

// HeraFocus returns the current Hera focus state (0=rail, 1=coord, 2=agent).
// Exposed for tests that assert wiring without scraping the rendered row.
func (sb *StatusBar) HeraFocus() int {
	return sb.heraFocus
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

// clock returns the widget's time source, falling back to time.Now if the
// StatusBar was built without NewStatusBar (e.g. a zero-value literal).
func (sb *StatusBar) clock() time.Time {
	if sb.now == nil {
		return time.Now()
	}
	return sb.now()
}

// expireNotices clears any transient notice whose TTL has elapsed (BUG-030).
// Called on every read path (Draw, Error, Info) so a stale notice is dropped
// the moment a repaint or accessor observes it past expiry. Runs only on the
// tview main goroutine (like all StatusBar mutation), so no lock is needed.
func (sb *StatusBar) expireNotices() {
	now := sb.clock()
	if sb.errMsg != "" && !sb.errExpiresAt.IsZero() && !now.Before(sb.errExpiresAt) {
		sb.errMsg = ""
		sb.errExpiresAt = time.Time{}
	}
	if sb.infoMsg != "" && !sb.infoExpiresAt.IsZero() && !now.Before(sb.infoExpiresAt) {
		sb.infoMsg = ""
		sb.infoExpiresAt = time.Time{}
	}
}

// SetError sets an error message to display. The notice shows instantly and
// auto-reverts to the default task counts after StatusNoticeTTL (BUG-030);
// each call resets that window so a fresh notice always gets its full TTL.
func (sb *StatusBar) SetError(msg string) {
	sb.errMsg = msg
	sb.errExpiresAt = sb.clock().Add(StatusNoticeTTL)
}

// ClearError clears the error message.
func (sb *StatusBar) ClearError() {
	sb.errMsg = ""
	sb.errExpiresAt = time.Time{}
}

// Error returns the currently-displayed error message ("" if none). Exposed
// for tests that assert error-path wiring without scraping the rendered row.
func (sb *StatusBar) Error() string {
	sb.expireNotices()
	return sb.errMsg
}

// SetInfo sets an informational (non-error) status message. Like SetError it
// shows instantly and auto-expires after StatusNoticeTTL (BUG-030).
func (sb *StatusBar) SetInfo(msg string) {
	sb.infoMsg = msg
	sb.infoExpiresAt = sb.clock().Add(StatusNoticeTTL)
}

// ClearInfo clears the informational status message.
func (sb *StatusBar) ClearInfo() {
	sb.infoMsg = ""
	sb.infoExpiresAt = time.Time{}
}

// Info returns the currently-displayed informational message ("" if none).
// Exposed for tests that assert info-path wiring without scraping the row,
// mirroring Error().
func (sb *StatusBar) Info() string {
	sb.expireNotices()
	return sb.infoMsg
}

// Draw renders the status bar.
func (sb *StatusBar) Draw(screen tcell.Screen) {
	// Drop any transient notice whose TTL has elapsed (BUG-030) before the
	// left-side branch reads errMsg/infoMsg, so an expired notice repaints as
	// the default counts. The app's ~1s onTick QueueUpdateDraw guarantees this
	// Draw runs within a tick of expiry even on an otherwise-static screen.
	sb.expireNotices()

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
			{"1", "tasks"}, {"2", "projects"}, {"?", "help"}, {"q", "quit"},
		}
	case TabHera:
		// Focus-aware: rail focus shows mutation keys; pane focus shows pane keys.
		// heraFocus == 0 → rail (default); 1 → coord pane; 2 → agent pane.
		// Key names match modal/help.go "Projects View (rail)" exactly.
		if sb.heraFocus == 0 {
			hints = []hint{
				{"j/k", "nav"}, {"SP", "fold"}, {"←", "parent"}, {"/", "filter"},
				{"Tab", "pane"}, {"w", "spawn"}, {"n", "coord"},
				{"s/S", "status"}, {"R", "retire"}, {"C", "prune"},
				{"^r", "prune-all"}, {"^d", "del"},
				{"?", "help"}, {"q", "quit"},
			}
		} else {
			// Coord or agent pane focused. q/1/2/3 AND Tab/Shift-Tab go to the
			// PTY (so the agent's autocomplete works — BUG-019), so omit them.
			// Pane↔pane movement is the Ctrl+Alt+←/→ ladder; ^Q escapes to rail.
			hints = []hint{
				{"^Q", "rail"}, {"^⌥←→", "pane"}, {"^Z", "fullscreen"},
				{"Cmd+↑↓", "rail nav"}, {"Sh+↑↓", "scroll"},
				{"?", "help"},
			}
		}
	default:
		hints = []hint{
			{"n", "new"}, {"RET", "attach"}, {"s", "status"}, {"r", "rename"},
			{"^p", "PR"}, {"^f", "fork"}, {"^d", "del"}, {"^r", "prune"}, {"H", "hera-workers"}, {"2", "projects"}, {"3", "settings"},
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
