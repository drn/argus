package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// NewTaskForm handles the new task creation UI.
type NewTaskForm struct {
	promptInput  textarea.Model
	projectNames []string
	projectIdx   int
	backendNames []string // sorted backend names
	backendIdx   int
	focused      int // 0 = project, 1 = backend, 2 = prompt
	theme        Theme
	projects     map[string]config.Project
	done         bool
	canceled     bool
	errMsg       string
	width        int
	height       int
	// autocomplete state
	skills    []SkillItem
	acOpen    bool
	acMatches []SkillItem
	acIdx     int
	acScroll  int
}

const (
	fieldProject = 0
	fieldBackend = 1
	fieldPrompt  = 2
)

// maxPromptLines is the maximum number of visible lines for the prompt textarea.
const maxPromptLines = 10

func NewNewTaskForm(theme Theme, projects map[string]config.Project, defaultProject string, backends map[string]config.Backend, defaultBackend string) NewTaskForm {
	promptInput := textarea.New()
	promptInput.Placeholder = "Prompt for the agent"
	promptInput.CharLimit = 0 // no limit
	promptInput.ShowLineNumbers = false
	promptInput.Prompt = ""
	promptInput.SetHeight(1)
	promptInput.MaxHeight = maxPromptLines
	promptInput.FocusedStyle.CursorLine = lipgloss.NewStyle()
	promptInput.BlurredStyle.CursorLine = lipgloss.NewStyle()
	promptInput.FocusedStyle.Base = lipgloss.NewStyle()
	promptInput.BlurredStyle.Base = lipgloss.NewStyle()
	promptInput.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	promptInput.BlurredStyle.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	// Disable enter key — we handle submit ourselves
	promptInput.KeyMap.InsertNewline = key.NewBinding(key.WithDisabled())
	promptInput.Focus()

	// Build sorted project name list
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names)

	// Default to the project the cursor is currently on
	idx := 0
	if defaultProject != "" {
		for i, n := range names {
			if n == defaultProject {
				idx = i
				break
			}
		}
	}

	// Build sorted backend name list
	backendNames := make([]string, 0, len(backends))
	for name := range backends {
		backendNames = append(backendNames, name)
	}
	sort.Strings(backendNames)

	// Pre-select the default backend
	backendIdx := 0
	for i, name := range backendNames {
		if name == defaultBackend {
			backendIdx = i
			break
		}
	}

	return NewTaskForm{
		promptInput:  promptInput,
		projectNames: names,
		projectIdx:   idx,
		backendNames: backendNames,
		backendIdx:   backendIdx,
		focused:      fieldPrompt,
		theme:        theme,
		projects:     projects,
	}
}

// skillsLoadedMsg carries the result of an async skill scan.
type skillsLoadedMsg []SkillItem

// loadSkillsCmd returns a Cmd that scans ~/.claude/skills/ in a background
// goroutine, keeping the UI responsive during filesystem discovery.
func loadSkillsCmd() tea.Cmd {
	return func() tea.Msg {
		return skillsLoadedMsg(LoadSkills(nil))
	}
}

