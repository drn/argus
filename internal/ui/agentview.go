package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/drn/argus/internal/agent"
	"github.com/hinshun/vt10x"
)

// AgentPanel identifies which panel has focus in the agent view.
type AgentPanel int

const (
	panelGit   AgentPanel = iota
	panelAgent            // center — terminal
	panelFiles            // right — changed files
)

// AgentViewTickMsg triggers a fast refresh of the agent terminal output.
type AgentViewTickMsg struct{}

// AgentView renders a three-panel layout: git status | agent terminal | file explorer.
type AgentView struct {
	theme    Theme
	runner   *agent.Runner
	taskID   string
	taskName string
	focus    AgentPanel
	width    int
	height   int

	gitstatus GitStatus
	files     FileExplorer

	// Cached git status for file explorer
	lastGitRefresh time.Time

	// Terminal render cache — avoids replaying entire ring buffer every tick
	cachedWriteCount uint64
	cachedTerminal   string
}

func NewAgentView(theme Theme, runner *agent.Runner) AgentView {
	return AgentView{
		theme:     theme,
		runner:    runner,
		focus:     panelAgent,
		gitstatus: NewGitStatus(theme),
		files:     NewFileExplorer(theme),
	}
}

// Enter sets up the agent view for a specific task.
func (av *AgentView) Enter(taskID, taskName string) {
	av.taskID = taskID
	av.taskName = taskName
	av.focus = panelAgent
	av.gitstatus.SetTask(taskID)
	av.lastGitRefresh = time.Time{}
}

func (av *AgentView) SetSize(w, h int) {
	if av.width != w || av.height != h {
		av.cachedTerminal = "" // invalidate cache on resize
	}
	av.width = w
	av.height = h
	leftW, centerW, rightW := av.splitWidths()
	// Reserve 1 for status bar
	contentH := h - 1
	av.gitstatus.SetSize(leftW, contentH)
	av.files.SetSize(rightW, contentH)
	// Resize PTY to match center panel (minus border)
	if sess := av.runner.Get(av.taskID); sess != nil {
		ptyRows := uint16(max(contentH-2, 5))
		ptyCols := uint16(max(centerW-4, 20))
		sess.Resize(ptyRows, ptyCols)
	}
	_ = centerW // used in View
}

// UpdateGitStatus handles a git status refresh message.
func (av *AgentView) UpdateGitStatus(msg GitStatusRefreshMsg) {
	if msg.TaskID == av.taskID {
		av.gitstatus.Update(msg)
		// Prefer uncommitted files; fall back to committed branch files
		if files := ParseGitStatus(msg.Status); len(files) > 0 {
			av.files.SetFiles(files)
		} else {
			av.files.SetFiles(ParseGitDiffNameStatus(msg.BranchFiles))
		}
		av.lastGitRefresh = time.Now()
	}
}

// NeedsGitRefresh returns true if git status should be refreshed.
func (av *AgentView) NeedsGitRefresh() bool {
	if av.taskID == "" {
		return false
	}
	return time.Since(av.lastGitRefresh) > 3*time.Second
}

// FocusLeft moves focus to the left panel.
func (av *AgentView) FocusLeft() {
	if av.focus > panelGit {
		av.focus--
	}
}

// FocusRight moves focus to the right panel.
func (av *AgentView) FocusRight() {
	if av.focus < panelFiles {
		av.focus++
	}
}

// HandleKey processes a key event. Returns true if the user wants to detach.
func (av *AgentView) HandleKey(msg tea.KeyMsg) (detach bool) {
	keyStr := msg.String()

	// Global agent view keys (regardless of focus)
	if keyStr == "ctrl+q" {
		return true
	}
	if keyStr == "ctrl+left" {
		av.FocusLeft()
		return false
	}
	if keyStr == "ctrl+right" {
		av.FocusRight()
		return false
	}

	// Panel-specific key handling
	switch av.focus {
	case panelAgent:
		// Forward all other keys to the PTY
		if sess := av.runner.Get(av.taskID); sess != nil {
			if b := keyMsgToBytes(msg); len(b) > 0 {
				sess.WriteInput(b)
			}
		}
	case panelGit:
		// Sidebar navigation (no-op for now, git status is read-only)
	case panelFiles:
		switch keyStr {
		case "up", "k":
			av.files.CursorUp()
		case "down", "j":
			av.files.CursorDown()
		}
	}
	return false
}

