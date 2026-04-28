package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/spinner"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
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
	srToDoProject
	srReviewPrompt
	srAPI
	srDaemon
	srSpinner
	srVaultPath
	srUpdateArgus
	srSourcePath
	srSchedule
)

// Vault key constants used in settingsRow.key for vault path rows.
const (
	vaultKeyMetis = "_metis_vault"
	vaultKeyArgus = "_argus_vault"
)

// svMaxACVisible is the maximum number of vault path autocomplete rows shown.
const svMaxACVisible = 8

// settingsRow is a single row in the settings section list.
type settingsRow struct {
	kind  settingsRowKind
	label string
	key   string // project/backend name for lookup
}

// SettingsView is the tcell settings tab with two panels.
type SettingsView struct {
	*tview.Box

	rows      []settingsRow
	cursor    int
	scrollOff int

	// Data.
	warnings       []string
	projects       []projectEntry
	backends       []backendEntry
	schedules      []*model.ScheduledTask
	defaultBackend string
	taskCounts     map[string]statusCounts

	// Sandbox.
	sandboxEnabled    bool
	sandboxAvailable  bool
	sandboxDenyRead   []string
	sandboxExtraWrite []string

	// KB.
	kbEnabled         bool
	metisVaultPath    string
	argusVaultPath    string
	metisVaultAtBoot  string // value when daemon started; used to show "restart required"
	argusVaultAtBoot  string
	vaultBootRecorded bool // true after first Refresh captures boot values
	kbTaskSync        bool
	autoStartTodos    bool
	autoStartInterval int

	// API.
	apiEnabled       bool
	apiEnabledAtBoot bool // value when daemon started; used to show "restart required"
	apiBootRecorded  bool // true after first Refresh captures boot value
	apiPort          int

	// Spinner.
	spinnerStyle string // current spinner style name

	// ToDo defaults.
	todoProject  string   // current default todo project
	projectNames []string // sorted project names for cycling

	// Review prompt.
	reviewPrompt  string // current review prompt template
	editingPrompt bool   // true when inline-editing the review prompt
	editPromptBuf string // buffer for in-progress edit

	// Vault path editing.
	editingVault     string   // which vault is being edited: vaultKeyMetis or vaultKeyArgus, or "" if not editing
	editVaultBuf     string   // buffer for in-progress vault path edit
	discoveredVaults []string // sorted absolute paths of discovered iCloud Obsidian vaults
	vaultAC          dirAC    // directory autocomplete for vault path editing

	// Logs detail scroll.
	logScrollOff int
	logLines     []string // cached lines for current log
	logKey       string   // which log is cached ("ux" or "daemon")

	// Daemon.
	daemonConnected  bool
	daemonRestarting bool

	// Self-update.
	argusSourcePath string
	editingSource   bool   // true while inline-editing the source path
	editSourceBuf   string // buffer for the source-path edit
	updating        bool   // true while go install is running
	updateStatus    string // last-result status line shown in detail panel
	updateOutput    string // last go-install output (for detail panel)

	// Callbacks.
	OnRestartDaemon  func()
	OnUpdateArgus    func() // triggered by the "Update Argus" row
	OnNewProject     func()
	OnEditProject    func(name string, p config.Project)
	OnDeleteProject  func(name string)
	OnNewBackend     func()
	OnEditBackend    func(name string, b config.Backend)
	OnQuickAdd       func()
	OnNewSchedule    func()
	OnEditSchedule   func(s *model.ScheduledTask)
	OnDeleteSchedule func(id string)
	OnRunSchedule    func(id string)

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
	// Show empty on error — settings view degrades gracefully to an empty list.
	projMap, err := sv.database.Projects()
	if err != nil {
		uxlog.Log("[settings] failed to load projects: %v", err)
	}
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
	if !sv.vaultBootRecorded {
		sv.metisVaultAtBoot = cfg.KB.MetisVaultPath
		sv.argusVaultAtBoot = cfg.KB.ArgusVaultPath
		sv.vaultBootRecorded = true
	}
	// Discover vaults once — filesystem scan is blocking I/O, avoid on every Refresh.
	if sv.discoveredVaults == nil {
		sv.discoveredVaults = config.DiscoverICloudVaults()
		uxlog.Log("[settings] discovered %d iCloud vaults", len(sv.discoveredVaults))
	}
	sv.kbTaskSync = cfg.KB.AutoCreateTasks
	sv.autoStartTodos = cfg.KB.AutoStartTodos
	sv.autoStartInterval = cfg.KB.AutoStartInterval
	if sv.autoStartInterval <= 0 {
		sv.autoStartInterval = config.DefaultAutoStartInterval
	}

	// API.
	if !sv.apiBootRecorded {
		sv.apiEnabledAtBoot = cfg.API.Enabled
		sv.apiBootRecorded = true
	}
	sv.apiEnabled = cfg.API.Enabled
	sv.apiPort = cfg.API.HTTPPort
	if sv.apiPort == 0 {
		sv.apiPort = 7743
	}

	// Spinner.
	sv.spinnerStyle = cfg.UI.SpinnerStyle
	if sv.spinnerStyle == "" {
		sv.spinnerStyle = string(spinner.StyleProgress)
	}

	// ToDo defaults.
	sv.todoProject = cfg.Defaults.TodoProject
	sv.projectNames = projNames

	// Review prompt.
	sv.reviewPrompt = cfg.Defaults.ReviewPrompt

	// Argus self-update source path.
	sv.argusSourcePath = cfg.Argus.SourcePath

	// Task counts — show empty on error (same graceful degradation as projects).
	tasks, err := sv.database.Tasks()
	if err != nil {
		uxlog.Log("[settings] failed to load tasks: %v", err)
	}
	sv.setTasks(tasks)

	// Schedules.
	schedules, err := sv.database.Schedules()
	if err != nil {
		uxlog.Log("[settings] failed to load schedules: %v", err)
	}
	sv.schedules = schedules

	sv.rebuildRows()
}