func (f *NewTaskForm) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Clear error on any keypress
		f.errMsg = ""
		switch msg.String() {
		case "esc":
			if f.acOpen {
				f.acOpen = false
				return nil
			}
			f.canceled = true
			return nil
		case "tab":
			switch f.focused {
			case fieldProject:
				f.focused = fieldBackend
				f.promptInput.Blur()
			case fieldBackend:
				f.focused = fieldPrompt
				return f.promptInput.Focus()
			default: // fieldPrompt
				f.focused = fieldProject
				f.promptInput.Blur()
			}
			return nil
		case "shift+tab":
			switch f.focused {
			case fieldPrompt:
				f.focused = fieldBackend
				f.promptInput.Blur()
			case fieldBackend:
				f.focused = fieldProject
			default: // fieldProject
				f.focused = fieldPrompt
				return f.promptInput.Focus()
			}
			return nil
		case "left":
			if f.focused == fieldProject && len(f.projectNames) > 0 {
				f.projectIdx = (f.projectIdx - 1 + len(f.projectNames)) % len(f.projectNames)
				return nil
			}
			if f.focused == fieldBackend && len(f.backendNames) > 0 {
				f.backendIdx = (f.backendIdx - 1 + len(f.backendNames)) % len(f.backendNames)
				return nil
			}
		case "right":
			if f.focused == fieldProject && len(f.projectNames) > 0 {
				f.projectIdx = (f.projectIdx + 1) % len(f.projectNames)
				return nil
			}
			if f.focused == fieldBackend && len(f.backendNames) > 0 {
				f.backendIdx = (f.backendIdx + 1) % len(f.backendNames)
				return nil
			}
		case "up":
			if f.acOpen && f.focused == fieldPrompt {
				f.acMoveUp()
				return nil
			}
			if f.focused == fieldProject {
				f.focused = fieldPrompt
				return f.promptInput.Focus()
			}
			if f.focused == fieldBackend {
				f.focused = fieldProject
				return nil
			}
			// Move to backend field if cursor is on the first visual line
			li := f.promptInput.LineInfo()
			if f.promptInput.Line() == 0 && li.RowOffset == 0 {
				f.focused = fieldBackend
				f.promptInput.Blur()
				return nil
			}
			// Otherwise let textarea handle up arrow for multi-line navigation
		case "down":
			if f.acOpen && f.focused == fieldPrompt {
				f.acMoveDown()
				return nil
			}
			if f.focused == fieldProject {
				f.focused = fieldBackend
				return nil
			}
			if f.focused == fieldBackend {
				f.focused = fieldPrompt
				return f.promptInput.Focus()
			}
			// Move to project field if cursor is on the last visual line
			li := f.promptInput.LineInfo()
			if f.promptInput.Line() == f.promptInput.LineCount()-1 && li.RowOffset == li.Height-1 {
				f.focused = fieldProject
				f.promptInput.Blur()
				return nil
			}
			// Otherwise let textarea handle down arrow for multi-line navigation
		case "enter":
			if f.focused == fieldProject {
				f.focused = fieldBackend
				return nil
			}
			if f.focused == fieldBackend {
				f.focused = fieldPrompt
				return f.promptInput.Focus()
			}
			// Select autocomplete suggestion if open
			if f.acOpen && len(f.acMatches) > 0 {
				f.promptInput.SetValue("/" + f.acMatches[f.acIdx].Name + " ")
				f.promptInput.CursorEnd()
				f.acOpen = false
				f.adjustPromptHeight()
				return nil
			}
			// Submit on enter at prompt field
			if strings.TrimSpace(f.promptInput.Value()) != "" {
				f.done = true
			}
			return nil
		}
		if f.focused == fieldPrompt {
			if applyWordNavTextarea(msg, &f.promptInput, f.adjustPromptHeight) {
				return nil
			}
		}
	}

	if f.focused == fieldPrompt {
		// Set height to max BEFORE Update so repositionView() inside
		// textarea.Update() doesn't scroll the viewport. With height=1,
		// a newly wrapped line causes repositionView to scroll down to
		// follow the cursor, and the subsequent SetHeight(2) doesn't
		// reset the scroll offset — hiding the first line.
		f.promptInput.SetHeight(maxPromptLines)
		var cmd tea.Cmd
		f.promptInput, cmd = f.promptInput.Update(msg)
		// Shrink height back to fit the actual visual line count
		f.adjustPromptHeight()
		f.updateAutocomplete()
		return cmd
	}

	return nil
}

// adjustPromptHeight resizes the prompt textarea to fit its current content.
func (f *NewTaskForm) adjustPromptHeight() {
	lines := f.visualLineCount()
	if lines < 1 {
		lines = 1
	}
	if lines > maxPromptLines {
		lines = maxPromptLines
	}
	f.promptInput.SetHeight(lines)
}

// visualLineCount returns the total number of visual lines in the textarea,
// accounting for soft wraps. For single hard lines (the normal case since
// enter is disabled), uses LineInfo().Height which is computed by the
// textarea's own internal memoizedWrap — guaranteed to match rendering.
// For multi-line pasted content, returns maxPromptLines to let the textarea
// handle scrolling rather than approximate the line count.
func (f *NewTaskForm) visualLineCount() int {
	if f.promptInput.Value() == "" {
		return 1
	}
	if f.promptInput.LineCount() > 1 {
		return maxPromptLines
	}
	return f.promptInput.LineInfo().Height
}

