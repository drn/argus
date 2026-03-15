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
	cachedWriteCount  uint64
	cachedTerminal    string
	cachedScrollOff   int // scroll offset when cache was generated

	// Scrollback support
	scrollOffset int // lines scrolled up from bottom (0 = follow tail)
	cachedLines  []string // cached rendered lines for scrolling without re-parsing

	// lastOutput holds the final ring buffer contents from a finished session
	// so we can display error output even after the session is removed.
	lastOutput []byte

	// Persistent vt10x terminal for incremental rendering (avoids full replay)
	vtTerm     vt10x.Terminal
	vtFedTotal uint64 // monotonic byte count fed to vtTerm
	vtCols     int    // vtTerm column count
	vtRows     int    // vtTerm row count

	// Diff viewer state
	diffMode       bool     // true when viewing a file's diff
	diffContent    string   // raw colorized diff output
	diffLines      []string // split lines for scrolling
	diffScrollOff  int      // scroll offset within diff
	worktreeDir    string   // resolved worktree directory for git commands
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
	av.lastOutput = nil
	av.cachedTerminal = ""
	av.cachedWriteCount = 0
	av.scrollOffset = 0
	av.cachedLines = nil
	av.vtTerm = nil
	av.vtFedTotal = 0
	av.diffMode = false
	av.diffContent = ""
	av.diffLines = nil
	av.diffScrollOff = 0
	av.worktreeDir = ""
}

// SetLastOutput stores the final ring buffer from a finished session
// so the terminal can still display output after the session is gone.
func (av *AgentView) SetLastOutput(output []byte) {
	av.lastOutput = output
}

func (av *AgentView) SetSize(w, h int) {
	if av.width != w || av.height != h {
		av.cachedTerminal = "" // invalidate cache on resize
		av.vtTerm = nil        // force vt10x re-creation
		av.vtFedTotal = 0
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
	return time.Since(av.lastGitRefresh) > gitRefreshInterval
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

// HandleKey processes a key event. Returns detach=true if the user wants to
// detach, and optionally a tea.Cmd (e.g. to fetch a file diff).
func (av *AgentView) HandleKey(msg tea.KeyMsg) (detach bool, cmd tea.Cmd) {
	keyStr := msg.String()

	// Global agent view keys (regardless of focus)
	if keyStr == "ctrl+q" {
		if av.diffMode {
			av.exitDiffMode()
			return false, nil
		}
		return true, nil
	}

	// In diff mode, handle keys for scrolling / navigation
	if av.diffMode {
		return false, av.handleDiffKey(msg)
	}

	// Panel switching: ctrl+left/right only.
	// Use type-based matching to handle terminals that set the Alt flag on
	// ctrl+arrow sequences (urxvt sends \x1b[Od which parses as
	// KeyCtrlLeft with Alt=true, producing "alt+ctrl+left").
	switch msg.Type {
	case tea.KeyCtrlLeft:
		av.FocusLeft()
		return false, nil
	case tea.KeyCtrlRight:
		av.FocusRight()
		return false, nil
	}

	// Panel-specific key handling
	switch av.focus {
	case panelAgent:
		// Scrollback keys (shift+up/down/pgup/pgdown) are intercepted
		_, _, dispH := av.terminalDisplaySize()
		switch keyStr {
		case "shift+up":
			av.scrollUp(1)
			return false, nil
		case "shift+down":
			av.scrollDown(1)
			return false, nil
		case "shift+pgup":
			av.scrollUp(dispH)
			return false, nil
		case "shift+pgdown":
			av.scrollDown(dispH)
			return false, nil
		case "shift+end":
			av.scrollOffset = 0
			return false, nil
		}
		// Any other key sent to PTY resets scroll to follow tail
		av.scrollOffset = 0
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
		case "esc":
			av.focus = panelAgent
		case "up", "k":
			av.files.CursorUp()
		case "down", "j":
			av.files.CursorDown()
		case "enter":
			return false, av.openFileDiff()
		}
	}
	return false, nil
}

// HandleMouse processes mouse events (scroll wheel).
func (av *AgentView) HandleMouse(msg tea.MouseMsg) {
	if av.diffMode {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			av.diffScrollUp(3)
		case tea.MouseButtonWheelDown:
			av.diffScrollDown(3)
		}
		return
	}
	if av.focus != panelAgent {
		return
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		av.scrollUp(3)
	case tea.MouseButtonWheelDown:
		av.scrollDown(3)
	}
}