// View renders the three-panel layout.
func (av *AgentView) View() string {
	_, centerW, _ := av.splitWidths()
	contentH := av.height - 1

	// Left panel: git status
	av.gitstatus.SetFocused(av.focus == panelGit)
	leftView := av.gitstatus.View()

	// Center panel: agent terminal
	centerView := av.renderTerminal(centerW, contentH)

	// Right panel: file explorer
	rightView := av.files.View(av.focus == panelFiles)

	// Ensure all panels are the right height
	leftView = padHeight(leftView, contentH)
	centerView = padHeight(centerView, contentH)
	rightView = padHeight(rightView, contentH)

	content := lipgloss.JoinHorizontal(lipgloss.Top, leftView, centerView, rightView)

	// Status bar
	bar := av.renderStatusBar()

	return content + "\n" + bar
}

func (av *AgentView) renderTerminal(w, h int) string {
	borderColor := "238"
	if av.focus == panelAgent {
		borderColor = "87"
	}
	innerH := max(h-2, 1)
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Width(w - 2).
		Height(innerH)

	sess := av.runner.Get(av.taskID)
	if sess == nil {
		// Show cached terminal output if available so the user can see
		// why the process exited (e.g. error messages from the agent).
		if av.cachedTerminal != "" {
			return border.Render(av.cachedTerminal)
		}
		empty := av.theme.Dimmed.
			Width(w - 4).
			Height(innerH).
			AlignHorizontal(lipgloss.Center).
			AlignVertical(lipgloss.Center)
		return border.Render(empty.Render("Agent not running\n\nPress ctrl+q to return"))
	}

	// Check if output has changed before expensive vt10x replay
	writeCount := sess.TotalWritten()
	if writeCount == av.cachedWriteCount && av.cachedTerminal != "" {
		return border.Render(av.cachedTerminal)
	}

	raw := sess.RecentOutput()
	if len(raw) == 0 {
		empty := av.theme.Dimmed.
			Width(w - 4).
			Height(innerH).
			AlignHorizontal(lipgloss.Center).
			AlignVertical(lipgloss.Center)
		return border.Render(empty.Render("Waiting for output..."))
	}

	content := av.formatTerminalOutput(raw, w, h)
	av.cachedWriteCount = writeCount
	av.cachedTerminal = content
	return border.Render(content)
}

func (av AgentView) formatTerminalOutput(raw []byte, panelW, panelH int) string {
	dispW := max(panelW-4, 10)
	dispH := max(panelH-4, 3)

	sess := av.runner.Get(av.taskID)
	vtCols, vtRows := dispW, dispH
	if sess != nil {
		c, r := sess.PTYSize()
		if c > 0 {
			vtCols = c
		}
		if r > 0 {
			vtRows = r
		}
	}

	vt := vt10x.New(vt10x.WithSize(vtCols, vtRows))
	vt.Write(raw)

	vt.Lock()
	defer vt.Unlock()

	// Get cursor position for rendering
	cur := vt.Cursor()
	curVisible := vt.CursorVisible()

	var lines []string
	for y := 0; y < vtRows; y++ {
		cursorX := -1
		if curVisible && y == cur.Y {
			cursorX = cur.X
		}
		line := renderLine(vt, y, vtCols, cursorX)
		lines = append(lines, line)
	}

	// Trim trailing empty lines
	for len(lines) > 0 && stripANSI(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) == 0 {
		return ""
	}

	// Take the tail and truncate
	if len(lines) > dispH {
		lines = lines[len(lines)-dispH:]
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, dispW, "\x1b[0m")
	}

	return strings.Join(lines, "\n")
}

func (av AgentView) renderStatusBar() string {
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("87"))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	// Left: task name + status indicator
	status := ""
	if av.runner.Get(av.taskID) == nil {
		status = labelStyle.Render(" (exited — ctrl+q to return)")
	}
	left := " " + av.theme.Normal.Render(av.taskName) + status

	// Right: keybinding hints
	keys := []struct{ key, label string }{
		{"ctrl+←", "panel left"},
		{"ctrl+→", "panel right"},
		{"ctrl+q", "detach"},
	}
	var parts []string
	for _, k := range keys {
		parts = append(parts, keyStyle.Render(k.key)+labelStyle.Render(" "+k.label))
	}
	right := strings.Join(parts, "  ") + " "

	// Focus indicator
	var focusLabel string
	switch av.focus {
	case panelGit:
		focusLabel = "GIT STATUS"
	case panelAgent:
		focusLabel = "TERMINAL"
	case panelFiles:
		focusLabel = "FILES"
	}
	center := av.theme.Section.Render(" [" + focusLabel + "] ")

	gap1 := av.width - lipgloss.Width(left) - lipgloss.Width(center) - lipgloss.Width(right)
	leftGap := gap1 / 2
	rightGap := gap1 - leftGap
	if leftGap < 0 {
		leftGap = 0
	}
	if rightGap < 0 {
		rightGap = 0
	}

	bar := av.theme.StatusBar.
		Width(av.width).
		Render(left + fmt.Sprintf("%*s", leftGap, "") + center + fmt.Sprintf("%*s", rightGap, "") + right)

	return bar
}

