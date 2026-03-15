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

	m.statusbar.SetProjectTab(m.activeTab == tabProjects)
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
	case viewHelp:
		return m.padToBottom(m.helpview.View(), bar)
	case viewPrompt:
		return m.padToBottom(m.promptView(), bar)
	case viewNewTask:
		return m.newtask.View() + "\n" + bar
	case viewNewProject:
		return m.newproject.View() + "\n" + bar
	}

	tabHeader := m.renderTabHeader()
	switch m.activeTab {
	case tabProjects:
		return m.renderProjectsView(tabHeader, bar)
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

// splitWidths returns a two-panel split for the projects tab.
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
		{"Projects", tabProjects},
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

func (m Model) renderProjectsView(tabHeader, bar string) string {
	projects := m.projectlist.View()
	rightContent := m.renderProjectDetail()
	body := lipgloss.JoinHorizontal(lipgloss.Top, projects, rightContent)
	content := tabHeader + "\n" + body
	return m.padToBottom(content, bar)
}

func (m Model) renderProjectDetail() string {
	entry := m.projectlist.Selected()
	_, rightWidth := m.splitWidths()
	contentHeight := m.height - 2

	if entry == nil {
		empty := m.theme.Dimmed.Render("  No project selected")
		return lipgloss.NewStyle().Width(rightWidth).Height(contentHeight).Render(empty)
	}

	var b strings.Builder
	b.WriteString(m.theme.Title.Render("  "+entry.Name) + "\n\n")

	fields := []struct{ label, value string }{
		{"Path", entry.Project.Path},
		{"Branch", entry.Project.Branch},
		{"Backend", entry.Project.Backend},
	}
	for _, f := range fields {
		val := f.value
		if val == "" {
			val = "(default)"
		}
		b.WriteString("  " + m.theme.Dimmed.Render(f.label+": ") + m.theme.Normal.Render(val) + "\n")
	}

	return lipgloss.NewStyle().Width(rightWidth).Height(contentHeight).Render(b.String())
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
	entry := m.projectlist.Selected()
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
	status := "  " + m.theme.Normal.Render(
		fmt.Sprintf("Cleaning up %d worktree(s)%s", m.pruneTotal, dots[dotIdx]),
	)
	hint := m.theme.Help.Render("  Removing worktrees and branches")
	body := title + "\n\n" + status + "\n\n" + hint
	return m.renderCenteredModal(body, 50)
}
