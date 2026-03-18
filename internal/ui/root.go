package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	dclient "github.com/drn/argus/internal/daemon/client"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/github"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// prURLRe matches GitHub pull request URLs in agent output.
var prURLRe = regexp.MustCompile(`https://github\.com/[a-zA-Z0-9_.\-]+/[a-zA-Z0-9_.\-]+/pull/\d+`)

type view int

const (
	viewTaskList view = iota
	viewNewTask
	viewHelp
	viewPrompt
	viewConfirmDelete
	viewNewProject
	viewEditProject
	viewConfirmDeleteProject
	viewConfirmDestroy
	viewPruning
	viewAgent
	viewSandboxConfig
	viewDaemonRestart
	viewDaemonLogs
	viewUXLogs
	viewRenameTask
	viewNewBackend
	viewEditBackend
)

type tab int

const (
	tabTasks tab = iota
	tabReviews
	tabSettings
)

// minAgentRunTime is the minimum time an agent must run before a clean exit
// is treated as a successful completion. Exits faster than this are treated
// as startup or configuration errors (the session ID is cleared so the user
// can retry).
const minAgentRunTime = 3 * time.Second

// TickMsg is sent periodically to update elapsed times.
type TickMsg struct{}

// AgentFinishedMsg is sent when an agent process exits.
type AgentFinishedMsg struct {
	TaskID     string
	Err        error
	Stopped    bool   // true if the process was explicitly stopped via Runner.Stop
	LastOutput []byte // final ring buffer contents for displaying errors
	StreamLost bool   // true when stream lost, not a real process exit
}

// AgentDetachedMsg is sent when the user detaches from a running agent.
type AgentDetachedMsg struct {
	TaskID string
}

// ResolveTaskDirMsg carries the result of async worktree directory resolution.
type ResolveTaskDirMsg struct {
	TaskID string
	Dir    string
}

// SessionResumedMsg is sent when a background session resume completes.
type SessionResumedMsg struct {
	TaskID string
	PID    int
	Err    error
}

// PruneProgressMsg signals that one worktree cleanup has completed.
// All cleanups run in parallel; each goroutine sends one of these.
type PruneProgressMsg struct{}

// DaemonRestartedMsg carries the result of a daemon restart attempt.
type DaemonRestartedMsg struct {
	Client *dclient.Client
	Err    error
}

// DaemonLogsMsg carries the loaded daemon log lines.
type DaemonLogsMsg struct {
	Lines []string
	Err   error
}

// UXLogsMsg carries the loaded UX log lines.
type UXLogsMsg struct {
	Lines []string
	Err   error
}

// FetchPRListMsg carries the result of a GitHub PR list fetch.
type FetchPRListMsg struct {
	PRs []github.PR
	Err error
}

// FetchPRFilesMsg carries changed files for a PR.
type FetchPRFilesMsg struct {
	Files []string
	Err   error
}

// FetchPRDiffMsg carries the raw diff for a single PR file.
type FetchPRDiffMsg struct {
	Diff string
	Err  error
}

// FetchPRCommentsMsg carries comments for a PR.
type FetchPRCommentsMsg struct {
	Comments []github.PRComment
	Err      error
}

// PostCommentMsg carries the result of posting a review comment.
type PostCommentMsg struct {
	Err error
}

// SubmitReviewMsg carries the result of submitting a full review.
type SubmitReviewMsg struct {
	Err error
}

// PRDetectedMsg is sent when a GitHub PR URL is found in agent output.
type PRDetectedMsg struct {
	TaskID string
	URL    string
}

// Model is the top-level Bubble Tea model.
type Model struct {
	db          *db.DB
	runner      agent.SessionProvider
	keys        KeyMap
	theme       Theme
	tasklist    TaskList
	settings    SettingsView
	reviews     *ReviewsView
	statusbar   StatusBar
	helpview    HelpView
	newtask     NewTaskForm
	renametask  RenameTaskForm
	projectform   ProjectForm
	backendform   BackendForm
	sandboxconfig SandboxConfigForm // sandbox settings editor
	preview     Preview
	gitstatus   *GitStatus
	detail      TaskDetail
	agentview   *AgentView
	taskLayout  PanelLayout
	current      view
	activeTab    tab
	width        int
	height       int
	quitting           bool
	agentTickActive    bool              // true while the 100ms AgentViewTickMsg chain is running
	daemonConnected    bool              // true when connected to daemon (sessions persist)
	daemonRestarting   bool              // true while daemon restart is in progress
	daemonFailures     int               // consecutive daemon ping failures
	resolvedDirs       map[string]string // taskID → resolved worktree dir (cache)
	idleUnvisited      map[string]bool   // task IDs idle since user last opened their agent view
	viewedWhileAgent   map[string]bool   // tasks viewed in agent view; suppresses idleUnvisited re-add

	// Prune progress state (shown in viewPruning modal)
	pruneTotal   int // total worktrees being cleaned up
	pruneCurrent int // number completed so far (0 = starting)

	// Daemon log viewer state
	daemonLogLines  []string // lines of the daemon log file
	daemonLogOffset int      // scroll offset for log viewer

	// UX log viewer state
	uxLogLines  []string // lines of the UX log file
	uxLogOffset int      // scroll offset for log viewer

	// program is set by SetProgram so daemon restart can register
	// OnSessionExit on the new client. Shared via pointer indirection
	// so Bubble Tea's value-receiver copies all see the same reference.
	program **tea.Program

	// restartedClient holds the new daemon client after a restart so
	// runTUI can close it on exit. Shared via pointer indirection.
	restartedClient **dclient.Client
}

// NewModel creates the top-level model. Set daemonConnected to true when the
// TUI is backed by a daemon process (sessions persist across restarts).
func NewModel(database *db.DB, runner agent.SessionProvider, daemonConnected bool) Model {
	theme := DefaultTheme()
	keys := DefaultKeyMap()

	tl := NewTaskList(theme)
	sv := NewSettingsView(theme)
	rv := NewReviewsView(theme)
	sb := NewStatusBar(theme)
	hv := NewHelpView(keys, theme)

	pv := NewPreview(theme, runner)
	gs := NewGitStatus(theme)
	dt := NewTaskDetail(theme)
	avv := NewAgentView(theme, runner, agent.SessionsDir())
	av := &avv

	m := Model{
		db:              database,
		runner:          runner,
		keys:            keys,
		theme:           theme,
		daemonConnected: daemonConnected,
		resolvedDirs:     make(map[string]string),
		idleUnvisited:    make(map[string]bool),
		viewedWhileAgent: make(map[string]bool),
		program:         new(*tea.Program),
		restartedClient: new(*dclient.Client),
		tasklist:    tl,
		settings:    sv,
		reviews:     rv,
		statusbar:   sb,
		helpview:    hv,
		preview:     pv,
		gitstatus:   &gs,
		detail:      dt,
		agentview:   av,
		taskLayout: NewPanelLayout([]PanelConfig{
			{Pct: 20, Min: 20},
			{Pct: 60, Min: 60},
			{Pct: 20, Min: 20},
		}),
		current:     viewTaskList,
		activeTab:   tabTasks,
	}
	m.refreshTasks()
	m.refreshSettings()
	return m
}

// RestartedClient returns the daemon client created during a restart, or nil.
// Called by runTUI after the program exits to ensure proper cleanup.
func (m Model) RestartedClient() *dclient.Client {
	if m.restartedClient == nil {
		return nil
	}
	return *m.restartedClient
}

