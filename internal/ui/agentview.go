package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// SessionLogLoadedMsg carries the contents of a session log file loaded
// asynchronously when entering the agent view for a completed task.
type SessionLogLoadedMsg struct {
	TaskID string
	Data   []byte
}

// AgentView renders a three-panel layout: git status | agent terminal | file explorer.
type AgentView struct {
	theme       Theme
	runner      agent.SessionProvider
	sessionsDir string // directory where session logs are stored
	taskID      string
	taskName    string
	taskPRURL   string
	focus       AgentPanel
	layout      PanelLayout
	width       int
	height      int

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
	diffMode         bool              // true when viewing a file's diff
	diffSplit        bool              // true = side-by-side, false = unified
	diffParsed       ParsedDiff        // parsed diff structure (for both views)
	diffRows         []SideBySideLine  // side-by-side rows (computed from diffParsed)
	diffUnified      []string          // unified view lines (highlighted, computed from diffParsed)
	diffWrappedLines []string          // diffUnified after Hardwrap — cache
	diffWrapWidth    int               // width used for diffWrappedLines; 0 = stale
	diffDispW        int               // last-computed display width for the diff panel
	diffScrollOff    int               // scroll offset within diff
	worktreeDir      string            // resolved worktree directory for git commands
	diffFileName     string            // current file being diffed (for highlighting)
}

func NewAgentView(theme Theme, runner agent.SessionProvider, sessionsDir string) AgentView {
	return AgentView{
		theme:       theme,
		runner:      runner,
		sessionsDir: sessionsDir,
		focus:       panelAgent,
		diffSplit:   true,
		layout: NewPanelLayout([]PanelConfig{
			{Pct: 20, Min: 20},
			{Pct: 60, Min: 60},
			{Pct: 20, Min: 20},
		}),
		gitstatus: NewGitStatus(theme),
		files:     NewFileExplorer(theme),
	}
}

// Enter sets up the agent view for a specific task.
func (av *AgentView) Enter(taskID, taskName string) {
	av.taskID = taskID
	av.taskName = taskName
	av.taskPRURL = ""
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
	av.diffSplit = true
	av.diffParsed = ParsedDiff{}
	av.diffRows = nil
	av.diffUnified = nil
	av.diffWrappedLines = nil
	av.diffWrapWidth = 0
	av.diffDispW = 0
	av.diffScrollOff = 0
	av.worktreeDir = ""
	av.diffFileName = ""
}

// LoadSessionLogCmd returns a tea.Cmd that asynchronously reads the session log
// for completed/stopped tasks so scrollback works after a daemon restart.
// Returns nil when a live session exists (ring buffer is authoritative).
func (av *AgentView) LoadSessionLogCmd(taskID string) tea.Cmd {
	if av.runner.Get(taskID) != nil || av.sessionsDir == "" {
		return nil
	}
	logPath := filepath.Join(av.sessionsDir, taskID+".log")
	return func() tea.Msg {
		data, err := os.ReadFile(logPath)
		if err != nil || len(data) == 0 {
			return nil
		}
		return SessionLogLoadedMsg{TaskID: taskID, Data: data}
	}
}

// SetPRURL updates the PR URL associated with the current task.
func (av *AgentView) SetPRURL(url string) {
	av.taskPRURL = url
}

// OpenPR opens the task's PR URL in the default browser.
// Returns a tea.Cmd that runs the open command, or nil if no PR URL is set.
func (av *AgentView) OpenPR() tea.Cmd {
	if av.taskPRURL == "" {
		return nil
	}
	url := av.taskPRURL
	return func() tea.Msg {
		exec.Command("open", url).Start() //nolint:errcheck
		return nil
	}
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
	// Reserve 1 for header + 1 for status bar
	contentH := h - 2
	av.layout.SetSize(w, contentH)
	widths := av.layout.SplitWidths()
	av.gitstatus.SetSize(widths[0], contentH)
	av.files.SetSize(widths[2], contentH)
	// Resize PTY to match center panel (minus border)
	if sess := av.runner.Get(av.taskID); sess != nil {
		ptyRows := uint16(max(contentH-2, 5))
		ptyCols := uint16(max(widths[1]-4, 20))
		sess.Resize(ptyRows, ptyCols)
	}
}

