package ui

import (
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/drn/argus/internal/config"
)

// sandboxEnabledState represents the per-project sandbox enabled override.
// 0 = inherit global, 1 = enabled, 2 = disabled
type sandboxEnabledState int

const (
	sandboxInherit  sandboxEnabledState = 0
	sandboxEnabled  sandboxEnabledState = 1
	sandboxDisabled sandboxEnabledState = 2
)

// NewProjectForm handles the new project creation and editing UI.
type ProjectForm struct {
	inputs         []textinput.Model
	focused        int
	sandboxState   sandboxEnabledState // per-project sandbox override
	theme          Theme
	done           bool
	canceled       bool
	width          int
	height         int
	editMode       bool   // true when editing an existing project
	originalName   string // project name being edited (read-only in edit mode)
}

const (
	projFieldName = iota
	projFieldPath
	projFieldBranch
	projFieldBackend
	projFieldSandboxDenyRead
	projFieldSandboxExtraWrite
	projFieldCount
)

func NewProjectForm(theme Theme) ProjectForm {
	inputs := make([]textinput.Model, projFieldCount)

	nameInput := textinput.New()
	nameInput.Placeholder = "Project name (e.g. argus)"
	nameInput.CharLimit = 40
	inputs[projFieldName] = nameInput

	pathInput := textinput.New()
	pathInput.Placeholder = "Path to repository"
	pathInput.CharLimit = 200
	inputs[projFieldPath] = pathInput

	branchInput := textinput.New()
	branchInput.Placeholder = "Base branch (e.g. origin/master)"
	branchInput.CharLimit = 60
	branchInput.SetValue("master")
	inputs[projFieldBranch] = branchInput

	backendInput := textinput.New()
	backendInput.Placeholder = "Backend (leave empty for default)"
	backendInput.CharLimit = 40
	inputs[projFieldBackend] = backendInput

	sandboxDenyInput := textinput.New()
	sandboxDenyInput.Placeholder = "Comma-separated paths (e.g. /secrets,~/.private)"
	sandboxDenyInput.CharLimit = 500
	inputs[projFieldSandboxDenyRead] = sandboxDenyInput

	sandboxWriteInput := textinput.New()
	sandboxWriteInput.Placeholder = "Comma-separated paths (e.g. ~/.npm,/var/cache)"
	sandboxWriteInput.CharLimit = 500
	inputs[projFieldSandboxExtraWrite] = sandboxWriteInput

	return ProjectForm{
		inputs:  inputs,
		focused: projFieldName,
		theme:   theme,
	}
}

// LoadProject switches the form into edit mode, pre-populating all fields
// with the existing project's values. The name field becomes read-only.
func (f *ProjectForm) LoadProject(name string, proj config.Project) {
	f.editMode = true
	f.originalName = name
	f.inputs[projFieldName].SetValue(name)
	f.inputs[projFieldPath].SetValue(proj.Path)
	f.inputs[projFieldBranch].SetValue(proj.Branch)
	f.inputs[projFieldBackend].SetValue(proj.Backend)
	f.inputs[projFieldSandboxDenyRead].SetValue(strings.Join(proj.Sandbox.DenyRead, ","))
	f.inputs[projFieldSandboxExtraWrite].SetValue(strings.Join(proj.Sandbox.ExtraWrite, ","))
	switch {
	case proj.Sandbox.Enabled == nil:
		f.sandboxState = sandboxInherit
	case *proj.Sandbox.Enabled:
		f.sandboxState = sandboxEnabled
	default:
		f.sandboxState = sandboxDisabled
	}
	// Start focus on path since name is read-only in edit mode.
	f.focused = projFieldPath
}

// nextField returns the next field index in the tab order.
func (f *ProjectForm) nextField() int {
	if f.editMode {
		// Cycle through path → branch → backend → sandboxDenyRead → sandboxExtraWrite → path.
		switch f.focused {
		case projFieldPath:
			return projFieldBranch
		case projFieldBranch:
			return projFieldBackend
		case projFieldBackend:
			return projFieldSandboxDenyRead
		case projFieldSandboxDenyRead:
			return projFieldSandboxExtraWrite
		default:
			return projFieldPath
		}
	}
	return (f.focused + 1) % projFieldCount
}

