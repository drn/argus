package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	m.statusbar.SetSettingsTab(m.activeTab == tabSettings)
	bar := m.statusbar.View()

	if m.current == viewAgent {
		return m.agentview.View()
	}

	switch m.current {
	case viewConfirmDeleteProject:
		return m.confirmDeleteProjectView() + "\n" + bar
	case viewConfirmDelete:
		return m.confirmDeleteView() + "\n" + bar
	case viewConfirmDestroy:
		return m.confirmDestroyView() + "\n" + bar
	case viewPruning:
		return m.pruneView() + "\n" + bar
	case viewSandboxInstall:
		return m.sandboxInstallView() + "\n" + bar
	case viewDaemonRestart:
		return m.daemonRestartView() + "\n" + bar
	case viewDaemonLogs:
		return m.daemonLogsView() + "\n" + bar
	case viewUXLogs:
		return m.uxLogsView() + "\n" + bar
	case viewHelp:
		return m.padToBottom(m.helpview.View(), bar)
	case viewPrompt:
		return m.padToBottom(m.promptView(), bar)
	case viewNewTask:
		return m.newtask.View() + "\n" + bar
	case viewNewProject:
		return m.newproject.View() + "\n" + bar
	case viewSandboxConfig:
		return m.sandboxconfig.View() + "\n" + bar
	}

	tabHeader := m.renderTabHeader()
	switch m.activeTab {
	case tabSettings:
		return m.renderSettingsView(tabHeader, bar)
	default:
		return m.renderTasksView(tabHeader, bar)
	}
}

func (m Model) padToBottom(content, bar string) string {
	barHeight := lipgloss.Height(bar)
	contentHeight := m.height - barHeight
	if contentHeight < 0 {
		contentHeight = 0
	}
	contentLines := lipgloss.Height(content)
	padding := ""
	if contentLines < contentHeight {
		padding = strings.Repeat("\n", contentHeight-contentLines)
	}
	return content + padding + "\n" + bar
}

func (m Model) splitCenterHeights(total int) (int, int) {
	gitH := total * 3 / 10
	if gitH < 5 {
		gitH = 5
	}
	if gitH > 15 {
		gitH = 15
	}
	previewH := total - gitH
	if previewH < 5 {
		previewH = 5
	}
	return gitH, previewH
}

// splitWidths returns a two-panel split for the settings tab.
func (m Model) splitWidths() (int, int) {
	left := m.width * 2 / 5
	if left < 30 {
		left = 30
	}
	if left > m.width-20 {
		left = m.width - 20
	}
	right := m.width - left
	return left, right
}


func (m Model) renderTabHeader() string {
	const (
		baseBg   = "236" // tmux C3 background
		activeFg = "236" // dark text on active tab
		activeBg = "103" // tmux C1 purple/lavender
		inFg     = "244" // tmux C3 text
		chevron  = "\ue0b0"
	)

	bg := lipgloss.Color(baseBg)
	abg := lipgloss.Color(activeBg)

	activeText := lipgloss.NewStyle().Foreground(lipgloss.Color(activeFg)).Background(abg)
	inactiveText := lipgloss.NewStyle().Foreground(lipgloss.Color(inFg)).Background(bg)

	tabs := []struct {
		label string
		t     tab
	}{
		{"Tasks", tabTasks},
		{"Settings", tabSettings},
	}

	var b strings.Builder
	for _, t := range tabs {
		if t.t == m.activeTab {
			// transition: base → active
			b.WriteString(lipgloss.NewStyle().Foreground(bg).Background(abg).Render(chevron))
			b.WriteString(activeText.Render(" " + t.label + " "))
			// transition: active → base
			b.WriteString(lipgloss.NewStyle().Foreground(abg).Background(bg).Render(chevron))
		} else {
			b.WriteString(inactiveText.Render("  " + t.label + " "))
		}
	}

	tabContent := b.String()
	return lipgloss.PlaceHorizontal(m.width, lipgloss.Center, tabContent,
		lipgloss.WithWhitespaceBackground(bg))
}

func (m Model) renderTasksView(tabHeader, bar string) string {
	if len(m.db.Tasks()) == 0 {
		content := m.emptyStateView()
		return m.padToBottom(content, bar)
	}

	widths := m.taskLayout.SplitWidths()
	contentHeight := m.taskLayout.Height()
	tasksContent := m.tasklist.View()
	tasksPanel := borderedPanel(widths[0], contentHeight, false, tasksContent)

	selected := m.tasklist.Selected()
	var taskID string
	if selected != nil {
		taskID = selected.ID
	}
	gitView := m.gitstatus.View()
	previewView := m.preview.View(taskID)
	centerContent := lipgloss.JoinVertical(lipgloss.Left, gitView, previewView)

	isRunning := selected != nil && m.runner.HasSession(selected.ID)
	detailView := m.detail.View(selected, isRunning)

	body := m.taskLayout.Render([]string{tasksPanel, centerContent, detailView})
	content := tabHeader + "\n" + body
	return m.padToBottom(content, bar)
}