// SetProgram stores the tea.Program reference so daemon restart can register
// the OnSessionExit callback on the new client. The `program` field is a
// double-pointer allocated in NewModel, so all Bubble Tea value copies
// share the same underlying *tea.Program slot.
func (m *Model) SetProgram(p *tea.Program) {
	*m.program = p
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tea.Tick(time.Second, func(_ time.Time) tea.Msg {
			return TickMsg{}
		}),
	}

	// Check if the runner already has sessions (e.g., daemon client discovered them).
	// If so, just sync state — don't try to resume.
	daemonRunning := len(m.runner.Running()) > 0

	uxlog.Log("Init: daemonRunning=%v, resuming in-progress tasks...", daemonRunning)

	if !daemonRunning {
		// Resume sessions for in-progress tasks that have a saved session ID.
		// Each resume runs in a background goroutine so the UI stays responsive.
		for _, t := range m.db.Tasks() {
			if t.Status == model.StatusInProgress && t.SessionID != "" {
				task := t // capture loop variable
				uxlog.Log("Init: resuming task %s (%s) session=%s", task.ID, task.Name, task.SessionID)

				// Kill any orphaned process from a previous Argus session.
				// When Argus exits, PTY master fds close and children get SIGHUP,
				// but we clean up any that might linger before resuming.
				if task.AgentPID > 0 {
					killStaleProcess(task.AgentPID)
					task.AgentPID = 0
				}

				// Backfill worktree path for tasks that don't have one stored.
				if task.Worktree == "" && task.Name != "" && task.Project != "" {
					if wt := discoverWorktree(task.Project, task.Name); wt != "" {
						task.Worktree = wt
						m.resolvedDirs[task.ID] = wt
					}
				}

				// If we still have no worktree, don't resume — revert to pending.
				if task.Worktree == "" {
					uxlog.Log("Init: no worktree for task %s (%s), reverting to pending", task.ID, task.Name)
					task.SetStatus(model.StatusPending)
					task.SessionID = ""
					task.StartedAt = time.Time{}
					_ = m.db.Update(task)
					continue
				}

				// Reset StartedAt before starting so the quick-exit check in
				// handleAgentFinished uses the resume time, not the original start.
				task.StartedAt = time.Now()
				_ = m.db.Update(task)

				cmds = append(cmds, func() tea.Msg {
					sess, err := m.runner.Start(task, m.db.Config(), 24, 80, true)
					if err != nil {
						return SessionResumedMsg{TaskID: task.ID, Err: err}
					}
					return SessionResumedMsg{TaskID: task.ID, PID: sess.PID()}
				})
			}
		}
	}

	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Reserve space: tab header(1) + statusbar(1)
		contentHeight := msg.Height - 2

		// Tasks tab: three-panel layout
		m.taskLayout.SetSize(msg.Width, contentHeight)
		widths := m.taskLayout.SplitWidths()
		m.tasklist.SetSize(widths[0]-2, contentHeight-2)
		gitH, previewH := m.splitCenterHeights(contentHeight)
		m.gitstatus.SetSize(widths[1], gitH)
		m.preview.SetSize(widths[1], previewH)
		m.detail.SetSize(widths[2], contentHeight)

		// Settings tab: 20% margin | 20% left | 40% right | 20% margin
		_, settingsLeft, _ := m.settingsWidths()
		m.settings.SetSize(settingsLeft, contentHeight)

		// Reviews tab
		m.reviews.SetSize(msg.Width, contentHeight)

		m.statusbar.SetWidth(msg.Width)
		m.newtask.SetSize(msg.Width, msg.Height)
		m.renametask.SetSize(msg.Width, msg.Height)
		m.projectform.SetSize(msg.Width, msg.Height)
		m.backendform.SetSize(msg.Width, msg.Height)
		m.sandboxconfig.SetSize(msg.Width, msg.Height)
		m.agentview.SetSize(msg.Width, msg.Height)
		return m, nil

	case TickMsg:
		var cmds []tea.Cmd
		cmds = append(cmds, tea.Tick(time.Second, func(_ time.Time) tea.Msg {
			return TickMsg{}
		}))
		// Skip task list refresh while in agent view or daemon restart —
		// in agent view it's not visible, during restart the runner is
		// being swapped and RPCs would timeout against the dead socket.
		if m.current != viewAgent && !m.daemonRestarting {
			m.refreshTasks()
			m.tasklist.Tick()
			if m.activeTab == tabSettings {
				m.settings.SetTasks(m.db.Tasks())
			}
		}
		if !m.daemonRestarting {
			if cmd := m.scheduleGitRefresh(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		// Reviews tab: auto-refresh stale data while the user is actively viewing.
		// - Comments: TTL 2 min, OR if PR.UpdatedAt is newer than last fetch.
		// - Diff: if PR.UpdatedAt is newer than when we fetched it (free check).
		if m.activeTab == tabReviews {
			if pr := m.reviews.SelectedPR(); pr != nil {
				if m.reviews.areCommentsStale() {
					cmds = append(cmds, fetchPRCommentsCmd(pr.RepoOwner, pr.Repo, pr.Number))
				}
				if m.reviews.isDiffStale() {
					// Re-fetch full diff; applyFileDiff will re-slice current file.
					cmds = append(cmds, fetchPRFullDiffCmd(pr.RepoOwner, pr.Repo, pr.Number))
				}
			}
		}
		// Scan in-progress sessions for GitHub PR URLs.
		// Always take the last match so that if the agent opens multiple PRs,
		// 'o' opens the most recent one.
		for _, taskID := range m.runner.Running() {
			if sess := m.runner.Get(taskID); sess != nil {
				// Only scan the last 32KB — PR URLs appear near the end of output.
				// Avoids copying the entire ring buffer on every tick.
				matches := prURLRe.FindAllString(string(sess.RecentOutputTail(32*1024)), -1)
				if len(matches) > 0 {
					url := matches[len(matches)-1]
					if t, err := m.db.Get(taskID); err == nil && t.PRURL != url {
						tid := taskID
						cmds = append(cmds, func() tea.Msg {
							return PRDetectedMsg{TaskID: tid, URL: url}
						})
					}
				}
			}
		}
		// Daemon health check — auto-restart if N consecutive ping failures.
		if m.daemonConnected && !m.daemonRestarting {
			if client, ok := m.runner.(*dclient.Client); ok {
				if err := client.Ping(); err != nil {
					m.daemonFailures++
					uxlog.Log("TickMsg: daemon ping failed (attempt %d) err=%v", m.daemonFailures, err)
					if m.daemonFailures >= 3 {
						uxlog.Log("TickMsg: daemon unresponsive, auto-restarting")
						m.daemonFailures = 0
						m.daemonRestarting = true
						return m, m.restartDaemonCmd()
					}
				} else {
					m.daemonFailures = 0
				}
			}
		}
		return m, tea.Batch(cmds...)

	case ResolveTaskDirMsg:
		if msg.Dir != "" {
			m.resolvedDirs[msg.TaskID] = msg.Dir
			// Persist discovered worktree only if it's actually inside the
			// worktree directory — never persist the project dir as a worktree.
			if t, err := m.db.Get(msg.TaskID); err == nil && t.Worktree == "" && isWorktreeSubdir(msg.Dir) {
				t.Worktree = msg.Dir
				_ = m.db.Update(t)
			}
			// Set worktree dir on agent view immediately.
			if m.current == viewAgent && m.agentview.taskID == msg.TaskID {
				m.agentview.SetWorktreeDir(msg.Dir)
			}
		}
		// Now that we have the dir, kick off git status fetch
		if t := m.selectedTaskForGit(); t != nil && t.ID == msg.TaskID && msg.Dir != "" {
			m.gitstatus.SetTask(t.ID)
			taskID := t.ID
			dir := msg.Dir
			return m, func() tea.Msg {
				return FetchGitStatus(taskID, dir)
			}
		}
		return m, nil

	case AgentViewTickMsg:
		// Fast tick for agent view — just triggers a re-render.
		// The chain stops itself when we leave agent view and clears
		// the flag so startOrAttach can restart it next time.
		if m.current == viewAgent {
			return m, tea.Tick(100*time.Millisecond, func(_ time.Time) tea.Msg {
				return AgentViewTickMsg{}
			})
		}
		m.agentTickActive = false
		return m, nil

	case GitStatusRefreshMsg:
		m.gitstatus.Update(msg)
		m.agentview.UpdateGitStatus(msg)
		return m, nil

	case FileDiffMsg:
		m.agentview.UpdateFileDiff(msg)
		return m, nil

	case DirFilesMsg:
		m.agentview.UpdateDirFiles(msg)
		return m, nil

	case SessionLogLoadedMsg:
		if m.agentview.taskID == msg.TaskID {
			m.agentview.SetLastOutput(msg.Data)
		}
		return m, nil

	case tea.MouseMsg:
		if m.current == viewAgent {
			m.agentview.HandleMouse(msg)
		}
		if m.current == viewDaemonLogs {
			m.handleDaemonLogsMouse(msg)
		}
		if m.current == viewUXLogs {
			m.handleUXLogsMouse(msg)
		}
		if m.activeTab == tabReviews {
			m.reviews.HandleMouse(msg)
		}
		return m, nil

	case SessionResumedMsg:
		return m.handleSessionResumed(msg)

	case AgentFinishedMsg:
		return m.handleAgentFinished(msg)

	case PRDetectedMsg:
		if t, err := m.db.Get(msg.TaskID); err == nil && t.PRURL != msg.URL {
			t.PRURL = msg.URL
			_ = m.db.Update(t)
			m.refreshTasks()
		}
		return m, nil

	case PruneProgressMsg:
		m.pruneCurrent++
		if m.pruneCurrent >= m.pruneTotal {
			// All parallel cleanups done.
			m.current = viewTaskList
			m.refreshTasks()
			return m, nil
		}
		// Still waiting for remaining parallel goroutines.
		return m, nil

	case DaemonRestartedMsg:
		m.daemonRestarting = false
		m.daemonFailures = 0
		uxlog.Log("DaemonRestartedMsg: err=%v hasClient=%v", msg.Err, msg.Client != nil)
		if msg.Err != nil {
			m.daemonConnected = false
			m.statusbar.SetError("daemon restart failed: " + msg.Err.Error())
			m.current = viewTaskList
			m.refreshSettings()
			return m, nil
		}
		// Swap runner to new daemon client. Don't close the old client
		// here — runTUI's defer client.Close() handles that.
		if msg.Client != nil && *m.program != nil {
			p := *m.program
			msg.Client.OnSessionExit(func(taskID string, info daemon.ExitInfo) {
				var exitErr error
				if info.Err != "" {
					exitErr = errors.New(info.Err)
				}
				p.Send(AgentFinishedMsg{
					TaskID:     taskID,
					Err:        exitErr,
					Stopped:    info.Stopped,
					LastOutput: info.LastOutput,
					StreamLost: info.StreamLost,
				})
			})
		}
		m.runner = msg.Client
		m.daemonConnected = true
		m.preview.runner = msg.Client
		m.agentview.runner = msg.Client
		*m.restartedClient = msg.Client

		// Reset in-progress tasks — daemon restart killed all sessions.
		// Keep SessionID so re-launching the task resumes the conversation
		// via --resume (Claude Code persists session state to disk).
		for _, t := range m.db.Tasks() {
			if t.Status == model.StatusInProgress {
				t.SetStatus(model.StatusPending)
				t.AgentPID = 0
				t.StartedAt = time.Time{}
				_ = m.db.Update(t)
			}
		}
		m.current = viewTaskList
		m.refreshTasks()
		m.refreshSettings()
		return m, nil

	case DaemonLogsMsg:
		if msg.Err != nil {
			m.statusbar.SetError("failed to read daemon log: " + msg.Err.Error())
			return m, nil
		}
		m.daemonLogLines = msg.Lines
		m.daemonLogOffset = max(0, len(msg.Lines)-m.daemonLogVisibleLines())
		m.current = viewDaemonLogs
		return m, nil

	case UXLogsMsg:
		if msg.Err != nil {
			m.statusbar.SetError("failed to read UX log: " + msg.Err.Error())
			return m, nil
		}
		m.uxLogLines = msg.Lines
		m.uxLogOffset = max(0, len(msg.Lines)-m.daemonLogVisibleLines())
		m.current = viewUXLogs
		return m, nil

	case AgentDetachedMsg:
		// User detached — agent still running in background
		m.refreshTasks()
		return m, nil

	case FetchPRListMsg:
		if msg.Err != nil {
			uxlog.Log("[reviews] fetch PR list failed: %v", msg.Err)
			m.reviews.SetLoadError(githubErrMsg(msg.Err))
		} else {
			uxlog.Log("[reviews] fetched %d PRs", len(msg.PRs))
			m.reviews.SetPRs(msg.PRs)
		}
		return m, nil

	case FetchPRFilesMsg:
		if msg.Err != nil {
			uxlog.Log("[reviews] fetch PR files failed: %v", msg.Err)
			m.statusbar.SetError(githubErrMsg(msg.Err))
		} else {
			uxlog.Log("[reviews] fetched %d files for PR", len(msg.Files))
			m.reviews.SetFiles(msg.Files)
		}
		return m, nil

	case FetchPRDiffMsg:
		if msg.Err != nil {
			uxlog.Log("[reviews] fetch PR diff failed: %v", msg.Err)
			m.statusbar.SetError(githubErrMsg(msg.Err))
		} else {
			uxlog.Log("[reviews] fetched full diff (%d bytes)", len(msg.Diff))
			// SetFullDiff caches the complete PR diff and extracts the current
			// file's slice immediately — subsequent file changes are free.
			m.reviews.SetFullDiff(msg.Diff)
		}
		return m, nil

	case FetchPRCommentsMsg:
		if msg.Err != nil {
			uxlog.Log("[reviews] fetch comments failed: %v", msg.Err)
			m.statusbar.SetError(githubErrMsg(msg.Err))
		} else {
			uxlog.Log("[reviews] fetched %d comments", len(msg.Comments))
			m.reviews.SetComments(msg.Comments)
		}
		return m, nil

	case PostCommentMsg:
		if msg.Err != nil {
			uxlog.Log("[reviews] post comment failed: %v", msg.Err)
			m.statusbar.SetError(githubErrMsg(msg.Err))
		} else {
			uxlog.Log("[reviews] comment posted successfully")
		}
		// Refresh comments after posting
		if pr := m.reviews.SelectedPR(); pr != nil {
			return m, fetchPRCommentsCmd(pr.RepoOwner, pr.Repo, pr.Number)
		}
		return m, nil

	case SubmitReviewMsg:
		if msg.Err != nil {
			uxlog.Log("[reviews] submit review failed: %v", msg.Err)
			m.statusbar.SetError(githubErrMsg(msg.Err))
		} else {
			uxlog.Log("[reviews] review submitted successfully")
		}
		return m, nil

	case detectBranchMsg:
		if m.current == viewNewProject || m.current == viewEditProject {
			cmd := m.projectform.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		m.statusbar.ClearError()
		return m.handleKey(msg)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.current {
	case viewNewTask:
		return m.handleNewTaskKey(msg)
	case viewRenameTask:
		return m.handleRenameTaskKey(msg)
	case viewNewProject:
		return m.handleNewProjectKey(msg)
	case viewEditProject:
		return m.handleEditProjectKey(msg)
	case viewHelp:
		m.current = viewTaskList
		return m, nil
	case viewPrompt:
		m.current = viewTaskList
		return m, nil
	case viewConfirmDelete:
		return m.handleConfirmDeleteKey(msg)
	case viewConfirmDestroy:
		return m.handleConfirmDestroyKey(msg)
	case viewConfirmDeleteProject:
		return m.handleConfirmDeleteProjectKey(msg)
	case viewDaemonLogs:
		return m.handleDaemonLogsKey(msg)
	case viewUXLogs:
		return m.handleUXLogsKey(msg)
	case viewPruning, viewDaemonRestart:
		// Absorb all keys while pruning or restarting — no interaction allowed.
		return m, nil
	case viewSandboxConfig:
		return m.handleSandboxConfigKey(msg)
	case viewNewBackend, viewEditBackend:
		return m.handleBackendFormKey(msg)
	case viewAgent:
		return m.handleAgentViewKey(msg)
	default:
		// Tab switching with 1/2/3 keys or left/right arrows
		switch msg.String() {
		case "1":
			m.activeTab = tabTasks
			return m, nil
		case "2":
			m.activeTab = tabReviews
			if m.reviews.canFetchPRList() {
				return m, m.reviews.StartLoading()
			}
			return m, nil
		case "3":
			m.activeTab = tabSettings
			return m, nil
		}
		switch {
		case key.Matches(msg, m.keys.TabLeft):
			if m.activeTab > tabTasks {
				m.activeTab--
			}
			return m, nil
		case key.Matches(msg, m.keys.TabRight):
			if m.activeTab < tabSettings {
				m.activeTab++
				if m.activeTab == tabReviews && m.reviews.canFetchPRList() {
					return m, m.reviews.StartLoading()
				}
			}
			return m, nil
		}
		if m.activeTab == tabSettings {
			return m.handleSettingsKey(msg)
		}
		if m.activeTab == tabReviews {
			return m.handleReviewsKey(msg)
		}
		return m.handleTaskListKey(msg)
	}
}

func (m Model) handleAgentViewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Cmd+Up/Down (sent as Alt+Up/Down by terminals) switches to adjacent task.
	if msg.Alt {
		switch msg.Type {
		case tea.KeyUp:
			return m.switchAgentTask(-1)
		case tea.KeyDown:
			return m.switchAgentTask(+1)
		}
	}

	detach, cmd := m.agentview.HandleKey(msg)
	if detach {
		m.current = viewTaskList
		m.refreshTasks()
		return m, nil
	}
	return m, cmd
}

func (m Model) handleReviewsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global quit
	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit
	}
	cmd := m.reviews.HandleKey(msg)
	return m, cmd
}