func (sv *SettingsView) SetDaemonConnected(connected bool) {
	sv.daemonConnected = connected
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
	if sv.daemonConnected {
		label := "  Restart Daemon"
		if sv.daemonRestarting {
			label = "  Restarting..."
		}
		sv.rows = append(sv.rows, settingsRow{kind: srDaemon, label: label, key: "_daemon_restart"})

		sourceLabel := "  Source path: " + sv.argusSourcePath
		if sv.editingSource {
			sourceLabel = "  Source path: " + sv.editSourceBuf + "▎"
		} else if sv.argusSourcePath == "" {
			sourceLabel = "  Source path: (not configured)"
		}
		sv.rows = append(sv.rows, settingsRow{kind: srSourcePath, label: sourceLabel, key: "_argus_source"})

		updateLabel := "  Update Argus (go install + restart)"
		if sv.updating {
			updateLabel = "  Updating..."
		}
		sv.rows = append(sv.rows, settingsRow{kind: srUpdateArgus, label: updateLabel, key: "_argus_update"})
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

	// Scheduled tasks section.
	sv.rows = append(sv.rows, settingsRow{kind: srSection, label: "Scheduled Tasks"})
	if len(sv.schedules) == 0 {
		sv.rows = append(sv.rows, settingsRow{kind: srSchedule, label: "  (no schedules — press n to add)"})
	} else {
		for _, s := range sv.schedules {
			marker := "  "
			if !s.Enabled {
				marker = "  ⊘ "
			}
			sv.rows = append(sv.rows, settingsRow{kind: srSchedule, label: marker + s.Name, key: s.ID})
		}
	}

	// Knowledge Base section.
	sv.rows = append(sv.rows, settingsRow{kind: srSection, label: "Knowledge Base"})
	kbLabel := "  Disabled"
	if sv.kbEnabled {
		kbLabel = "  Enabled"
	}
	sv.rows = append(sv.rows, settingsRow{kind: srKB, label: kbLabel, key: "_kb"})

	// Vault path rows.
	metisLabel := "  Metis: " + sv.metisVaultPath
	if sv.editingVault == vaultKeyMetis {
		metisLabel = "  Metis: " + sv.editVaultBuf + "▎"
	} else if sv.metisVaultPath == "" {
		metisLabel = "  Metis: (not configured)"
	}
	if sv.vaultBootRecorded && sv.metisVaultPath != sv.metisVaultAtBoot {
		metisLabel += " (restart required)"
	}
	sv.rows = append(sv.rows, settingsRow{kind: srVaultPath, label: metisLabel, key: vaultKeyMetis})

	argusLabel := "  Argus: " + sv.argusVaultPath
	if sv.editingVault == vaultKeyArgus {
		argusLabel = "  Argus: " + sv.editVaultBuf + "▎"
	} else if sv.argusVaultPath == "" {
		argusLabel = "  Argus: (not configured)"
	}
	if sv.vaultBootRecorded && sv.argusVaultPath != sv.argusVaultAtBoot {
		argusLabel += " (restart required)"
	}
	sv.rows = append(sv.rows, settingsRow{kind: srVaultPath, label: argusLabel, key: vaultKeyArgus})

	// API section.
	sv.rows = append(sv.rows, settingsRow{kind: srSection, label: "Remote API"})
	apiLabel := "  Disabled"
	if sv.apiEnabled {
		apiLabel = fmt.Sprintf("  Enabled (port %d)", sv.apiPort)
	}
	if sv.apiBootRecorded && sv.apiEnabled != sv.apiEnabledAtBoot {
		apiLabel += " (restart required)"
	}
	sv.rows = append(sv.rows, settingsRow{kind: srAPI, label: apiLabel, key: "_api"})

	// Default ToDo project.
	todoLabel := "  ToDo Project: (none)"
	if sv.todoProject != "" {
		todoLabel = "  ToDo Project: " + sv.todoProject
	}
	sv.rows = append(sv.rows, settingsRow{kind: srToDoProject, label: todoLabel, key: "_todo_project"})

	// Review prompt.
	rpLabel := "  Review Prompt: " + sv.reviewPrompt
	if sv.editingPrompt {
		rpLabel = "  Review Prompt: " + sv.editPromptBuf + "▎"
	}
	sv.rows = append(sv.rows, settingsRow{kind: srReviewPrompt, label: rpLabel, key: "_review_prompt"})

	// Spinner style.
	spinLabel := fmt.Sprintf("  Spinner: %s", spinner.Get(spinner.Style(sv.spinnerStyle)).Label)
	sv.rows = append(sv.rows, settingsRow{kind: srSpinner, label: spinLabel, key: "_spinner"})

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

// PasteHandler implements tview's paste interface for inline editors.
func (sv *SettingsView) PasteHandler() func(pastedText string, setFocus func(p tview.Primitive)) {
	return sv.WrapPasteHandler(func(pastedText string, setFocus func(p tview.Primitive)) {
		if pastedText == "" {
			return
		}
		if sv.editingPrompt {
			sv.editPromptBuf += pastedText
			sv.rebuildRows()
		} else if sv.editingVault != "" {
			sv.editVaultBuf += pastedText
			sv.vaultAC.Update(sv.editVaultBuf)
			sv.rebuildRows()
		} else if sv.editingSource {
			sv.editSourceBuf += pastedText
			sv.rebuildRows()
		}
	})
}

// IsEditing returns true when the user is inline-editing any field.
func (sv *SettingsView) IsEditing() bool {
	return sv.editingPrompt || sv.editingVault != "" || sv.editingSource
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
	if sv.editingPrompt {
		return sv.handleEditPromptKey(ev)
	}
	if sv.editingVault != "" {
		return sv.handleEditVaultKey(ev)
	}
	if sv.editingSource {
		return sv.handleEditSourceKey(ev)
	}
	switch ev.Key() {
	case tcell.KeyUp:
		sv.moveCursor(-1)
		return true
	case tcell.KeyDown:
		sv.moveCursor(1)
		return true
	case tcell.KeyLeft:
		switch sv.currentSection() {
		case srToDoProject:
			sv.cycleTodoProject(-1)
			return true
		case srSpinner:
			sv.cycleSpinner(-1)
			return true
		case srVaultPath:
			sv.cycleVaultPath(-1)
			return true
		}
		return false
	case tcell.KeyRight:
		switch sv.currentSection() {
		case srToDoProject:
			sv.cycleTodoProject(1)
			return true
		case srSpinner:
			sv.cycleSpinner(1)
			return true
		case srVaultPath:
			sv.cycleVaultPath(1)
			return true
		}
		return false
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
			return sv.handleDeleteOrDefault()
		case 'n':
			return sv.handleNew()
		case 'e':
			return sv.handleEdit()
		case 'a':
			return sv.handleToggleAutoStart()
		case 'i':
			return sv.handleQuickAdd()
		case 't':
			return sv.handleToggleSchedule()
		case 'r':
			return sv.handleRunSchedule()
		}
	}
	return false
}

func (sv *SettingsView) handleToggleAutoStart() bool {
	row := sv.SelectedRow()
	if row == nil || row.kind != srKB {
		return false
	}
	sv.autoStartTodos = !sv.autoStartTodos
	val := "false"
	if sv.autoStartTodos {
		val = "true"
		// Auto-start implies auto-create — enable it too.
		if !sv.kbTaskSync {
			sv.kbTaskSync = true
			sv.database.SetConfigValue("kb.auto_create_tasks", "true")
		}
	} else {
		// Disabling auto-start also disables auto-create to avoid
		// silently falling back to fsnotify watching on daemon restart.
		if sv.kbTaskSync {
			sv.kbTaskSync = false
			sv.database.SetConfigValue("kb.auto_create_tasks", "false")
		}
	}
	sv.database.SetConfigValue("kb.auto_start_todos", val)
	uxlog.Log("[settings] auto-start todos toggled to %s", val)
	sv.rebuildRows()
	return true
}

func (sv *SettingsView) handleQuickAdd() bool {
	if sv.currentSection() != srProject {
		return false
	}
	if sv.OnQuickAdd != nil {
		sv.OnQuickAdd()
		return true
	}
	return false
}

// HandleMouse handles mouse events (scroll wheel on logs detail).
func (sv *SettingsView) HandleMouse(action tview.MouseAction) bool {
	row := sv.SelectedRow()
	if row == nil || row.kind != srLogs {
		return false
	}
	switch action {
	case tview.MouseScrollUp:
		if sv.logScrollOff > 0 {
			sv.logScrollOff--
		}
		return true
	case tview.MouseScrollDown:
		sv.logScrollOff++
		return true
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
	// Reset log scroll when leaving a log row or switching logs.
	if row := sv.SelectedRow(); row == nil || row.kind != srLogs || row.key != sv.logKey {
		sv.logScrollOff = 0
		sv.logLines = nil
		sv.logKey = ""
	}
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
	case srAPI:
		// Toggle API.
		sv.apiEnabled = !sv.apiEnabled
		val := "false"
		if sv.apiEnabled {
			val = "true"
		}
		sv.database.SetConfigValue("api.enabled", val)
		uxlog.Log("[settings] API toggled to %s", val)
		sv.rebuildRows()
		return true
	case srToDoProject:
		sv.cycleTodoProject(1)
		return true
	case srSpinner:
		sv.cycleSpinner(1)
		return true
	case srVaultPath:
		// Start inline editing for the selected vault path.
		sv.editingVault = row.key
		sv.vaultAC.Close()
		if row.key == vaultKeyMetis {
			sv.editVaultBuf = sv.metisVaultPath
		} else if row.key == vaultKeyArgus {
			sv.editVaultBuf = sv.argusVaultPath
		}
		sv.rebuildRows()
		return true
	case srReviewPrompt:
		sv.editingPrompt = true
		sv.editPromptBuf = sv.reviewPrompt
		sv.rebuildRows()
		return true
	case srDaemon:
		if !sv.daemonRestarting && sv.OnRestartDaemon != nil {
			sv.daemonRestarting = true
			sv.rebuildRows()
			sv.OnRestartDaemon()
		}
		return true
	case srSourcePath:
		sv.editingSource = true
		sv.editSourceBuf = sv.argusSourcePath
		sv.rebuildRows()
		return true
	case srUpdateArgus:
		if !sv.updating && sv.OnUpdateArgus != nil {
			sv.updating = true
			sv.updateStatus = "Running go install ./..."
			sv.updateOutput = ""
			sv.rebuildRows()
			sv.OnUpdateArgus()
		}
		return true
	}
	return false
}

func (sv *SettingsView) handleDeleteOrDefault() bool {
	switch sv.currentSection() {
	case srProject:
		return sv.handleDeleteProject()
	case srBackend:
		return sv.handleSetDefault()
	case srSchedule:
		return sv.handleDeleteSchedule()
	}
	return false
}

func (sv *SettingsView) handleDeleteSchedule() bool {
	s := sv.SelectedSchedule()
	if s == nil || sv.OnDeleteSchedule == nil {
		return false
	}
	sv.OnDeleteSchedule(s.ID)
	return true
}

func (sv *SettingsView) handleDeleteProject() bool {
	pe := sv.SelectedProject()
	if pe == nil {
		return false
	}
	if sv.OnDeleteProject != nil {
		sv.OnDeleteProject(pe.Name)
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

// currentSection returns the section kind for the currently selected row.
func (sv *SettingsView) currentSection() settingsRowKind {
	if sv.cursor < 0 || sv.cursor >= len(sv.rows) {
		return srSection
	}
	return sv.rows[sv.cursor].kind
}

func (sv *SettingsView) handleNew() bool {
	switch sv.currentSection() {
	case srProject:
		if sv.OnNewProject != nil {
			sv.OnNewProject()
			return true
		}
	case srBackend:
		if sv.OnNewBackend != nil {
			sv.OnNewBackend()
			return true
		}
	case srSchedule:
		if sv.OnNewSchedule != nil {
			sv.OnNewSchedule()
			return true
		}
	}
	return false
}

func (sv *SettingsView) handleEdit() bool {
	switch sv.currentSection() {
	case srProject:
		if pe := sv.SelectedProject(); pe != nil && sv.OnEditProject != nil {
			sv.OnEditProject(pe.Name, pe.Project)
			return true
		}
	case srBackend:
		if be := sv.SelectedBackend(); be != nil && sv.OnEditBackend != nil {
			sv.OnEditBackend(be.Name, be.Backend)
			return true
		}
	case srSchedule:
		if s := sv.SelectedSchedule(); s != nil && sv.OnEditSchedule != nil {
			sv.OnEditSchedule(s)
			return true
		}
	}
	return false
}

// SelectedSchedule returns the schedule at the cursor, or nil.
func (sv *SettingsView) SelectedSchedule() *model.ScheduledTask {
	row := sv.SelectedRow()
	if row == nil || row.kind != srSchedule || row.key == "" {
		return nil
	}
	for _, s := range sv.schedules {
		if s.ID == row.key {
			return s
		}
	}
	return nil
}

func (sv *SettingsView) handleToggleSchedule() bool {
	s := sv.SelectedSchedule()
	if s == nil {
		return false
	}
	s.Enabled = !s.Enabled
	if err := sv.database.UpdateSchedule(s); err != nil {
		uxlog.Log("[settings] toggle schedule %s: %v", s.ID, err)
		return true
	}
	uxlog.Log("[settings] schedule %s enabled=%v", s.ID, s.Enabled)
	sv.rebuildRows()
	return true
}

func (sv *SettingsView) handleRunSchedule() bool {
	s := sv.SelectedSchedule()
	if s == nil || sv.OnRunSchedule == nil {
		return false
	}
	sv.OnRunSchedule(s.ID)
	return true
}

// handleEditSourceKey handles keystrokes while inline-editing the Argus source path.
func (sv *SettingsView) handleEditSourceKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEnter:
		sv.argusSourcePath = sv.editSourceBuf
		sv.editingSource = false
		if err := sv.database.SetConfigValue("argus.source_path", sv.argusSourcePath); err != nil {
			uxlog.Log("[settings] failed to persist argus source path: %v", err)
		}
		uxlog.Log("[settings] argus source path set to %q", sv.argusSourcePath)
		sv.rebuildRows()
		return true
	case tcell.KeyEscape:
		sv.editingSource = false
		sv.rebuildRows()
		return true
	case tcell.KeyDown, tcell.KeyUp:
		return true
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(sv.editSourceBuf) > 0 {
			_, size := utf8.DecodeLastRuneInString(sv.editSourceBuf)
			sv.editSourceBuf = sv.editSourceBuf[:len(sv.editSourceBuf)-size]
			sv.rebuildRows()
		}
		return true
	case tcell.KeyRune:
		sv.editSourceBuf += string(ev.Rune())
		sv.rebuildRows()
		return true
	}
	return false
}

// SetUpdateResult records the outcome of a self-update run for display.
func (sv *SettingsView) SetUpdateResult(output, status string) {
	sv.updating = false
	sv.updateOutput = output
	sv.updateStatus = status
	sv.rebuildRows()
}

// handleEditPromptKey handles keystrokes while inline-editing the review prompt.
func (sv *SettingsView) handleEditPromptKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEnter:
		sv.reviewPrompt = sv.editPromptBuf
		sv.editingPrompt = false
		if err := sv.database.SetConfigValue("defaults.review_prompt", sv.reviewPrompt); err != nil {
			uxlog.Log("[settings] failed to persist review prompt: %v", err)
		}
		uxlog.Log("[settings] review prompt set to %q", sv.reviewPrompt)
		sv.rebuildRows()
		return true
	case tcell.KeyEscape:
		sv.editingPrompt = false
		sv.rebuildRows()
		return true
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(sv.editPromptBuf) > 0 {
			_, size := utf8.DecodeLastRuneInString(sv.editPromptBuf)
			sv.editPromptBuf = sv.editPromptBuf[:len(sv.editPromptBuf)-size]
			sv.rebuildRows()
		}
		return true
	case tcell.KeyRune:
		sv.editPromptBuf += string(ev.Rune())
		sv.rebuildRows()
		return true
	}
	return false
}

