package tui2

import (
	"os"
	"path/filepath"
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
		Path: t.TempDir(),
	})
	testutil.Equal(t, pf.sandboxIdx, 0) // nil → Inherit
}

func TestProjectForm_LoadProject_SandboxEnabled(t *testing.T) {
	pf := NewProjectForm()
	v := true
	pf.LoadProject("test", config.Project{
		Path:    t.TempDir(),
		Sandbox: config.ProjectSandboxConfig{Enabled: &v},
	})
	testutil.Equal(t, pf.sandboxIdx, 1) // true → Enabled
}

func TestProjectForm_LoadProject_SandboxDisabled(t *testing.T) {
	pf := NewProjectForm()
	v := false
	pf.LoadProject("test", config.Project{
		Path:    t.TempDir(),
		Sandbox: config.ProjectSandboxConfig{Enabled: &v},
	})
	testutil.Equal(t, pf.sandboxIdx, 2) // false → Disabled
}

func TestProjectForm_Result_SandboxInherit(t *testing.T) {
	pf := NewProjectForm()
	pf.fields[pfFieldName] = []rune("test")
	pf.fields[pfFieldPath] = []rune(t.TempDir())
	pf.sandboxIdx = 0 // Inherit

	_, proj := pf.Result()
	testutil.Nil(t, proj.Sandbox.Enabled)
}

func TestProjectForm_Result_SandboxEnabled(t *testing.T) {
	pf := NewProjectForm()
	pf.fields[pfFieldName] = []rune("test")
	pf.fields[pfFieldPath] = []rune(t.TempDir())
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
	pf.fields[pfFieldPath] = []rune(t.TempDir())
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
	pf.LoadProject("test", config.Project{Path: t.TempDir()})
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
	dir := t.TempDir()
	pf.LoadProject("myproj", config.Project{
		Path:    dir,
		Branch:  "main",
		Backend: "claude",
		Sandbox: config.ProjectSandboxConfig{Enabled: &v},
	})

	name, proj := pf.Result()
	testutil.Equal(t, name, "myproj")
	testutil.Equal(t, proj.Path, dir)
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
		Path: t.TempDir(),
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

// --- Path autocomplete tests ---

// setupACDirs creates a temp directory with subdirectories for autocomplete tests.
// Returns the temp root path (absolute, symlink-resolved).
func setupACDirs(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	// Resolve symlinks so macOS /var → /private/var doesn't cause mismatches.
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(root, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func typeRunes(pf *ProjectForm, s string) {
	for _, r := range s {
		pf.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
}

func TestProjectForm_PathAC_TypingOpensDropdown(t *testing.T) {
	root := setupACDirs(t, "alpha", "beta", "gamma")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/")

	testutil.Equal(t, pf.pathACOpen, true)
	testutil.Equal(t, len(pf.pathACMatches), 3)
}

func TestProjectForm_PathAC_PrefixFilters(t *testing.T) {
	root := setupACDirs(t, "alpha", "arc", "beta")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/a")

	testutil.Equal(t, pf.pathACOpen, true)
	testutil.Equal(t, len(pf.pathACMatches), 2) // alpha, arc
}

func TestProjectForm_PathAC_TabAccepts(t *testing.T) {
	root := setupACDirs(t, "alpha", "beta")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/a")

	testutil.Equal(t, pf.pathACOpen, true)
	// First match is "alpha".
	testutil.Contains(t, pf.pathACMatches[0], "alpha")

	// Tab accepts.
	pf.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	got := string(pf.fields[pfFieldPath])
	testutil.Contains(t, got, "alpha/")
}

func TestProjectForm_PathAC_DownUpNavigates(t *testing.T) {
	root := setupACDirs(t, "aaa", "bbb", "ccc")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/")

	testutil.Equal(t, pf.pathACIdx, 0)

	// Down → 1
	pf.HandleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	testutil.Equal(t, pf.pathACIdx, 1)

	// Down → 2
	pf.HandleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	testutil.Equal(t, pf.pathACIdx, 2)

	// Down → wraps to 0
	pf.HandleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	testutil.Equal(t, pf.pathACIdx, 0)

	// Up → wraps to 2
	pf.HandleKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	testutil.Equal(t, pf.pathACIdx, 2)
}

func TestProjectForm_PathAC_EscapeClosesDropdown(t *testing.T) {
	root := setupACDirs(t, "alpha", "beta")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/")

	testutil.Equal(t, pf.pathACOpen, true)

	// Escape closes dropdown but does NOT cancel form.
	pf.HandleKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	testutil.Equal(t, pf.pathACOpen, false)
	testutil.Equal(t, pf.canceled, false)
}

func TestProjectForm_PathAC_BackspaceUpdatesAC(t *testing.T) {
	root := setupACDirs(t, "alpha", "arc", "beta")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/al")

	testutil.Equal(t, pf.pathACOpen, true)
	testutil.Equal(t, len(pf.pathACMatches), 1) // alpha

	// Backspace → "a" prefix → alpha + arc
	pf.HandleKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))
	testutil.Equal(t, len(pf.pathACMatches), 2) // alpha, arc
}

func TestProjectForm_PathAC_HiddenDirsExcluded(t *testing.T) {
	root := setupACDirs(t, ".hidden", "visible", "other")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/")

	testutil.Equal(t, pf.pathACOpen, true)
	// .hidden should not appear in matches.
	for _, m := range pf.pathACMatches {
		if filepath.Base(m) == ".hidden" {
			t.Errorf("hidden dir should be excluded, got %s", m)
		}
	}
	testutil.Equal(t, len(pf.pathACMatches), 2) // visible, other
}

func TestProjectForm_PathAC_EnterAcceptsWhenOpen(t *testing.T) {
	root := setupACDirs(t, "alpha", "beta")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/a")

	testutil.Equal(t, pf.pathACOpen, true)

	// Enter accepts the autocomplete instead of advancing fields.
	pf.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	testutil.Equal(t, pf.focused, pfFieldPath) // still on path field
	testutil.Contains(t, string(pf.fields[pfFieldPath]), "alpha/")
}

func TestProjectForm_PathAC_CaseInsensitive(t *testing.T) {
	root := setupACDirs(t, "MyProject", "mylib")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/my")

	testutil.Equal(t, pf.pathACOpen, true)
	testutil.Equal(t, len(pf.pathACMatches), 2) // both match case-insensitively
}

func TestProjectForm_Result_ExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home dir")
	}

	pf := NewProjectForm()
	pf.fields[pfFieldName] = []rune("test")
	pf.fields[pfFieldPath] = []rune("~/Development/myproj")

	_, proj := pf.Result()
	testutil.Equal(t, proj.Path, filepath.Join(home, "Development/myproj"))
}

