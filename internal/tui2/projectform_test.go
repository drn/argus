package tui2

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

func TestProjectForm_NewDefaults(t *testing.T) {
	pf := NewProjectForm()
	testutil.Equal(t, pf.sandboxIdx, 0) // default is Inherit
	testutil.Equal(t, pf.focused, 0)
	testutil.Equal(t, pf.editMode, false)
}

func TestProjectForm_LoadProject_SandboxInherit(t *testing.T) {
	pf := NewProjectForm()
	pf.LoadProject("test", config.Project{
		Path: "/tmp/test",
	})
	testutil.Equal(t, pf.sandboxIdx, 0) // nil → Inherit
}

func TestProjectForm_LoadProject_SandboxEnabled(t *testing.T) {
	pf := NewProjectForm()
	v := true
	pf.LoadProject("test", config.Project{
		Path:    "/tmp/test",
		Sandbox: config.ProjectSandboxConfig{Enabled: &v},
	})
	testutil.Equal(t, pf.sandboxIdx, 1) // true → Enabled
}

func TestProjectForm_LoadProject_SandboxDisabled(t *testing.T) {
	pf := NewProjectForm()
	v := false
	pf.LoadProject("test", config.Project{
		Path:    "/tmp/test",
		Sandbox: config.ProjectSandboxConfig{Enabled: &v},
	})
	testutil.Equal(t, pf.sandboxIdx, 2) // false → Disabled
}

func TestProjectForm_Result_SandboxInherit(t *testing.T) {
	pf := NewProjectForm()
	pf.fields[pfFieldName] = []rune("test")
	pf.fields[pfFieldPath] = []rune("/tmp/test")
	pf.sandboxIdx = 0 // Inherit

	_, proj := pf.Result()
	testutil.Nil(t, proj.Sandbox.Enabled)
}

func TestProjectForm_Result_SandboxEnabled(t *testing.T) {
	pf := NewProjectForm()
	pf.fields[pfFieldName] = []rune("test")
	pf.fields[pfFieldPath] = []rune("/tmp/test")
	pf.sandboxIdx = 1 // Enabled

	_, proj := pf.Result()
	if proj.Sandbox.Enabled == nil {
		t.Fatal("expected Sandbox.Enabled to be non-nil")
	}
	testutil.Equal(t, *proj.Sandbox.Enabled, true)
}

func TestProjectForm_Result_SandboxDisabled(t *testing.T) {
	pf := NewProjectForm()
	pf.fields[pfFieldName] = []rune("test")
	pf.fields[pfFieldPath] = []rune("/tmp/test")
	pf.sandboxIdx = 2 // Disabled

	_, proj := pf.Result()
	if proj.Sandbox.Enabled == nil {
		t.Fatal("expected Sandbox.Enabled to be non-nil")
	}
	testutil.Equal(t, *proj.Sandbox.Enabled, false)
}

func TestProjectForm_SandboxSelector_LeftRight(t *testing.T) {
	pf := NewProjectForm()
	pf.focused = pfFieldSandbox
	testutil.Equal(t, pf.sandboxIdx, 0) // Inherit

	// Right → Enabled
	pf.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone))
	testutil.Equal(t, pf.sandboxIdx, 1)

	// Right → Disabled
	pf.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone))
	testutil.Equal(t, pf.sandboxIdx, 2)

	// Right → wraps to Inherit
	pf.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone))
	testutil.Equal(t, pf.sandboxIdx, 0)

	// Left → wraps to Disabled
	pf.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone))
	testutil.Equal(t, pf.sandboxIdx, 2)
}

func TestProjectForm_TabCyclesToSandbox(t *testing.T) {
	pf := NewProjectForm()
	pf.focused = pfFieldBackend

	// Tab from Backend → Sandbox
	pf.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	testutil.Equal(t, pf.focused, pfFieldSandbox)

	// Tab from Sandbox → wraps to Name
	pf.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	testutil.Equal(t, pf.focused, pfFieldName)
}

func TestProjectForm_EnterOnSandbox_SubmitsForm(t *testing.T) {
	pf := NewProjectForm()
	pf.focused = pfFieldSandbox

	pf.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	testutil.Equal(t, pf.done, true)
}

func TestProjectForm_EnterOnBackend_AdvancesToSandbox(t *testing.T) {
	pf := NewProjectForm()
	pf.focused = pfFieldBackend

	pf.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	testutil.Equal(t, pf.focused, pfFieldSandbox)
	testutil.Equal(t, pf.done, false)
}

func TestProjectForm_BacktabFromName_GoesToSandbox(t *testing.T) {
	pf := NewProjectForm()
	pf.focused = pfFieldName

	pf.HandleKey(tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone))
	testutil.Equal(t, pf.focused, pfFieldSandbox)
}

func TestProjectForm_EditMode_BacktabSkipsName(t *testing.T) {
	pf := NewProjectForm()
	pf.LoadProject("test", config.Project{Path: "/tmp"})
	// In edit mode, focused starts at Path.
	pf.focused = pfFieldPath

	// Backtab from Path skips Name → goes to Sandbox.
	pf.HandleKey(tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone))
	testutil.Equal(t, pf.focused, pfFieldSandbox)
}

func TestProjectForm_TabFromSandbox_EditMode(t *testing.T) {
	pf := NewProjectForm()
	pf.LoadProject("test", config.Project{Path: "/tmp"})
	pf.focused = pfFieldSandbox

	// Tab from Sandbox → wraps to Name → edit mode skips → Path.
	pf.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	testutil.Equal(t, pf.focused, pfFieldPath)
}

func TestProjectForm_PasteSandbox_Ignored(t *testing.T) {
	pf := NewProjectForm()
	pf.focused = pfFieldSandbox
	paste := pf.PasteHandler()
	paste("garbage", func(p tview.Primitive) {})
	// sandboxIdx should remain at default (0 = Inherit).
	testutil.Equal(t, pf.sandboxIdx, 0)
}

func TestProjectForm_RoundTrip(t *testing.T) {
	// Load a project with sandbox enabled, verify it round-trips.
	pf := NewProjectForm()
	v := true
	pf.LoadProject("myproj", config.Project{
		Path:    "/code/myproj",
		Branch:  "main",
		Backend: "claude",
		Sandbox: config.ProjectSandboxConfig{Enabled: &v},
	})

	name, proj := pf.Result()
	testutil.Equal(t, name, "myproj")
	testutil.Equal(t, proj.Path, "/code/myproj")
	testutil.Equal(t, proj.Branch, "main")
	testutil.Equal(t, proj.Backend, "claude")
	if proj.Sandbox.Enabled == nil {
		t.Fatal("expected Sandbox.Enabled to be non-nil")
	}
	testutil.Equal(t, *proj.Sandbox.Enabled, true)
}

func TestProjectForm_RoundTrip_PreservesPathLists(t *testing.T) {
	// Verify DenyRead/ExtraWrite survive the load→result round-trip.
	pf := NewProjectForm()
	v := true
	pf.LoadProject("proj", config.Project{
		Path: "/code/proj",
		Sandbox: config.ProjectSandboxConfig{
			Enabled:    &v,
			DenyRead:   []string{"/secret", "/credentials"},
			ExtraWrite: []string{"/tmp/build"},
		},
	})

	_, proj := pf.Result()
	testutil.DeepEqual(t, proj.Sandbox.DenyRead, []string{"/secret", "/credentials"})
	testutil.DeepEqual(t, proj.Sandbox.ExtraWrite, []string{"/tmp/build"})
}
