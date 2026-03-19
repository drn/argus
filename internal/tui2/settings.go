package tui2

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// settingsRowKind identifies what kind of row this is in the settings list.
type settingsRowKind int

const (
	srSection settingsRowKind = iota
	srWarning
	srProject
	srBackend
	srSandbox
	srLogs
	srKB
)

// settingsRow is a single row in the settings section list.
type settingsRow struct {
	kind  settingsRowKind
	label string
	key   string // project/backend name for lookup
}

// SettingsView is the tcell settings tab with two panels.
type SettingsView struct {
	*tview.Box

	rows    []settingsRow
	cursor  int
	scrollOff int

	// Data.
	warnings       []string
	projects       []projectEntry
	backends       []backendEntry
	defaultBackend string
	taskCounts     map[string]statusCounts

	// Sandbox.
	sandboxEnabled   bool
	sandboxAvailable bool
	sandboxDenyRead  []string
	sandboxExtraWrite []string

	// KB.
	kbEnabled      bool
	metisVaultPath string
	argusVaultPath string
	kbTaskSync     bool

	// DB reference for toggling values.
	database *db.DB
}

type projectEntry struct {
	Name    string
	Project config.Project
}

type backendEntry struct {
	Name    string
	Backend config.Backend
}

type statusCounts struct {
	pending    int
	inProgress int
	inReview   int
	complete   int
}

// NewSettingsView creates a new settings panel.
func NewSettingsView(database *db.DB) *SettingsView {
	return &SettingsView{
		Box:        tview.NewBox(),
		taskCounts: make(map[string]statusCounts),
		database:   database,
	}
}

// Refresh reloads all settings data from the database.
func (sv *SettingsView) Refresh() {
	cfg := sv.database.Config()

	// Warnings.
	sv.warnings = nil
	// Note: daemon connectivity warning is set externally via SetDaemonConnected.

	// Sandbox.
	sv.sandboxEnabled = cfg.Sandbox.Enabled
	sv.sandboxAvailable = agent.IsSandboxAvailable()
	sv.sandboxDenyRead = cfg.Sandbox.DenyRead
	sv.sandboxExtraWrite = cfg.Sandbox.ExtraWrite

	// Backends.
	sv.defaultBackend = cfg.Defaults.Backend
	sv.backends = nil
	names := make([]string, 0, len(cfg.Backends))
	for name := range cfg.Backends {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sv.backends = append(sv.backends, backendEntry{Name: name, Backend: cfg.Backends[name]})
	}

	// Projects.
	projMap := sv.database.Projects()
	sv.projects = nil
	projNames := make([]string, 0, len(projMap))
	for name := range projMap {
		projNames = append(projNames, name)
	}
	sort.Strings(projNames)
	for _, name := range projNames {
		sv.projects = append(sv.projects, projectEntry{Name: name, Project: projMap[name]})
	}

	// KB.
	sv.kbEnabled = cfg.KB.Enabled
	sv.metisVaultPath = cfg.KB.MetisVaultPath
	sv.argusVaultPath = cfg.KB.ArgusVaultPath
	sv.kbTaskSync = cfg.KB.AutoCreateTasks

	// Task counts.
	tasks := sv.database.Tasks()
	sv.setTasks(tasks)

	sv.rebuildRows()
}

func (sv *SettingsView) SetDaemonConnected(connected bool) {
	if !connected {
		sv.warnings = []string{"Running in-process mode (daemon not connected)"}
	} else {
		sv.warnings = nil
	}
	sv.rebuildRows()
}

func (sv *SettingsView) setTasks(tasks []*model.Task) {
	sv.taskCounts = make(map[string]statusCounts)
	for _, t := range tasks {
		c := sv.taskCounts[t.Project]
		switch t.Status {
		case model.StatusPending:
			c.pending++
		case model.StatusInProgress:
			c.inProgress++
		case model.StatusInReview:
			c.inReview++
		case model.StatusComplete:
			c.complete++
		}
		sv.taskCounts[t.Project] = c
	}
}