func (m Model) renderSettingsView(tabHeader, bar string) string {
	leftWidth, rightWidth := m.splitWidths()
	contentHeight := m.height - 2
	leftContent := m.settings.View()
	leftPanel := borderedPanel(leftWidth, contentHeight, false, leftContent)
	rightPanel := m.settings.RenderDetail(rightWidth, contentHeight)
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
	content := tabHeader + "\n" + body
	return m.padToBottom(content, bar)
}

func (m Model) emptyStateView() string {
	banner := renderBanner(m.width)
	hint := m.theme.Dimmed.Render("Press [n] to create your first task")
	hint = lipgloss.PlaceHorizontal(m.width, lipgloss.Center, hint)
	block := banner + "\n\n" + hint

	barHeight := 1
	contentHeight := m.height - barHeight - 1
	blockHeight := lipgloss.Height(block)
	topPad := (contentHeight - blockHeight) / 2
	if topPad < 0 {
		topPad = 0
	}

	return strings.Repeat("\n", topPad) + block
}

func (m Model) promptView() string {
	t := m.tasklist.Selected()
	if t == nil {
		return ""
	}

	title := m.theme.Title.Render("Prompt: " + t.Name)
	prompt := t.Prompt
	if prompt == "" {
		prompt = m.theme.Dimmed.Render("(no prompt set)")
	}

	return title + "\n\n  " + prompt + "\n\n" +
		m.theme.Help.Render("  Press any key to close")
}

func (m Model) renderCenteredModal(body string, preferredWidth int) string {
	w := preferredWidth
	if m.width > 0 && w > m.width-4 {
		w = m.width - 4
	}
	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("238")).
		Padding(1, 2).
		Width(w).
		Render(body)
	return lipgloss.Place(m.width, m.height-1, lipgloss.Center, lipgloss.Center, modal)
}

func (m Model) confirmDeleteProjectView() string {
	entry := m.settings.SelectedProject()
	if entry == nil {
		return ""
	}
	title := m.theme.Title.Render("Delete project?")
	name := "  " + m.theme.Normal.Render(entry.Name)
	path := "  " + m.theme.Dimmed.Render(entry.Project.Path)
	hint := m.theme.Help.Render("  [enter] confirm  [esc] cancel")
	body := title + "\n\n" + name + "\n" + path + "\n\n" + hint
	return m.renderCenteredModal(body, 50)
}

func (m Model) confirmDeleteView() string {
	t := m.tasklist.Selected()
	if t == nil {
		return ""
	}
	title := m.theme.Title.Render("Delete task?")
	name := "  " + m.theme.Normal.Render(t.Name)
	hint := m.theme.Help.Render("  [enter] confirm  [esc] cancel")
	body := title + "\n\n" + name + "\n\n" + hint
	return m.renderCenteredModal(body, 50)
}

func (m Model) confirmDestroyView() string {
	t := m.tasklist.Selected()
	if t == nil {
		return ""
	}
	var details []string
	details = append(details, "  "+m.theme.Normal.Render(t.Name))
	if t.Worktree != "" {
		details = append(details, "  "+m.theme.Dimmed.Render("worktree: "+t.Worktree))
	}
	if t.Branch != "" {
		details = append(details, "  "+m.theme.Dimmed.Render("branch: "+t.Branch))
	}
	title := m.theme.Title.Render("Destroy task?")
	subtitle := m.theme.Help.Render("  This will terminate the agent, remove the worktree and branch, and delete the task.")
	hint := m.theme.Help.Render("  [enter] confirm  [esc] cancel")
	body := title + "\n" + subtitle + "\n\n" +
		strings.Join(details, "\n") + "\n\n" + hint
	return m.renderCenteredModal(body, 60)
}

func (m Model) pruneView() string {
	title := m.theme.Title.Render("Pruning completed tasks")
	// Spinner-like dots that animate with the 1s tick
	dots := []string{".", "..", "..."}
	dotIdx := int(time.Now().UnixMilli()/500) % len(dots)
	var statusText string
	if m.pruneCurrent == 0 {
		statusText = fmt.Sprintf("Cleaning up %d worktree(s)%s", m.pruneTotal, dots[dotIdx])
	} else {
		statusText = fmt.Sprintf("Cleaning up worktrees (%d/%d)%s", m.pruneCurrent, m.pruneTotal, dots[dotIdx])
	}
	status := "  " + m.theme.Normal.Render(statusText)
	hint := m.theme.Help.Render("  Removing worktrees and branches")
	body := title + "\n\n" + status + "\n\n" + hint
	return m.renderCenteredModal(body, 50)
}

func (m Model) daemonRestartView() string {
	title := m.theme.Title.Render("Restarting Daemon")
	dots := []string{".", "..", "..."}
	dotIdx := int(time.Now().UnixMilli()/500) % len(dots)
	status := "  " + m.theme.Normal.Render("Stopping sessions and restarting"+dots[dotIdx])
	hint := m.theme.Help.Render("  Please wait")
	body := title + "\n\n" + status + "\n\n" + hint
	return m.renderCenteredModal(body, 50)
}