// updateAutocomplete recomputes the autocomplete matches based on the current
// prompt value. Autocomplete is active when the value starts with "/" and
// contains no spaces (the user is still typing the skill name).
func (f *NewTaskForm) updateAutocomplete() {
	val := f.promptInput.Value()
	// Close once the user moves past the skill name (typed a space to add args).
	if !strings.HasPrefix(val, "/") || strings.ContainsRune(val[1:], ' ') {
		f.acOpen = false
		return
	}
	filter := val[1:]
	f.acMatches = filterSkills(f.skills, filter)
	if len(f.acMatches) == 0 {
		f.acOpen = false
		return
	}
	f.acOpen = true
	if f.acIdx >= len(f.acMatches) {
		f.acIdx = 0
		f.acScroll = 0
	}
}

// acMoveDown moves the autocomplete cursor down one item (wraps around).
func (f *NewTaskForm) acMoveDown() {
	if len(f.acMatches) == 0 {
		return
	}
	f.acIdx = (f.acIdx + 1) % len(f.acMatches)
	if f.acIdx == 0 {
		f.acScroll = 0
	} else if f.acIdx >= f.acScroll+acMaxVisible {
		f.acScroll = f.acIdx - acMaxVisible + 1
	}
}

// acMoveUp moves the autocomplete cursor up one item (wraps around).
func (f *NewTaskForm) acMoveUp() {
	if len(f.acMatches) == 0 {
		return
	}
	if f.acIdx == 0 {
		f.acIdx = len(f.acMatches) - 1
		if f.acIdx >= acMaxVisible {
			f.acScroll = f.acIdx - acMaxVisible + 1
		}
	} else {
		f.acIdx--
		if f.acIdx < f.acScroll {
			f.acScroll = f.acIdx
		}
	}
}

func (f *NewTaskForm) SelectedProject() string {
	if len(f.projectNames) == 0 {
		return ""
	}
	return f.projectNames[f.projectIdx]
}

// SelectedBackend returns the selected backend name, or "" if no backends are configured.
func (f *NewTaskForm) SelectedBackend() string {
	if len(f.backendNames) == 0 {
		return ""
	}
	return f.backendNames[f.backendIdx]
}

func (f *NewTaskForm) Task() *model.Task {
	project := f.SelectedProject()
	prompt := strings.TrimSpace(f.promptInput.Value())

	name := model.GenerateNameFromPrompt(prompt)

	branch := "master"
	if p, ok := f.projects[project]; ok {
		if p.Branch != "" {
			branch = p.Branch
		}
	}

	return &model.Task{
		Name:    name,
		Status:  model.StatusPending,
		Project: project,
		Branch:  branch,
		Prompt:  prompt,
		Backend: f.SelectedBackend(),
	}
}

func (f *NewTaskForm) Done() bool     { return f.done }
func (f *NewTaskForm) Canceled() bool { return f.canceled }

// SetError sets an error message to display in the form and resets the
// done flag so the form remains open for the user to retry.
func (f *NewTaskForm) SetError(msg string) {
	f.errMsg = msg
	f.done = false
}

func (f *NewTaskForm) SetSize(w, h int) {
	f.width = w
	f.height = h
	// Guard against zero-valued form (textarea not initialized via constructor).
	// projects is nil when zero-valued but always a valid map when constructed.
	if f.projects == nil {
		return
	}
	inputWidth := f.modalWidth() - 4
	if inputWidth < 20 {
		inputWidth = 20
	}
	f.promptInput.SetWidth(inputWidth)
}

func (f NewTaskForm) modalWidth() int {
	return clampModalWidth(f.width)
}