func (sv *SettingsView) rebuildRows() {
	sv.rows = nil

	// Status section.
	sv.rows = append(sv.rows, settingsRow{kind: srSection, label: "Status"})
	if len(sv.warnings) == 0 {
		sv.rows = append(sv.rows, settingsRow{kind: srWarning, label: "  System status", key: "_ok"})
	} else {
		for i, w := range sv.warnings {
			sv.rows = append(sv.rows, settingsRow{kind: srWarning, label: "  ⚠ " + w, key: fmt.Sprintf("_warn_%d", i)})
		}
	}

	// Sandbox section.
	sv.rows = append(sv.rows, settingsRow{kind: srSection, label: "Sandbox"})
	label := "  Disabled"
	if sv.sandboxEnabled {
		label = "  Enabled"
	}
	sv.rows = append(sv.rows, settingsRow{kind: srSandbox, label: label, key: "_sandbox"})

	// Projects section.
	sv.rows = append(sv.rows, settingsRow{kind: srSection, label: "Projects"})
	if len(sv.projects) == 0 {
		sv.rows = append(sv.rows, settingsRow{kind: srProject, label: "  (no projects)"})
	} else {
		for _, p := range sv.projects {
			sv.rows = append(sv.rows, settingsRow{kind: srProject, label: "  " + p.Name, key: p.Name})
		}
	}

	// Backends section.
	bLabel := "Backends"
	if sv.defaultBackend != "" {
		bLabel = fmt.Sprintf("Backends (default: %s)", sv.defaultBackend)
	}
	sv.rows = append(sv.rows, settingsRow{kind: srSection, label: bLabel})
	for _, b := range sv.backends {
		sv.rows = append(sv.rows, settingsRow{kind: srBackend, label: "  " + b.Name, key: b.Name})
	}

	// Knowledge Base section.
	sv.rows = append(sv.rows, settingsRow{kind: srSection, label: "Knowledge Base"})
	kbLabel := "  Disabled"
	if sv.kbEnabled {
		kbLabel = "  Enabled"
	}
	sv.rows = append(sv.rows, settingsRow{kind: srKB, label: kbLabel, key: "_kb"})

	// Logs section.
	sv.rows = append(sv.rows, settingsRow{kind: srSection, label: "Logs"})
	sv.rows = append(sv.rows, settingsRow{kind: srLogs, label: "  UX Log", key: "ux"})
	sv.rows = append(sv.rows, settingsRow{kind: srLogs, label: "  Daemon Log", key: "daemon"})

	// Clamp cursor.
	if sv.cursor >= len(sv.rows) {
		sv.cursor = max(0, len(sv.rows)-1)
	}
	sv.skipToSelectable(1)
}

// skipToSelectable moves the cursor to the next/prev selectable row.
func (sv *SettingsView) skipToSelectable(dir int) {
	for sv.cursor >= 0 && sv.cursor < len(sv.rows) && sv.rows[sv.cursor].kind == srSection {
		sv.cursor += dir
	}
	if sv.cursor < 0 || (sv.cursor < len(sv.rows) && sv.rows[sv.cursor].kind == srSection) {
		// Went past the top — search forward for the first selectable row.
		sv.cursor = 0
		for sv.cursor < len(sv.rows) && sv.rows[sv.cursor].kind == srSection {
			sv.cursor++
		}
	}
	if sv.cursor >= len(sv.rows) {
		// Went past the bottom — search backward for the last selectable row.
		sv.cursor = len(sv.rows) - 1
		for sv.cursor >= 0 && sv.rows[sv.cursor].kind == srSection {
			sv.cursor--
		}
	}
}

// SelectedRow returns the currently selected row.
func (sv *SettingsView) SelectedRow() *settingsRow {
	if sv.cursor >= 0 && sv.cursor < len(sv.rows) {
		return &sv.rows[sv.cursor]
	}
	return nil
}

// SelectedProject returns the project at the cursor, or nil.
func (sv *SettingsView) SelectedProject() *projectEntry {
	row := sv.SelectedRow()
	if row == nil || row.kind != srProject || row.key == "" {
		return nil
	}
	for i := range sv.projects {
		if sv.projects[i].Name == row.key {
			return &sv.projects[i]
		}
	}
	return nil
}

// SelectedBackend returns the backend at the cursor, or nil.
func (sv *SettingsView) SelectedBackend() *backendEntry {
	row := sv.SelectedRow()
	if row == nil || row.kind != srBackend {
		return nil
	}
	for i := range sv.backends {
		if sv.backends[i].Name == row.key {
			return &sv.backends[i]
		}
	}
	return nil
}

