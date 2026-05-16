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
	"github.com/drn/argus/internal/launchagent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/spinner"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
)

// settingsRowKind identifies what kind of row this is in the settings list.
type settingsRowKind int

// srNone is the sentinel returned by currentRowKind() when the active
// category has no rows. It is intentionally negative so no positive iota
// constant below can ever equal it — switch statements that don't match
// fall through cleanly.
const srNone settingsRowKind = -1

const (
	srWarning settingsRowKind = iota
	srProject
	srBackend
	srSandbox
	srLogs
	srKB
	srAPI
	srDaemon
	srSpinner
	srVaultPath
	srUpdateArgus
	srSourcePath
	srSchedule
	srAutoStart
)

// settingsCategory groups related settings rows into a left-rail entry.
type settingsCategory int

const (
	catSystem settingsCategory = iota
	catSandbox
	catProjects
	catBackends
	catSchedules
	catKnowledgeBase
	catRemoteAPI
	catAppearance
	catLogs
)

// Label returns the human-readable name shown in the left rail.
func (c settingsCategory) Label() string {
	switch c {
	case catSystem:
		return "System"
	case catSandbox:
		return "Sandbox"
	case catProjects:
		return "Projects"
	case catBackends:
		return "Backends"
	case catSchedules:
		return "Schedules"
	case catKnowledgeBase:
		return "Knowledge Base"
	case catRemoteAPI:
		return "Remote API"
	case catAppearance:
		return "Appearance"
	case catLogs:
		return "Logs"
	}
	return "?"
}

// allCategories is the fixed left-rail order.
var allCategories = []settingsCategory{
	catSystem, catSandbox, catProjects, catBackends, catSchedules,
	catKnowledgeBase, catRemoteAPI, catAppearance, catLogs,
}

// settingsFocus is which sub-panel currently owns input within the settings view.
type settingsFocus int

const (
	focusRail settingsFocus = iota
	focusPane
)

// Vault key constants used in settingsRow.key for vault path rows.
const (
	vaultKeyMetis = "_metis_vault"
)

// svMaxACVisible is the maximum number of vault path autocomplete rows shown.
const svMaxACVisible = 8

// Layout constants for the right pane in renderPane.
//
//   - svMaxItemsVisible caps the items-list height so the detail panel
//     below it has room to breathe. Tuned to fit the longest category
//     (Projects) without dominating the pane.
//   - svDetailReserve is the row budget reserved below the items list:
//     1 separator + 6 detail rows minimum.
const (
	svMaxItemsVisible = 8
	svDetailReserve   = 7
)

// settingsRow is a single row in the settings section list.
type settingsRow struct {
	kind  settingsRowKind
	label string
	key   string // project/backend name for lookup
}