// UpdateGitStatus handles a git status refresh message.
func (av *AgentView) UpdateGitStatus(msg GitStatusRefreshMsg) {
	if msg.TaskID == av.taskID {
		av.gitstatus.Update(msg)
		// Show all changes on the branch: merge committed files (base) with
		// uncommitted changes (overlay). Uncommitted status wins on conflict.
		av.files.SetFiles(MergeChangedFiles(
			ParseGitDiffNameStatus(msg.BranchFiles),
			ParseGitStatus(msg.Status),
		))
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
// The git status panel is not focusable, so the leftmost stop is panelAgent.
func (av *AgentView) FocusLeft() {
	if av.focus > panelAgent {
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
		if av.focus == panelFiles {
			av.focus = panelAgent
			return false, nil
		}
		return true, nil
	}

	// In diff mode, handle keys for scrolling / navigation
	if av.diffMode {
		return false, av.handleDiffKey(msg)
	}

	// Panel switching: Ctrl+left/right.
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
	case panelFiles:
		switch keyStr {
		case "esc":
			av.focus = panelAgent
		case "up", "k":
			if dir := av.files.CursorUp(); dir != "" {
				return false, av.fetchDirChildren(dir)
			}
		case "down", "j":
			if dir := av.files.CursorDown(); dir != "" {
				return false, av.fetchDirChildren(dir)
			}
		case "enter":
			return false, av.openFileDiff()
		case "o":
			return false, av.openInFinder()
		case "e":
			return false, av.openInEditor()
		case "t":
			return false, av.openTerminal()
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

// fetchDirChildren returns a command to fetch children for an expanded directory.
func (av *AgentView) fetchDirChildren(dirPath string) tea.Cmd {
	taskID := av.taskID
	dir := av.worktreeDir
	return func() tea.Msg {
		return FetchDirFiles(taskID, dir, dirPath)
	}
}

// UpdateDirFiles handles the result of a directory file listing.
func (av *AgentView) UpdateDirFiles(msg DirFilesMsg) {
	if msg.TaskID != av.taskID {
		return
	}
	av.files.SetDirChildren(msg.DirPath, msg.Files)
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

// openInFinder opens Finder to the selected file's location.
func (av *AgentView) openInFinder() tea.Cmd {
	f := av.files.SelectedFile()
	if f == nil || av.worktreeDir == "" {
		return nil
	}
	fullPath := filepath.Join(av.worktreeDir, f.Path)
	return func() tea.Msg {
		exec.Command("open", "-R", fullPath).Start()
		return nil
	}
}

// openInEditor opens a new tmux tab with nvim editing the selected file.
func (av *AgentView) openInEditor() tea.Cmd {
	f := av.files.SelectedFile()
	if f == nil || av.worktreeDir == "" {
		return nil
	}
	fullPath := filepath.Join(av.worktreeDir, f.Path)
	return func() tea.Msg {
		exec.Command("tmux", "new-window", "nvim", fullPath).Start()
		return nil
	}
}

// openTerminal opens a new tmux tab cd'd to the worktree directory.
func (av *AgentView) openTerminal() tea.Cmd {
	if av.worktreeDir == "" {
		return nil
	}
	dir := av.worktreeDir
	return func() tea.Msg {
		exec.Command("tmux", "new-window", "-c", dir).Start()
		return nil
	}
}

// UpdateFileDiff handles the result of an async file diff fetch.
func (av *AgentView) UpdateFileDiff(msg FileDiffMsg) {
	if msg.TaskID != av.taskID {
		return
	}
	av.diffMode = true
	av.diffScrollOff = 0
	av.diffFileName = msg.FilePath
	av.diffWrappedLines = nil
	av.diffWrapWidth = 0
	// diffDispW is intentionally preserved: panel width hasn't changed, so
	// diffLineCount can return correct wrapped counts without waiting for the
	// next render pass. exitDiffMode clears it since the panel leaves the screen.
	if msg.Diff == "" {
		av.diffParsed = ParsedDiff{}
		av.diffRows = nil
		av.diffUnified = nil
	} else {
		av.diffParsed = ParseUnifiedDiff(msg.Diff)
		av.diffRows = BuildSideBySide(av.diffParsed)
		av.diffUnified = RenderUnifiedLines(av.diffParsed, msg.FilePath)
	}
}

// SetWorktreeDir sets the resolved worktree directory for git commands.
func (av *AgentView) SetWorktreeDir(dir string) {
	av.worktreeDir = dir
}

func (av *AgentView) exitDiffMode() {
	av.diffMode = false
	av.diffParsed = ParsedDiff{}
	av.diffRows = nil
	av.diffUnified = nil
	av.diffWrappedLines = nil
	av.diffWrapWidth = 0
	av.diffDispW = 0
	av.diffScrollOff = 0
	av.diffFileName = ""
}

// wrapDiffLines returns unified diff lines hard-wrapped to dispW, caching the
// result so wrapping is only recomputed when content or width changes.
func (av *AgentView) wrapDiffLines(dispW int) []string {
	if av.diffWrappedLines != nil && av.diffWrapWidth == dispW {
		return av.diffWrappedLines
	}
	var wrapped []string
	for _, line := range av.diffUnified {
		w := ansi.Hardwrap(line, dispW, true)
		parts := strings.Split(w, "\n")
		wrapped = append(wrapped, parts...)
	}
	av.diffWrappedLines = wrapped
	av.diffWrapWidth = dispW
	return wrapped
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
	case "s":
		av.diffSplit = !av.diffSplit
		av.diffScrollOff = 0
	}
	return nil
}

func (av *AgentView) diffVisibleRows() int {
	contentH := av.height - 2
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

func (av *AgentView) diffLineCount() int {
	if av.diffSplit {
		return len(av.diffRows)
	}
	// Always use the wrapped count so scroll bounds are correct. diffDispW is
	// set by renderDiffPanel on every render; wrapDiffLines caches the result.
	if av.diffDispW > 0 {
		return len(av.wrapDiffLines(av.diffDispW))
	}
	return len(av.diffUnified)
}

func (av *AgentView) diffScrollDown(n int) {
	maxOff := av.diffLineCount() - av.diffVisibleRows()
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
	widths := av.layout.SplitWidths()
	centerW := widths[1]
	contentH := av.layout.Height()

	header := av.renderHeader()

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

	content := av.layout.Render([]string{leftView, centerView, rightView})

	// Status bar
	bar := av.renderStatusBar()

	return header + "\n" + content + "\n" + bar
}

// renderHeader renders a centered tab-style header with the task name.
func (av *AgentView) renderHeader() string {
	const (
		baseBg   = "236"
		activeFg = "236"
		activeBg = "103"
		chevron  = "\ue0b0"
	)

	bg := lipgloss.Color(baseBg)
	abg := lipgloss.Color(activeBg)

	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Foreground(bg).Background(abg).Render(chevron))
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(activeFg)).Background(abg).Render(" " + av.taskName + " "))
	b.WriteString(lipgloss.NewStyle().Foreground(abg).Background(bg).Render(chevron))

	tabContent := b.String()
	return lipgloss.NewStyle().
		Background(bg).
		Width(av.width).
		Render(lipgloss.PlaceHorizontal(av.width, lipgloss.Center, tabContent, lipgloss.WithWhitespaceBackground(bg)))
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
		return border.Render(empty.Render("Agent not running\n\nPress ^q to return"))
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
		// Scrollback mode: full replay (only triggered by scroll events).
		// Capture old line count so we can anchor the view at an absolute
		// position — when new lines arrive, increment scrollOffset to
		// compensate so the displayed content stays fixed (tmux-style).
		oldLineCount := len(av.cachedLines)
		content = av.formatTerminalOutput(raw, w, h)
		if oldLineCount > 0 {
			delta := len(av.cachedLines) - oldLineCount
			if delta > 0 {
				av.scrollOffset += delta
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

	dispW := max(w-4, 10)
	dispH := max(h-4, 3)
	av.diffDispW = dispW // must be set before diffLineCount so scroll bounds use wrapped widths

	lineCount := av.diffLineCount()
	if lineCount == 0 {
		empty := av.theme.Dimmed.
			Width(w - 4).
			Height(innerH).
			AlignHorizontal(lipgloss.Center).
			AlignVertical(lipgloss.Center)
		return border.Render(empty.Render("No diff available"))
	}

	// Header
	fileName := av.diffFileName
	if f := av.files.SelectedFile(); f != nil {
		fileName = f.Path
	}
	modeLabel := "split"
	if !av.diffSplit {
		modeLabel = "unified"
	}
	header := RenderDiffHeader(fileName, av.files.scroll.Cursor(), av.files.FileCount(), modeLabel, av.theme)

	// Visible diff rows (minus header)
	visibleH := dispH - 1
	if visibleH < 1 {
		visibleH = 1
	}

	var content string
	if av.diffSplit {
		content = RenderSideBySide(av.diffRows, fileName, dispW, visibleH, av.diffScrollOff, av.theme)
	} else {
		wrapped := av.wrapDiffLines(dispW)
		content = RenderUnified(wrapped, visibleH, av.diffScrollOff)
	}

	return border.Render(header + "\n" + content)
}

// renderIncremental feeds only new bytes to a persistent vt10x terminal,
// avoiding the O(buffer_size) full replay on every render tick.
func (av *AgentView) renderIncremental(sess agent.SessionHandle, raw []byte, totalWritten uint64, panelW, panelH int) string {
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

	lines := make([]string, 0, ptyRows)
	for y := 0; y < ptyRows; y++ {
		cursorX := -1
		if y == cur.Y {
			cursorX = cur.X
		}
		lines = append(lines, renderLine(av.vtTerm, y, ptyCols, cursorX))
	}

	// Trim trailing empty lines, but never trim the cursor line — TUI agents
	// like Claude Code park the cursor at an empty bottom row after rendering,
	// so stripANSI strips the colored-background cursor cell to "" and the
	// trimmer would remove it, making the cursor invisible.
	for len(lines) > 0 && len(lines)-1 != cur.Y && stripANSI(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) == 0 {
		return ""
	}

	// Take tail if more lines than display height
	if len(lines) > dispH {
		lines = lines[len(lines)-dispH:]
	}

	// Trim leading empty lines (e.g. Codex positions its TUI content in the
	// lower portion of the terminal, leaving the top rows blank).
	// Track how many lines were removed from the front so we can protect the
	// cursor line from being trimmed here too.
	frontRemoved := 0
	for len(lines) > 0 && frontRemoved != cur.Y && stripANSI(lines[0]) == "" {
		lines = lines[1:]
		frontRemoved++
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
		status = labelStyle.Render(" (exited — ^q to return)")
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
			{"s", "split/unified"},
			{"esc", "close"},
		}
	} else {
		keys = []struct{ key, label string }{
			{"⌘↑/↓", "task"},
			{"⇧↑/↓", "scroll"},
			{"C-←/→", "panel"},
			{"^q", "detach"},
		}
		if av.taskPRURL != "" {
			keys = append(keys, struct{ key, label string }{"⌘O", "open PR"})
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
		if av.diffSplit {
			focusLabel = "DIFF SPLIT"
		} else {
			focusLabel = "DIFF UNIFIED"
		}
	} else {
		switch av.focus {
		case panelAgent:
			focusLabel = "TERMINAL"
		case panelFiles:
			focusLabel = "FILES"
		}
	}
	center := av.theme.Section.Render(" [" + focusLabel + "] ")

	centerW := lipgloss.Width(center)
	leftW := lipgloss.Width(left)
	// ⌘ renders as 2 cells in most terminals but go-runewidth reports 1.
	// Count occurrences and add the extra width so the gap math is correct.
	rightW := lipgloss.Width(right) + strings.Count(right, "⌘")

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
	widths := av.layout.SplitWidths()
	cw := widths[1]
	contentH := av.layout.Height()
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



