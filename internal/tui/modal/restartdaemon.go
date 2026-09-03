package modal

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
)

// skewChoice identifies which action a skew modal button triggers.
type skewChoice int

const (
	choiceRestartDaemon skewChoice = iota
	choiceRestartSupervisor
	choiceSkip
)

// skewButton is one selectable action rendered on the modal's button row.
type skewButton struct {
	label  string
	choice skewChoice
}

// RestartDaemonModal prompts the user to restart an out-of-date process when
// its binary differs from the TUI's. It is skew-aware: the daemon and/or the
// session-supervisor may be stale, and the modal renders the rich identity
// (commit SHA + dirty + path, short-hash fallback) of whichever is stale and
// offers the relevant restart action(s) plus Skip. Tab/→/l move the selection
// forward, Backtab/←/h backward; Enter activates the focused button; Esc skips.
// r/R chooses "Restart daemon" (when present) and s/S chooses Skip — the
// legacy daemon-only shortcuts, unchanged.
//
// The supervisor restart is the destructive one (it SIGHUPs every agent), so it
// carries NO letter shortcut and, in the app, opens a second confirmation that
// names the agent count; this modal only records the CHOICE.
//
// State is normally mutated only from InputHandler, but when both processes
// are stale the app can partially resolve one action via ResolveRestartDaemon/
// ResolveRestartSupervisor — this removes the handled button and re-arms the
// modal (done/chose clear) so it stays open for the remaining action instead
// of the app tearing it down.
type RestartDaemonModal struct {
	*tview.Box
	daemonStale        bool
	supervisorStale    bool
	daemonIdentity     string // rich identity of the daemon binary (display-only)
	supervisorIdentity string // rich identity of the supervisor binary (display-only)
	// supervisorConsequence is a short clause naming what the supervisor's skew
	// actually COSTS — "live sessions are affected" vs "spawn config only —
	// running agents are unaffected". Without it a modal opened because the
	// DAEMON is stale would offer a supervisor restart, which SIGHUPs every
	// agent, with nothing telling the operator the supervisor's own mismatch may
	// only affect sessions started from now on. Empty renders nothing.
	supervisorConsequence string

	buttons  []skewButton
	selected int        // index into buttons
	chose    skewChoice // set once a choice is made
	done     bool
}

// NewSkewModal builds the skew modal from the daemon/supervisor staleness flags
// and each stale process's display identity. Buttons are built dynamically: a
// "Restart daemon" button when the daemon is stale, a "Restart supervisor"
// button when the supervisor is stale, and always a "Skip" button. The first
// restart action is selected by default. When neither is stale the modal is
// Skip-only (a benign no-op prompt).
func NewSkewModal(daemonStale, supervisorStale bool, daemonIdentity, supervisorIdentity string) *RestartDaemonModal {
	m := &RestartDaemonModal{
		Box:                tview.NewBox(),
		daemonStale:        daemonStale,
		supervisorStale:    supervisorStale,
		daemonIdentity:     daemonIdentity,
		supervisorIdentity: supervisorIdentity,
	}
	if daemonStale {
		m.buttons = append(m.buttons, skewButton{"[ Restart daemon ]", choiceRestartDaemon})
	}
	if supervisorStale {
		m.buttons = append(m.buttons, skewButton{"[ Restart supervisor ]", choiceRestartSupervisor})
	}
	m.buttons = append(m.buttons, skewButton{"[ Skip ]", choiceSkip})
	return m
}

// ChoseRestartDaemon reports whether the user picked "Restart daemon".
func (m *RestartDaemonModal) ChoseRestartDaemon() bool {
	return m.done && m.chose == choiceRestartDaemon
}

// ChoseRestartSupervisor reports whether the user picked "Restart supervisor".
func (m *RestartDaemonModal) ChoseRestartSupervisor() bool {
	return m.done && m.chose == choiceRestartSupervisor
}

// ChoseSkip reports whether the user picked Skip (or pressed Esc).
func (m *RestartDaemonModal) ChoseSkip() bool { return m.done && m.chose == choiceSkip }

// Done reports whether the user has made a choice.
func (m *RestartDaemonModal) Done() bool { return m.done }

// Selected returns the currently focused button index (for tests).
func (m *RestartDaemonModal) Selected() int { return m.selected }

// choose records a choice by button kind if such a button exists.
func (m *RestartDaemonModal) choose(c skewChoice) {
	for _, b := range m.buttons {
		if b.choice == c {
			m.chose = c
			m.done = true
			return
		}
	}
}

// removeButton drops the button for choice c (if present) and re-arms the
// modal for another round of input: selection resets to the first remaining
// button and done/chose clear so InputHandler starts fresh.
func (m *RestartDaemonModal) removeButton(c skewChoice) {
	kept := m.buttons[:0]
	for _, b := range m.buttons {
		if b.choice != c {
			kept = append(kept, b)
		}
	}
	m.buttons = kept
	m.selected = 0
	m.done = false
	m.chose = choiceSkip // clear alongside done so a stale choice can't be read before the next input
}

// hasRestartAction reports whether any non-Skip button remains.
func (m *RestartDaemonModal) hasRestartAction() bool {
	for _, b := range m.buttons {
		if b.choice != choiceSkip {
			return true
		}
	}
	return false
}

