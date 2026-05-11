package tui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/launchagent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// --- Settings detail render functions ---

func makeSettings(t *testing.T) *SettingsView {
	t.Helper()
	d := testDB(t)
	sv := NewSettingsView(d)
	sv.Refresh()
	return sv
}

// selectRowInCategory switches to the category and parks the cursor on the
// first row matching kind (and key, if non-empty). Fails the test when no
// matching row is found — useful for catching test/data drift.
func selectRowInCategory(t *testing.T, sv *SettingsView, c settingsCategory, kind settingsRowKind, key string) {
	t.Helper()
	sv.setCategory(c)
	for i, r := range sv.rows {
		if r.kind == kind && (key == "" || r.key == key) {
			sv.cursor = i
			return
		}
	}
	t.Fatalf("row kind=%d key=%q not found in category %s", kind, key, c.Label())
}

func TestSettings_RenderSandboxDetail(t *testing.T) {
	sv := makeSettings(t)
	selectRowInCategory(t, sv, catSandbox, srSandbox, "")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderProjectDetail(t *testing.T) {
	d := testDB(t)
	v := true
	d.SetProject("p", config.Project{
		Path:   "/tmp",
		Branch: "main",
		Sandbox: config.ProjectSandboxConfig{
			Enabled:    &v,
			DenyRead:   []string{"/secret"},
			ExtraWrite: []string{"/etc"},
		},
	})
	sv := NewSettingsView(d)
	sv.Refresh()
	selectRowInCategory(t, sv, catProjects, srProject, "p")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderBackendDetail(t *testing.T) {
	sv := makeSettings(t)
	selectRowInCategory(t, sv, catBackends, srBackend, "")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderKBDetail(t *testing.T) {
	sv := makeSettings(t)
	selectRowInCategory(t, sv, catKnowledgeBase, srKB, "")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderVaultPathDetail(t *testing.T) {
	sv := makeSettings(t)
	sv.metisVaultPath = "/some/vault"
	sv.discoveredVaults = []string{"/some/vault", "/other/vault"}
	sv.rebuildRows()
	selectRowInCategory(t, sv, catKnowledgeBase, srVaultPath, vaultKeyMetis)
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderVaultPathDetail_Editing(t *testing.T) {
	sv := makeSettings(t)
	sv.editingVault = vaultKeyMetis
	sv.editVaultBuf = "/edit"
	sv.rebuildRows()
	selectRowInCategory(t, sv, catKnowledgeBase, srVaultPath, vaultKeyMetis)
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderSpinnerDetail(t *testing.T) {
	sv := makeSettings(t)
	selectRowInCategory(t, sv, catAppearance, srSpinner, "")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderDaemonDetail(t *testing.T) {
	sv := makeSettings(t)
	sv.SetDaemonConnected(true)
	selectRowInCategory(t, sv, catSystem, srDaemon, "")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderDaemonDetail_Restarting(t *testing.T) {
	sv := makeSettings(t)
	sv.SetDaemonConnected(true)
	sv.SetDaemonRestarting(true)
	selectRowInCategory(t, sv, catSystem, srDaemon, "")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderSourcePathDetail(t *testing.T) {
	sv := makeSettings(t)
	sv.SetDaemonConnected(true)
	selectRowInCategory(t, sv, catSystem, srSourcePath, "")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderUpdateArgusDetail(t *testing.T) {
	sv := makeSettings(t)
	sv.SetDaemonConnected(true)
	sv.argusSourcePath = "/tmp/argus"
	sv.updateOutput = "go install output\nline 2"
	sv.updateStatus = "Failed: oops"
	sv.rebuildRows()
	selectRowInCategory(t, sv, catSystem, srUpdateArgus, "")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderUpdateArgusDetail_NoSource(t *testing.T) {
	sv := makeSettings(t)
	sv.SetDaemonConnected(true)
	sv.argusSourcePath = ""
	sv.rebuildRows()
	selectRowInCategory(t, sv, catSystem, srUpdateArgus, "")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderUpdateArgusDetail_Updating(t *testing.T) {
	sv := makeSettings(t)
	sv.SetDaemonConnected(true)
	sv.argusSourcePath = "/tmp"
	sv.updating = true
	sv.rebuildRows()
	selectRowInCategory(t, sv, catSystem, srUpdateArgus, "")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderScheduleDetail_Empty(t *testing.T) {
	sv := makeSettings(t)
	selectRowInCategory(t, sv, catSchedules, srSchedule, "")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderScheduleDetail_Selected(t *testing.T) {
	d := testDB(t)
	now := time.Now()
	s := &model.ScheduledTask{
		ID:         "id",
		Name:       "weekly task",
		Project:    "p",
		Backend:    "claude",
		Schedule:   "@weekly",
		Prompt:     "do it\nline 2",
		Enabled:    true,
		LastRunAt:  now,
		NextRunAt:  now.Add(time.Hour),
		LastTaskID: "task-id",
		LastError:  "some error",
	}
	d.AddSchedule(s)
	sv := NewSettingsView(d)
	sv.Refresh()
	selectRowInCategory(t, sv, catSchedules, srSchedule, "id")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderAutoStartDetail(t *testing.T) {
	sv := makeSettings(t)
	if !launchagent.Available() {
		t.Skip("launchagent not available")
	}
	selectRowInCategory(t, sv, catSystem, srAutoStart, "")
	sv.autoStartMessage = "test message"
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderAutoStartDetail_Busy(t *testing.T) {
	sv := makeSettings(t)
	if !launchagent.Available() {
		t.Skip("launchagent not available")
	}
	sv.autoStartBusy = true
	sv.rebuildRows()
	selectRowInCategory(t, sv, catSystem, srAutoStart, "")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderAutoStartDetail_InstalledNotLoaded(t *testing.T) {
	sv := makeSettings(t)
	if !launchagent.Available() {
		t.Skip("launchagent not available")
	}
	sv.autoStartStatus = launchagent.Status{
		Installed: true,
		Loaded:    false,
		PlistPath: "/some/path",
	}
	sv.rebuildRows()
	selectRowInCategory(t, sv, catSystem, srAutoStart, "")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderWarningDetail(t *testing.T) {
	sv := makeSettings(t)
	sv.SetDaemonConnected(false) // forces a warning row
	selectRowInCategory(t, sv, catSystem, srWarning, "")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderWarningDetail_OK(t *testing.T) {
	sv := makeSettings(t)
	sv.SetDaemonConnected(true) // clears warnings → "_ok" row
	selectRowInCategory(t, sv, catSystem, srWarning, "_ok")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_TinyRect(t *testing.T) {
	sv := makeSettings(t)
	sv.SetRect(0, 0, 0, 0)
	sv.Draw(drawSim(t))
}

func TestSettings_NarrowRect(t *testing.T) {
	sv := makeSettings(t)
	sv.SetRect(0, 0, 30, 30)
	sv.Draw(drawSim(t))
}

// --- Settings handlers ---

func TestSettings_CycleSpinner(t *testing.T) {
	sv := makeSettings(t)
	prev := sv.spinnerStyle
	sv.cycleSpinner(1)
	if sv.spinnerStyle == prev {
		t.Error("spinner should cycle")
	}
	sv.cycleSpinner(-1)
}

// TestSettings_RenderNarrowPane drives the right-pane separator banner at
// inner widths that exposed a `name[:iw-4]` slice panic before the iw>=5
// guard landed, plus the `cmd[:w-12]` slice in renderBackendDetail.
// Regression test: must NOT panic across all categories.
func TestSettings_RenderNarrowPane(t *testing.T) {
	d := testDB(t)
	if err := d.SetProject("p1", config.Project{Path: "/tmp/p1", Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	sv := NewSettingsView(d)
	sv.Refresh()

	widths := []int{4, 5, 6, 8, 10, 12, 15, 20, 30}

	for _, c := range []settingsCategory{catProjects, catBackends, catKnowledgeBase, catSandbox} {
		sv.setCategory(c)
		for _, w := range widths {
			sv.SetRect(0, 0, w, 12)
			sv.Draw(drawSim(t)) // must not panic
		}
	}
}

func TestSettings_RenderSandboxDetail_WithDenyAndExtraWrite(t *testing.T) {
	d := testDB(t)
	sv := NewSettingsView(d)
	sv.Refresh()
	sv.sandboxEnabled = true
	sv.sandboxDenyRead = []string{"/secrets", "/etc/passwd"}
	sv.sandboxExtraWrite = []string{"/tmp/build", "/var/cache"}
	selectRowInCategory(t, sv, catSandbox, srSandbox, "")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderAPIDetail(t *testing.T) {
	sv := makeSettings(t)
	selectRowInCategory(t, sv, catRemoteAPI, srAPI, "")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))

	// Also cover the "enabled + restart required" branch.
	sv.apiEnabled = true
	sv.apiPort = 7743
	sv.apiBootRecorded = true
	sv.apiEnabledAtBoot = false
	sv.rebuildRows()
	selectRowInCategory(t, sv, catRemoteAPI, srAPI, "")
	sv.Draw(drawSim(t))
}

func TestSettings_RenderLogsDetail(t *testing.T) {
	sv := makeSettings(t)
	selectRowInCategory(t, sv, catLogs, srLogs, "ux")
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))

	selectRowInCategory(t, sv, catLogs, srLogs, "daemon")
	sv.Draw(drawSim(t))
}

func TestSettings_HandleDeleteSchedule(t *testing.T) {
	d := testDB(t)
	s := &model.ScheduledTask{ID: "id", Name: "x", Project: "p", Prompt: "x", Schedule: "@daily"}
	d.AddSchedule(s)
	sv := NewSettingsView(d)
	sv.Refresh()
	selectRowInCategory(t, sv, catSchedules, srSchedule, "id")
	called := ""
	sv.OnDeleteSchedule = func(id string) { called = id }
	got := sv.handleDeleteSchedule()
	testutil.Equal(t, got, true)
	testutil.Equal(t, called, "id")
}

func TestSettings_HandleDeleteSchedule_NoCallback(t *testing.T) {
	sv := makeSettings(t)
	got := sv.handleDeleteSchedule()
	testutil.Equal(t, got, false)
}

func TestSettings_HandleToggleSchedule(t *testing.T) {
	d := testDB(t)
	s := &model.ScheduledTask{ID: "id", Name: "x", Project: "p", Prompt: "x", Schedule: "@daily", Enabled: true}
	d.AddSchedule(s)
	sv := NewSettingsView(d)
	sv.Refresh()
	selectRowInCategory(t, sv, catSchedules, srSchedule, "id")
	got := sv.handleToggleSchedule()
	testutil.Equal(t, got, true)
	updated, _ := d.GetSchedule("id")
	testutil.Equal(t, updated.Enabled, false)
}

func TestSettings_HandleToggleSchedule_NoSelection(t *testing.T) {
	sv := makeSettings(t)
	got := sv.handleToggleSchedule()
	testutil.Equal(t, got, false)
}

func TestSettings_HandleRunSchedule(t *testing.T) {
	d := testDB(t)
	s := &model.ScheduledTask{ID: "rid", Name: "r", Project: "p", Prompt: "x", Schedule: "@daily"}
	d.AddSchedule(s)
	sv := NewSettingsView(d)
	sv.Refresh()
	selectRowInCategory(t, sv, catSchedules, srSchedule, "rid")
	called := ""
	sv.OnRunSchedule = func(id string) { called = id }
	got := sv.handleRunSchedule()
	testutil.Equal(t, got, true)
	testutil.Equal(t, called, "rid")
}

func TestSettings_HandleRunSchedule_NoCallback(t *testing.T) {
	sv := makeSettings(t)
	got := sv.handleRunSchedule()
	testutil.Equal(t, got, false)
}

func TestSettings_SelectedSchedule_Empty(t *testing.T) {
	sv := makeSettings(t)
	got := sv.SelectedSchedule()
	testutil.Nil(t, got)
}

func TestSettings_SetAutoStartResult(t *testing.T) {
	sv := makeSettings(t)
	sv.autoStartBusy = true
	sv.SetAutoStartResult("done", launchagent.Status{Installed: true})
	testutil.Equal(t, sv.autoStartBusy, false)
	testutil.Equal(t, sv.autoStartMessage, "done")
}

// 't', 'r' on schedule rows
func TestSettings_HandleKey_T(t *testing.T) {
	d := testDB(t)
	s := &model.ScheduledTask{ID: "id", Name: "x", Project: "p", Prompt: "x", Schedule: "@daily", Enabled: true}
	d.AddSchedule(s)
	sv := NewSettingsView(d)
	sv.Refresh()
	selectRowInCategory(t, sv, catSchedules, srSchedule, "id")
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 't', 0))
	testutil.Equal(t, got, true)
}

func TestSettings_HandleKey_R(t *testing.T) {
	d := testDB(t)
	s := &model.ScheduledTask{ID: "id", Name: "x", Project: "p", Prompt: "x", Schedule: "@daily"}
	d.AddSchedule(s)
	sv := NewSettingsView(d)
	sv.Refresh()
	selectRowInCategory(t, sv, catSchedules, srSchedule, "id")
	called := false
	sv.OnRunSchedule = func(id string) { called = true }
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'r', 0))
	testutil.Equal(t, got, true)
	testutil.Equal(t, called, true)
}

func TestSettings_HandleKey_LeftRightOnSpinner(t *testing.T) {
	sv := makeSettings(t)
	selectRowInCategory(t, sv, catAppearance, srSpinner, "")
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
	testutil.Equal(t, got, true)
	got = sv.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	testutil.Equal(t, got, true)
}

func TestSettings_HandleKey_LeftRightOnVault(t *testing.T) {
	sv := makeSettings(t)
	sv.discoveredVaults = []string{"/a", "/b"}
	selectRowInCategory(t, sv, catKnowledgeBase, srVaultPath, vaultKeyMetis)
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
	testutil.Equal(t, got, true)
	got = sv.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	testutil.Equal(t, got, true)
}

func TestSettings_HandleKey_LeftFromPaneSwitchesFocus(t *testing.T) {
	sv := makeSettings(t)
	selectRowInCategory(t, sv, catBackends, srBackend, "")
	// Focus starts on the pane (default). Left should move it to the rail.
	testutil.Equal(t, sv.focus, focusPane)
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
	testutil.Equal(t, got, true)
	testutil.Equal(t, sv.focus, focusRail)
}

func TestSettings_PasteHandler_NoOpWhenNotEditing(t *testing.T) {
	sv := makeSettings(t)
	paste := sv.PasteHandler()
	paste("garbage", func(p tview.Primitive) {})
	testutil.Equal(t, sv.IsEditing(), false)
}

func TestSettings_PasteHandler_EmptyTextNoOp(t *testing.T) {
	sv := makeSettings(t)
	sv.editingVault = vaultKeyMetis
	sv.editVaultBuf = "x"
	paste := sv.PasteHandler()
	paste("", func(p tview.Primitive) {})
	testutil.Equal(t, sv.editVaultBuf, "x")
}

func TestSettings_PasteHandler_AppendsToVaultEdit(t *testing.T) {
	sv := makeSettings(t)
	sv.editingVault = vaultKeyMetis
	sv.editVaultBuf = "/v"
	paste := sv.PasteHandler()
	paste("ault", func(p tview.Primitive) {})
	testutil.Equal(t, sv.editVaultBuf, "/vault")
}

func TestSettings_PasteHandler_AppendsToSourceEdit(t *testing.T) {
	sv := makeSettings(t)
	sv.editingSource = true
	sv.editSourceBuf = "/p"
	paste := sv.PasteHandler()
	paste("ath", func(p tview.Primitive) {})
	testutil.Equal(t, sv.editSourceBuf, "/path")
}

// --- Settings Page ---

func TestSettingsPage_DrawZeroRect(t *testing.T) {
	sv := makeSettings(t)
	sp := NewSettingsPage(sv)
	sp.SetRect(0, 0, 0, 0)
	sp.Draw(drawSim(t))
}

func TestSettingsPage_MouseHandler(t *testing.T) {
	sv := makeSettings(t)
	selectRowInCategory(t, sv, catLogs, srLogs, "")
	sv.logLines = []string{"a", "b", "c"}
	sv.logScrollOff = 1
	sv.logKey = sv.SelectedRow().key

	sp := NewSettingsPage(sv)
	sp.SetRect(0, 0, 100, 30)
	handler := sp.MouseHandler()

	// Scroll wheel events: settings forwards them to HandleMouse, which
	// honors them only when the active row is a log row.
	ev := tcell.NewEventMouse(0, 0, tcell.WheelUp, 0)
	consumed, _ := handler(tview.MouseScrollUp, ev, func(p tview.Primitive) {})
	testutil.Equal(t, consumed, true)
}

// --- More targeted app tests ---

func TestApp_OpenInFinder_NoFile(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.openInFinder()
}

func TestApp_OpenInEditor_NoFile(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.openInEditor()
}

func TestApp_OpenTerminal_NoWorktree(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.openTerminal()
}

// --- handleNewTaskKey ---

func TestApp_HandleNewTaskKey_Cancel(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("p", config.Project{Path: t.TempDir()})

	app.onNewTask()
	app.handleNewTaskKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestApp_HandleNewTaskKey_PassesThrough(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("p", config.Project{Path: t.TempDir()})

	app.onNewTask()
	app.handleNewTaskKey(tcell.NewEventKey(tcell.KeyRune, 'a', 0))
}