// --- Key handling ---

func (sv *SettingsView) HandleKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyUp:
		sv.moveCursor(-1)
		return true
	case tcell.KeyDown:
		sv.moveCursor(1)
		return true
	case tcell.KeyEnter:
		return sv.handleEnter()
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'k':
			sv.moveCursor(-1)
			return true
		case 'j':
			sv.moveCursor(1)
			return true
		case 'd':
			return sv.handleSetDefault()
		}
	}
	return false
}

func (sv *SettingsView) moveCursor(dir int) {
	sv.cursor += dir
	if sv.cursor < 0 {
		sv.cursor = 0
	}
	if sv.cursor >= len(sv.rows) {
		sv.cursor = len(sv.rows) - 1
	}
	sv.skipToSelectable(dir)
}

func (sv *SettingsView) handleEnter() bool {
	row := sv.SelectedRow()
	if row == nil {
		return false
	}
	switch row.kind {
	case srSandbox:
		// Toggle sandbox.
		sv.sandboxEnabled = !sv.sandboxEnabled
		val := "false"
		if sv.sandboxEnabled {
			val = "true"
		}
		sv.database.SetConfigValue("sandbox.enabled", val)
		uxlog.Log("[settings] sandbox toggled to %s", val)
		sv.rebuildRows()
		return true
	case srKB:
		// Toggle KB.
		sv.kbEnabled = !sv.kbEnabled
		val := "false"
		if sv.kbEnabled {
			val = "true"
		}
		sv.database.SetConfigValue("kb.enabled", val)
		uxlog.Log("[settings] KB toggled to %s", val)
		sv.rebuildRows()
		return true
	}
	return false
}

func (sv *SettingsView) handleSetDefault() bool {
	be := sv.SelectedBackend()
	if be == nil || be.Name == sv.defaultBackend {
		return false
	}
	sv.database.SetConfigValue("default_backend", be.Name)
	sv.defaultBackend = be.Name
	uxlog.Log("[settings] default backend set to %s", be.Name)
	sv.rebuildRows()
	return true
}

// --- Draw ---

func (sv *SettingsView) Draw(screen tcell.Screen) {
	sv.Box.DrawForSubclass(screen, sv)
	x, y, width, height := sv.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Two-panel layout: 40% list | 60% detail.
	leftW := width * 40 / 100
	if leftW < 25 {
		leftW = min(25, width)
	}
	rightW := width - leftW

	sv.renderList(screen, x, y, leftW, height)
	if rightW > 0 {
		sv.renderDetail(screen, x+leftW, y, rightW, height)
	}
}

func (sv *SettingsView) renderList(screen tcell.Screen, x, y, w, h int) {
	drawBorder(screen, x, y, w, h, StyleFocusedBorder)

	innerX := x + 1
	innerY := y + 1
	innerW := w - 2
	innerH := h - 2
	if innerW <= 0 || innerH <= 0 {
		return
	}

	// Adjust scroll offset.
	if sv.cursor < sv.scrollOff {
		sv.scrollOff = sv.cursor
	}
	if sv.cursor >= sv.scrollOff+innerH {
		sv.scrollOff = sv.cursor - innerH + 1
	}

	for i := range innerH {
		rowIdx := sv.scrollOff + i
		if rowIdx >= len(sv.rows) {
			break
		}
		row := sv.rows[rowIdx]
		style := tcell.StyleDefault

		switch row.kind {
		case srSection:
			style = tcell.StyleDefault.Foreground(ColorTitle).Bold(true)
		case srWarning:
			style = tcell.StyleDefault.Foreground(ColorInProgress)
		}
		if row.kind != srSection && rowIdx == sv.cursor {
			style = style.Background(ColorHighlight)
		}

		label := row.label
		if len(label) > innerW {
			label = label[:innerW]
		}
		drawText(screen, innerX, innerY+i, innerW, label, style)
	}
}