func (f NewTaskForm) View() string {
	// Guard against zero-valued form (textarea not initialized via constructor).
	if f.projects == nil {
		return ""
	}
	var b strings.Builder

	// Project selector
	projStyle := f.theme.Dimmed
	if f.focused == fieldProject {
		projStyle = f.theme.Selected
	}
	b.WriteString(projStyle.Render("Project:") + "\n")
	b.WriteString(f.renderProjectSelector() + "\n\n")

	// Backend selector
	backendStyle := f.theme.Dimmed
	if f.focused == fieldBackend {
		backendStyle = f.theme.Selected
	}
	b.WriteString(backendStyle.Render("Backend:") + "\n")
	b.WriteString(f.renderBackendSelector() + "\n\n")

	// Prompt input
	promptStyle := f.theme.Dimmed
	if f.focused == fieldPrompt {
		promptStyle = f.theme.Selected
	}
	b.WriteString(promptStyle.Render("Prompt:") + "\n")
	b.WriteString(f.promptInput.View() + "\n")
	if f.acOpen { // acOpen is only true when acMatches is non-empty
		b.WriteString(f.renderAutocomplete() + "\n")
	}
	b.WriteString("\n")

	if f.errMsg != "" {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
		b.WriteString(errStyle.Render("Error: "+f.errMsg) + "\n\n")
	}

	b.WriteString(f.theme.Help.Render("tab/shift+tab: navigate  ←/→: select  enter: submit  esc: cancel"))

	mw := f.modalWidth()

	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("87")).
		Padding(1, 2).
		Width(mw).
		Render(f.theme.Title.Render("New Task") + "\n\n" + b.String())

	return lipgloss.Place(f.width, f.height, lipgloss.Center, lipgloss.Center, modal)
}

func (f NewTaskForm) renderBackendSelector() string {
	if len(f.backendNames) == 0 {
		return f.theme.Dimmed.Render("  (no backends configured)")
	}

	arrow := f.theme.Dimmed
	selected := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("87"))

	left := arrow.Render("◀ ")
	right := arrow.Render(" ▶")
	name := selected.Render(f.backendNames[f.backendIdx])

	counter := f.theme.Dimmed.Render(
		fmt.Sprintf(" (%d/%d)", f.backendIdx+1, len(f.backendNames)))

	return "  " + left + name + right + counter
}

// renderAutocomplete renders the skill suggestion dropdown below the prompt input.
func (f NewTaskForm) renderAutocomplete() string {
	inputW := f.modalWidth() - 4 // matches textarea width

	// Compute the longest skill name for alignment
	maxName := 0
	end := f.acScroll + acMaxVisible
	if end > len(f.acMatches) {
		end = len(f.acMatches)
	}
	for i := f.acScroll; i < end; i++ {
		n := ansi.StringWidth("/" + f.acMatches[i].Name)
		if n > maxName {
			maxName = n
		}
	}

	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("87"))
	var rows []string
	for i := f.acScroll; i < end; i++ {
		skill := f.acMatches[i]
		isSelected := i == f.acIdx

		indicator := "  "
		nameStr := "/" + skill.Name
		if isSelected {
			indicator = "▶ "
			nameStr = selectedStyle.Render(nameStr)
		} else {
			nameStr = f.theme.Dimmed.Render(nameStr)
		}

		// Pad name to maxName display columns for alignment.
		// Use ansi.StringWidth for multibyte/wide-character safety.
		plainNameW := ansi.StringWidth("/" + skill.Name)
		padding := strings.Repeat(" ", maxName-plainNameW+2)

		// Truncate description to fit remaining display width.
		// Guard descW <= 0 explicitly — avoids negative slice index when
		// the modal is very narrow (zero-dimension invariant).
		descW := inputW - ansi.StringWidth(indicator) - maxName - 2
		desc := skill.Description
		if descW <= 0 {
			desc = ""
		} else {
			runes := []rune(desc)
			if len(runes) > descW {
				desc = string(runes[:descW-1]) + "…"
			}
		}
		descStr := f.theme.Dimmed.Render(desc)

		rows = append(rows, indicator+nameStr+padding+descStr)
	}

	// Scroll indicator when list is longer than visible window
	if len(f.acMatches) > acMaxVisible {
		count := f.theme.Dimmed.Render(
			fmt.Sprintf("  (%d/%d)", f.acIdx+1, len(f.acMatches)))
		rows = append(rows, count)
	}

	return strings.Join(rows, "\n")
}

func (f NewTaskForm) renderProjectSelector() string {
	if len(f.projectNames) == 0 {
		return f.theme.Dimmed.Render("  (no projects configured)")
	}

	arrow := f.theme.Dimmed
	selected := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("87"))

	left := arrow.Render("◀ ")
	right := arrow.Render(" ▶")
	name := selected.Render(f.projectNames[f.projectIdx])

	counter := f.theme.Dimmed.Render(
		fmt.Sprintf(" (%d/%d)", f.projectIdx+1, len(f.projectNames)))

	return "  " + left + name + right + counter
}