// ResolveRestartDaemon marks "Restart daemon" as handled and removes its
// button. Reports whether a restart action (the supervisor's) still remains,
// so the caller can leave the modal open instead of dismissing it.
func (m *RestartDaemonModal) ResolveRestartDaemon() bool {
	m.removeButton(choiceRestartDaemon)
	m.daemonStale = false
	return m.hasRestartAction()
}

// ResolveRestartSupervisor marks "Restart supervisor" as handled. Restarting
// the supervisor also bounces the daemon (see App.handleRestartSupervisorKey),
// so this resolves the daemon's pending restart too and removes both buttons
// — unlike ResolveRestartDaemon, no restart action can ever remain, so unlike
// that method this reports nothing.
func (m *RestartDaemonModal) ResolveRestartSupervisor() {
	m.removeButton(choiceRestartDaemon)
	m.removeButton(choiceRestartSupervisor)
	m.daemonStale = false
	m.supervisorStale = false
}

// InputHandler handles key events for the skew modal.
func (m *RestartDaemonModal) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		n := len(m.buttons)
		if n == 0 {
			return
		}
		switch event.Key() {
		case tcell.KeyTab, tcell.KeyRight:
			m.selected = (m.selected + 1) % n
		case tcell.KeyBacktab, tcell.KeyLeft:
			m.selected = (m.selected - 1 + n) % n
		case tcell.KeyEnter:
			m.chose = m.buttons[m.selected].choice
			m.done = true
		case tcell.KeyEscape:
			m.choose(choiceSkip)
		case tcell.KeyRune:
			switch event.Rune() {
			case 'r', 'R':
				m.choose(choiceRestartDaemon)
			case 's', 'S':
				m.choose(choiceSkip)
			case 'h':
				m.selected = (m.selected - 1 + n) % n
			case 'l':
				m.selected = (m.selected + 1) % n
			}
		}
	})
}

// bodyLines returns the modal's body: an explanatory sentence, a blank line,
// and one identity line per stale process.
func (m *RestartDaemonModal) bodyLines() []string {
	lines := []string{
		"A newer argus binary is installed than one or more",
		"running processes. Restart to load the new code.",
		"",
	}
	if m.daemonStale {
		id := m.daemonIdentity
		if id == "" {
			id = "(identity unknown)"
		}
		lines = append(lines, "daemon: "+id)
	}
	if m.supervisorStale {
		id := m.supervisorIdentity
		if id == "" {
			id = "(identity unknown)"
		}
		lines = append(lines, "supervisor: "+id)
		if m.supervisorConsequence != "" {
			lines = append(lines, "  ("+m.supervisorConsequence+")")
		}
	}
	return lines
}

// SetSupervisorConsequence records the short clause describing what the
// supervisor's skew costs. Call before the modal is first drawn.
func (m *RestartDaemonModal) SetSupervisorConsequence(s string) {
	m.supervisorConsequence = s
}

// titleText picks the modal title from which process(es) are stale.
func (m *RestartDaemonModal) titleText() string {
	switch {
	case m.daemonStale && m.supervisorStale:
		return "Binaries out of date"
	case m.supervisorStale:
		return "Supervisor out of date"
	default:
		return "Daemon out of date"
	}
}

// Draw renders the skew modal as a centered dialog covering its full rect.
func (m *RestartDaemonModal) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)
	x, y, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	lines := m.bodyLines()
	formW := min(72, width-4)
	if formW < 12 {
		formW = width
	}
	// Chrome: top border, title, blank, [body], blank, buttons, bottom border.
	formH := min(len(lines)+6, height)
	formX := x + (width-formW)/2
	formY := max(y+(height-formH)/2, y)

	// Clear the modal area (no Sync — full-rect cover; see ui-threading gotcha).
	for row := formY; row < formY+formH && row < y+height; row++ {
		for col := formX; col < formX+formW && col < x+width; col++ {
			screen.SetContent(col, row, ' ', nil, tcell.StyleDefault)
		}
	}

	widget.DrawBorder(screen, formX, formY, formW, formH, theme.StyleFocusedBorder)
	widget.DrawText(screen, formX+2, formY+1, formW-4, m.titleText(), theme.StyleTitle)
	maxBody := max(formH-6, 0)
	if len(lines) > maxBody {
		lines = lines[:maxBody]
	}
	for i, line := range lines {
		widget.DrawText(screen, formX+2, formY+3+i, formW-4, line, theme.StyleNormal)
	}

	// Button row centered on the bottom body row.
	totalW := 0
	for i, b := range m.buttons {
		totalW += len(b.label)
		if i > 0 {
			totalW += 2
		}
	}
	startX := formX + (formW-totalW)/2
	if startX < formX+1 {
		startX = formX + 1
	}
	btnRow := formY + formH - 2
	col := startX
	for i, b := range m.buttons {
		style := theme.StyleNormal
		if i == m.selected {
			style = theme.StyleSelected
		}
		widget.DrawText(screen, col, btnRow, len(b.label), b.label, style)
		col += len(b.label) + 2
	}
}