func (sv *SettingsView) renderDetail(screen tcell.Screen, x, y, w, h int) {
	drawBorder(screen, x, y, w, h, StyleBorder)

	innerX := x + 1
	innerY := y + 1
	innerW := w - 2
	innerH := h - 2
	if innerW <= 0 || innerH <= 0 {
		return
	}

	row := sv.SelectedRow()
	if row == nil {
		return
	}

	switch row.kind {
	case srWarning:
		sv.renderWarningDetail(screen, innerX, innerY, innerW, innerH, row)
	case srSandbox:
		sv.renderSandboxDetail(screen, innerX, innerY, innerW, innerH)
	case srProject:
		sv.renderProjectDetail(screen, innerX, innerY, innerW, innerH, row)
	case srBackend:
		sv.renderBackendDetail(screen, innerX, innerY, innerW, innerH, row)
	case srKB:
		sv.renderKBDetail(screen, innerX, innerY, innerW, innerH)
	case srLogs:
		sv.renderLogsDetail(screen, innerX, innerY, innerW, innerH, row)
	}
}

func (sv *SettingsView) renderWarningDetail(screen tcell.Screen, x, y, w, h int, row *settingsRow) {
	if row.key == "_ok" {
		drawText(screen, x, y, w, "System Status", StyleTitle)
		drawText(screen, x, y+2, w, "Daemon is running", tcell.StyleDefault.Foreground(ColorComplete))
	} else {
		drawText(screen, x, y, w, "Warning", StyleTitle)
		drawText(screen, x, y+2, w, row.label, tcell.StyleDefault.Foreground(ColorInProgress))
	}
}

func (sv *SettingsView) renderSandboxDetail(screen tcell.Screen, x, y, w, h int) {
	drawText(screen, x, y, w, "Sandbox Configuration", StyleTitle)
	row := 2

	status := "Disabled"
	statusColor := ColorError
	if sv.sandboxEnabled {
		status = "Enabled"
		statusColor = ColorComplete
	}
	drawText(screen, x, y+row, w, "Status: "+status, tcell.StyleDefault.Foreground(statusColor))
	row++

	avail := "Not available"
	if sv.sandboxAvailable {
		avail = "Available (sandbox-exec)"
	}
	drawText(screen, x, y+row, w, "Runtime: "+avail, StyleDimmed)
	row += 2

	if len(sv.sandboxDenyRead) > 0 {
		drawText(screen, x, y+row, w, "Deny Read:", tcell.StyleDefault.Foreground(ColorTitle))
		row++
		for _, p := range sv.sandboxDenyRead {
			if row >= h {
				break
			}
			drawText(screen, x, y+row, w, "  "+p, StyleDimmed)
			row++
		}
		row++
	}

	if len(sv.sandboxExtraWrite) > 0 {
		drawText(screen, x, y+row, w, "Extra Write:", tcell.StyleDefault.Foreground(ColorTitle))
		row++
		for _, p := range sv.sandboxExtraWrite {
			if row >= h {
				break
			}
			drawText(screen, x, y+row, w, "  "+p, StyleDimmed)
			row++
		}
	}

	if row+2 < h {
		drawText(screen, x, y+h-1, w, "[enter] toggle", StyleDimmed)
	}
}

func (sv *SettingsView) renderProjectDetail(screen tcell.Screen, x, y, w, h int, row *settingsRow) {
	pe := sv.SelectedProject()
	if pe == nil {
		drawText(screen, x, y, w, "(no project selected)", StyleDimmed)
		return
	}

	drawText(screen, x, y, w, pe.Name, StyleTitle)
	r := 2

	drawText(screen, x, y+r, w, "Config", tcell.StyleDefault.Foreground(ColorTitle))
	r++
	drawText(screen, x, y+r, w, "  Path: "+pe.Project.Path, StyleDimmed)
	r++
	drawText(screen, x, y+r, w, "  Branch: "+pe.Project.Branch, StyleDimmed)
	r++
	backend := pe.Project.Backend
	if backend == "" {
		backend = "(default)"
	}
	drawText(screen, x, y+r, w, "  Backend: "+backend, StyleDimmed)
	r += 2

	// Task counts.
	counts, ok := sv.taskCounts[pe.Name]
	if ok {
		drawText(screen, x, y+r, w, "Tasks", tcell.StyleDefault.Foreground(ColorTitle))
		r++
		total := counts.pending + counts.inProgress + counts.inReview + counts.complete
		drawText(screen, x, y+r, w, fmt.Sprintf("  %d pending  %d active  %d review  %d done",
			counts.pending, counts.inProgress, counts.inReview, counts.complete), StyleDimmed)
		r++
		if total > 0 && w > 4 {
			pct := counts.complete * 100 / total
			drawText(screen, x, y+r, w, fmt.Sprintf("  %d%% complete", pct), StyleDimmed)
		}
	}
}