// switchAgentTask navigates to the adjacent task's agent view (dir: -1=prev, +1=next).
func (m Model) switchAgentTask(dir int) (tea.Model, tea.Cmd) {
	next := m.tasklist.AdjacentTask(m.agentview.taskID, dir)
	if next == nil {
		return m, nil
	}
	return m.startOrAttach(next)
}

func (m Model) handleTaskListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		m.quitting = true
		return m, tea.Quit

	case key.Matches(msg, m.keys.Up):
		m.tasklist.CursorUp()
		return m, m.scheduleGitRefresh()

	case key.Matches(msg, m.keys.Down):
		m.tasklist.CursorDown()
		return m, m.scheduleGitRefresh()

	case key.Matches(msg, m.keys.New):
		defaultProject := ""
		if t := m.tasklist.Selected(); t != nil {
			defaultProject = t.Project
		}
		cfg := m.db.Config()
		m.newtask = NewNewTaskForm(m.theme, m.db.Projects(), defaultProject, cfg.Backends, cfg.Defaults.Backend)
		m.newtask.SetSize(m.width, m.height)
		m.current = viewNewTask
		return m, nil

	case key.Matches(msg, m.keys.StatusFwd):
		if t := m.tasklist.Selected(); t != nil {
			t.SetStatus(t.Status.Next())
			_ = m.db.Update(t)
			m.refreshTasks()
		}
		return m, nil

	case key.Matches(msg, m.keys.StatusRev):
		if t := m.tasklist.Selected(); t != nil {
			t.SetStatus(t.Status.Prev())
			_ = m.db.Update(t)
			m.refreshTasks()
		}
		return m, nil

	case key.Matches(msg, m.keys.Delete):
		if m.tasklist.Selected() != nil {
			m.current = viewConfirmDelete
		}
		return m, nil

	case key.Matches(msg, m.keys.Help):
		m.current = viewHelp
		return m, nil

	case key.Matches(msg, m.keys.Attach):
		return m.attachAgent()

	case key.Matches(msg, m.keys.Prompt):
		if t := m.tasklist.Selected(); t != nil && t.Status != model.StatusComplete {
			m.current = viewPrompt
		}
		return m, nil

	case key.Matches(msg, m.keys.Destroy):
		if m.tasklist.Selected() != nil {
			m.current = viewConfirmDestroy
		}
		return m, nil

	case msg.String() == "r":
		if t := m.tasklist.Selected(); t != nil {
			m.renametask = NewRenameTaskForm(m.theme, t.ID, t.Name)
			m.renametask.SetSize(m.width, m.height)
			m.current = viewRenameTask
		}
		return m, nil

	case msg.String() == "a":
		if t := m.tasklist.Selected(); t != nil {
			t.Archived = !t.Archived
			_ = m.db.Update(t)
			m.refreshTasks()
		}
		return m, nil

	case msg.String() == "o":
		if t := m.tasklist.Selected(); t != nil && t.PRURL != "" {
			url := t.PRURL
			return m, func() tea.Msg {
				exec.Command("open", url).Start() //nolint:errcheck
				return nil
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.Prune):
		pruned, err := m.db.PruneCompleted()
		if err != nil {
			m.statusbar.SetError(err.Error())
			return m, nil
		}
		if len(pruned) == 0 {
			return m, nil
		}

		// Stop sessions synchronously (fast, in-process).
		for _, t := range pruned {
			if m.runner.HasSession(t.ID) {
				_ = m.runner.Stop(t.ID)
			}
		}

		// Check if any worktree cleanup is needed.
		cfg := m.db.Config()
		needsCleanup := cfg.UI.ShouldCleanupWorktrees()
		var toClean []*model.Task
		if needsCleanup {
			for _, t := range pruned {
				if t.Worktree != "" {
					toClean = append(toClean, t)
				}
			}
		}

		if len(toClean) == 0 {
			// No worktree cleanup needed — just refresh immediately.
			m.refreshTasks()
			return m, nil
		}

		// Show progress modal and run all cleanups in parallel.
		m.pruneTotal = len(toClean)
		m.pruneCurrent = 0
		m.current = viewPruning

		cmds := make([]tea.Cmd, len(toClean))
		for i, t := range toClean {
			cmds[i] = pruneOneCmd(t, cfg)
		}
		return m, tea.Batch(cmds...)
	}

	return m, nil
}