// openFileDiff starts an async fetch of the selected file's diff.
func (av *AgentView) openFileDiff() tea.Cmd {
	f := av.files.SelectedFile()
	if f == nil || av.worktreeDir == "" {
		return nil
	}
	taskID := av.taskID
	dir := av.worktreeDir
	path := f.Path
	return func() tea.Msg {
		return FetchFileDiff(taskID, dir, path)
	}
}

// UpdateFileDiff handles the result of an async file diff fetch.
func (av *AgentView) UpdateFileDiff(msg FileDiffMsg) {
	if msg.TaskID != av.taskID {
		return
	}
	av.diffMode = true
	av.diffContent = msg.Diff
	av.diffScrollOff = 0
	if msg.Diff == "" {
		av.diffLines = []string{"(no diff)"}
	} else {
		av.diffLines = strings.Split(msg.Diff, "\n")
	}
}

// SetWorktreeDir sets the resolved worktree directory for git commands.
func (av *AgentView) SetWorktreeDir(dir string) {
	av.worktreeDir = dir
}

func (av *AgentView) exitDiffMode() {
	av.diffMode = false
	av.diffContent = ""
	av.diffLines = nil
	av.diffScrollOff = 0
}

func (av *AgentView) handleDiffKey(msg tea.KeyMsg) tea.Cmd {
	keyStr := msg.String()
	switch keyStr {
	case "esc", "q":
		av.exitDiffMode()
		av.focus = panelAgent
	case "up", "k":
		// Move to previous file and show its diff
		av.files.CursorUp()
		return av.openFileDiff()
	case "down", "j":
		// Move to next file and show its diff
		av.files.CursorDown()
		return av.openFileDiff()
	case "shift+up", "pgup":
		av.diffScrollUp(av.diffVisibleRows())
	case "shift+down", "pgdown":
		av.diffScrollDown(av.diffVisibleRows())
	}
	return nil
}

func (av *AgentView) diffVisibleRows() int {
	contentH := av.height - 1
	rows := contentH - 4
	if rows < 3 {
		rows = 3
	}
	return rows
}

func (av *AgentView) diffScrollUp(n int) {
	av.diffScrollOff -= n
	if av.diffScrollOff < 0 {
		av.diffScrollOff = 0
	}
}

func (av *AgentView) diffScrollDown(n int) {
	maxOff := len(av.diffLines) - av.diffVisibleRows()
	if maxOff < 0 {
		maxOff = 0
	}
	av.diffScrollOff += n
	if av.diffScrollOff > maxOff {
		av.diffScrollOff = maxOff
	}
}