func (sv *SettingsView) renderBackendDetail(screen tcell.Screen, x, y, w, h int, row *settingsRow) {
	be := sv.SelectedBackend()
	if be == nil {
		drawText(screen, x, y, w, "(no backend selected)", StyleDimmed)
		return
	}

	drawText(screen, x, y, w, be.Name, StyleTitle)
	r := 1
	if be.Name == sv.defaultBackend {
		drawText(screen, x, y+r, w, "★ Default backend", tcell.StyleDefault.Foreground(ColorComplete))
		r++
	}
	r++

	drawText(screen, x, y+r, w, "Config", tcell.StyleDefault.Foreground(ColorTitle))
	r++
	cmd := be.Backend.Command
	if len(cmd) > w-12 {
		cmd = cmd[:w-12] + "…"
	}
	drawText(screen, x, y+r, w, "  Command: "+cmd, StyleDimmed)
	r++
	drawText(screen, x, y+r, w, "  Prompt Flag: "+be.Backend.PromptFlag, StyleDimmed)
	r += 2

	hints := "[d] set as default"
	if be.Name == sv.defaultBackend {
		hints = "(already default)"
	}
	if r < h {
		drawText(screen, x, y+r, w, hints, StyleDimmed)
	}
}

func (sv *SettingsView) renderKBDetail(screen tcell.Screen, x, y, w, h int) {
	drawText(screen, x, y, w, "Knowledge Base", StyleTitle)
	r := 2

	status := "Disabled"
	statusColor := ColorError
	if sv.kbEnabled {
		status = "Enabled"
		statusColor = ColorComplete
	}
	drawText(screen, x, y+r, w, "Status: "+status, tcell.StyleDefault.Foreground(statusColor))
	r += 2

	drawText(screen, x, y+r, w, "Metis Vault:", tcell.StyleDefault.Foreground(ColorTitle))
	r++
	vault := sv.metisVaultPath
	if vault == "" {
		vault = "(not configured)"
	}
	drawText(screen, x, y+r, w, "  "+vault, StyleDimmed)
	r += 2

	drawText(screen, x, y+r, w, "Argus Vault:", tcell.StyleDefault.Foreground(ColorTitle))
	r++
	vault = sv.argusVaultPath
	if vault == "" {
		vault = "(not configured)"
	}
	drawText(screen, x, y+r, w, "  "+vault, StyleDimmed)
	r += 2

	syncLabel := "Off"
	if sv.kbTaskSync {
		syncLabel = "On"
	}
	drawText(screen, x, y+r, w, "Task Sync: "+syncLabel, StyleDimmed)
	r += 2

	if r < h {
		drawText(screen, x, y+r, w, "[enter] toggle KB", StyleDimmed)
	}
}

func (sv *SettingsView) renderLogsDetail(screen tcell.Screen, x, y, w, h int, row *settingsRow) {
	dataDir := db.DataDir()
	var title, logPath string
	switch row.key {
	case "ux":
		title = "UX Log"
		logPath = uxlog.Path(dataDir)
	case "daemon":
		title = "Daemon Log"
		logPath = dataDir + "/daemon.log"
	default:
		return
	}

	drawText(screen, x, y, w, title, StyleTitle)
	drawText(screen, x, y+2, w, logPath, StyleDimmed)

	// Show tail of the log file.
	lines := tailFile(logPath, h-4)
	for i, line := range lines {
		if y+4+i >= y+h {
			break
		}
		if len(line) > w {
			line = line[:w]
		}
		drawText(screen, x, y+4+i, w, line, tcell.StyleDefault)
	}
}

// tailFile reads the last n lines from a file.
func tailFile(path string, n int) []string {
	if n <= 0 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{"(file not found)"}
	}
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return []string{"(empty)"}
	}
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// --- Helpers ---

func drawMultiLine(screen tcell.Screen, x, y, w int, text string, style tcell.Style) int {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if len(line) > w {
			line = line[:w]
		}
		drawText(screen, x, y+i, w, line, style)
	}
	return len(lines)
}
