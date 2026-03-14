package ui

import (
	"strings"

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

func (m Model) splitRightHeights(total int) (int, int) {
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
	activeStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("87")).
		Underline(false)
	inactiveStyle := m.theme.Dimmed

	tabs := []struct {
		label string
		key   string
		t     tab
	}{
		{"TASKS", "1", tabTasks},
		{"PROJECTS", "2", tabProjects},
	}

	var parts []string
	for _, t := range tabs {
		style := inactiveStyle
		if t.t == m.activeTab {
			style = activeStyle
		}
		parts = append(parts, style.Render("  "+t.label+" "))
	}
	header := strings.Join(parts, "  ")
	return lipgloss.PlaceHorizontal(m.width, lipgloss.Center, header)
}

func (m Model) renderTasksView(tabHeader, bar string) string {
	if len(m.db.Tasks()) == 0 {
		content := m.emptyStateView()
		return m.padToBottom(content, bar)
	}

	tasks := m.tasklist.View()
	var taskID string
	if t := m.tasklist.Selected(); t != nil {
		taskID = t.ID
	}
	gitView := m.gitstatus.View()
	previewView := m.preview.View(taskID)
	rightContent := lipgloss.JoinVertical(lipgloss.Left, gitView, previewView)
	body := lipgloss.JoinHorizontal(lipgloss.Top, tasks, rightContent)
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
	contentHeight := m.height - 3

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