// handleEditVaultKey handles keystrokes while inline-editing a vault path.
func (sv *SettingsView) handleEditVaultKey(ev *tcell.EventKey) bool {
	// Delegate navigation keys to the autocomplete widget.
	consumed, accepted := sv.vaultAC.HandleKey(ev, sv.editVaultBuf)
	if accepted != "" {
		sv.editVaultBuf = accepted
		sv.rebuildRows()
		return true
	}
	if consumed {
		sv.rebuildRows()
		return true
	}

	switch ev.Key() {
	case tcell.KeyEnter:
		path := sv.editVaultBuf
		key := sv.editingVault
		sv.editingVault = ""
		sv.vaultAC.Close()
		if key == vaultKeyMetis {
			sv.metisVaultPath = path
			if err := sv.database.SetConfigValue("kb.metis_vault_path", path); err != nil {
				uxlog.Log("[settings] failed to persist metis vault path: %v", err)
			}
			uxlog.Log("[settings] metis vault path set to %q", path)
		} else if key == vaultKeyArgus {
			sv.argusVaultPath = path
			if err := sv.database.SetConfigValue("kb.argus_vault_path", path); err != nil {
				uxlog.Log("[settings] failed to persist argus vault path: %v", err)
			}
			uxlog.Log("[settings] argus vault path set to %q", path)
		}
		sv.rebuildRows()
		return true
	case tcell.KeyEscape:
		sv.editingVault = ""
		sv.vaultAC.Close()
		sv.rebuildRows()
		return true
	case tcell.KeyDown, tcell.KeyUp:
		return true // consume to avoid cursor movement while editing
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(sv.editVaultBuf) > 0 {
			_, size := utf8.DecodeLastRuneInString(sv.editVaultBuf)
			sv.editVaultBuf = sv.editVaultBuf[:len(sv.editVaultBuf)-size]
			sv.vaultAC.Update(sv.editVaultBuf)
			sv.rebuildRows()
		}
		return true
	case tcell.KeyRune:
		sv.editVaultBuf += string(ev.Rune())
		sv.vaultAC.Update(sv.editVaultBuf)
		sv.rebuildRows()
		return true
	}
	return false
}