// pruneOneCmd cleans a single worktree and signals completion via PruneProgressMsg.
// All pruneOneCmds are batched so they run in parallel.
func pruneOneCmd(t *model.Task, cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		repoDir := agent.ResolveDir(t, cfg)
		removeWorktreeAndBranch(t.Worktree, t.Branch, repoDir)
		return PruneProgressMsg{}
	}
}

func (m Model) attachAgent() (tea.Model, tea.Cmd) {
	t := m.tasklist.Selected()
	if t == nil {
		return m, nil
	}
	if t.Status == model.StatusComplete {
		m.statusbar.SetError("cannot attach to a completed task")
		return m, nil
	}
	return m.startOrAttach(t)
}

func (m Model) startOrAttach(t *model.Task) (tea.Model, tea.Cmd) {
	uxlog.Log("startOrAttach: task=%s (%s) status=%s session=%s worktree=%s",
		t.ID, t.Name, t.Status, t.SessionID, t.Worktree)

	// If session already exists in runner, switch to agent view
	if m.runner.Get(t.ID) != nil {
		uxlog.Log("startOrAttach: session already exists for %s, attaching", t.ID)
		return m, m.enterAgentView(t.ID, t.Name)
	}

	// Never start an agent without worktree isolation.
	if t.Worktree == "" {
		uxlog.Log("startOrAttach: BLOCKED task=%s has no worktree", t.ID)
		m.statusbar.SetError("task has no worktree — delete and recreate it")
		return m, nil
	}

	// Determine if this is a resume. Claude-style backends use SessionID;
	// Codex-style backends (ResumeCommand != "") have no session pinning,
	// so detect resume by checking if the task was previously started and
	// is still pending (not completed — a completed task reset to pending
	// should start fresh, not resume).
	resume := t.SessionID != ""
	if !resume && !t.StartedAt.IsZero() && t.Status == model.StatusPending {
		// Previously started, no pinned session, and pending — resume for Codex-style backends.
		resume = true
	}
	if !resume {
		// Generate session ID for Claude-style backends (new session).
		cfg := m.db.Config()
		backend, _ := agent.ResolveBackend(t, cfg)
		if backend.ResumeCommand == "" {
			t.SessionID = model.GenerateSessionID()
		}
	}
	uxlog.Log("startOrAttach: resume=%v session=%s", resume, t.SessionID)

	// Cache the worktree dir (already set during task creation).
	if t.Worktree != "" {
		m.resolvedDirs[t.ID] = t.Worktree
	}

	// Persist status and StartedAt BEFORE starting the process so that
	// handleAgentFinished always sees fresh data even if the process exits
	// immediately (race between Start returning and the wait goroutine).
	t.SetStatus(model.StatusInProgress)
	t.StartedAt = time.Now()
	_ = m.db.Update(t)

	// Start a new session — use agent view panel dimensions for PTY size
	avWidths := m.agentview.layout.SplitWidths()
	centerW := avWidths[1]
	contentH := m.height - 1
	ptyRows := uint16(max(contentH-4, 10))
	ptyCols := uint16(max(centerW-4, 40))
	uxlog.Log("startOrAttach: starting session pty=%dx%d", ptyCols, ptyRows)
	sess, err := m.runner.Start(t, m.db.Config(), ptyRows, ptyCols, resume)
	if err != nil {
		// Start failed — revert status and session ID so the task
		// doesn't appear as "in progress" with no running agent.
		uxlog.Log("startOrAttach: START FAILED task=%s err=%v", t.ID, err)
		t.SetStatus(model.StatusPending)
		t.SessionID = ""
		t.StartedAt = time.Time{}
		_ = m.db.Update(t)
		m.statusbar.SetError(err.Error())
		return m, nil
	}

	uxlog.Log("startOrAttach: started task=%s pid=%d", t.ID, sess.PID())
	t.AgentPID = sess.PID()
	_ = m.db.Update(t)
	m.refreshTasks()

	return m, m.enterAgentView(t.ID, t.Name)
}