// prevField returns the previous field index in the tab order.
func (f *ProjectForm) prevField() int {
	if f.editMode {
		switch f.focused {
		case projFieldPath:
			return projFieldSandboxExtraWrite
		case projFieldBranch:
			return projFieldPath
		case projFieldBackend:
			return projFieldBranch
		case projFieldSandboxDenyRead:
			return projFieldBackend
		default:
			return projFieldSandboxDenyRead
		}
	}
	return (f.focused - 1 + projFieldCount) % projFieldCount
}

// lastField returns the last editable field index (the submit-on-enter field).
func (f *ProjectForm) lastField() int {
	return projFieldSandboxExtraWrite
}

func (f *ProjectForm) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case detectBranchMsg:
		// Auto-fill branch field if user hasn't manually changed it.
		cur := strings.TrimSpace(f.inputs[projFieldBranch].Value())
		if msg.branch != "" && (cur == "master" || cur == "main" || cur == "") {
			f.inputs[projFieldBranch].SetValue(msg.branch)
		}
		return nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			f.canceled = true
			return nil
		case "tab", "down":
			prev := f.focused
			f.focused = f.nextField()
			cmds := []tea.Cmd{f.focusCurrent()}
			if prev == projFieldPath && !f.editMode {
				cmds = append(cmds, f.detectDefaultBranch())
			}
			return tea.Batch(cmds...)
		case "shift+tab", "up":
			prev := f.focused
			f.focused = f.prevField()
			cmds := []tea.Cmd{f.focusCurrent()}
			if prev == projFieldPath && !f.editMode {
				cmds = append(cmds, f.detectDefaultBranch())
			}
			return tea.Batch(cmds...)
		case "ctrl+e":
			// Cycle sandbox enabled state: inherit → enabled → disabled → inherit
			f.sandboxState = (f.sandboxState + 1) % 3
			return nil
		case "enter":
			if f.focused == f.lastField() {
				// Submit on enter at last field
				nameOK := f.editMode || strings.TrimSpace(f.inputs[projFieldName].Value()) != ""
				if nameOK && strings.TrimSpace(f.inputs[projFieldPath].Value()) != "" {
					f.done = true
				}
				return nil
			}
			f.focused = f.nextField()
			return f.focusCurrent()
		}
		if applyWordNavTextinput(msg, &f.inputs[f.focused]) {
			return nil
		}
	}

	var cmd tea.Cmd
	f.inputs[f.focused], cmd = f.inputs[f.focused].Update(msg)
	return cmd
}

type detectBranchMsg struct{ branch string }

// detectDefaultBranch returns a tea.Cmd that detects the default remote branch
// for the path currently entered in the form.
func (f *ProjectForm) detectDefaultBranch() tea.Cmd {
	path := strings.TrimSpace(f.inputs[projFieldPath].Value())
	if path == "" {
		return nil
	}
	return func() tea.Msg {
		return detectBranchMsg{branch: detectRemoteDefaultBranch(path)}
	}
}

// detectRemoteDefaultBranch returns a ref like "upstream/master" for the given repo path.
// Prefers upstream over origin. Falls back to "" if detection fails.
func detectRemoteDefaultBranch(repoDir string) string {
	// Try each remote in priority order: upstream first, then origin.
	for _, remote := range []string{"upstream", "origin"} {
		// Try symbolic-ref first (set by clone, or `git remote set-head <remote> --auto`).
		cmd := exec.Command("git", "symbolic-ref", "refs/remotes/"+remote+"/HEAD")
		cmd.Dir = repoDir
		if out, err := cmd.Output(); err == nil {
			ref := strings.TrimSpace(string(out))
			// refs/remotes/upstream/master → upstream/master
			ref = strings.TrimPrefix(ref, "refs/remotes/")
			if ref != "" {
				return ref
			}
		}

		// Fallback: query the remote directly.
		cmd = exec.Command("git", "ls-remote", "--symref", remote, "HEAD")
		cmd.Dir = repoDir
		if out, err := cmd.Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(line, "ref:") && strings.Contains(line, "HEAD") {
					parts := strings.Fields(line)
					if len(parts) >= 2 {
						branch := strings.TrimPrefix(parts[1], "refs/heads/")
						return remote + "/" + branch
					}
				}
			}
		}
	}

	return ""
}

func (f *ProjectForm) focusCurrent() tea.Cmd {
	cmds := make([]tea.Cmd, len(f.inputs))
	for i := range f.inputs {
		if i == f.focused {
			cmds[i] = f.inputs[i].Focus()
		} else {
			f.inputs[i].Blur()
		}
	}
	return tea.Batch(cmds...)
}