// cycleTodoProject cycles the default todo project forward or backward through
// the sorted project list. An empty string ("none") is included as the first option.
func (sv *SettingsView) cycleTodoProject(dir int) {
	if len(sv.projectNames) == 0 {
		return
	}
	// Prepend empty ("none") option. If todoProject was set to a since-deleted
	// project, the lookup finds no match and idx stays at 0, effectively resetting
	// to "none" on the first cycle — this is intentional.
	options := append([]string{""}, sv.projectNames...)
	idx := 0
	for i, n := range options {
		if n == sv.todoProject {
			idx = i
			break
		}
	}
	idx = (idx + dir + len(options)) % len(options)
	sv.todoProject = options[idx]
	if err := sv.database.SetConfigValue("defaults.todo_project", sv.todoProject); err != nil {
		uxlog.Log("[settings] failed to persist todo project: %v", err)
	}
	uxlog.Log("[settings] default todo project set to %q", sv.todoProject)
	sv.rebuildRows()
}

// cycleSpinner cycles the spinner style forward or backward.
func (sv *SettingsView) cycleSpinner(dir int) {
	var next spinner.Style
	if dir > 0 {
		next = spinner.Next(spinner.Style(sv.spinnerStyle))
	} else {
		next = spinner.Prev(spinner.Style(sv.spinnerStyle))
	}
	sv.spinnerStyle = string(next)
	if err := sv.database.SetConfigValue("ui.spinner", sv.spinnerStyle); err != nil {
		uxlog.Log("[settings] failed to persist spinner style: %v", err)
	}
	widget.SetActiveSpinner(sv.spinnerStyle)
	uxlog.Log("[settings] spinner style set to %q", sv.spinnerStyle)
	sv.rebuildRows()
}