// enterAgentView is the single entry point for switching to agent view.
// It initializes the agent view state and starts the 100ms tick chain
// (if not already running). All agent view entry must go through here
// to prevent tick accumulation from multiple start points.
func (m *Model) enterAgentView(taskID, taskName string) tea.Cmd {
	// User is viewing the agent — clear the "idle unvisited" flag so the task
	// no longer displays as "in review" in the task list. Also sync the copy on
	// TaskList immediately (rather than waiting for the next tick).
	delete(m.idleUnvisited, taskID)
	m.viewedWhileAgent[taskID] = true
	idleUnvisited := make([]string, 0, len(m.idleUnvisited))
	for id := range m.idleUnvisited {
		idleUnvisited = append(idleUnvisited, id)
	}
	m.tasklist.SetIdleUnvisited(idleUnvisited)
	m.agentview.Enter(taskID, taskName)
	m.agentview.SetSize(m.width, m.height)
	if dir, ok := m.resolvedDirs[taskID]; ok && dir != "" {
		m.agentview.SetWorktreeDir(dir)
	}
	m.current = viewAgent
	logCmd := m.agentview.LoadSessionLogCmd(taskID)
	// Start the 100ms tick chain if not already running. The chain
	// self-perpetuates via the AgentViewTickMsg handler and stops
	// itself when m.current leaves viewAgent.
	if m.agentTickActive {
		return logCmd
	}
	m.agentTickActive = true
	return tea.Batch(
		logCmd,
		tea.Tick(100*time.Millisecond, func(_ time.Time) tea.Msg {
			return AgentViewTickMsg{}
		}),
	)
}

// determinePostExitStatus decides what status a task should have after its
// agent process exits. Returns the new status and whether it was a quick exit
// (error/too-fast) that should keep the agent view open.
func determinePostExitStatus(msg AgentFinishedMsg, t *model.Task) (newStatus model.Status, clearSession bool, quickExit bool) {
	if msg.Stopped {
		return model.StatusInReview, false, false
	}
	if msg.Err != nil {
		return t.Status, true, true
	}
	if !t.StartedAt.IsZero() && time.Since(t.StartedAt) < minAgentRunTime {
		return t.Status, true, true
	}
	if t.Worktree != "" && !dirExists(t.Worktree) {
		return model.StatusComplete, false, false
	}
	return model.StatusComplete, false, false
}

func (m Model) handleAgentFinished(msg AgentFinishedMsg) (tea.Model, tea.Cmd) {
	uxlog.Log("handleAgentFinished: task=%s err=%v stopped=%v streamLost=%v lastOutput=%d bytes",
		msg.TaskID, msg.Err, msg.Stopped, msg.StreamLost, len(msg.LastOutput))

	if msg.StreamLost {
		uxlog.Log("handleAgentFinished: stream lost task=%s — keeping InProgress", msg.TaskID)
		m.statusbar.SetError(fmt.Sprintf("stream lost for task %s — press Enter to reconnect", msg.TaskID))
		m.refreshTasks()
		return m, nil
	}

	t, err := m.db.Get(msg.TaskID)
	if err != nil {
		uxlog.Log("handleAgentFinished: db.Get failed task=%s err=%v", msg.TaskID, err)
		return m, nil
	}

	uxlog.Log("handleAgentFinished: task=%s currentStatus=%s startedAt=%v age=%v worktree=%s",
		t.ID, t.Status, t.StartedAt, time.Since(t.StartedAt), t.Worktree)

	t.AgentPID = 0
	newStatus, clearSession, quickExit := determinePostExitStatus(msg, t)
	uxlog.Log("handleAgentFinished: task=%s newStatus=%s clearSession=%v quickExit=%v",
		t.ID, newStatus, clearSession, quickExit)
	t.SetStatus(newStatus)
	if clearSession {
		t.SessionID = ""
	}

	// Scan LastOutput for a PR URL in case the agent finished before the tick
	// scanner had a chance to pick it up (e.g. fast-finishing agents).
	if t.PRURL == "" {
		if matches := prURLRe.FindAllString(string(msg.LastOutput), -1); len(matches) > 0 {
			t.PRURL = matches[len(matches)-1]
		}
	}

	if m.current == viewAgent && m.agentview.taskID == msg.TaskID {
		m.agentview.SetLastOutput(msg.LastOutput)
	}

	if m.current == viewAgent && m.agentview.taskID == msg.TaskID && !quickExit {
		m.current = viewTaskList
	}
	_ = m.db.Update(t)

	m.refreshTasks()
	return m, nil
}

func (m Model) handleSessionResumed(msg SessionResumedMsg) (tea.Model, tea.Cmd) {
	uxlog.Log("handleSessionResumed: task=%s err=%v pid=%d", msg.TaskID, msg.Err, msg.PID)

	t, err := m.db.Get(msg.TaskID)
	if err != nil {
		uxlog.Log("handleSessionResumed: db.Get failed task=%s err=%v", msg.TaskID, err)
		return m, nil
	}

	if msg.Err != nil {
		// Resume failed — clear session ID so next manual start is fresh
		uxlog.Log("handleSessionResumed: RESUME FAILED task=%s err=%v", t.ID, msg.Err)
		t.SessionID = ""
		t.AgentPID = 0
		_ = m.db.Update(t)
	} else {
		uxlog.Log("handleSessionResumed: task=%s pid=%d", t.ID, msg.PID)
		t.AgentPID = msg.PID
		_ = m.db.Update(t)
	}

	m.refreshTasks()
	return m, nil
}

func (m Model) handleNewTaskKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmd := m.newtask.Update(msg)

	if m.newtask.Canceled() {
		m.current = viewTaskList
		return m, nil
	}

	if m.newtask.Done() {
		task := m.newtask.Task()

		// Create worktree BEFORE persisting the task. If this fails, keep
		// the form open with the error so the user can retry.
		projDir := agent.ResolveDir(task, m.db.Config())
		if projDir == "" {
			m.newtask.SetError("no project directory configured")
			return m, nil
		}
		wt, finalName, wtErr := agent.CreateWorktree(projDir, task.Project, task.Name, task.Branch)
		if wtErr != nil {
			m.newtask.SetError(wtErr.Error())
			return m, nil
		}
		task.Worktree = wt
		task.Name = finalName
		task.Branch = "argus/" + finalName

		_ = m.db.Add(task)
		m.refreshTasks()
		m.current = viewTaskList
		return m.startOrAttach(task)
	}

	return m, cmd
}

