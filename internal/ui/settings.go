package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// settingsRowKind identifies what type of row is in the settings list.
type settingsRowKind int

const (
	settingsRowSection  settingsRowKind = iota // non-selectable section header
	settingsRowWarning                         // warning item
	settingsRowProject                         // project item
	settingsRowBackend                         // backend item
	settingsRowSandbox                         // sandbox config item
	settingsRowDaemonLogs                      // daemon logs item
	settingsRowUXLogs                          // UX debug logs item
)

type settingsRow struct {
	kind  settingsRowKind
	label string
	key   string // project name or backend name for lookup
}

// SettingsView renders the settings tab with sections for status, projects, and backends.
type SettingsView struct {
	rows             []settingsRow
	scroll           ScrollState
	theme            Theme
	width            int
	height           int
	warnings         []string
	projects         []projectEntry
	backends         []backendEntry
	taskCounts       map[string]statusCounts
	sandboxEnabled    bool
	sandboxAvailable  bool
	sandboxDenyRead   []string
	sandboxExtraWrite []string
}

type backendEntry struct {
	Name    string
	Backend config.Backend
}

// NewSettingsView creates a new settings view.
func NewSettingsView(theme Theme) SettingsView {
	return SettingsView{
		theme:      theme,
		taskCounts: make(map[string]statusCounts),
	}
}

func (sv *SettingsView) SetSize(w, h int) {
	sv.width = w
	sv.height = h
}

// SetWarnings updates the list of status warnings.
func (sv *SettingsView) SetWarnings(warnings []string) {
	sv.warnings = warnings
	sv.rebuildRows()
}

// SetProjects updates the project list.
func (sv *SettingsView) SetProjects(projects map[string]config.Project) {
	sv.projects = nil
	for name, proj := range projects {
		sv.projects = append(sv.projects, projectEntry{Name: name, Project: proj})
	}
	sortProjects(sv.projects)
	sv.rebuildRows()
}

// SetBackends updates the backend list.
func (sv *SettingsView) SetBackends(backends map[string]config.Backend) {
	sv.backends = nil
	for name, b := range backends {
		sv.backends = append(sv.backends, backendEntry{Name: name, Backend: b})
	}
	// Sort alphabetically
	sortBackends(sv.backends)
	sv.rebuildRows()
}

// SetTasks computes per-project task status counts.
func (sv *SettingsView) SetTasks(tasks []*model.Task) {
	counts := make(map[string]statusCounts, len(sv.projects))
	for _, t := range tasks {
		sc := counts[t.Project]
		switch t.Status {
		case model.StatusPending:
			sc.Pending++
		case model.StatusInProgress:
			sc.InProgress++
		case model.StatusInReview:
			sc.InReview++
		case model.StatusComplete:
			sc.Complete++
		}
		counts[t.Project] = sc
	}
	sv.taskCounts = counts
}

// TaskCounts returns the task status counts for the named project.
func (sv *SettingsView) TaskCounts(name string) statusCounts {
	return sv.taskCounts[name]
}

// SetSandboxConfig updates the sandbox display state.
func (sv *SettingsView) SetSandboxConfig(enabled, available bool, denyRead, extraWrite []string) {
	sv.sandboxEnabled = enabled
	sv.sandboxAvailable = available
	sv.sandboxDenyRead = denyRead
	sv.sandboxExtraWrite = extraWrite
	sv.rebuildRows()
}