// cycleVaultPath cycles the vault path forward or backward through discovered iCloud vaults.
func (sv *SettingsView) cycleVaultPath(dir int) {
	if len(sv.discoveredVaults) == 0 {
		return
	}
	row := sv.SelectedRow()
	if row == nil || row.kind != srVaultPath {
		return
	}

	currentPath := sv.argusVaultPath
	dbKey := "kb.argus_vault_path"
	if row.key == vaultKeyMetis {
		currentPath = sv.metisVaultPath
		dbKey = "kb.metis_vault_path"
	}

	// Find current index in discovered vaults.
	idx := -1
	for i, v := range sv.discoveredVaults {
		if v == currentPath {
			idx = i
			break
		}
	}

	// Cycle. If current path not in list, start at first (forward) or last (backward).
	n := len(sv.discoveredVaults)
	if idx < 0 {
		if dir > 0 {
			idx = 0
		} else {
			idx = n - 1
		}
	} else {
		idx = (idx + dir + n) % n
	}

	newPath := sv.discoveredVaults[idx]
	if row.key == vaultKeyMetis {
		sv.metisVaultPath = newPath
	} else {
		sv.argusVaultPath = newPath
	}
	if err := sv.database.SetConfigValue(dbKey, newPath); err != nil {
		uxlog.Log("[settings] failed to persist vault path: %v", err)
	}
	uxlog.Log("[settings] %s cycled to %q", dbKey, newPath)
	sv.rebuildRows()
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
	widget.DrawBorder(screen, x, y, w, h, theme.StyleFocusedBorder)

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
	// Clamp so we don't strand scroll past the end (e.g. after window resize).
	if maxOff := max(0, len(sv.rows)-innerH); sv.scrollOff > maxOff {
		sv.scrollOff = maxOff
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
			style = tcell.StyleDefault.Foreground(theme.ColorTitle).Bold(true)
		case srWarning:
			style = tcell.StyleDefault.Foreground(theme.ColorInProgress)
		}
		if row.kind != srSection && rowIdx == sv.cursor {
			style = style.Foreground(theme.ColorSelected).Bold(true)
		}

		label := row.label
		if len(label) > innerW {
			label = label[:innerW]
		}
		widget.DrawText(screen, innerX, innerY+i, innerW, label, style)
	}
}

func (sv *SettingsView) renderDetail(screen tcell.Screen, x, y, w, h int) {
	widget.DrawBorder(screen, x, y, w, h, theme.StyleBorder)

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
	case srVaultPath:
		sv.renderVaultPathDetail(screen, innerX, innerY, innerW, innerH, row)
	case srToDoProject:
		sv.renderToDoProjectDetail(screen, innerX, innerY, innerW, innerH)
	case srSpinner:
		sv.renderSpinnerDetail(screen, innerX, innerY, innerW, innerH)
	case srReviewPrompt:
		sv.renderReviewPromptDetail(screen, innerX, innerY, innerW, innerH)
	case srLogs:
		sv.renderLogsDetail(screen, innerX, innerY, innerW, innerH, row)
	case srDaemon:
		sv.renderDaemonDetail(screen, innerX, innerY, innerW, innerH)
	case srSourcePath:
		sv.renderSourcePathDetail(screen, innerX, innerY, innerW, innerH)
	case srUpdateArgus:
		sv.renderUpdateArgusDetail(screen, innerX, innerY, innerW, innerH)
	case srSchedule:
		sv.renderScheduleDetail(screen, innerX, innerY, innerW, innerH)
	}
}

func (sv *SettingsView) renderScheduleDetail(screen tcell.Screen, x, y, w, h int) {
	s := sv.SelectedSchedule()
	if s == nil {
		widget.DrawText(screen, x, y, w, "Scheduled Tasks", theme.StyleTitle)
		widget.DrawText(screen, x, y+2, w, "Recurring tasks fired by the daemon's", theme.StyleDimmed)
		widget.DrawText(screen, x, y+3, w, "scheduler. Each fire creates a fresh task", theme.StyleDimmed)
		widget.DrawText(screen, x, y+4, w, "and worktree.", theme.StyleDimmed)
		if h > 1 {
			widget.DrawText(screen, x, y+h-1, w, "[n] new schedule", theme.StyleDimmed)
		}
		return
	}

	widget.DrawText(screen, x, y, w, s.Name, theme.StyleTitle)
	r := 1
	statusLabel := "Enabled"
	statusColor := theme.ColorComplete
	if !s.Enabled {
		statusLabel = "Disabled"
		statusColor = theme.ColorError
	}
	widget.DrawText(screen, x, y+r, w, "Status: "+statusLabel, tcell.StyleDefault.Foreground(statusColor))
	r += 2

	widget.DrawText(screen, x, y+r, w, "Project: "+s.Project, theme.StyleDimmed)
	r++
	backend := s.Backend
	if backend == "" {
		backend = "(default)"
	}
	widget.DrawText(screen, x, y+r, w, "Backend: "+backend, theme.StyleDimmed)
	r++
	widget.DrawText(screen, x, y+r, w, "Schedule: "+s.Schedule, theme.StyleDimmed)
	r++

	if !s.NextRunAt.IsZero() {
		widget.DrawText(screen, x, y+r, w, "Next: "+s.NextRunAt.Format("2006-01-02 15:04 MST"), theme.StyleDimmed)
		r++
	}
	if !s.LastRunAt.IsZero() {
		last := "Last: " + s.LastRunAt.Format("2006-01-02 15:04 MST")
		if s.LastTaskID != "" {
			last += "  task " + s.LastTaskID
		}
		widget.DrawText(screen, x, y+r, w, last, theme.StyleDimmed)
		r++
	}
	if s.LastError != "" {
		widget.DrawText(screen, x, y+r, w, "Error: "+s.LastError, tcell.StyleDefault.Foreground(theme.ColorError))
		r++
	}
	r++

	if r < h-1 {
		widget.DrawText(screen, x, y+r, w, "Prompt:", tcell.StyleDefault.Foreground(theme.ColorTitle))
		r++
		for line := range strings.SplitSeq(s.Prompt, "\n") {
			if r >= h-2 {
				widget.DrawText(screen, x, y+r, w, "…", theme.StyleDimmed)
				break
			}
			if len(line) > w {
				line = line[:w]
			}
			widget.DrawText(screen, x, y+r, w, line, theme.StyleDimmed)
			r++
		}
	}

	if h > 1 {
		widget.DrawText(screen, x, y+h-1, w, "[n] new  [e] edit  [t] toggle  [r] run now  [d] delete", theme.StyleDimmed)
	}
}

func (sv *SettingsView) renderSourcePathDetail(screen tcell.Screen, x, y, w, h int) {
	widget.DrawText(screen, x, y, w, "Argus Source Path", theme.StyleTitle)
	r := 2
	path := sv.argusSourcePath
	if sv.editingSource {
		path = sv.editSourceBuf + "▎"
	} else if path == "" {
		path = "(not configured)"
	}
	widget.DrawText(screen, x, y+r, w, "Path: "+path, theme.StyleDimmed)
	r += 2
	if r >= h {
		return
	}
	widget.DrawText(screen, x, y+r, w, "Local clone of github.com/drn/argus used by", theme.StyleDimmed)
	r++
	widget.DrawText(screen, x, y+r, w, "the \"Update Argus\" action to run go install.", theme.StyleDimmed)
	if h > 1 {
		widget.DrawText(screen, x, y+h-1, w, "[enter] edit", theme.StyleDimmed)
	}
}