// View renders the three-panel layout.
func (av *AgentView) View() string {
	_, centerW, _ := av.splitWidths()
	contentH := av.height - 1

	// Left panel: git status
	av.gitstatus.SetFocused(av.focus == panelGit)
	leftView := av.gitstatus.View()

	// Center panel: diff viewer or agent terminal
	var centerView string
	if av.diffMode {
		centerView = av.renderDiffPanel(centerW, contentH)
	} else {
		centerView = av.renderTerminal(centerW, contentH)
	}

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
		// Show last output from the finished session so the user can see
		// why the process exited (e.g. error messages from the agent).
		if len(av.lastOutput) > 0 {
			content := av.formatTerminalOutput(av.lastOutput, w, h)
			return border.Render(content)
		}
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
	if writeCount == av.cachedWriteCount && av.scrollOffset == av.cachedScrollOff && av.cachedTerminal != "" {
		return border.Render(av.cachedTerminal)
	}
	// If only scroll changed but output is the same, re-slice cached lines
	if writeCount == av.cachedWriteCount && len(av.cachedLines) > 0 {
		dispW := max(w-4, 10)
		dispH := max(h-4, 3)
		content := av.sliceCachedLines(dispW, dispH)
		av.cachedScrollOff = av.scrollOffset
		av.cachedTerminal = content
		return border.Render(content)
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

	var content string
	if av.scrollOffset > 0 {
		// Scrollback mode: full replay (only triggered by scroll events)
		content = av.formatTerminalOutput(raw, w, h)
	} else {
		// Normal follow-tail mode: incremental feed to persistent vt10x
		content = av.renderIncremental(sess, raw, writeCount, w, h)
	}
	av.cachedWriteCount = writeCount
	av.cachedScrollOff = av.scrollOffset
	av.cachedTerminal = content
	return border.Render(content)
}

func (av *AgentView) renderDiffPanel(w, h int) string {
	borderColor := "87" // always focused when viewing diff
	innerH := max(h-2, 1)
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Width(w - 2).
		Height(innerH)

	if len(av.diffLines) == 0 {
		empty := av.theme.Dimmed.
			Width(w - 4).
			Height(innerH).
			AlignHorizontal(lipgloss.Center).
			AlignVertical(lipgloss.Center)
		return border.Render(empty.Render("No diff available"))
	}

	dispW := max(w-4, 10)
	dispH := max(h-4, 3)

	// Header
	fileName := ""
	if f := av.files.SelectedFile(); f != nil {
		fileName = f.Path
	}
	header := av.theme.Section.Render("  DIFF") +
		av.theme.Dimmed.Render(" " + fileName) +
		av.theme.Dimmed.Render(fmt.Sprintf("  [%d/%d]", av.files.scroll.Cursor()+1, av.files.FileCount()))
	headerLines := 1

	// Visible diff lines
	visibleH := dispH - headerLines
	if visibleH < 1 {
		visibleH = 1
	}

	end := av.diffScrollOff + visibleH
	if end > len(av.diffLines) {
		end = len(av.diffLines)
	}
	start := av.diffScrollOff
	if start > end {
		start = end
	}

	var b strings.Builder
	b.WriteString(header + "\n")
	for i := start; i < end; i++ {
		line := av.diffLines[i]
		// Truncate to panel width (ANSI-aware)
		line = ansi.Truncate(line, dispW, "\x1b[0m")
		b.WriteString(line)
		if i < end-1 {
			b.WriteString("\n")
		}
	}

	return border.Render(b.String())
}

// renderIncremental feeds only new bytes to a persistent vt10x terminal,
// avoiding the O(buffer_size) full replay on every render tick.
func (av *AgentView) renderIncremental(sess *agent.Session, raw []byte, totalWritten uint64, panelW, panelH int) string {
	dispW := max(panelW-4, 10)
	dispH := max(panelH-4, 3)

	ptyCols, ptyRows := sess.PTYSize()
	if ptyCols < 20 {
		ptyCols = 80
	}
	if ptyRows < 5 {
		ptyRows = dispH
	}

	// Initialize or reset vt10x if dimensions changed
	if av.vtTerm == nil || av.vtCols != ptyCols || av.vtRows != ptyRows {
		av.vtTerm = vt10x.New(vt10x.WithSize(ptyCols, ptyRows))
		av.vtFedTotal = 0
		av.vtCols = ptyCols
		av.vtRows = ptyRows
	}

	// Feed only new bytes to the persistent terminal
	newBytes := totalWritten - av.vtFedTotal
	if newBytes > uint64(len(raw)) {
		// Ring buffer wrapped past what we've seen — full reset
		av.vtTerm = vt10x.New(vt10x.WithSize(ptyCols, ptyRows))
		av.vtTerm.Write(raw)
	} else if newBytes > 0 {
		av.vtTerm.Write(raw[len(raw)-int(newBytes):])
	}
	av.vtFedTotal = totalWritten

	// Render current screen from persistent vt10x
	av.vtTerm.Lock()
	defer av.vtTerm.Unlock()

	cur := av.vtTerm.Cursor()
	curVisible := av.vtTerm.CursorVisible()

	lines := make([]string, 0, ptyRows)
	for y := 0; y < ptyRows; y++ {
		cursorX := -1
		if curVisible && y == cur.Y {
			cursorX = cur.X
		}
		lines = append(lines, renderLine(av.vtTerm, y, ptyCols, cursorX))
	}

	// Trim trailing empty lines
	for len(lines) > 0 && stripANSI(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) == 0 {
		return ""
	}

	// Take tail if more lines than display height
	if len(lines) > dispH {
		lines = lines[len(lines)-dispH:]
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, dispW, "\x1b[0m")
	}

	return strings.Join(lines, "\n")
}

func (av *AgentView) formatTerminalOutput(raw []byte, panelW, panelH int) string {
	dispW := max(panelW-4, 10)
	dispH := max(panelH-4, 3)

	sess := av.runner.Get(av.taskID)
	vtCols := dispW
	if sess != nil {
		c, _ := sess.PTYSize()
		if c > 0 {
			vtCols = c
		}
	}

	vtRows := estimateVTRows(raw, vtCols, dispH)
	lines := replayVT10X(raw, vtCols, vtRows, true)

	if len(lines) == 0 {
		return ""
	}

	av.cachedLines = lines
	return av.windowLines(lines, dispW, dispH)
}

