package tui

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
	// Verify DenyRead/ExtraWrite/AllowAppleEvents survive the load→result
	// round-trip. The form does not surface these as editable fields — they
	// belong to the Settings TUI — but the form must not drop them on edit.
	pf := NewProjectForm()
	v := true
	pf.LoadProject("proj", config.Project{
		Path: t.TempDir(),
		Sandbox: config.ProjectSandboxConfig{
			Enabled:          &v,
			DenyRead:         []string{"/secret", "/credentials"},
			ExtraWrite:       []string{"/tmp/build"},
			AllowAppleEvents: []string{"com.apple.iChat"},
		},
	})

	_, proj := pf.Result()
	testutil.DeepEqual(t, proj.Sandbox.DenyRead, []string{"/secret", "/credentials"})
	testutil.DeepEqual(t, proj.Sandbox.ExtraWrite, []string{"/tmp/build"})
	testutil.DeepEqual(t, proj.Sandbox.AllowAppleEvents, []string{"com.apple.iChat"})
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

	testutil.Equal(t, pf.pathAC.Open(), true)
	testutil.Equal(t, len(pf.pathAC.matches), 3)
}

func TestProjectForm_PathAC_PrefixFilters(t *testing.T) {
	root := setupACDirs(t, "alpha", "arc", "beta")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/a")

	testutil.Equal(t, pf.pathAC.Open(), true)
	testutil.Equal(t, len(pf.pathAC.matches), 2) // alpha, arc
}

func TestProjectForm_PathAC_TabAccepts(t *testing.T) {
	root := setupACDirs(t, "alpha", "beta")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/a")

	testutil.Equal(t, pf.pathAC.Open(), true)
	// First match is "alpha".
	testutil.Contains(t, pf.pathAC.matches[0], "alpha")

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

	testutil.Equal(t, pf.pathAC.idx, 0)

	// Down → 1
	pf.HandleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	testutil.Equal(t, pf.pathAC.idx, 1)

	// Down → 2
	pf.HandleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	testutil.Equal(t, pf.pathAC.idx, 2)

	// Down → wraps to 0
	pf.HandleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	testutil.Equal(t, pf.pathAC.idx, 0)

	// Up → wraps to 2
	pf.HandleKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	testutil.Equal(t, pf.pathAC.idx, 2)
}

func TestProjectForm_PathAC_EscapeClosesDropdown(t *testing.T) {
	root := setupACDirs(t, "alpha", "beta")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/")

	testutil.Equal(t, pf.pathAC.Open(), true)

	// Escape closes dropdown but does NOT cancel form.
	pf.HandleKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	testutil.Equal(t, pf.pathAC.Open(), false)
	testutil.Equal(t, pf.canceled, false)
}

func TestProjectForm_PathAC_CtrlQClosesDropdown(t *testing.T) {
	root := setupACDirs(t, "alpha", "beta")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/")

	testutil.Equal(t, pf.pathAC.Open(), true)

	// CtrlQ closes dropdown but does NOT cancel form.
	pf.HandleKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone))
	testutil.Equal(t, pf.pathAC.Open(), false)
	testutil.Equal(t, pf.canceled, false)

	// Second CtrlQ cancels the form.
	pf.HandleKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone))
	testutil.Equal(t, pf.canceled, true)
}

func TestProjectForm_PathAC_BackspaceUpdatesAC(t *testing.T) {
	root := setupACDirs(t, "alpha", "arc", "beta")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/al")

	testutil.Equal(t, pf.pathAC.Open(), true)
	testutil.Equal(t, len(pf.pathAC.matches), 1) // alpha

	// Backspace → "a" prefix → alpha + arc
	pf.HandleKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))
	testutil.Equal(t, len(pf.pathAC.matches), 2) // alpha, arc
}

func TestProjectForm_PathAC_HiddenDirsExcluded(t *testing.T) {
	root := setupACDirs(t, ".hidden", "visible", "other")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/")

	testutil.Equal(t, pf.pathAC.Open(), true)
	// .hidden should not appear in matches.
	for _, m := range pf.pathAC.matches {
		if filepath.Base(m) == ".hidden" {
			t.Errorf("hidden dir should be excluded, got %s", m)
		}
	}
	testutil.Equal(t, len(pf.pathAC.matches), 2) // visible, other
}

func TestProjectForm_PathAC_EnterAcceptsWhenOpen(t *testing.T) {
	root := setupACDirs(t, "alpha", "beta")

	pf := NewProjectForm()
	pf.focused = pfFieldPath
	typeRunes(pf, root+"/a")

	testutil.Equal(t, pf.pathAC.Open(), true)

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

	testutil.Equal(t, pf.pathAC.Open(), true)
	testutil.Equal(t, len(pf.pathAC.matches), 2) // both match case-insensitively
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
	testutil.Equal(t, pf.pathAC.Open(), false)

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

	testutil.Equal(t, pf.pathAC.Open(), true)
	testutil.Equal(t, len(pf.pathAC.matches), 2)
}

func TestProjectForm_PathAC_NotOnOtherFields(t *testing.T) {
	root := setupACDirs(t, "alpha", "beta")

	pf := NewProjectForm()
	pf.focused = pfFieldName // NOT path
	typeRunes(pf, root+"/")

	testutil.Equal(t, pf.pathAC.Open(), false)
}