func (sv *SettingsView) renderUpdateArgusDetail(screen tcell.Screen, x, y, w, h int) {
	widget.DrawText(screen, x, y, w, "Update Argus", theme.StyleTitle)
	r := 2
	if sv.argusSourcePath == "" {
		widget.DrawText(screen, x, y+r, w, "Source path not configured.", tcell.StyleDefault.Foreground(theme.ColorError))
		r++
		widget.DrawText(screen, x, y+r, w, "Set it on the row above first.", theme.StyleDimmed)
		return
	}
	widget.DrawText(screen, x, y+r, w, "Source: "+sv.argusSourcePath, theme.StyleDimmed)
	r += 2
	if sv.updating {
		widget.DrawText(screen, x, y+r, w, "Running go install...", tcell.StyleDefault.Foreground(theme.ColorInProgress))
		r++
	} else if sv.updateStatus != "" {
		color := theme.ColorComplete
		if strings.HasPrefix(sv.updateStatus, "Failed") {
			color = theme.ColorError
		}
		widget.DrawText(screen, x, y+r, w, sv.updateStatus, tcell.StyleDefault.Foreground(color))
		r += 2
	}
	if sv.updateOutput != "" {
		for line := range strings.SplitSeq(strings.TrimRight(sv.updateOutput, "\n"), "\n") {
			if r >= h-1 {
				widget.DrawText(screen, x, y+r, w, "...", theme.StyleDimmed)
				break
			}
			widget.DrawText(screen, x, y+r, w, line, theme.StyleDimmed)
			r++
		}
	} else if !sv.updating && r < h-1 {
		widget.DrawText(screen, x, y+r, w, "Runs git pull --ff-only and go install ./...", theme.StyleDimmed)
		r++
		widget.DrawText(screen, x, y+r, w, "then restarts the daemon.", theme.StyleDimmed)
	}
	if h > 1 {
		widget.DrawText(screen, x, y+h-1, w, "[enter] update & restart", theme.StyleDimmed)
	}
}

func (sv *SettingsView) renderWarningDetail(screen tcell.Screen, x, y, w, h int, row *settingsRow) {
	if row.key == "_ok" {
		widget.DrawText(screen, x, y, w, "System Status", theme.StyleTitle)
		widget.DrawText(screen, x, y+2, w, "Daemon is running", tcell.StyleDefault.Foreground(theme.ColorComplete))
	} else {
		widget.DrawText(screen, x, y, w, "Warning", theme.StyleTitle)
		widget.DrawText(screen, x, y+2, w, row.label, tcell.StyleDefault.Foreground(theme.ColorInProgress))
	}
}

func (sv *SettingsView) renderSandboxDetail(screen tcell.Screen, x, y, w, h int) {
	widget.DrawText(screen, x, y, w, "Sandbox Configuration", theme.StyleTitle)
	row := 2

	status := "Disabled"
	statusColor := theme.ColorError
	if sv.sandboxEnabled {
		status = "Enabled"
		statusColor = theme.ColorComplete
	}
	widget.DrawText(screen, x, y+row, w, "Status: "+status, tcell.StyleDefault.Foreground(statusColor))
	row++

	avail := "Not available"
	if sv.sandboxAvailable {
		avail = "Available (sandbox-exec)"
	}
	widget.DrawText(screen, x, y+row, w, "Runtime: "+avail, theme.StyleDimmed)
	row += 2

	if len(sv.sandboxDenyRead) > 0 {
		widget.DrawText(screen, x, y+row, w, "Deny Read:", tcell.StyleDefault.Foreground(theme.ColorTitle))
		row++
		for _, p := range sv.sandboxDenyRead {
			if row >= h {
				break
			}
			widget.DrawText(screen, x, y+row, w, "  "+p, theme.StyleDimmed)
			row++
		}
		row++
	}

	if len(sv.sandboxExtraWrite) > 0 {
		widget.DrawText(screen, x, y+row, w, "Extra Write:", tcell.StyleDefault.Foreground(theme.ColorTitle))
		row++
		for _, p := range sv.sandboxExtraWrite {
			if row >= h {
				break
			}
			widget.DrawText(screen, x, y+row, w, "  "+p, theme.StyleDimmed)
			row++
		}
	}

	if row+2 < h {
		widget.DrawText(screen, x, y+h-1, w, "[enter] toggle", theme.StyleDimmed)
	}
}

func (sv *SettingsView) renderProjectDetail(screen tcell.Screen, x, y, w, h int, row *settingsRow) {
	pe := sv.SelectedProject()
	if pe == nil {
		widget.DrawText(screen, x, y, w, "(no project selected)", theme.StyleDimmed)
		return
	}

	widget.DrawText(screen, x, y, w, pe.Name, theme.StyleTitle)
	r := 2

	widget.DrawText(screen, x, y+r, w, "Config", tcell.StyleDefault.Foreground(theme.ColorTitle))
	r++
	widget.DrawText(screen, x, y+r, w, "  Path: "+pe.Project.Path, theme.StyleDimmed)
	r++
	widget.DrawText(screen, x, y+r, w, "  Branch: "+pe.Project.Branch, theme.StyleDimmed)
	r++
	backend := pe.Project.Backend
	if backend == "" {
		backend = "(default)"
	}
	widget.DrawText(screen, x, y+r, w, "  Backend: "+backend, theme.StyleDimmed)
	r += 2

	// Sandbox override.
	if r >= h {
		return
	}
	widget.DrawText(screen, x, y+r, w, "Sandbox", tcell.StyleDefault.Foreground(theme.ColorTitle))
	r++
	sandboxLabel := "Inherit (global)"
	sandboxColor := tcell.ColorDefault
	if pe.Project.Sandbox.Enabled != nil {
		if *pe.Project.Sandbox.Enabled {
			sandboxLabel = "Enabled (override)"
			sandboxColor = theme.ColorComplete
		} else {
			sandboxLabel = "Disabled (override)"
			sandboxColor = theme.ColorError
		}
	}
	if r >= h {
		return
	}
	widget.DrawText(screen, x, y+r, w, "  Mode: "+sandboxLabel, tcell.StyleDefault.Foreground(sandboxColor))
	r++
	if len(pe.Project.Sandbox.DenyRead) > 0 && r < h {
		widget.DrawText(screen, x, y+r, w, "  Deny Read:", theme.StyleDimmed)
		r++
		for _, p := range pe.Project.Sandbox.DenyRead {
			if r >= h {
				break
			}
			widget.DrawText(screen, x, y+r, w, "    "+p, theme.StyleDimmed)
			r++
		}
	}
	if len(pe.Project.Sandbox.ExtraWrite) > 0 && r < h {
		widget.DrawText(screen, x, y+r, w, "  Extra Write:", theme.StyleDimmed)
		r++
		for _, p := range pe.Project.Sandbox.ExtraWrite {
			if r >= h {
				break
			}
			widget.DrawText(screen, x, y+r, w, "    "+p, theme.StyleDimmed)
			r++
		}
	}
	if len(pe.Project.Sandbox.DenyRead) > 0 || len(pe.Project.Sandbox.ExtraWrite) > 0 {
		r++
	}

	// Task counts.
	counts, ok := sv.taskCounts[pe.Name]
	if ok && r+2 < h {
		widget.DrawText(screen, x, y+r, w, "Tasks", tcell.StyleDefault.Foreground(theme.ColorTitle))
		r++
		total := counts.pending + counts.inProgress + counts.inReview + counts.complete
		widget.DrawText(screen, x, y+r, w, fmt.Sprintf("  %d pending  %d active  %d review  %d done",
			counts.pending, counts.inProgress, counts.inReview, counts.complete), theme.StyleDimmed)
		r++
		if total > 0 && w > 4 {
			pct := counts.complete * 100 / total
			widget.DrawText(screen, x, y+r, w, fmt.Sprintf("  %d%% complete", pct), theme.StyleDimmed)
		}
	}

	if h > 2 {
		widget.DrawText(screen, x, y+h-1, w, "[n] new  [e] edit  [d] delete  [i] quick add", theme.StyleDimmed)
	}
}