func (av AgentView) renderStatusBar() string {
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("87"))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	// Left: task name + status indicator
	status := ""
	if av.runner.Get(av.taskID) == nil {
		status = labelStyle.Render(" (exited — ctrl+q to return)")
	} else if av.scrollOffset > 0 {
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render(
			fmt.Sprintf(" [SCROLL -%d]", av.scrollOffset))
	}
	left := " " + av.theme.Normal.Render(av.taskName) + status

	// Right: keybinding hints
	var keys []struct{ key, label string }
	if av.diffMode {
		keys = []struct{ key, label string }{
			{"↑/↓", "file"},
			{"scroll", "navigate"},
			{"esc", "close"},
		}
	} else {
		keys = []struct{ key, label string }{
			{"⌘↑/↓", "task"},
			{"⇧↑/↓", "scroll"},
			{"ctrl+←/→", "panel"},
			{"ctrl+q", "detach"},
		}
	}
	var parts []string
	for _, k := range keys {
		parts = append(parts, keyStyle.Render(k.key)+labelStyle.Render(" "+k.label))
	}
	right := strings.Join(parts, "  ") + " "

	// Focus indicator — truly centered on the bar
	var focusLabel string
	if av.diffMode {
		focusLabel = "DIFF"
	} else {
		switch av.focus {
		case panelGit:
			focusLabel = "GIT STATUS"
		case panelAgent:
			focusLabel = "TERMINAL"
		case panelFiles:
			focusLabel = "FILES"
		}
	}
	center := av.theme.Section.Render(" [" + focusLabel + "] ")

	centerW := lipgloss.Width(center)
	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)

	// Place center at the true midpoint of the bar
	centerStart := (av.width - centerW) / 2
	leftGap := centerStart - leftW
	if leftGap < 1 {
		leftGap = 1
	}
	rightGap := av.width - leftW - leftGap - centerW - rightW
	if rightGap < 1 {
		rightGap = 1
	}

	bar := av.theme.StatusBar.
		Width(av.width).
		Render(left + fmt.Sprintf("%*s", leftGap, "") + center + fmt.Sprintf("%*s", rightGap, "") + right)

	return bar
}

// windowLines applies scroll offset to select a visible window, then truncates.
func (av *AgentView) windowLines(lines []string, dispW, dispH int) string {
	if len(lines) == 0 {
		return ""
	}
	end := len(lines) - av.scrollOffset
	if end < 0 {
		end = 0
	}
	start := end - dispH
	if start < 0 {
		start = 0
	}
	visible := lines[start:end]
	result := make([]string, len(visible))
	for i, line := range visible {
		result[i] = ansi.Truncate(line, dispW, "\x1b[0m")
	}
	return strings.Join(result, "\n")
}

// sliceCachedLines selects the visible window from cachedLines using scrollOffset.
func (av *AgentView) sliceCachedLines(dispW, dispH int) string {
	return av.windowLines(av.cachedLines, dispW, dispH)
}

// terminalDisplaySize returns the usable display dimensions inside the terminal panel.
func (av *AgentView) terminalDisplaySize() (dispW, dispH int, centerW int) {
	_, cw, _ := av.splitWidths()
	contentH := av.height - 1
	return max(cw-4, 10), max(contentH-4, 3), cw
}

// scrollUp scrolls the terminal view up by n lines.
func (av *AgentView) scrollUp(n int) {
	av.scrollOffset += n
	// Clamp to max only when cachedLines is populated. When empty (incremental
	// render mode), allow scrollOffset to grow so the next View() triggers
	// formatTerminalOutput which populates cachedLines.
	if len(av.cachedLines) > 0 {
		_, dispH, _ := av.terminalDisplaySize()
		maxScroll := len(av.cachedLines) - dispH
		if maxScroll < 0 {
			maxScroll = 0
		}
		if av.scrollOffset > maxScroll {
			av.scrollOffset = maxScroll
		}
	}
}

// scrollDown scrolls the terminal view down by n lines (toward tail).
func (av *AgentView) scrollDown(n int) {
	av.scrollOffset -= n
	if av.scrollOffset < 0 {
		av.scrollOffset = 0
	}
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