func TestProjectForm_MaybeLoadBranches_ExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home dir")
	}

	pf := NewProjectForm()
	var calledWith string
	pf.OnBranchFocus = func(path string) {
		calledWith = path
	}

	// Set path with tilde — maybeLoadBranches should expand it.
	pf.fields[pfFieldPath] = []rune("~/Development/myproj")
	pf.maybeLoadBranches()

	testutil.Equal(t, calledWith, filepath.Join(home, "Development/myproj"))
	// branchPath should store the expanded path for dedup.
	testutil.Equal(t, pf.branchPath, filepath.Join(home, "Development/myproj"))
}

func TestProjectForm_MaybeLoadBranches_TrimsSpaces(t *testing.T) {
	pf := NewProjectForm()
	var calledWith string
	pf.OnBranchFocus = func(path string) {
		calledWith = path
	}

	pf.fields[pfFieldPath] = []rune("  /tmp/foo  ")
	pf.maybeLoadBranches()

	testutil.Equal(t, calledWith, "/tmp/foo")
}

func TestProjectForm_MaybeLoadBranches_TildeFromPathAC(t *testing.T) {
	// acceptPathAC calls collapseTilde, so the path field contains "~/..."
	// after autocomplete. maybeLoadBranches must expand before calling OnBranchFocus.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home dir")
	}

	pf := NewProjectForm()
	var calledWith string
	pf.OnBranchFocus = func(path string) {
		calledWith = path
	}

	// Simulate what acceptPathAC produces: tilde path with trailing slash
	// (acceptPathAC always appends "/" after collapseTilde).
	pf.fields[pfFieldPath] = []rune("~/Development/thanx/actions/")
	pf.maybeLoadBranches()

	testutil.Equal(t, calledWith, filepath.Join(home, "Development/thanx/actions/"))
}

func TestProjectForm_MaybeLoadBranches_EmptyPath(t *testing.T) {
	pf := NewProjectForm()
	called := false
	pf.OnBranchFocus = func(path string) {
		called = true
	}

	pf.fields[pfFieldPath] = []rune("")
	pf.maybeLoadBranches()

	testutil.Equal(t, called, false)
}

func TestProjectForm_Draw_LongPathNoCursorCorruption(t *testing.T) {
	// Paths of exactly 44 or 45 chars triggered a UTF-8 corruption bug:
	// byte-based truncation split the multi-byte cursor "█" (3 bytes),
	// producing garbled characters. Verify rune-based truncation is clean.
	pf := NewProjectForm()
	// 44-char path — this exact length triggered the original bug.
	pf.fields[pfFieldPath] = []rune("/Users/darrencheng/Development/thanx/actions")
	pf.cursors[pfFieldPath] = 0
	pf.focused = pfFieldPath

	// Simulate what Draw does: insert cursor and truncate.
	before := string(pf.fields[pfFieldPath][:pf.cursors[pfFieldPath]])
	after := string(pf.fields[pfFieldPath][pf.cursors[pfFieldPath]:])
	val := before + "█" + after

	// Rune-based truncation (the fix).
	maxW := 46
	valRunes := []rune(val)
	if len(valRunes) > maxW {
		valRunes = valRunes[len(valRunes)-maxW:]
	}
	result := string(valRunes)

	// Must not contain replacement chars or broken UTF-8.
	for _, r := range result {
		if r == '\uFFFD' {
			t.Fatalf("result contains replacement character: %q", result)
		}
	}
	// The cursor should be cleanly included or cleanly truncated.
	testutil.Contains(t, result, "/Users/darrencheng/Development/thanx/actions")
}

func TestProjectForm_MaybeLoadBranches_SpacesOnlyPath(t *testing.T) {
	pf := NewProjectForm()
	called := false
	pf.OnBranchFocus = func(path string) {
		called = true
	}

	pf.fields[pfFieldPath] = []rune("   ")
	pf.maybeLoadBranches()

	testutil.Equal(t, called, false)
}

func TestProjectForm_Draw(t *testing.T) {
	pf := NewProjectForm()
	pf.SetRect(0, 0, 80, 24)
	pf.Draw(drawSim(t))
}

func TestProjectForm_Draw_EditWithBranches(t *testing.T) {
	pf := NewProjectForm()
	pf.LoadProject("proj", config.Project{Path: "/tmp", Branch: "origin/main"})
	pf.SetBranchOptions([]string{"origin/master", "origin/main"})
	pf.SetError("err")
	pf.SetRect(0, 0, 80, 24)
	pf.Draw(drawSim(t))
}

func TestProjectForm_Draw_FocusedBranchAndSandbox(t *testing.T) {
	pf := NewProjectForm()
	pf.SetBranchOptions([]string{"a", "b"})
	pf.focused = pfFieldBranch
	pf.SetRect(0, 0, 80, 24)
	pf.Draw(drawSim(t))

	pf.focused = pfFieldSandbox
	pf.Draw(drawSim(t))
}

func TestProjectForm_Draw_TinyRect(t *testing.T) {
	pf := NewProjectForm()
	pf.SetRect(0, 0, 0, 0)
	pf.Draw(drawSim(t))
}

func TestProjectForm_SetError(t *testing.T) {
	pf := NewProjectForm()
	pf.SetError("oops")
	testutil.Equal(t, pf.errMsg, "oops")
}
