// Package agentview defines runtime-agnostic state, interfaces, and types for
// the agent view. These abstractions separate session display logic, scroll
// management, and focus handling from the tcell/tview rendering layer.
package agentview

import "time"

// Panel identifies which panel has focus in the agent view.
// Git status is display-only (not focusable).
type Panel int

const (
	PanelTerminal Panel = iota // center — agent terminal
	PanelFiles                 // right — file explorer
)

// DiffState tracks the diff viewer's mode and scroll position.
type DiffState struct {
	Active    bool   // true when viewing a file diff
	Split     bool   // true = side-by-side, false = unified
	ScrollOff int    // scroll offset within the diff
	FileName  string // file currently being diffed
}

// State holds the runtime-agnostic portion of the agent view's state.
// Rendering-specific fields (terminal emulator, render cache, layout widgets)
// remain in the UI-runtime-specific layer.
type State struct {
	TaskID   string
	TaskName string
	PRURL    string
	Focus    Panel

	// Terminal scrollback
	ScrollOffset int

	// Last output from a finished session (for post-exit display)
	LastOutput []byte

	// Worktree directory resolved for git commands
	WorktreeDir string

	// Git status refresh timing
	LastGitRefresh time.Time

	// Diff viewer
	Diff DiffState
}

// gitRefreshInterval is the minimum time between git status refreshes.
// Must match internal/ui/gitstatus.go's gitRefreshInterval (3s).
const gitRefreshInterval = 3 * time.Second

// New returns a State with sensible defaults.
func New() State {
	return State{
		Focus: PanelTerminal,
		Diff:  DiffState{Split: true},
	}
}

// Reset prepares the state for displaying a new task.
func (s *State) Reset(taskID, taskName string) {
	s.TaskID = taskID
	s.TaskName = taskName
	s.PRURL = ""
	s.Focus = PanelTerminal
	s.ScrollOffset = 0
	s.LastOutput = nil
	s.WorktreeDir = ""
	s.LastGitRefresh = time.Time{}
	s.Diff = DiffState{Split: true}
}

// NeedsGitRefresh reports whether git status should be refreshed.
func (s *State) NeedsGitRefresh() bool {
	if s.TaskID == "" {
		return false
	}
	return time.Since(s.LastGitRefresh) > gitRefreshInterval
}

// FocusLeft moves focus toward the terminal panel (leftmost focusable).
func (s *State) FocusLeft() {
	if s.Focus > PanelTerminal {
		s.Focus--
	}
}

// FocusRight moves focus toward the files panel (rightmost focusable).
func (s *State) FocusRight() {
	if s.Focus < PanelFiles {
		s.Focus++
	}
}

// ScrollUp scrolls the terminal view up by n lines.
// maxLines is the total number of cached lines; pass 0 when unknown (e.g.,
// incremental render mode before cachedLines is populated) to allow free
// growth — clamping only applies when maxLines > 0.
// dispH is the visible display height.
func (s *State) ScrollUp(n, maxLines, dispH int) {
	s.ScrollOffset += n
	if maxLines > 0 {
		maxScroll := max(maxLines-dispH, 0)
		if s.ScrollOffset > maxScroll {
			s.ScrollOffset = maxScroll
		}
	}
}

// ScrollDown scrolls the terminal view down (toward tail) by n lines.
func (s *State) ScrollDown(n int) {
	s.ScrollOffset -= n
	if s.ScrollOffset < 0 {
		s.ScrollOffset = 0
	}
}

// DiffScrollUp scrolls the diff viewer up by n lines.
func (s *State) DiffScrollUp(n int) {
	s.Diff.ScrollOff -= n
	if s.Diff.ScrollOff < 0 {
		s.Diff.ScrollOff = 0
	}
}

// DiffScrollDown scrolls the diff viewer down by n lines.
// lineCount is the total number of diff lines.
// visibleRows is the number of rows visible in the diff panel.
func (s *State) DiffScrollDown(n, lineCount, visibleRows int) {
	maxOff := max(lineCount-visibleRows, 0)
	s.Diff.ScrollOff = min(s.Diff.ScrollOff+n, maxOff)
}

// EnterDiff puts the view into diff mode for the given file.
func (s *State) EnterDiff(fileName string) {
	s.Diff.Active = true
	s.Diff.ScrollOff = 0
	s.Diff.FileName = fileName
}

// ExitDiff leaves diff mode and resets diff state.
// Split is preserved — the user's split/unified preference persists across
// diff opens within a task session (matching exitDiffMode in agentview.go).
func (s *State) ExitDiff() {
	s.Diff.Active = false
	s.Diff.ScrollOff = 0
	s.Diff.FileName = ""
}