func (m Model) handleRenameTaskKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmd := m.renametask.Update(msg)

	if m.renametask.Canceled() {
		m.current = viewTaskList
		return m, nil
	}

	if m.renametask.Done() {
		newName := m.renametask.NewName()
		taskID := m.renametask.TaskID()

		t, err := m.db.Get(taskID)
		if err != nil {
			m.renametask.SetError(err.Error())
			return m, nil
		}

		// No-op if name unchanged.
		if newName == t.Name {
			m.current = viewTaskList
			return m, nil
		}

		// Rename is display-only — worktree dir and branch stay the same
		// so rename works even while an agent is running.
		t.Name = newName
		if err := m.db.Update(t); err != nil {
			m.statusbar.SetError(fmt.Sprintf("rename succeeded but DB update failed: %v", err))
		}

		// Update resolved dirs cache.
		if t.Worktree != "" {
			m.resolvedDirs[t.ID] = t.Worktree
		}

		m.refreshTasks()
		m.current = viewTaskList
		return m, nil
	}

	return m, cmd
}

// handleConfirmAction handles enter/esc for confirmation dialogs.
// The cleanup function is called with the selected task on confirmation.
func (m Model) handleConfirmAction(msg tea.KeyMsg, cleanup func(*model.Task)) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		if t := m.tasklist.Selected(); t != nil {
			if m.runner.HasSession(t.ID) {
				_ = m.runner.Stop(t.ID)
			}
			cleanup(t)
			_ = m.db.Delete(t.ID)
			m.refreshTasks()
		}
		m.current = viewTaskList
		return m, nil
	case tea.KeyEsc:
		m.current = viewTaskList
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) handleConfirmDeleteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.handleConfirmAction(msg, func(t *model.Task) {
		cfg := m.db.Config()
		if t.Worktree != "" && cfg.UI.ShouldCleanupWorktrees() {
			repoDir := agent.ResolveDir(t, cfg)
			removeWorktreeAndBranch(t.Worktree, t.Branch, repoDir)
		} else if t.Branch != "" {
			if repoDir := agent.ResolveDir(t, cfg); repoDir != "" {
				deleteBranch(repoDir, t.Branch)
				deleteRemoteBranch(repoDir, t.Branch)
			}
		}
		os.Remove(agent.SessionLogPath(t.ID)) //nolint:errcheck
	})
}

func (m Model) handleConfirmDestroyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.handleConfirmAction(msg, func(t *model.Task) {
		cfg := m.db.Config()
		if t.Worktree != "" {
			repoDir := agent.ResolveDir(t, cfg)
			removeWorktreeAndBranch(t.Worktree, t.Branch, repoDir)
		} else if t.Branch != "" {
			if repoDir := agent.ResolveDir(t, cfg); repoDir != "" {
				deleteBranch(repoDir, t.Branch)
				deleteRemoteBranch(repoDir, t.Branch)
			}
		}
		os.Remove(agent.SessionLogPath(t.ID)) //nolint:errcheck
	})
}

func (m Model) handleSandboxConfigKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmd := m.sandboxconfig.Update(msg)

	if m.sandboxconfig.Canceled() {
		m.current = viewTaskList
		return m, nil
	}

	if m.sandboxconfig.Done() {
		enabled, denyRead, extraWrite := m.sandboxconfig.Result()
		var saveErr error
		if err := m.db.SetSandboxEnabled(enabled); err != nil {
			saveErr = err
		}
		if err := m.db.SetConfigValue("sandbox.deny_read", denyRead); err != nil {
			saveErr = err
		}
		if err := m.db.SetConfigValue("sandbox.extra_write", extraWrite); err != nil {
			saveErr = err
		}
		if saveErr != nil {
			m.statusbar.SetError("failed to save sandbox config")
		}
		agent.ResetSandboxCache()
		m.refreshSettings()
		m.current = viewTaskList
		return m, nil
	}

	return m, cmd
}

func (m Model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		m.quitting = true
		return m, tea.Quit

	case key.Matches(msg, m.keys.Up):
		m.settings.CursorUp()
		return m, nil

	case key.Matches(msg, m.keys.Down):
		m.settings.CursorDown()
		return m, nil

	case key.Matches(msg, m.keys.New):
		sel := m.settings.Selected()
		if sel == nil || sel.kind == settingsRowProject {
			m.projectform = NewProjectForm(m.theme)
			m.projectform.SetSize(m.width, m.height)
			m.current = viewNewProject
			return m, m.projectform.inputs[0].Focus()
		}
		if sel != nil && sel.kind == settingsRowBackend {
			m.backendform = NewBackendForm(m.theme)
			m.backendform.SetSize(m.width, m.height)
			m.current = viewNewBackend
			return m, m.backendform.inputs[0].Focus()
		}
		return m, nil

	case key.Matches(msg, m.keys.Edit):
		if entry := m.settings.SelectedProject(); entry != nil {
			m.projectform = NewProjectForm(m.theme)
			m.projectform.SetSize(m.width, m.height)
			m.projectform.LoadProject(entry.Name, entry.Project)
			m.current = viewEditProject
			return m, m.projectform.inputs[projFieldPath].Focus()
		}
		if entry := m.settings.SelectedBackend(); entry != nil {
			m.backendform = NewBackendForm(m.theme)
			m.backendform.SetSize(m.width, m.height)
			m.backendform.LoadBackend(entry.Name, entry.Backend)
			m.current = viewEditBackend
			return m, m.backendform.inputs[backendFieldCommand].Focus()
		}
		return m, nil

	case msg.Type == tea.KeyEnter || msg.Type == tea.KeySpace:
		sel := m.settings.Selected()
		if sel != nil && sel.kind == settingsRowSandbox {
			cfg := m.db.Config()
			m.sandboxconfig = NewSandboxConfigForm(
				m.theme,
				cfg.Sandbox.Enabled,
				cfg.Sandbox.DenyRead,
				cfg.Sandbox.ExtraWrite,
			)
			m.sandboxconfig.SetSize(m.width, m.height)
			m.current = viewSandboxConfig
			return m, m.sandboxconfig.FocusFirst()
		}
		if sel != nil && sel.kind == settingsRowDaemonLogs {
			return m, m.loadDaemonLogsCmd()
		}
		if sel != nil && sel.kind == settingsRowUXLogs {
			return m, m.loadUXLogsCmd()
		}
		return m, nil

	case key.Matches(msg, m.keys.Delete):
		if m.settings.SelectedProject() != nil {
			m.current = viewConfirmDeleteProject
			return m, nil
		}
		// 'd' on a backend row sets it as the default backend.
		if entry := m.settings.SelectedBackend(); entry != nil {
			_ = m.db.SetConfigValue("defaults.backend", entry.Name)
			m.refreshSettings()
			m.statusbar.SetError("default backend → " + entry.Name)
			return m, nil
		}
		return m, nil

	case key.Matches(msg, m.keys.RestartDaemon):
		if !m.daemonConnected {
			m.statusbar.SetError("no daemon to restart (running in-process)")
			return m, nil
		}
		m.daemonRestarting = true
		m.current = viewDaemonRestart
		return m, m.restartDaemonCmd()

	case key.Matches(msg, m.keys.Help):
		m.current = viewHelp
		return m, nil
	}

	return m, nil
}

// restartDaemonCmd returns a tea.Cmd that shuts down the current daemon,
// waits for cleanup, starts a new one, and returns a DaemonRestartedMsg.
func (m Model) restartDaemonCmd() tea.Cmd {
	sockPath := daemon.DefaultSocketPath()
	client, ok := m.runner.(*dclient.Client)
	return func() tea.Msg {
		// Shut down the current daemon.
		if ok {
			_ = client.Shutdown()
		}
		dclient.WaitForShutdown(sockPath, 3*time.Second)

		// Start a new daemon.
		newClient, err := dclient.AutoStart(sockPath)
		if err != nil {
			return DaemonRestartedMsg{Err: err}
		}
		return DaemonRestartedMsg{Client: newClient}
	}
}