func (sv *SettingsView) renderBackendDetail(screen tcell.Screen, x, y, w, h int, row *settingsRow) {
	be := sv.SelectedBackend()
	if be == nil {
		widget.DrawText(screen, x, y, w, "(no backend selected)", theme.StyleDimmed)
		return
	}

	widget.DrawText(screen, x, y, w, be.Name, theme.StyleTitle)
	r := 1
	if be.Name == sv.defaultBackend {
		widget.DrawText(screen, x, y+r, w, "★ Default backend", tcell.StyleDefault.Foreground(theme.ColorComplete))
		r++
	}
	r++

	widget.DrawText(screen, x, y+r, w, "Config", tcell.StyleDefault.Foreground(theme.ColorTitle))
	r++
	cmd := be.Backend.Command
	if len(cmd) > w-12 {
		cmd = cmd[:w-12] + "…"
	}
	widget.DrawText(screen, x, y+r, w, "  Command: "+cmd, theme.StyleDimmed)
	r++
	widget.DrawText(screen, x, y+r, w, "  Prompt Flag: "+be.Backend.PromptFlag, theme.StyleDimmed)
	r += 2

	hints := "[d] set as default  [n] new  [e] edit"
	if be.Name == sv.defaultBackend {
		hints = "(already default)  [n] new  [e] edit"
	}
	if r < h {
		widget.DrawText(screen, x, y+r, w, hints, theme.StyleDimmed)
	}
}

func (sv *SettingsView) renderKBDetail(screen tcell.Screen, x, y, w, h int) {
	widget.DrawText(screen, x, y, w, "Knowledge Base", theme.StyleTitle)
	r := 2

	status := "Disabled"
	statusColor := theme.ColorError
	if sv.kbEnabled {
		status = "Enabled"
		statusColor = theme.ColorComplete
	}
	widget.DrawText(screen, x, y+r, w, "Status: "+status, tcell.StyleDefault.Foreground(statusColor))
	r += 2

	widget.DrawText(screen, x, y+r, w, "Metis Vault:", tcell.StyleDefault.Foreground(theme.ColorTitle))
	r++
	vault := sv.metisVaultPath
	if vault == "" {
		vault = "(not configured)"
	}
	widget.DrawText(screen, x, y+r, w, "  "+vault, theme.StyleDimmed)
	r += 2

	widget.DrawText(screen, x, y+r, w, "Argus Vault:", tcell.StyleDefault.Foreground(theme.ColorTitle))
	r++
	vault = sv.argusVaultPath
	if vault == "" {
		vault = "(not configured)"
	}
	widget.DrawText(screen, x, y+r, w, "  "+vault, theme.StyleDimmed)
	r += 2

	syncLabel := "Off"
	if sv.kbTaskSync {
		syncLabel = "On"
	}
	widget.DrawText(screen, x, y+r, w, "Task Sync: "+syncLabel, theme.StyleDimmed)
	r += 2

	autoStartLabel := "Off"
	autoStartColor := theme.StyleDimmed
	if sv.autoStartTodos {
		autoStartLabel = fmt.Sprintf("On (every %ds)", sv.autoStartInterval)
		autoStartColor = tcell.StyleDefault.Foreground(theme.ColorComplete)
	}
	widget.DrawText(screen, x, y+r, w, "Auto-Start ToDos: "+autoStartLabel, autoStartColor)
	r++
	widget.DrawText(screen, x, y+r, w, "  Polls vault and starts new todos automatically", theme.StyleDimmed)
	r += 2

	if r < h {
		widget.DrawText(screen, x, y+r, w, "[enter] toggle KB  [a] toggle auto-start", theme.StyleDimmed)
	}
}

func (sv *SettingsView) renderVaultPathDetail(screen tcell.Screen, x, y, w, h int, row *settingsRow) {
	isMetis := row.key == vaultKeyMetis
	title := "Argus Vault"
	path := sv.argusVaultPath
	desc := "Obsidian vault for task syncing."
	if isMetis {
		title = "Metis Vault"
		path = sv.metisVaultPath
		desc = "Obsidian vault for KB indexing."
	}

	widget.DrawText(screen, x, y, w, title, theme.StyleTitle)
	r := 2

	display := path
	editing := sv.editingVault == row.key
	if editing {
		display = sv.editVaultBuf + "▎"
	} else if display == "" {
		display = "(not configured)"
	}
	widget.DrawText(screen, x, y+r, w, display, tcell.StyleDefault.Foreground(theme.ColorComplete))
	r++

	// Autocomplete dropdown (only when editing this vault).
	if editing {
		r += sv.vaultAC.Draw(screen, x, y+r, w, svMaxACVisible)
	}
	r++

	widget.DrawText(screen, x, y+r, w, desc, theme.StyleDimmed)
	r += 2

	// List discovered vaults (like spinner detail lists available styles).
	if !editing && len(sv.discoveredVaults) > 0 {
		widget.DrawText(screen, x, y+r, w, "Discovered iCloud vaults:", tcell.StyleDefault.Foreground(theme.ColorTitle))
		r++
		home, _ := os.UserHomeDir()
		for _, v := range sv.discoveredVaults {
			if r >= h-1 {
				break
			}
			label := "  " + strings.TrimPrefix(v, home)
			style := theme.StyleDimmed
			if v == path {
				style = tcell.StyleDefault.Foreground(theme.ColorSelected).Bold(true)
			}
			widget.DrawText(screen, x, y+r, w, label, style)
			r++
		}
		r++
	}

	if r < h {
		if editing {
			widget.DrawText(screen, x, y+r, w, "[enter] save  [tab] complete  [esc] cancel", theme.StyleDimmed)
		} else if len(sv.discoveredVaults) > 0 {
			widget.DrawText(screen, x, y+r, w, "[enter] edit path  [◀/▶] cycle vaults", theme.StyleDimmed)
		} else {
			widget.DrawText(screen, x, y+r, w, "[enter] edit path", theme.StyleDimmed)
		}
	}
}