// SettingsView is the tcell settings tab with two panels: a left rail of
// category names and a right pane that renders the active category's rows
// and the detail of the selected row.
type SettingsView struct {
	*tview.Box

	category settingsCategory // active category in the left rail
	focus    settingsFocus    // which sub-panel owns input

	rows      []settingsRow // rows for the active category only (no section headers)
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
	sandboxEnabled          bool
	sandboxAvailable        bool
	sandboxDenyRead         []string
	sandboxExtraWrite       []string
	sandboxAllowAppleEvents []string

	// KB.
	kbEnabled         bool
	metisVaultPath    string
	metisVaultAtBoot  string // value when daemon started; used to show "restart required"
	vaultBootRecorded bool   // true after first Refresh captures boot value

	// API.
	apiEnabled       bool
	apiEnabledAtBoot bool // value when daemon started; used to show "restart required"
	apiBootRecorded  bool // true after first Refresh captures boot value
	apiPort          int

	// Spinner.
	spinnerStyle string // current spinner style name

	// Project name list (used by other UI features).
	projectNames []string

	// Vault path editing.
	editingVault     string   // vaultKeyMetis when editing, "" otherwise
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

	// Auto-start (LaunchAgent on macOS).
	autoStartStatus  launchagent.Status
	autoStartBusy    bool   // true while install/uninstall is in flight
	autoStartMessage string // last result, shown in detail panel

	// Self-update.
	argusSourcePath string
	editingSource   bool   // true while inline-editing the source path
	editSourceBuf   string // buffer for the source-path edit
	updating        bool   // true while go install is running
	updateStatus    string // last-result status line shown in detail panel
	updateOutput    string // last go-install output (for detail panel)

	// Callbacks.
	OnRestartDaemon          func()
	OnUpdateArgus            func()                        // triggered by the "Update Argus" row
	OnToggleAutoStart        func(currentlyInstalled bool) // dispatched off the UI thread by app.go
	OnNewProject             func()
	OnEditProject            func(name string, p config.Project)
	OnEditProjectAppleEvents func(name string, p config.Project)
	OnDeleteProject          func(name string)
	OnQuickAdd               func()
	OnNewSchedule            func()
	OnEditSchedule           func(s *model.ScheduledTask)
	OnDeleteSchedule         func(id string)
	OnRunSchedule            func(id string)

	// OnBranchChange fires whenever the active category changes or focus
	// moves between the left rail and the right pane — i.e. whenever the
	// set of cells the Draw will write differs from the previous frame.
	// The app wires this to forceRedraw so tcell's per-cell diff doesn't
	// leave ghost cells from the previous category's content. See
	// gotchas/ui-threading.md.
	OnBranchChange func()

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

// NewSettingsView creates a new settings panel. Defaults to focusing the
// right pane and the System category so the first interactive row is
// immediately usable — pressing Left moves to the category rail.
func NewSettingsView(database *db.DB) *SettingsView {
	return &SettingsView{
		Box:        tview.NewBox(),
		taskCounts: make(map[string]statusCounts),
		database:   database,
		category:   catSystem,
		focus:      focusPane,
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
	sv.sandboxAllowAppleEvents = cfg.Sandbox.AllowAppleEvents

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
	if !sv.vaultBootRecorded {
		sv.metisVaultAtBoot = cfg.KB.MetisVaultPath
		sv.vaultBootRecorded = true
	}
	// Discover vaults once — filesystem scan is blocking I/O, avoid on every Refresh.
	if sv.discoveredVaults == nil {
		sv.discoveredVaults = config.DiscoverICloudVaults()
		uxlog.Log("[settings] discovered %d iCloud vaults", len(sv.discoveredVaults))
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

	sv.projectNames = projNames

	// Argus self-update source path.
	sv.argusSourcePath = cfg.Argus.SourcePath

	// Auto-start LaunchAgent status. CurrentStatus shells out to launchctl
	// (~5ms) so refreshing on every Refresh is fine — Refresh runs at most
	// once per second and on user-triggered refreshes.
	sv.autoStartStatus = launchagent.CurrentStatus()

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

// rebuildRows rebuilds sv.rows for the active category only. The left rail
// is fixed and not part of sv.rows.
func (sv *SettingsView) rebuildRows() {
	sv.rows = nil

	switch sv.category {
	case catSystem:
		if len(sv.warnings) == 0 {
			sv.rows = append(sv.rows, settingsRow{kind: srWarning, label: "System status", key: "_ok"})
		} else {
			for i, w := range sv.warnings {
				sv.rows = append(sv.rows, settingsRow{kind: srWarning, label: "⚠ " + w, key: fmt.Sprintf("_warn_%d", i)})
			}
		}
		if sv.daemonConnected {
			label := "Restart Daemon"
			if sv.daemonRestarting {
				label = "Restarting..."
			}
			sv.rows = append(sv.rows, settingsRow{kind: srDaemon, label: label, key: "_daemon_restart"})

			sourceLabel := "Source path: " + sv.argusSourcePath
			if sv.editingSource {
				sourceLabel = "Source path: " + sv.editSourceBuf + "▎"
			} else if sv.argusSourcePath == "" {
				sourceLabel = "Source path: (not configured)"
			}
			sv.rows = append(sv.rows, settingsRow{kind: srSourcePath, label: sourceLabel, key: "_argus_source"})

			updateLabel := "Update Argus (go install + restart)"
			if sv.updating {
				updateLabel = "Updating..."
			}
			sv.rows = append(sv.rows, settingsRow{kind: srUpdateArgus, label: updateLabel, key: "_argus_update"})
		}
		if launchagent.Available() {
			autoLabel := "Auto-start at login: disabled"
			if sv.autoStartStatus.Installed {
				if sv.autoStartStatus.Loaded {
					autoLabel = "Auto-start at login: enabled"
				} else {
					autoLabel = "Auto-start at login: installed (not loaded)"
				}
			}
			if sv.autoStartBusy {
				autoLabel = "Auto-start at login: working..."
			}
			sv.rows = append(sv.rows, settingsRow{kind: srAutoStart, label: autoLabel, key: "_autostart"})
		}

	case catSandbox:
		label := "Disabled"
		if sv.sandboxEnabled {
			label = "Enabled"
		}
		sv.rows = append(sv.rows, settingsRow{kind: srSandbox, label: label, key: "_sandbox"})

	case catProjects:
		if len(sv.projects) == 0 {
			sv.rows = append(sv.rows, settingsRow{kind: srProject, label: "(no projects — press n to add)"})
		} else {
			for _, p := range sv.projects {
				sv.rows = append(sv.rows, settingsRow{kind: srProject, label: p.Name, key: p.Name})
			}
		}

	case catBackends:
		if len(sv.backends) == 0 {
			sv.rows = append(sv.rows, settingsRow{kind: srBackend, label: "(no backends — press n to add)"})
		} else {
			for _, b := range sv.backends {
				label := b.Name
				if b.Name == sv.defaultBackend {
					label = "★ " + b.Name
				}
				sv.rows = append(sv.rows, settingsRow{kind: srBackend, label: label, key: b.Name})
			}
		}

	case catSchedules:
		if len(sv.schedules) == 0 {
			sv.rows = append(sv.rows, settingsRow{kind: srSchedule, label: "(no schedules — press n to add)"})
		} else {
			for _, s := range sv.schedules {
				marker := ""
				if !s.Enabled {
					marker = "⊘ "
				}
				sv.rows = append(sv.rows, settingsRow{kind: srSchedule, label: marker + s.Name, key: s.ID})
			}
		}

	case catKnowledgeBase:
		kbLabel := "KB: Disabled"
		if sv.kbEnabled {
			kbLabel = "KB: Enabled"
		}
		sv.rows = append(sv.rows, settingsRow{kind: srKB, label: kbLabel, key: "_kb"})

		metisLabel := "Metis: " + sv.metisVaultPath
		if sv.editingVault == vaultKeyMetis {
			metisLabel = "Metis: " + sv.editVaultBuf + "▎"
		} else if sv.metisVaultPath == "" {
			metisLabel = "Metis: (not configured)"
		}
		if sv.vaultBootRecorded && sv.metisVaultPath != sv.metisVaultAtBoot {
			metisLabel += " (restart required)"
		}
		sv.rows = append(sv.rows, settingsRow{kind: srVaultPath, label: metisLabel, key: vaultKeyMetis})

	case catRemoteAPI:
		apiLabel := "Disabled"
		if sv.apiEnabled {
			apiLabel = fmt.Sprintf("Enabled (port %d)", sv.apiPort)
		}
		if sv.apiBootRecorded && sv.apiEnabled != sv.apiEnabledAtBoot {
			apiLabel += " (restart required)"
		}
		sv.rows = append(sv.rows, settingsRow{kind: srAPI, label: apiLabel, key: "_api"})

	case catAppearance:
		spinLabel := fmt.Sprintf("Spinner: %s", spinner.Get(spinner.Style(sv.spinnerStyle)).Label)
		sv.rows = append(sv.rows, settingsRow{kind: srSpinner, label: spinLabel, key: "_spinner"})

	case catLogs:
		sv.rows = append(sv.rows, settingsRow{kind: srLogs, label: "UX Log", key: "ux"})
		sv.rows = append(sv.rows, settingsRow{kind: srLogs, label: "Daemon Log", key: "daemon"})
	}

	// Clamp cursor.
	if sv.cursor < 0 {
		sv.cursor = 0
	}
	if sv.cursor >= len(sv.rows) {
		sv.cursor = max(0, len(sv.rows)-1)
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
		if sv.editingVault != "" {
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
	return sv.editingVault != "" || sv.editingSource
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
	if sv.editingVault != "" {
		return sv.handleEditVaultKey(ev)
	}
	if sv.editingSource {
		return sv.handleEditSourceKey(ev)
	}
	switch ev.Key() {
	case tcell.KeyUp:
		if sv.focus == focusRail {
			return sv.moveCategory(-1)
		}
		sv.moveCursor(-1)
		return true
	case tcell.KeyDown:
		if sv.focus == focusRail {
			return sv.moveCategory(1)
		}
		sv.moveCursor(1)
		return true
	case tcell.KeyLeft:
		if sv.focus == focusRail {
			return false
		}
		switch sv.currentRowKind() {
		case srSpinner:
			sv.cycleSpinner(-1)
			return true
		case srVaultPath:
			sv.cycleVaultPath(-1)
			return true
		}
		sv.setFocus(focusRail)
		return true
	case tcell.KeyRight:
		if sv.focus == focusRail {
			sv.setFocus(focusPane)
			return true
		}
		switch sv.currentRowKind() {
		case srSpinner:
			sv.cycleSpinner(1)
			return true
		case srVaultPath:
			sv.cycleVaultPath(1)
			return true
		}
		return false
	case tcell.KeyEnter:
		if sv.focus == focusRail {
			sv.setFocus(focusPane)
			return true
		}
		return sv.handleEnter()
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'k':
			if sv.focus == focusRail {
				return sv.moveCategory(-1)
			}
			sv.moveCursor(-1)
			return true
		case 'j':
			if sv.focus == focusRail {
				return sv.moveCategory(1)
			}
			sv.moveCursor(1)
			return true
		case 'h':
			if sv.focus == focusPane {
				sv.setFocus(focusRail)
				return true
			}
			return false
		case 'l':
			if sv.focus == focusRail {
				sv.setFocus(focusPane)
				return true
			}
			return false
		case 'd':
			if sv.focus == focusRail {
				return false
			}
			return sv.handleDeleteOrDefault()
		case 'n':
			if sv.focus == focusRail {
				return false
			}
			return sv.handleNew()
		case 'e':
			if sv.focus == focusRail {
				return false
			}
			return sv.handleEdit()
		case 'i':
			if sv.focus == focusRail {
				return false
			}
			return sv.handleQuickAdd()
		case 'a':
			if sv.focus == focusRail {
				return false
			}
			return sv.handleAppleEvents()
		case 't':
			if sv.focus == focusRail {
				return false
			}
			return sv.handleToggleSchedule()
		case 'r':
			if sv.focus == focusRail {
				return false
			}
			return sv.handleRunSchedule()
		}
	}
	return false
}

func (sv *SettingsView) handleQuickAdd() bool {
	if sv.category != catProjects {
		return false
	}
	if sv.OnQuickAdd != nil {
		sv.OnQuickAdd()
		return true
	}
	return false
}

// handleAppleEvents fires the OnEditProjectAppleEvents callback for the
// currently-selected project row, which opens the AppleEvents allowlist
// picker modal. Returns false (key not consumed) when no project row is
// selected so the keypress falls through to the global handler.
func (sv *SettingsView) handleAppleEvents() bool {
	if sv.category != catProjects {
		return false
	}
	pe := sv.SelectedProject()
	if pe == nil || sv.OnEditProjectAppleEvents == nil {
		return false
	}
	sv.OnEditProjectAppleEvents(pe.Name, pe.Project)
	return true
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

// HandleClick routes a left-click to the rail (when the click falls inside
// the rail rect) or to the pane. A click on a rail item switches both the
// active category and focus; a click anywhere in the pane switches focus
// to the pane. The actual click-to-select-row behavior in the pane is
// intentionally not wired — keyboard already covers it and adding row hit
// testing here would couple this method to the Draw layout math.
func (sv *SettingsView) HandleClick(mx, my int) {
	x, y, width, height := sv.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}
	if mx < x || mx >= x+width || my < y || my >= y+height {
		return
	}

	railW := computeRailW(width)

	if mx < x+railW {
		// Click landed in the rail — pick the category at that row.
		ix, iy := x+1, y+1
		ih := height - 2
		row := my - iy
		if row < 0 || row >= ih || mx < ix {
			return
		}
		if row < len(allCategories) {
			sv.setCategory(allCategories[row])
		}
		sv.setFocus(focusRail)
		return
	}

	// Pane click — move focus into the pane.
	sv.setFocus(focusPane)
}

func (sv *SettingsView) moveCursor(dir int) {
	if len(sv.rows) == 0 {
		sv.cursor = 0
		return
	}
	sv.cursor += dir
	if sv.cursor < 0 {
		sv.cursor = 0
	}
	if sv.cursor >= len(sv.rows) {
		sv.cursor = len(sv.rows) - 1
	}
	// Reset log scroll when leaving a log row or switching logs.
	if row := sv.SelectedRow(); row == nil || row.kind != srLogs || row.key != sv.logKey {
		sv.logScrollOff = 0
		sv.logLines = nil
		sv.logKey = ""
	}
}

// moveCategory shifts the active category. Returns true if the category
// actually changed (so the caller can fire OnBranchChange).
func (sv *SettingsView) moveCategory(dir int) bool {
	idx := -1
	for i, c := range allCategories {
		if c == sv.category {
			idx = i
			break
		}
	}
	if idx < 0 {
		idx = 0
	}
	next := idx + dir
	if next < 0 || next >= len(allCategories) {
		return false
	}
	sv.setCategory(allCategories[next])
	return true
}

// setCategory switches the active category, resets cursor, and rebuilds rows.
// Fires OnBranchChange when the category actually changes.
func (sv *SettingsView) setCategory(c settingsCategory) {
	if sv.category == c {
		return
	}
	sv.category = c
	sv.cursor = 0
	sv.scrollOff = 0
	sv.logScrollOff = 0
	sv.logLines = nil
	sv.logKey = ""
	sv.rebuildRows()
	sv.notifyBranchChange()
}

// setFocus moves input focus between the rail and the pane. Fires
// OnBranchChange only on an actual change so border styling and pending
// redraws are minimized.
func (sv *SettingsView) setFocus(f settingsFocus) {
	if sv.focus == f {
		return
	}
	sv.focus = f
	sv.notifyBranchChange()
}

func (sv *SettingsView) notifyBranchChange() {
	if sv.OnBranchChange != nil {
		sv.OnBranchChange()
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
	case srSpinner:
		sv.cycleSpinner(1)
		return true
	case srVaultPath:
		// Start inline editing for the selected vault path.
		sv.editingVault = row.key
		sv.vaultAC.Close()
		if row.key == vaultKeyMetis {
			sv.editVaultBuf = sv.metisVaultPath
		}
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
	case srAutoStart:
		sv.toggleAutoStart()
		return true
	}
	return false
}

// toggleAutoStart marks the row busy and dispatches the actual install/uninstall
// to app.go via OnToggleAutoStart, which runs the launchctl work in a goroutine
// and reports back via SetAutoStartResult. Runs entirely on the tview goroutine
// — never blocks on launchctl.
func (sv *SettingsView) toggleAutoStart() {
	if !launchagent.Available() || sv.autoStartBusy || sv.OnToggleAutoStart == nil {
		return
	}
	sv.autoStartBusy = true
	sv.autoStartMessage = ""
	sv.rebuildRows()
	sv.OnToggleAutoStart(sv.autoStartStatus.Installed)
}

// SetAutoStartResult is called from the app goroutine (via QueueUpdateDraw)
// once the install/uninstall completes. Clears busy, refreshes status, and
// stores a message for the detail panel.
func (sv *SettingsView) SetAutoStartResult(message string, status launchagent.Status) {
	sv.autoStartBusy = false
	sv.autoStartMessage = message
	sv.autoStartStatus = status
	sv.rebuildRows()
}

func (sv *SettingsView) handleDeleteOrDefault() bool {
	switch sv.currentRowKind() {
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

// currentRowKind returns the kind of the currently selected row, or srNone
// when the active category has no rows.
func (sv *SettingsView) currentRowKind() settingsRowKind {
	if sv.cursor < 0 || sv.cursor >= len(sv.rows) {
		return srNone
	}
	return sv.rows[sv.cursor].kind
}

// handleNew dispatches the "new" action — keyed off sv.category rather than
// the selected row kind, because "new" doesn't have a row to inspect (and
// empty list categories still need to fire). handleEdit and
// handleDeleteOrDefault, by contrast, switch on currentRowKind() because
// they operate on the selected row. Don't merge the two patterns.
func (sv *SettingsView) handleNew() bool {
	switch sv.category {
	case catProjects:
		if sv.OnNewProject != nil {
			sv.OnNewProject()
			return true
		}
	case catSchedules:
		if sv.OnNewSchedule != nil {
			sv.OnNewSchedule()
			return true
		}
	}
	return false
}

func (sv *SettingsView) handleEdit() bool {
	switch sv.currentRowKind() {
	case srProject:
		if pe := sv.SelectedProject(); pe != nil && sv.OnEditProject != nil {
			sv.OnEditProject(pe.Name, pe.Project)
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
	case tcell.KeyDown, tcell.KeyUp, tcell.KeyLeft, tcell.KeyRight:
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
		}
		sv.rebuildRows()
		return true
	case tcell.KeyEscape:
		sv.editingVault = ""
		sv.vaultAC.Close()
		sv.rebuildRows()
		return true
	case tcell.KeyDown, tcell.KeyUp, tcell.KeyLeft, tcell.KeyRight:
		return true // consume to avoid cursor movement / tab switch while editing
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

	if row.key != vaultKeyMetis {
		return
	}
	currentPath := sv.metisVaultPath
	dbKey := "kb.metis_vault_path"

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
	sv.metisVaultPath = newPath
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

	railW := computeRailW(width)
	paneW := width - railW

	sv.renderRail(screen, x, y, railW, height)
	if paneW > 0 {
		sv.renderPane(screen, x+railW, y, paneW, height)
	}
}

// renderRail draws the left category list. The border is highlighted when the
// rail owns focus.
func (sv *SettingsView) renderRail(screen tcell.Screen, x, y, w, h int) {
	border := theme.StyleBorder
	if sv.focus == focusRail {
		border = theme.StyleFocusedBorder
	}
	widget.DrawBorder(screen, x, y, w, h, border)
	ix, iy, iw, ih := x+1, y+1, w-2, h-2
	if iw <= 0 || ih <= 0 {
		return
	}
	widget.FillArea(screen, ix, iy, iw, ih, ' ', tcell.StyleDefault)

	for i, c := range allCategories {
		if i >= ih {
			break
		}
		label := c.Label()
		style := theme.StyleDimmed
		if c == sv.category {
			style = tcell.StyleDefault.Foreground(theme.ColorSelected).Bold(true)
		}
		prefix := "  "
		if c == sv.category && sv.focus == focusRail {
			prefix = "▸ "
		}
		widget.DrawText(screen, ix, iy+i, iw, truncRunes(prefix+label, iw), style)
	}
}

// renderPane draws the right pane for the active category. Layout:
//
//	┌──────────────────────────────┐
//	│ <category title>             │
//	│                              │  ← items list (only when len(rows) > 1)
//	│ ▸ row 0                      │
//	│   row 1                      │
//	│ ── <selected> ──             │  ← separator
//	│ <detail of selected row>     │
//	└──────────────────────────────┘
//
// For single-row categories (Sandbox, RemoteAPI), the items list and
// separator are skipped — the detail renderer gets the full pane below the
// title.
func (sv *SettingsView) renderPane(screen tcell.Screen, x, y, w, h int) {
	border := theme.StyleBorder
	if sv.focus == focusPane {
		border = theme.StyleFocusedBorder
	}
	widget.DrawBorder(screen, x, y, w, h, border)
	ix, iy, iw, ih := x+1, y+1, w-2, h-2
	if iw <= 0 || ih <= 0 {
		return
	}
	widget.FillArea(screen, ix, iy, iw, ih, ' ', tcell.StyleDefault)

	// Title row.
	widget.DrawText(screen, ix, iy, iw, sv.category.Label(), theme.StyleTitle)
	row0 := 2 // title + blank line

	row := sv.SelectedRow()
	useItems := len(sv.rows) > 1

	if useItems {
		// Reserve up to itemsCap rows for the items list, leaving
		// svDetailReserve rows below for the separator + detail.
		available := ih - row0
		if available <= 0 {
			return
		}
		itemsCap := available - svDetailReserve
		if itemsCap < 1 {
			itemsCap = 1
		}
		if itemsCap > svMaxItemsVisible {
			itemsCap = svMaxItemsVisible
		}
		if itemsCap > len(sv.rows) {
			itemsCap = len(sv.rows)
		}

		// Scroll math — keep cursor in view.
		if sv.cursor < sv.scrollOff {
			sv.scrollOff = sv.cursor
		}
		if sv.cursor >= sv.scrollOff+itemsCap {
			sv.scrollOff = sv.cursor - itemsCap + 1
		}
		if maxOff := max(0, len(sv.rows)-itemsCap); sv.scrollOff > maxOff {
			sv.scrollOff = maxOff
		}

		for i := 0; i < itemsCap; i++ {
			idx := sv.scrollOff + i
			if idx >= len(sv.rows) {
				break
			}
			r := sv.rows[idx]
			style := tcell.StyleDefault
			if r.kind == srWarning {
				style = style.Foreground(theme.ColorInProgress)
			}
			prefix := "  "
			if idx == sv.cursor {
				prefix = "▸ "
				if sv.focus == focusPane {
					style = style.Foreground(theme.ColorSelected).Bold(true)
				}
			}
			widget.DrawText(screen, ix, iy+row0+i, iw, truncRunes(prefix+r.label, iw), style)
		}
		row0 += itemsCap

		// Separator with selected row's name. Use a single dimmed line.
		// At narrow widths (iw < 5), skip the named banner and draw a plain
		// rule — otherwise reserving 4 cells for "── … " would underflow.
		if row0 < ih && iw > 0 {
			sep := strings.Repeat("─", iw)
			if row != nil && row.key != "" && iw >= 5 {
				name := truncRunes(row.label, iw-4)
				banner := "── " + name + " "
				bannerW := utf8.RuneCountInString(banner)
				if bannerW < iw {
					sep = banner + strings.Repeat("─", iw-bannerW)
				} else {
					sep = banner
				}
			}
			widget.DrawText(screen, ix, iy+row0, iw, sep, theme.StyleDimmed)
			row0++
		}
	}

	// Detail of selected row.
	detailH := ih - row0
	if detailH <= 0 || row == nil {
		return
	}
	sv.renderRowDetail(screen, ix, iy+row0, iw, detailH, row)
}

// renderRowDetail dispatches to the per-row detail renderer.
func (sv *SettingsView) renderRowDetail(screen tcell.Screen, x, y, w, h int, row *settingsRow) {
	switch row.kind {
	case srWarning:
		sv.renderWarningDetail(screen, x, y, w, h, row)
	case srSandbox:
		sv.renderSandboxDetail(screen, x, y, w, h)
	case srProject:
		sv.renderProjectDetail(screen, x, y, w, h, row)
	case srBackend:
		sv.renderBackendDetail(screen, x, y, w, h, row)
	case srKB:
		sv.renderKBDetail(screen, x, y, w, h)
	case srAPI:
		sv.renderAPIDetail(screen, x, y, w, h)
	case srVaultPath:
		sv.renderVaultPathDetail(screen, x, y, w, h, row)
	case srSpinner:
		sv.renderSpinnerDetail(screen, x, y, w, h)
	case srLogs:
		sv.renderLogsDetail(screen, x, y, w, h, row)
	case srDaemon:
		sv.renderDaemonDetail(screen, x, y, w, h)
	case srSourcePath:
		sv.renderSourcePathDetail(screen, x, y, w, h)
	case srUpdateArgus:
		sv.renderUpdateArgusDetail(screen, x, y, w, h)
	case srSchedule:
		sv.renderScheduleDetail(screen, x, y, w, h)
	case srAutoStart:
		sv.renderAutoStartDetail(screen, x, y, w, h)
	}
}

// renderAPIDetail draws the Remote API status block in the right pane.
func (sv *SettingsView) renderAPIDetail(screen tcell.Screen, x, y, w, h int) {
	widget.DrawText(screen, x, y, w, "Remote API", theme.StyleTitle)
	r := 2

	status := "Disabled"
	statusColor := theme.ColorError
	if sv.apiEnabled {
		status = fmt.Sprintf("Enabled (port %d)", sv.apiPort)
		statusColor = theme.ColorComplete
	}
	widget.DrawText(screen, x, y+r, w, "Status: "+status, tcell.StyleDefault.Foreground(statusColor))
	r += 2

	if sv.apiBootRecorded && sv.apiEnabled != sv.apiEnabledAtBoot {
		widget.DrawText(screen, x, y+r, w, "(restart required)", tcell.StyleDefault.Foreground(theme.ColorInProgress))
		r += 2
	}

	if r < h {
		widget.DrawText(screen, x, y+r, w, "Localhost + Tailscale-bound HTTP", theme.StyleDimmed)
		r++
		if r < h {
			widget.DrawText(screen, x, y+r, w, "API + mobile PWA.", theme.StyleDimmed)
		}
	}
	if h > 1 {
		widget.DrawText(screen, x, y+h-1, w, "[enter] toggle", theme.StyleDimmed)
	}
}

func (sv *SettingsView) renderAutoStartDetail(screen tcell.Screen, x, y, w, h int) {
	widget.DrawText(screen, x, y, w, "Auto-start at login", theme.StyleTitle)
	r := 2

	statusLabel := "Disabled"
	statusColor := theme.ColorError
	switch {
	case sv.autoStartStatus.Installed && sv.autoStartStatus.Loaded:
		statusLabel = "Enabled (running)"
		statusColor = theme.ColorComplete
	case sv.autoStartStatus.Installed:
		statusLabel = "Installed (not loaded)"
		statusColor = theme.ColorInProgress
	}
	widget.DrawText(screen, x, y+r, w, "Status: "+statusLabel, tcell.StyleDefault.Foreground(statusColor))
	r += 2

	if sv.autoStartStatus.PlistPath != "" {
		widget.DrawText(screen, x, y+r, w, "Plist:", tcell.StyleDefault.Foreground(theme.ColorTitle))
		r++
		widget.DrawText(screen, x, y+r, w, "  "+sv.autoStartStatus.PlistPath, theme.StyleDimmed)
		r += 2
	}

	if sv.autoStartMessage != "" && r < h-1 {
		widget.DrawText(screen, x, y+r, w, sv.autoStartMessage, theme.StyleDimmed)
		r += 2
	}

	if r < h-1 {
		widget.DrawText(screen, x, y+r, w, "Uses launchd. Restarts on crash;", theme.StyleDimmed)
		r++
		widget.DrawText(screen, x, y+r, w, "honors `argus daemon stop` (clean exit).", theme.StyleDimmed)
	}

	if h > 1 {
		var hint string
		switch {
		case sv.autoStartBusy:
			hint = "working..."
		case sv.autoStartStatus.Installed:
			hint = "[enter] disable"
		default:
			hint = "[enter] enable"
		}
		widget.DrawText(screen, x, y+h-1, w, hint, theme.StyleDimmed)
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
			widget.DrawText(screen, x, y+r, w, truncRunes(line, w), theme.StyleDimmed)
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

	if len(sv.sandboxAllowAppleEvents) > 0 {
		// Separator above this section only when ExtraWrite rendered. DenyRead
		// already emits its own trailing row++ (so adding one here would
		// double-space when DenyRead is shown but ExtraWrite is empty), and
		// ExtraWrite intentionally omits its trailing row++ to preserve the
		// pre-existing spacing footprint when AllowAppleEvents is empty
		// (the "[enter] toggle" footer's row+2 < h guard relies on row being
		// tight).
		if len(sv.sandboxExtraWrite) > 0 {
			row++
		}
		widget.DrawText(screen, x, y+row, w, "Allow AppleEvents:", tcell.StyleDefault.Foreground(theme.ColorTitle))
		row++
		for _, b := range sv.sandboxAllowAppleEvents {
			if row >= h {
				break
			}
			widget.DrawText(screen, x, y+row, w, "  "+b, theme.StyleDimmed)
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
	if len(pe.Project.Sandbox.AllowAppleEvents) > 0 && r < h {
		widget.DrawText(screen, x, y+r, w, "  Allow AppleEvents:", theme.StyleDimmed)
		r++
		for _, b := range pe.Project.Sandbox.AllowAppleEvents {
			if r >= h {
				break
			}
			widget.DrawText(screen, x, y+r, w, "    "+b, theme.StyleDimmed)
			r++
		}
	}
	if len(pe.Project.Sandbox.DenyRead) > 0 || len(pe.Project.Sandbox.ExtraWrite) > 0 || len(pe.Project.Sandbox.AllowAppleEvents) > 0 {
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
		widget.DrawText(screen, x, y+h-1, w, "[n] new  [e] edit  [d] delete  [i] quick add  [a] apple events", theme.StyleDimmed)
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
	// Rune-aware truncation: w-12 can go negative at extreme widths.
	cmd := be.Backend.Command
	if budget := w - 12; budget > 0 && utf8.RuneCountInString(cmd) > budget {
		cmd = truncRunes(cmd, budget) + "…"
	} else if budget <= 0 {
		cmd = ""
	}
	widget.DrawText(screen, x, y+r, w, "  Command: "+cmd, theme.StyleDimmed)
	r++
	widget.DrawText(screen, x, y+r, w, "  Prompt Flag: "+be.Backend.PromptFlag, theme.StyleDimmed)
	r += 2

	hints := "[d] set as default  (read-only: backends are hardcoded)"
	if be.Name == sv.defaultBackend {
		hints = "(already default; backends are hardcoded)"
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

	if r < h {
		widget.DrawText(screen, x, y+r, w, "[enter] toggle KB", theme.StyleDimmed)
	}
}

func (sv *SettingsView) renderVaultPathDetail(screen tcell.Screen, x, y, w, h int, row *settingsRow) {
	if row.key != vaultKeyMetis {
		return
	}
	title := "Metis Vault"
	path := sv.metisVaultPath
	desc := "Obsidian vault for KB indexing."

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
		line := truncRunes(sv.logLines[lineIdx], w)
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

// computeRailW returns the left-rail width for the given total settings-view
// width. Target is 20 cells (fits "Knowledge Base" + selection marker), but
// capped at width/3 so the right pane never starves. The 12-cell floor only
// kicks in when the total has room for it — narrower terminals get a
// proportional rail rather than overflowing.
//
// Used by both Draw (to lay out the rail) and HandleClick (to hit-test rail
// clicks); the two MUST agree, so keep the math in one place.
func computeRailW(width int) int {
	railW := 20
	if railW > width/3 {
		railW = width / 3
	}
	if railW < 12 && width >= 12 {
		railW = 12
	}
	if railW > width {
		railW = width
	}
	return railW
}

// truncRunes returns the longest prefix of s whose rune count is <= max.
// Byte slicing on a multibyte rune boundary panics or produces invalid UTF-8;
// callers that need to clip to a cell width must go through this helper. A
// non-positive max returns "".
func truncRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i]
		}
		n++
	}
	return s
}