func (sv *SettingsView) rebuildRows() {
	sv.rows = nil

	// STATUS section
	sv.rows = append(sv.rows, settingsRow{kind: settingsRowSection, label: "STATUS"})
	if len(sv.warnings) == 0 {
		// No warnings — still show an "all good" row
		sv.rows = append(sv.rows, settingsRow{kind: settingsRowWarning, label: "System status", key: "_ok"})
	} else {
		for i, w := range sv.warnings {
			sv.rows = append(sv.rows, settingsRow{kind: settingsRowWarning, label: w, key: fmt.Sprintf("_warn_%d", i)})
		}
	}

	// Log viewer rows (in STATUS section)
	sv.rows = append(sv.rows, settingsRow{kind: settingsRowDaemonLogs, label: "Daemon Logs", key: "_logs"})
	sv.rows = append(sv.rows, settingsRow{kind: settingsRowUXLogs, label: "UX Logs", key: "_uxlogs"})

	// SANDBOX section
	sv.rows = append(sv.rows, settingsRow{kind: settingsRowSection, label: "SANDBOX"})
	if sv.sandboxEnabled {
		sv.rows = append(sv.rows, settingsRow{kind: settingsRowSandbox, label: "Enabled", key: "_sandbox"})
	} else {
		sv.rows = append(sv.rows, settingsRow{kind: settingsRowSandbox, label: "Disabled", key: "_sandbox"})
	}

	// PROJECTS section
	sv.rows = append(sv.rows, settingsRow{kind: settingsRowSection, label: "PROJECTS"})
	for _, p := range sv.projects {
		sv.rows = append(sv.rows, settingsRow{kind: settingsRowProject, label: p.Name, key: p.Name})
	}
	if len(sv.projects) == 0 {
		// Show a hint row that is selectable so n=new still works
		sv.rows = append(sv.rows, settingsRow{kind: settingsRowProject, label: "(no projects)", key: ""})
	}

	// BACKENDS section
	sv.rows = append(sv.rows, settingsRow{kind: settingsRowSection, label: "BACKENDS"})
	for _, b := range sv.backends {
		sv.rows = append(sv.rows, settingsRow{kind: settingsRowBackend, label: b.Name, key: b.Name})
	}

	sv.scroll.ClampCursor(len(sv.rows))
	// Ensure cursor is on a selectable row.
	sv.ensureSelectableRow(1)
}

// selectableCount returns the number of selectable (non-header) rows.
func (sv *SettingsView) selectableCount() int {
	n := 0
	for _, r := range sv.rows {
		if r.kind != settingsRowSection {
			n++
		}
	}
	return n
}

// CursorUp moves cursor to previous selectable row.
func (sv *SettingsView) CursorUp() {
	sv.moveCursor(-1)
}

// CursorDown moves cursor to next selectable row.
func (sv *SettingsView) CursorDown() {
	sv.moveCursor(1)
}

func (sv *SettingsView) moveCursor(dir int) {
	if len(sv.rows) == 0 {
		return
	}
	cur := sv.scroll.Cursor()
	for {
		cur += dir
		if cur < 0 || cur >= len(sv.rows) {
			return // hit boundary
		}
		if sv.rows[cur].kind != settingsRowSection {
			sv.scroll.SetCursor(cur)
			sv.adjustOffset()
			return
		}
	}
}

func (sv *SettingsView) ensureSelectableRow(dir int) {
	if len(sv.rows) == 0 {
		return
	}
	cur := sv.scroll.Cursor()
	if cur < len(sv.rows) && sv.rows[cur].kind != settingsRowSection {
		return
	}
	sv.moveCursor(dir)
}

func (sv *SettingsView) adjustOffset() {
	visible := sv.visibleRows()
	cur := sv.scroll.Cursor()
	off := sv.scroll.Offset()
	if cur < off {
		sv.scroll.SetOffset(cur)
	} else if cur >= off+visible {
		sv.scroll.SetOffset(cur - visible + 1)
	}
}

func (sv *SettingsView) visibleRows() int {
	if sv.height <= 2 {
		return 1
	}
	return sv.height - 2 // account for borders
}

// Selected returns the currently selected row, or nil if none.
func (sv *SettingsView) Selected() *settingsRow {
	if len(sv.rows) == 0 {
		return nil
	}
	c := sv.scroll.Cursor()
	if c >= 0 && c < len(sv.rows) && sv.rows[c].kind != settingsRowSection {
		return &sv.rows[c]
	}
	return nil
}

// SelectedProject returns the project entry for the currently selected project row.
func (sv *SettingsView) SelectedProject() *projectEntry {
	sel := sv.Selected()
	if sel == nil || sel.kind != settingsRowProject || sel.key == "" {
		return nil
	}
	for i := range sv.projects {
		if sv.projects[i].Name == sel.key {
			return &sv.projects[i]
		}
	}
	return nil
}