// splitWidths returns left, center, right panel widths.
// Center gets ~60%, left and right split the remainder, with min widths.
func (av AgentView) splitWidths() (int, int, int) {
	minLeft := 20
	minCenter := 60
	minRight := 20

	// If terminal is too narrow, compress proportionally
	if av.width < minLeft+minCenter+minRight {
		center := av.width * 6 / 10
		if center < 30 {
			center = 30
		}
		side := (av.width - center) / 2
		if side < 10 {
			side = 10
		}
		right := av.width - center - side
		if right < 0 {
			right = 0
		}
		return side, center, right
	}

	center := av.width * 6 / 10
	if center < minCenter {
		center = minCenter
	}
	remaining := av.width - center
	left := remaining / 2
	right := remaining - left
	if left < minLeft {
		left = minLeft
		right = remaining - left
	}
	if right < minRight {
		right = minRight
		left = remaining - right
	}
	return left, center, right
}

// padHeight ensures a rendered string fills exactly h lines.
func padHeight(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) >= h {
		return strings.Join(lines[:h], "\n")
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// keyMsgToBytes converts a Bubble Tea key message to raw terminal bytes.
func keyMsgToBytes(msg tea.KeyMsg) []byte {
	switch msg.Type {
	case tea.KeyRunes:
		return []byte(string(msg.Runes))
	case tea.KeySpace:
		return []byte{' '}
	case tea.KeyEnter:
		return []byte{'\r'}
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyTab:
		return []byte{'\t'}
	case tea.KeyShiftTab:
		return []byte("\x1b[Z")
	case tea.KeyEscape:
		return []byte{0x1b}
	case tea.KeyUp:
		if msg.Alt {
			return []byte("\x1b[1;3A")
		}
		return []byte("\x1b[A")
	case tea.KeyDown:
		if msg.Alt {
			return []byte("\x1b[1;3B")
		}
		return []byte("\x1b[B")
	case tea.KeyRight:
		if msg.Alt {
			return []byte("\x1b[1;3C")
		}
		return []byte("\x1b[C")
	case tea.KeyLeft:
		if msg.Alt {
			return []byte("\x1b[1;3D")
		}
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyCtrlA:
		return []byte{0x01}
	case tea.KeyCtrlB:
		return []byte{0x02}
	case tea.KeyCtrlC:
		return []byte{0x03}
	case tea.KeyCtrlD:
		return []byte{0x04}
	case tea.KeyCtrlE:
		return []byte{0x05}
	case tea.KeyCtrlF:
		return []byte{0x06}
	case tea.KeyCtrlG:
		return []byte{0x07}
	case tea.KeyCtrlH:
		return []byte{0x08}
	case tea.KeyCtrlK:
		return []byte{0x0b}
	case tea.KeyCtrlL:
		return []byte{0x0c}
	case tea.KeyCtrlN:
		return []byte{0x0e}
	case tea.KeyCtrlO:
		return []byte{0x0f}
	case tea.KeyCtrlP:
		return []byte{0x10}
	case tea.KeyCtrlR:
		return []byte{0x12}
	case tea.KeyCtrlS:
		return []byte{0x13}
	case tea.KeyCtrlT:
		return []byte{0x14}
	case tea.KeyCtrlU:
		return []byte{0x15}
	case tea.KeyCtrlV:
		return []byte{0x16}
	case tea.KeyCtrlW:
		return []byte{0x17}
	case tea.KeyCtrlX:
		return []byte{0x18}
	case tea.KeyCtrlY:
		return []byte{0x19}
	case tea.KeyCtrlZ:
		return []byte{0x1a}
	case tea.KeyF1:
		return []byte("\x1bOP")
	case tea.KeyF2:
		return []byte("\x1bOQ")
	case tea.KeyF3:
		return []byte("\x1bOR")
	case tea.KeyF4:
		return []byte("\x1bOS")
	case tea.KeyF5:
		return []byte("\x1b[15~")
	case tea.KeyF6:
		return []byte("\x1b[17~")
	case tea.KeyF7:
		return []byte("\x1b[18~")
	case tea.KeyF8:
		return []byte("\x1b[19~")
	case tea.KeyF9:
		return []byte("\x1b[20~")
	case tea.KeyF10:
		return []byte("\x1b[21~")
	case tea.KeyF11:
		return []byte("\x1b[23~")
	case tea.KeyF12:
		return []byte("\x1b[24~")
	}

	// Fallback: use the string representation
	s := msg.String()
	if s != "" {
		return []byte(s)
	}
	return nil
}