func (m Model) handleNewProjectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmd := m.projectform.Update(msg)

	if m.projectform.Canceled() {
		m.current = viewTaskList
		m.activeTab = tabSettings
		return m, nil
	}

	if m.projectform.Done() {
		name, proj := m.projectform.ProjectEntry()
		_ = m.db.SetProject(name, proj)
		m.refreshSettings()
		m.current = viewTaskList
		m.activeTab = tabSettings
		return m, nil
	}

	return m, cmd
}

func (m Model) handleEditProjectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmd := m.projectform.Update(msg)

	if m.projectform.Canceled() {
		m.current = viewTaskList
		m.activeTab = tabSettings
		return m, nil
	}

	if m.projectform.Done() {
		name, proj := m.projectform.ProjectEntry()
		_ = m.db.SetProject(name, proj)
		m.refreshSettings()
		m.current = viewTaskList
		m.activeTab = tabSettings
		return m, nil
	}

	return m, cmd
}

func (m Model) handleBackendFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmd := m.backendform.Update(msg)

	if m.backendform.Canceled() {
		m.current = viewTaskList
		m.activeTab = tabSettings
		return m, nil
	}

	if m.backendform.Done() {
		name, backend := m.backendform.BackendEntry()
		_ = m.db.SetBackend(name, backend)
		m.refreshSettings()
		m.current = viewTaskList
		m.activeTab = tabSettings
		return m, nil
	}

	return m, cmd
}

func (m Model) handleConfirmDeleteProjectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		if entry := m.settings.SelectedProject(); entry != nil {
			_ = m.db.DeleteProject(entry.Name)
			m.refreshSettings()
		}
		m.current = viewTaskList
		return m, nil
	case tea.KeyEsc:
		m.current = viewTaskList
		return m, nil
	default:
		// Any other key cancels
		m.current = viewTaskList
		return m, nil
	}
}

func (m *Model) refreshSettings() {
	var warnings []string
	if !m.daemonConnected {
		warnings = append(warnings, "In-process mode: sessions won't persist")
	}
	m.settings.SetWarnings(warnings)
	cfg := m.db.Config()
	m.settings.SetProjects(m.db.Projects())
	m.settings.SetDefaultBackend(cfg.Defaults.Backend)
	m.settings.SetBackends(cfg.Backends)
	m.settings.SetTasks(m.db.Tasks())
	// IsSandboxAvailable() is cached via sync.Once — only slow on the first call.
	// Always probe so the settings view shows correct install status regardless of
	// whether sandbox is currently enabled.
	available := agent.IsSandboxAvailable()
	m.settings.SetSandboxConfig(
		cfg.Sandbox.Enabled,
		available,
		cfg.Sandbox.DenyRead,
		cfg.Sandbox.ExtraWrite,
	)
}

// selectedTaskForGit returns the task whose git status should be refreshed.
func (m Model) selectedTaskForGit() *model.Task {
	if m.current == viewAgent {
		if t, err := m.db.Get(m.agentview.taskID); err == nil {
			return t
		}
		return nil
	}
	return m.tasklist.Selected()
}

// scheduleGitRefresh checks the selected task and returns a tea.Cmd to
// refresh git status asynchronously. Directory resolution is cached; on a
// cache miss the slow discovery runs in a background goroutine.
func (m Model) scheduleGitRefresh() tea.Cmd {
	t := m.selectedTaskForGit()
	if t == nil {
		m.gitstatus.SetTask("")
		return nil
	}

	// Fast path: dir already cached.
	if dir, ok := m.resolvedDirs[t.ID]; ok && dir != "" {
		m.gitstatus.SetTask(t.ID)
		if m.current == viewAgent && m.agentview.taskID == t.ID {
			m.agentview.SetWorktreeDir(dir)
		}
		needsMain := m.gitstatus.NeedsRefresh()
		needsAgent := m.current == viewAgent && m.agentview.NeedsGitRefresh()
		if needsMain || needsAgent {
			taskID := t.ID
			return func() tea.Msg {
				return FetchGitStatus(taskID, dir)
			}
		}
		return nil
	}

	// Try cheap resolution first (stored worktree, project config, runner).
	dir := m.resolveTaskDirFast(t)
	if dir != "" {
		m.resolvedDirs[t.ID] = dir
		m.gitstatus.SetTask(t.ID)
		taskID := t.ID
		return func() tea.Msg {
			return FetchGitStatus(taskID, dir)
		}
	}

	// Slow path: need to discover worktree — run async.
	// Compute the base dir cheaply (project path or runner work dir).
	baseDir := agent.ResolveDir(t, m.db.Config())
	if baseDir == "" {
		baseDir = m.runner.WorkDir(t.ID)
	}
	if baseDir == "" {
		m.gitstatus.SetTask("")
		return nil
	}
	taskID := t.ID
	taskName := t.Name
	projectName := t.Project
	return func() tea.Msg {
		return resolveTaskDirAsync(taskID, taskName, projectName, baseDir)
	}
}

// resolveTaskDirFast returns the task's working directory using only cheap
// lookups (cached worktree path, project config, runner). Returns "" if the
// directory cannot be determined without running git commands.
func (m Model) resolveTaskDirFast(t *model.Task) string {
	dir := t.Worktree

	// Validate stored worktree: must exist and match the task name.
	if dir != "" {
		if !dirExists(dir) || filepath.Base(dir) != t.Name {
			t.Worktree = ""
			_ = m.db.Update(t)
			delete(m.resolvedDirs, t.ID)
			dir = ""
		}
	}

	if dir != "" {
		return dir
	}

	// No stored worktree — can't resolve without async discovery.
	return ""
}

// resolveTaskDirAsync performs worktree discovery off the main thread.
func resolveTaskDirAsync(taskID, taskName, projectName, baseDir string) ResolveTaskDirMsg {
	dir := baseDir
	if wt := discoverWorktree(projectName, taskName); wt != "" {
		dir = wt
	}
	return ResolveTaskDirMsg{TaskID: taskID, Dir: dir}
}

func (m *Model) refreshTasks() {
	tasks := m.db.Tasks()
	running := m.runner.Running()
	idle := m.runner.Idle()

	// Update idleUnvisited: add newly-idle tasks, remove tasks no longer idle.
	newIdle := toStringSet(idle)
	for id := range newIdle {
		if !m.tasklist.idle[id] {
			// Newly idle — mark as unvisited until user opens the agent view.
			m.idleUnvisited[id] = true
		}
	}
	for id := range m.idleUnvisited {
		if !newIdle[id] {
			// No longer idle (agent produced output again) — clear unvisited.
			delete(m.idleUnvisited, id)
		}
	}

	// If the user recently viewed a task's agent view, suppress the
	// idleUnvisited flag for it. refreshTasks is skipped while in agent
	// view, so m.tasklist.idle goes stale — the "newly idle" check above
	// may false-positive for a task the user was actively watching.
	for id := range m.viewedWhileAgent {
		delete(m.idleUnvisited, id)
		if !newIdle[id] {
			// Task is no longer idle — safe to clear the guard permanently.
			delete(m.viewedWhileAgent, id)
		}
	}

	idleUnvisited := make([]string, 0, len(m.idleUnvisited))
	for id := range m.idleUnvisited {
		idleUnvisited = append(idleUnvisited, id)
	}

	m.tasklist.SetTasks(tasks)
	m.tasklist.SetRunning(running)
	m.tasklist.SetIdle(idle)
	m.tasklist.SetIdleUnvisited(idleUnvisited)
	m.statusbar.SetTasks(tasks)
	m.statusbar.SetRunning(running)
}