// SelectedBackend returns the backend entry for the currently selected backend row.
func (sv *SettingsView) SelectedBackend() *backendEntry {
	sel := sv.Selected()
	if sel == nil || sel.kind != settingsRowBackend {
		return nil
	}
	for i := range sv.backends {
		if sv.backends[i].Name == sel.key {
			return &sv.backends[i]
		}
	}
	return nil
}

// View renders the left panel of the settings view (section list).
func (sv SettingsView) View() string {
	if len(sv.rows) == 0 {
		return "\n" + sv.theme.Dimmed.Render("    No settings available.")
	}

	var b strings.Builder
	visible := sv.visibleRows()
	offset := sv.scroll.Offset()
	cursor := sv.scroll.Cursor()
	end := offset + visible
	if end > len(sv.rows) {
		end = len(sv.rows)
	}

	for i := offset; i < end; i++ {
		row := sv.rows[i]
		switch row.kind {
		case settingsRowSection:
			if i > offset {
				b.WriteString("\n")
			}
			b.WriteString(" " + sv.theme.Section.Render(" "+row.label) + "\n")
		default:
			selected := i == cursor
			cur := "  "
			if selected {
				cur = sv.theme.Selected.Render(">") + " "
			}

			labelStyle := sv.theme.Normal
			if selected {
				labelStyle = sv.theme.Selected
			}
			if row.kind == settingsRowWarning && row.key != "_ok" {
				labelStyle = sv.theme.Error
				if selected {
					labelStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
				}
			}
			if row.kind == settingsRowWarning && row.key == "_ok" {
				labelStyle = sv.theme.Complete
				if selected {
					labelStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("78"))
				}
			}

			icon := ""
			switch row.kind {
			case settingsRowWarning:
				if row.key == "_ok" {
					icon = "  "
				} else {
					icon = "  "
				}
			case settingsRowSandbox:
				if sv.sandboxEnabled {
					icon = "  "
				} else {
					icon = "  "
				}
			case settingsRowDaemonLogs:
				icon = "  "
			case settingsRowUXLogs:
				icon = "  "
			case settingsRowProject:
				icon = "  "
			case settingsRowBackend:
				icon = "  "
			}

			b.WriteString(" " + cur + icon + labelStyle.Render(row.label) + "\n")
		}
	}

	return b.String()
}

// RenderDetail renders the right panel detail for the selected item.
func (sv SettingsView) RenderDetail(rightWidth, contentHeight int) string {
	sel := sv.Selected()
	if sel == nil {
		return borderedPanel(rightWidth, contentHeight, false,
			sv.theme.Dimmed.Render(" No item selected"))
	}

	innerW := max(rightWidth-4, 10)
	innerH := max(contentHeight-2, 1)

	var content string
	switch sel.kind {
	case settingsRowWarning:
		content = sv.renderWarningDetail(sel, innerW)
	case settingsRowSandbox:
		content = sv.renderSandboxDetail(innerW)
	case settingsRowDaemonLogs:
		content = sv.renderDaemonLogsDetail(innerW)
	case settingsRowUXLogs:
		content = sv.renderUXLogsDetail(innerW)
	case settingsRowProject:
		content = sv.renderProjectDetail(sel, innerW)
	case settingsRowBackend:
		content = sv.renderBackendDetail(sel, innerW)
	default:
		content = sv.theme.Dimmed.Render(" Select an item")
	}

	lines := strings.Split(content, "\n")
	if len(lines) > innerH {
		lines = lines[:innerH]
		content = strings.Join(lines, "\n")
	}

	return borderedPanel(rightWidth, contentHeight, false, content)
}

func (sv SettingsView) renderDaemonLogsDetail(_ int) string {
	var b strings.Builder
	b.WriteString(sv.theme.Title.Render(" Daemon Logs") + "\n\n")
	b.WriteString("  " + sv.theme.Dimmed.Render("View recent daemon log output.") + "\n\n")
	b.WriteString("  " + sv.theme.Help.Render("Press [enter] to open log viewer") + "\n")
	return b.String()
}