func (m Model) daemonLogsView() string {
	visible := m.daemonLogVisibleLines()
	totalLines := len(m.daemonLogLines)

	title := m.theme.Title.Render("Daemon Logs")

	// Scroll indicator
	var scrollInfo string
	if totalLines > visible {
		end := m.daemonLogOffset + visible
		if end > totalLines {
			end = totalLines
		}
		scrollInfo = m.theme.Dimmed.Render(fmt.Sprintf("  lines %d-%d of %d", m.daemonLogOffset+1, end, totalLines))
	}

	// Build log content
	var logContent strings.Builder
	end := m.daemonLogOffset + visible
	if end > totalLines {
		end = totalLines
	}
	start := m.daemonLogOffset
	if start < 0 {
		start = 0
	}
	for i := start; i < end; i++ {
		line := m.daemonLogLines[i]
		logContent.WriteString("  " + m.theme.Dimmed.Render(line) + "\n")
	}
	// Pad remaining lines
	rendered := end - start
	for i := rendered; i < visible; i++ {
		logContent.WriteString("\n")
	}

	hint := m.theme.Help.Render("  [esc/enter/q] close  [↑↓] scroll  [pgup/pgdn] page  [home/end] jump")

	body := title + scrollInfo + "\n\n" + logContent.String() + "\n" + hint
	w := m.width * 8 / 10
	if w < 40 {
		w = 40
	}
	if m.width > 0 && w > m.width-4 {
		w = m.width - 4
	}

	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("238")).
		Padding(1, 2).
		Width(w).
		Render(body)
	return lipgloss.Place(m.width, m.height-1, lipgloss.Center, lipgloss.Center, modal)
}

func (m Model) uxLogsView() string {
	visible := m.daemonLogVisibleLines() // same sizing
	totalLines := len(m.uxLogLines)

	title := m.theme.Title.Render("UX Logs")

	var scrollInfo string
	if totalLines > visible {
		end := m.uxLogOffset + visible
		if end > totalLines {
			end = totalLines
		}
		scrollInfo = m.theme.Dimmed.Render(fmt.Sprintf("  lines %d-%d of %d", m.uxLogOffset+1, end, totalLines))
	}

	var logContent strings.Builder
	end := m.uxLogOffset + visible
	if end > totalLines {
		end = totalLines
	}
	start := m.uxLogOffset
	if start < 0 {
		start = 0
	}
	for i := start; i < end; i++ {
		line := m.uxLogLines[i]
		logContent.WriteString("  " + m.theme.Dimmed.Render(line) + "\n")
	}
	rendered := end - start
	for i := rendered; i < visible; i++ {
		logContent.WriteString("\n")
	}

	hint := m.theme.Help.Render("  [esc/enter/q] close  [↑↓] scroll  [pgup/pgdn] page  [home/end] jump")

	body := title + scrollInfo + "\n\n" + logContent.String() + "\n" + hint
	w := m.width * 8 / 10
	if w < 40 {
		w = 40
	}
	if m.width > 0 && w > m.width-4 {
		w = m.width - 4
	}

	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("238")).
		Padding(1, 2).
		Width(w).
		Render(body)
	return lipgloss.Place(m.width, m.height-1, lipgloss.Center, lipgloss.Center, modal)
}

func (m Model) sandboxInstallView() string {
	title := m.theme.Title.Render("Sandbox Required")

	if m.sandboxInstalling {
		dots := []string{".", "..", "..."}
		dotIdx := int(time.Now().UnixMilli()/500) % len(dots)
		status := "  " + m.theme.Normal.Render("Installing @anthropic-ai/sandbox-runtime"+dots[dotIdx])
		hint := m.theme.Help.Render("  npm install -g @anthropic-ai/sandbox-runtime")
		body := title + "\n\n" + status + "\n\n" + hint
		return m.renderCenteredModal(body, 58)
	}

	if m.sandboxInstallResult != "" {
		var status string
		if strings.HasPrefix(m.sandboxInstallResult, "Install failed") {
			status = "  " + m.theme.Error.Render(m.sandboxInstallResult)
		} else {
			status = "  " + m.theme.Complete.Render(m.sandboxInstallResult)
		}
		hint := m.theme.Help.Render("  [enter] continue  [esc] close")
		body := title + "\n\n" + status + "\n\n" + hint
		return m.renderCenteredModal(body, 58)
	}

	desc := "  " + m.theme.Normal.Render("Sandbox is enabled but srt is not installed.")
	desc2 := "  " + m.theme.Normal.Render("Install @anthropic-ai/sandbox-runtime globally?")
	hint := m.theme.Help.Render("  [enter] install  [esc] skip (run unsandboxed)")
	body := title + "\n\n" + desc + "\n" + desc2 + "\n\n" + hint
	return m.renderCenteredModal(body, 58)
}