// loadDaemonLogsCmd returns a tea.Cmd that reads the daemon log file.
func (m Model) loadDaemonLogsCmd() tea.Cmd {
	return func() tea.Msg {
		logPath := filepath.Join(db.DataDir(), "daemon.log")
		data, err := os.ReadFile(logPath)
		if err != nil {
			return DaemonLogsMsg{Err: err}
		}
		lines := strings.Split(string(data), "\n")
		// Cap to last 1000 lines to keep memory reasonable.
		if len(lines) > 1000 {
			lines = lines[len(lines)-1000:]
		}
		return DaemonLogsMsg{Lines: lines}
	}
}

// daemonLogVisibleLines returns the number of log lines visible in the modal.
func (m Model) daemonLogVisibleLines() int {
	// Modal height is ~80% of screen, minus borders/padding/title/hint.
	h := m.height * 8 / 10
	h -= 6 // title, hint, padding, borders
	if h < 1 {
		h = 1
	}
	return h
}

func (m Model) handleDaemonLogsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape, tea.KeyEnter:
		m.daemonLogLines = nil
		m.daemonLogOffset = 0
		m.current = viewTaskList
		m.activeTab = tabSettings
		return m, nil
	case tea.KeyUp:
		if m.daemonLogOffset > 0 {
			m.daemonLogOffset--
		}
		return m, nil
	case tea.KeyDown:
		maxOff := len(m.daemonLogLines) - m.daemonLogVisibleLines()
		if maxOff < 0 {
			maxOff = 0
		}
		if m.daemonLogOffset < maxOff {
			m.daemonLogOffset++
		}
		return m, nil
	case tea.KeyPgUp:
		m.daemonLogOffset -= m.daemonLogVisibleLines()
		if m.daemonLogOffset < 0 {
			m.daemonLogOffset = 0
		}
		return m, nil
	case tea.KeyPgDown:
		m.daemonLogOffset += m.daemonLogVisibleLines()
		maxOff := len(m.daemonLogLines) - m.daemonLogVisibleLines()
		if maxOff < 0 {
			maxOff = 0
		}
		if m.daemonLogOffset > maxOff {
			m.daemonLogOffset = maxOff
		}
		return m, nil
	case tea.KeyHome:
		m.daemonLogOffset = 0
		return m, nil
	case tea.KeyEnd:
		maxOff := len(m.daemonLogLines) - m.daemonLogVisibleLines()
		if maxOff < 0 {
			maxOff = 0
		}
		m.daemonLogOffset = maxOff
		return m, nil
	}

	// q also closes
	if msg.String() == "q" {
		m.daemonLogLines = nil
		m.daemonLogOffset = 0
		m.current = viewTaskList
		m.activeTab = tabSettings
		return m, nil
	}

	return m, nil
}

func (m *Model) handleDaemonLogsMouse(msg tea.MouseMsg) {
	visible := m.daemonLogVisibleLines()
	maxOff := len(m.daemonLogLines) - visible
	if maxOff < 0 {
		maxOff = 0
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.daemonLogOffset -= 3
		if m.daemonLogOffset < 0 {
			m.daemonLogOffset = 0
		}
	case tea.MouseButtonWheelDown:
		m.daemonLogOffset += 3
		if m.daemonLogOffset > maxOff {
			m.daemonLogOffset = maxOff
		}
	}
}

// loadUXLogsCmd returns a tea.Cmd that reads the UX log file.
func (m Model) loadUXLogsCmd() tea.Cmd {
	return func() tea.Msg {
		logPath := filepath.Join(db.DataDir(), "ux.log")
		data, err := os.ReadFile(logPath)
		if err != nil {
			return UXLogsMsg{Err: err}
		}
		lines := strings.Split(string(data), "\n")
		if len(lines) > 1000 {
			lines = lines[len(lines)-1000:]
		}
		return UXLogsMsg{Lines: lines}
	}
}

func (m Model) handleUXLogsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape, tea.KeyEnter:
		m.uxLogLines = nil
		m.uxLogOffset = 0
		m.current = viewTaskList
		m.activeTab = tabSettings
		return m, nil
	case tea.KeyUp:
		if m.uxLogOffset > 0 {
			m.uxLogOffset--
		}
		return m, nil
	case tea.KeyDown:
		maxOff := len(m.uxLogLines) - m.daemonLogVisibleLines()
		if maxOff < 0 {
			maxOff = 0
		}
		if m.uxLogOffset < maxOff {
			m.uxLogOffset++
		}
		return m, nil
	case tea.KeyPgUp:
		m.uxLogOffset -= m.daemonLogVisibleLines()
		if m.uxLogOffset < 0 {
			m.uxLogOffset = 0
		}
		return m, nil
	case tea.KeyPgDown:
		m.uxLogOffset += m.daemonLogVisibleLines()
		maxOff := len(m.uxLogLines) - m.daemonLogVisibleLines()
		if maxOff < 0 {
			maxOff = 0
		}
		if m.uxLogOffset > maxOff {
			m.uxLogOffset = maxOff
		}
		return m, nil
	case tea.KeyHome:
		m.uxLogOffset = 0
		return m, nil
	case tea.KeyEnd:
		maxOff := len(m.uxLogLines) - m.daemonLogVisibleLines()
		if maxOff < 0 {
			maxOff = 0
		}
		m.uxLogOffset = maxOff
		return m, nil
	}

	if msg.String() == "q" {
		m.uxLogLines = nil
		m.uxLogOffset = 0
		m.current = viewTaskList
		m.activeTab = tabSettings
		return m, nil
	}

	return m, nil
}

// githubErrMsg returns a user-friendly error message for GitHub API errors,
// with a distinct message for rate limit errors (403/429).
func githubErrMsg(err error) string {
	if errors.Is(err, github.ErrRateLimit) {
		return "GitHub rate limit exceeded — wait a minute and try again (5k req/hr REST, 30 req/min Search)"
	}
	return err.Error()
}

// fetchPRListCmd kicks off an async fetch of all open PRs and review requests.
func fetchPRListCmd() tea.Cmd {
	return func() tea.Msg {
		prs, err := github.FetchPRList()
		return FetchPRListMsg{PRs: prs, Err: err}
	}
}

// fetchPRFilesCmd kicks off an async fetch of changed files for a PR.
func fetchPRFilesCmd(owner, repo string, number int) tea.Cmd {
	return func() tea.Msg {
		files, err := github.FetchPRFiles(owner, repo, number)
		return FetchPRFilesMsg{Files: files, Err: err}
	}
}

// fetchPRFullDiffCmd fetches the complete diff for a PR once and caches it.
// Subsequent file selections re-slice the cached diff without API calls.
func fetchPRFullDiffCmd(owner, repo string, number int) tea.Cmd {
	return func() tea.Msg {
		diff, err := github.FetchPRFullDiff(owner, repo, number)
		return FetchPRDiffMsg{Diff: diff, Err: err}
	}
}


// fetchPRCommentsCmd kicks off an async fetch of review comments for a PR.
func fetchPRCommentsCmd(owner, repo string, number int) tea.Cmd {
	return func() tea.Msg {
		comments, err := github.FetchPRComments(owner, repo, number)
		return FetchPRCommentsMsg{Comments: comments, Err: err}
	}
}

// postCommentCmd kicks off an async post of a review comment.
func postCommentCmd(pr *github.PR, path string, line int, body string) tea.Cmd {
	return func() tea.Msg {
		err := github.PostReviewComment(pr.RepoOwner, pr.Repo, pr.Number, pr.HeadSHA, path, line, body)
		return PostCommentMsg{Err: err}
	}
}

func (m *Model) handleUXLogsMouse(msg tea.MouseMsg) {
	visible := m.daemonLogVisibleLines()
	maxOff := len(m.uxLogLines) - visible
	if maxOff < 0 {
		maxOff = 0
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.uxLogOffset -= 3
		if m.uxLogOffset < 0 {
			m.uxLogOffset = 0
		}
	case tea.MouseButtonWheelDown:
		m.uxLogOffset += 3
		if m.uxLogOffset > maxOff {
			m.uxLogOffset = maxOff
		}
	}
}