func (sv SettingsView) renderUXLogsDetail(_ int) string {
	var b strings.Builder
	b.WriteString(sv.theme.Title.Render(" UX Logs") + "\n\n")
	b.WriteString("  " + sv.theme.Dimmed.Render("View TUI debug log output.") + "\n")
	b.WriteString("  " + sv.theme.Dimmed.Render("Tracks task starts, exits, status") + "\n")
	b.WriteString("  " + sv.theme.Dimmed.Render("transitions, and daemon client events.") + "\n\n")
	b.WriteString("  " + sv.theme.Help.Render("Press [enter] to open log viewer") + "\n")
	return b.String()
}

func (sv SettingsView) renderSandboxDetail(_ int) string {
	var b strings.Builder

	b.WriteString(sv.theme.Title.Render(" Sandbox") + "\n\n")

	// Status
	if sv.sandboxEnabled {
		b.WriteString("  " + sv.theme.Complete.Render("Enabled") + "\n")
	} else {
		b.WriteString("  " + sv.theme.Dimmed.Render("Disabled") + "\n")
	}

	// Availability
	if sv.sandboxAvailable {
		b.WriteString("  " + sv.theme.Complete.Render("sandbox-exec available") + "\n")
	} else {
		b.WriteString("  " + sv.theme.Error.Render("sandbox-exec not found") + "\n")
	}

	// Deny read
	if len(sv.sandboxDenyRead) > 0 {
		b.WriteString("\n" + sv.theme.Section.Render("  DENY READ") + "\n")
		for _, p := range sv.sandboxDenyRead {
			b.WriteString("  " + sv.theme.Normal.Render(p) + "\n")
		}
	}

	// Extra write
	if len(sv.sandboxExtraWrite) > 0 {
		b.WriteString("\n" + sv.theme.Section.Render("  EXTRA WRITE") + "\n")
		for _, p := range sv.sandboxExtraWrite {
			b.WriteString("  " + sv.theme.Normal.Render(p) + "\n")
		}
	}

	return b.String()
}

func (sv SettingsView) renderWarningDetail(sel *settingsRow, _ int) string {
	var b strings.Builder

	if sel.key == "_ok" {
		b.WriteString(sv.theme.Title.Render(" System Status") + "\n\n")
		b.WriteString(sv.theme.Complete.Render("  System status") + "\n\n")
		b.WriteString("  " + sv.theme.Dimmed.Render("Daemon is running. Sessions will persist") + "\n")
		b.WriteString("  " + sv.theme.Dimmed.Render("across TUI restarts.") + "\n\n")
		b.WriteString("  " + sv.theme.Dimmed.Render("Press [r] to restart the daemon.") + "\n")
	} else {
		b.WriteString(sv.theme.Title.Render(" Warning") + "\n\n")
		b.WriteString("  " + sv.theme.Error.Render(sel.label) + "\n\n")
		// Provide contextual help based on the warning
		if strings.Contains(sel.label, "in-process") || strings.Contains(sel.label, "persist") {
			b.WriteString("  " + sv.theme.Normal.Render("Agent sessions are running inside the") + "\n")
			b.WriteString("  " + sv.theme.Normal.Render("TUI process. When Argus exits, all") + "\n")
			b.WriteString("  " + sv.theme.Normal.Render("running sessions will be terminated.") + "\n\n")
			b.WriteString("  " + sv.theme.Dimmed.Render("This usually means the daemon failed") + "\n")
			b.WriteString("  " + sv.theme.Dimmed.Render("to auto-start. Try running:") + "\n\n")
			b.WriteString("  " + sv.theme.Normal.Render("  argus daemon &") + "\n")
		}
	}

	return b.String()
}