func TestProjectForm_Result_TrimSpaces(t *testing.T) {
	pf := NewProjectForm()
	pf.fields[pfFieldName] = []rune("test")
	pf.fields[pfFieldPath] = []rune("  /tmp/foo  ")

	_, proj := pf.Result()
	testutil.Equal(t, proj.Path, "/tmp/foo")
}

func TestProjectForm_PathAC_TabOnClosedTriggersAC(t *testing.T) {
	root := setupACDirs(t, "alpha", "beta")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/")

	// Close AC first.
	pf.HandleKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	testutil.Equal(t, pf.pathACOpen, false)

	// Tab triggers + accepts.
	pf.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	got := string(pf.fields[pfFieldPath])
	testutil.Contains(t, got, "alpha/")
}

func TestProjectForm_PathAC_PasteTriggersAC(t *testing.T) {
	root := setupACDirs(t, "alpha", "beta")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	paste := pf.PasteHandler()
	paste(root+"/", func(p tview.Primitive) {})

	testutil.Equal(t, pf.pathACOpen, true)
	testutil.Equal(t, len(pf.pathACMatches), 2)
}

func TestProjectForm_PathAC_NotOnOtherFields(t *testing.T) {
	root := setupACDirs(t, "alpha", "beta")

	pf := NewProjectForm()
	pf.focused = pfFieldName // NOT path
	typeRunes(pf, root+"/")

	testutil.Equal(t, pf.pathACOpen, false)
}
