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

func TestSettings_RenderSandboxDetail(t *testing.T) {
	sv := makeSettings(t)
	for i, row := range sv.rows {
		if row.kind == srSandbox {
			sv.cursor = i
			break
		}
	}
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
	for i, row := range sv.rows {
		if row.kind == srProject && row.key == "p" {
			sv.cursor = i
			break
		}
	}
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderBackendDetail(t *testing.T) {
	sv := makeSettings(t)
	for i, row := range sv.rows {
		if row.kind == srBackend {
			sv.cursor = i
			break
		}
	}
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderKBDetail(t *testing.T) {
	sv := makeSettings(t)
	for i, row := range sv.rows {
		if row.kind == srKB {
			sv.cursor = i
			break
		}
	}
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderVaultPathDetail(t *testing.T) {
	sv := makeSettings(t)
	sv.metisVaultPath = "/some/vault"
	sv.discoveredVaults = []string{"/some/vault", "/other/vault"}
	sv.rebuildRows()
	for i, row := range sv.rows {
		if row.kind == srVaultPath && row.key == vaultKeyMetis {
			sv.cursor = i
			break
		}
	}
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderVaultPathDetail_Editing(t *testing.T) {
	sv := makeSettings(t)
	sv.editingVault = vaultKeyMetis
	sv.editVaultBuf = "/edit"
	sv.rebuildRows()
	for i, row := range sv.rows {
		if row.kind == srVaultPath && row.key == vaultKeyMetis {
			sv.cursor = i
			break
		}
	}
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderSpinnerDetail(t *testing.T) {
	sv := makeSettings(t)
	for i, row := range sv.rows {
		if row.kind == srSpinner {
			sv.cursor = i
			break
		}
	}
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderDaemonDetail(t *testing.T) {
	sv := makeSettings(t)
	sv.SetDaemonConnected(true)
	for i, row := range sv.rows {
		if row.kind == srDaemon {
			sv.cursor = i
			break
		}
	}
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderDaemonDetail_Restarting(t *testing.T) {
	sv := makeSettings(t)
	sv.SetDaemonConnected(true)
	sv.SetDaemonRestarting(true)
	for i, row := range sv.rows {
		if row.kind == srDaemon {
			sv.cursor = i
			break
		}
	}
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderSourcePathDetail(t *testing.T) {
	sv := makeSettings(t)
	sv.SetDaemonConnected(true)
	for i, row := range sv.rows {
		if row.kind == srSourcePath {
			sv.cursor = i
			break
		}
	}
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
	for i, row := range sv.rows {
		if row.kind == srUpdateArgus {
			sv.cursor = i
			break
		}
	}
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderUpdateArgusDetail_NoSource(t *testing.T) {
	sv := makeSettings(t)
	sv.SetDaemonConnected(true)
	sv.argusSourcePath = ""
	sv.rebuildRows()
	for i, row := range sv.rows {
		if row.kind == srUpdateArgus {
			sv.cursor = i
			break
		}
	}
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderUpdateArgusDetail_Updating(t *testing.T) {
	sv := makeSettings(t)
	sv.SetDaemonConnected(true)
	sv.argusSourcePath = "/tmp"
	sv.updating = true
	sv.rebuildRows()
	for i, row := range sv.rows {
		if row.kind == srUpdateArgus {
			sv.cursor = i
			break
		}
	}
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderScheduleDetail_Empty(t *testing.T) {
	sv := makeSettings(t)
	for i, row := range sv.rows {
		if row.kind == srSchedule {
			sv.cursor = i
			break
		}
	}
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
	for i, row := range sv.rows {
		if row.kind == srSchedule && row.key == "id" {
			sv.cursor = i
			break
		}
	}
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderAutoStartDetail(t *testing.T) {
	sv := makeSettings(t)
	if !launchagent.Available() {
		t.Skip("launchagent not available")
	}
	for i, row := range sv.rows {
		if row.kind == srAutoStart {
			sv.cursor = i
			break
		}
	}
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
	for i, row := range sv.rows {
		if row.kind == srAutoStart {
			sv.cursor = i
			break
		}
	}
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
	for i, row := range sv.rows {
		if row.kind == srAutoStart {
			sv.cursor = i
			break
		}
	}
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderWarningDetail(t *testing.T) {
	sv := makeSettings(t)
	for i, row := range sv.rows {
		if row.kind == srWarning {
			sv.cursor = i
			break
		}
	}
	sv.SetRect(0, 0, 100, 30)
	sv.Draw(drawSim(t))
}

func TestSettings_RenderWarningDetail_OK(t *testing.T) {
	sv := makeSettings(t)
	sv.SetDaemonConnected(true) // clears warnings → "_ok" row
	for i, row := range sv.rows {
		if row.kind == srWarning {
			sv.cursor = i
			break
		}
	}
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

func TestSettings_HandleDeleteSchedule(t *testing.T) {
	d := testDB(t)
	s := &model.ScheduledTask{ID: "id", Name: "x", Project: "p", Prompt: "x", Schedule: "@daily"}
	d.AddSchedule(s)
	sv := NewSettingsView(d)
	sv.Refresh()
	for i, row := range sv.rows {
		if row.kind == srSchedule && row.key == "id" {
			sv.cursor = i
			break
		}
	}
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
	for i, row := range sv.rows {
		if row.kind == srSchedule && row.key == "id" {
			sv.cursor = i
			break
		}
	}
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
	for i, row := range sv.rows {
		if row.kind == srSchedule && row.key == "rid" {
			sv.cursor = i
			break
		}
	}
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
	for i, row := range sv.rows {
		if row.kind == srSchedule && row.key == "id" {
			sv.cursor = i
			break
		}
	}
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 't', 0))
	testutil.Equal(t, got, true)
}

func TestSettings_HandleKey_R(t *testing.T) {
	d := testDB(t)
	s := &model.ScheduledTask{ID: "id", Name: "x", Project: "p", Prompt: "x", Schedule: "@daily"}
	d.AddSchedule(s)
	sv := NewSettingsView(d)
	sv.Refresh()
	for i, row := range sv.rows {
		if row.kind == srSchedule && row.key == "id" {
			sv.cursor = i
			break
		}
	}
	called := false
	sv.OnRunSchedule = func(id string) { called = true }
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'r', 0))
	testutil.Equal(t, got, true)
	testutil.Equal(t, called, true)
}

func TestSettings_HandleKey_LeftRightOnSpinner(t *testing.T) {
	sv := makeSettings(t)
	for i, row := range sv.rows {
		if row.kind == srSpinner {
			sv.cursor = i
			break
		}
	}
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
	testutil.Equal(t, got, true)
	got = sv.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	testutil.Equal(t, got, true)
}

func TestSettings_HandleKey_LeftRightOnVault(t *testing.T) {
	sv := makeSettings(t)
	sv.discoveredVaults = []string{"/a", "/b"}
	for i, row := range sv.rows {
		if row.kind == srVaultPath {
			sv.cursor = i
			break
		}
	}
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
	testutil.Equal(t, got, true)
	got = sv.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	testutil.Equal(t, got, true)
}

func TestSettings_HandleKey_LeftRightFallthrough(t *testing.T) {
	sv := makeSettings(t)
	for i, row := range sv.rows {
		if row.kind == srBackend {
			sv.cursor = i
			break
		}
	}
	got := sv.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
	testutil.Equal(t, got, false)
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
	for i, row := range sv.rows {
		if row.kind == srLogs {
			sv.cursor = i
			break
		}
	}
	sv.logLines = []string{"a", "b", "c"}
	sv.logScrollOff = 1
	sv.logKey = sv.SelectedRow().key

	sp := NewSettingsPage(sv)
	sp.SetRect(0, 0, 100, 30)
	handler := sp.MouseHandler()

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