func (sv *SettingsView) renderSpinnerDetail(screen tcell.Screen, x, y, w, h int) {
	widget.DrawText(screen, x, y, w, "Spinner Style", theme.StyleTitle)
	r := 2

	active := spinner.Get(spinner.Style(sv.spinnerStyle))
	widget.DrawText(screen, x, y+r, w, active.Label, tcell.StyleDefault.Foreground(theme.ColorComplete))
	r++

	// Show a preview of the spinner frames.
	preview := "  Frames: "
	for _, f := range active.Frames {
		preview += string(f) + " "
	}
	widget.DrawText(screen, x, y+r, w, preview, theme.StyleDimmed)
	r += 2

	// List all available styles.
	widget.DrawText(screen, x, y+r, w, "Available styles:", tcell.StyleDefault.Foreground(theme.ColorTitle))
	r++
	for _, s := range spinner.All {
		if r >= h {
			break
		}
		label := "  " + s.Label
		style := theme.StyleDimmed
		if s.Style == active.Style {
			style = tcell.StyleDefault.Foreground(theme.ColorSelected).Bold(true)
		}
		widget.DrawText(screen, x, y+r, w, label, style)
		r++
	}

	if r+1 < h {
		widget.DrawText(screen, x, y+h-1, w, "[enter/◀/▶] cycle styles", theme.StyleDimmed)
	}
}

func (sv *SettingsView) renderToDoProjectDetail(screen tcell.Screen, x, y, w, h int) {
	widget.DrawText(screen, x, y, w, "Default ToDo Project", theme.StyleTitle)
	r := 2

	proj := sv.todoProject
	if proj == "" {
		widget.DrawText(screen, x, y+r, w, "(none)", theme.StyleDimmed)
	} else {
		widget.DrawText(screen, x, y+r, w, proj, tcell.StyleDefault.Foreground(theme.ColorComplete))
	}
	r += 2

	widget.DrawText(screen, x, y+r, w, "The project pre-selected when launching", theme.StyleDimmed)
	r++
	widget.DrawText(screen, x, y+r, w, "a to-do note as a new task.", theme.StyleDimmed)
	r += 2

	if r < h {
		widget.DrawText(screen, x, y+r, w, "[enter/◀/▶] cycle projects", theme.StyleDimmed)
	}
}

func (sv *SettingsView) renderReviewPromptDetail(screen tcell.Screen, x, y, w, h int) {
	widget.DrawText(screen, x, y, w, "Review Prompt", theme.StyleTitle)
	r := 2

	prompt := sv.reviewPrompt
	if sv.editingPrompt {
		prompt = sv.editPromptBuf + "▎"
	}
	widget.DrawText(screen, x, y+r, w, prompt, tcell.StyleDefault.Foreground(theme.ColorComplete))
	r += 2

	widget.DrawText(screen, x, y+r, w, "The prompt sent to the agent when starting", theme.StyleDimmed)
	r++
	widget.DrawText(screen, x, y+r, w, "a PR review task (Ctrl+R in Reviews tab).", theme.StyleDimmed)
	r++
	widget.DrawText(screen, x, y+r, w, "The PR URL is appended automatically.", theme.StyleDimmed)
	r += 2

	if r < h {
		if sv.editingPrompt {
			widget.DrawText(screen, x, y+r, w, "[enter] save  [esc] cancel", theme.StyleDimmed)
		} else {
			widget.DrawText(screen, x, y+r, w, "[enter] edit", theme.StyleDimmed)
		}
	}
}

func (sv *SettingsView) renderDaemonDetail(screen tcell.Screen, x, y, w, h int) {
	widget.DrawText(screen, x, y, w, "Daemon", theme.StyleTitle)
	r := 2

	if sv.daemonRestarting {
		widget.DrawText(screen, x, y+r, w, "Restarting daemon...", tcell.StyleDefault.Foreground(theme.ColorInProgress))
	} else {
		widget.DrawText(screen, x, y+r, w, "Daemon is running", tcell.StyleDefault.Foreground(theme.ColorComplete))
		r += 2
		widget.DrawText(screen, x, y+r, w, "[enter] restart daemon", theme.StyleDimmed)
	}
}

// SetDaemonRestarting updates the restarting state from the app.
func (sv *SettingsView) SetDaemonRestarting(restarting bool) {
	sv.daemonRestarting = restarting
	if !restarting {
		// Daemon just came back — re-capture boot state on next Refresh.
		sv.apiBootRecorded = false
		sv.vaultBootRecorded = false
	}
	sv.rebuildRows()
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

	widget.DrawText(screen, x, y, w, title, theme.StyleTitle)
	widget.DrawText(screen, x, y+2, w, logPath, theme.StyleDimmed)

	// Load/cache log lines.
	if sv.logKey != row.key {
		sv.logLines = readLogLines(logPath)
		sv.logKey = row.key
		// Auto-scroll to bottom on first load.
		visibleRows := h - 4
		if len(sv.logLines) > visibleRows {
			sv.logScrollOff = len(sv.logLines) - visibleRows
		} else {
			sv.logScrollOff = 0
		}
	}

	// Clamp scroll offset.
	visibleRows := h - 4
	if visibleRows <= 0 {
		return
	}
	maxScroll := len(sv.logLines) - visibleRows
	if maxScroll < 0 {
		maxScroll = 0
	}
	if sv.logScrollOff > maxScroll {
		sv.logScrollOff = maxScroll
	}

	// Render visible lines.
	for i := range visibleRows {
		lineIdx := sv.logScrollOff + i
		if lineIdx >= len(sv.logLines) {
			break
		}
		line := sv.logLines[lineIdx]
		if len(line) > w {
			line = line[:w]
		}
		widget.DrawText(screen, x, y+4+i, w, line, tcell.StyleDefault)
	}
}

// readLogLines reads all lines from a log file.
func readLogLines(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{"(file not found)"}
	}
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return []string{"(empty)"}
	}
	return strings.Split(text, "\n")
}

// --- Helpers ---

func drawMultiLine(screen tcell.Screen, x, y, w int, text string, style tcell.Style) int {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if len(line) > w {
			line = line[:w]
		}
		widget.DrawText(screen, x, y+i, w, line, style)
	}
	return len(lines)
}