func (f *ProjectForm) ProjectEntry() (string, config.Project) {
	name := strings.TrimSpace(f.inputs[projFieldName].Value())
	if f.editMode {
		name = f.originalName
	}
	path := strings.TrimSpace(f.inputs[projFieldPath].Value())
	branch := strings.TrimSpace(f.inputs[projFieldBranch].Value())
	backend := strings.TrimSpace(f.inputs[projFieldBackend].Value())

	var sandboxEnabledPtr *bool
	switch f.sandboxState {
	case sandboxEnabled:
		v := true
		sandboxEnabledPtr = &v
	case sandboxDisabled:
		v := false
		sandboxEnabledPtr = &v
	}

	return name, config.Project{
		Path:    path,
		Branch:  branch,
		Backend: backend,
		Sandbox: config.ProjectSandboxConfig{
			Enabled:    sandboxEnabledPtr,
			DenyRead:   splitFormCSV(f.inputs[projFieldSandboxDenyRead].Value()),
			ExtraWrite: splitFormCSV(f.inputs[projFieldSandboxExtraWrite].Value()),
		},
	}
}

// splitFormCSV splits a comma-separated string, trimming whitespace and dropping empty entries.
func splitFormCSV(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func (f *ProjectForm) Done() bool     { return f.done }
func (f *ProjectForm) Canceled() bool { return f.canceled }

// sandboxStateLabel returns a styled label for the current sandbox enabled state.
func (f ProjectForm) sandboxStateLabel() string {
	switch f.sandboxState {
	case sandboxEnabled:
		return f.theme.Complete.Render("Enabled")
	case sandboxDisabled:
		return f.theme.Error.Render("Disabled")
	default:
		return f.theme.Dimmed.Render("Inherit (global)")
	}
}

func (f *ProjectForm) SetSize(w, h int) {
	f.width = w
	f.height = h
	inputWidth := f.modalWidth() - 6
	if inputWidth < 20 {
		inputWidth = 20
	}
	for i := range f.inputs {
		f.inputs[i].Width = inputWidth
	}
}

func (f ProjectForm) modalWidth() int {
	return clampModalWidth(f.width)
}

func (f ProjectForm) View() string {
	// Guard against zero-valued form (inputs not initialized via constructor).
	if len(f.inputs) == 0 {
		return ""
	}
	var b strings.Builder

	// Core fields: Name, Path, Branch, Backend
	coreLabels := []string{"Name:", "Path:", "Branch:", "Backend:"}
	for i, label := range coreLabels {
		if f.editMode && i == projFieldName {
			// In edit mode, name is read-only — show as a label, not an input.
			b.WriteString(f.theme.Dimmed.Render(label) + "\n")
			b.WriteString(f.theme.Normal.Render(f.originalName) + "\n\n")
			continue
		}
		style := f.theme.Dimmed
		if i == f.focused {
			style = f.theme.Selected
		}
		b.WriteString(style.Render(label) + "\n")
		b.WriteString(f.inputs[i].View() + "\n\n")
	}

	// Sandbox section
	b.WriteString(f.theme.Section.Render("SANDBOX") + "\n")
	sandboxLabel := f.sandboxStateLabel()
	b.WriteString(f.theme.Dimmed.Render("Enabled: ") + sandboxLabel + f.theme.Dimmed.Render("  (ctrl+e to cycle)") + "\n\n")

	sandboxFields := []struct {
		label string
		field int
	}{
		{"Sandbox Deny Read:", projFieldSandboxDenyRead},
		{"Sandbox Extra Write:", projFieldSandboxExtraWrite},
	}
	for _, sf := range sandboxFields {
		style := f.theme.Dimmed
		if sf.field == f.focused {
			style = f.theme.Selected
		}
		b.WriteString(style.Render(sf.label) + "\n")
		b.WriteString(f.inputs[sf.field].View() + "\n\n")
	}

	b.WriteString(f.theme.Help.Render("tab/shift+tab: navigate  ctrl+e: cycle sandbox  enter: submit  esc: cancel"))

	mw := f.modalWidth()

	title := "New Project"
	if f.editMode {
		title = "Edit Project"
	}

	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("87")).
		Padding(1, 2).
		Width(mw).
		Render(f.theme.Title.Render(title) + "\n\n" + b.String())

	return lipgloss.Place(f.width, f.height, lipgloss.Center, lipgloss.Center, modal)
}