func (sv SettingsView) renderProjectDetail(sel *settingsRow, innerW int) string {
	if sel.key == "" {
		return sv.theme.Dimmed.Render(" No projects configured.\n\n Press [n] to add one.")
	}

	entry := sv.SelectedProject()
	if entry == nil {
		return sv.theme.Dimmed.Render(" Project not found")
	}

	var b strings.Builder

	// Title
	name := entry.Name
	if len(name) > innerW-2 {
		name = name[:innerW-5] + "..."
	}
	b.WriteString(sv.theme.Title.Render(" "+name) + "\n\n")

	// Configuration section
	b.WriteString(sv.theme.Section.Render("  CONFIG") + "\n")
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
		b.WriteString("  " + sv.theme.Dimmed.Render(f.label+": ") + sv.theme.Normal.Render(val) + "\n")
	}

	// Task summary section
	sc := sv.taskCounts[entry.Name]
	total := sc.Total()

	b.WriteString("\n" + sv.theme.Section.Render("  TASKS") + "\n")

	if total == 0 {
		b.WriteString("  " + sv.theme.Dimmed.Render("No tasks yet. Press [1] to switch to tasks.") + "\n")
	} else {
		statuses := []struct {
			label string
			count int
			style lipgloss.Style
		}{
			{"Pending", sc.Pending, sv.theme.Pending},
			{"In Progress", sc.InProgress, sv.theme.InProgress},
			{"In Review", sc.InReview, sv.theme.InReview},
			{"Complete", sc.Complete, sv.theme.Complete},
		}
		for _, s := range statuses {
			if s.count > 0 {
				b.WriteString(fmt.Sprintf("  %s  %s\n",
					s.style.Render(fmt.Sprintf("%2d", s.count)),
					sv.theme.Normal.Render(s.label)))
			}
		}

		barWidth := innerW - 4
		if barWidth > 0 && total > 0 {
			b.WriteString("\n")
			bar := renderProgressBar(sv.theme, sc, barWidth)
			b.WriteString("  " + bar + "\n")
			pct := sc.Complete * 100 / total
			b.WriteString("  " + sv.theme.Dimmed.Render(fmt.Sprintf("%d%% complete", pct)) + "\n")
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func (sv SettingsView) renderBackendDetail(sel *settingsRow, _ int) string {
	entry := sv.SelectedBackend()
	if entry == nil {
		return sv.theme.Dimmed.Render(" Backend not found")
	}

	var b strings.Builder

	b.WriteString(sv.theme.Title.Render(" "+entry.Name) + "\n\n")

	b.WriteString(sv.theme.Section.Render("  CONFIG") + "\n")
	b.WriteString("  " + sv.theme.Dimmed.Render("Command: ") + sv.theme.Normal.Render(entry.Backend.Command) + "\n")

	promptFlag := entry.Backend.PromptFlag
	if promptFlag == "" {
		promptFlag = "(none — uses stdin)"
	}
	b.WriteString("  " + sv.theme.Dimmed.Render("Prompt Flag: ") + sv.theme.Normal.Render(promptFlag) + "\n")

	return b.String()
}

// renderProgressBar renders a horizontal progress bar with colored segments.
func renderProgressBar(theme Theme, sc statusCounts, width int) string {
	total := sc.Total()
	if total == 0 || width <= 0 {
		return ""
	}

	segments := []struct {
		count int
		ch    string
		color string
	}{
		{sc.Complete, "█", "78"},
		{sc.InReview, "█", "81"},
		{sc.InProgress, "█", "214"},
		{sc.Pending, "░", "245"},
	}

	lastNonZero := -1
	for i, seg := range segments {
		if seg.count > 0 {
			lastNonZero = i
		}
	}

	var bar strings.Builder
	remaining := width
	for i, seg := range segments {
		if seg.count == 0 {
			continue
		}
		w := seg.count * width / total
		if w < 1 {
			w = 1
		}
		if i == lastNonZero {
			w = remaining
		}
		if w <= 0 {
			continue
		}
		remaining -= w
		bar.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color(seg.color)).
			Render(strings.Repeat(seg.ch, w)))
	}

	return bar.String()
}

func sortBackends(entries []backendEntry) {
	// Simple insertion sort — typically very few backends.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].Name < entries[j-1].Name; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}
