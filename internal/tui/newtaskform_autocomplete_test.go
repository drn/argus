package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/skills"
	"github.com/drn/argus/internal/testutil"
)

// --- NewTaskForm Draw with all autocomplete dropdowns open ---

func TestNewTaskForm_Draw_WithSkillAC(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
	// Force autocomplete open with synthetic skills.
	f.acOpen = true
	f.acMatches = []skills.SkillItem{
		{Name: "review", Description: "Run a review"},
		{Name: "commit", Description: "Make a commit"},
	}
	f.SetRect(0, 0, 100, 30)
	f.Draw(drawSim(t))
}

func TestNewTaskForm_Draw_WithSkillAC_LongList(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
	// Force autocomplete with > acMaxVisible items.
	for i := 0; i < 30; i++ {
		f.acMatches = append(f.acMatches, skills.SkillItem{
			Name:        "skill-" + itoa(i),
			Description: "Description that is potentially long enough to truncate",
		})
	}
	f.acOpen = true
	f.acIdx = 15
	f.acScroll = 10
	f.SetRect(0, 0, 100, 30)
	f.Draw(drawSim(t))
}

func TestNewTaskForm_Draw_WithProjectAC(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"alpha": {}, "beta": {}, "gamma": {}}, "alpha",
		map[string]config.Backend{"b": {}}, "b",
	)
	// Open project AC.
	f.focused = ntFieldProject
	f.projInput = []rune("a")
	f.projCursorPos = 1
	f.updateProjectAC()
	f.SetRect(0, 0, 100, 30)
	f.Draw(drawSim(t))
}

func TestNewTaskForm_Draw_WithProjectAC_LongList(t *testing.T) {
	projects := make(map[string]config.Project)
	for i := 0; i < 30; i++ {
		projects["proj-"+itoa(i)] = config.Project{}
	}
	f := NewNewTaskForm(projects, "proj-0", map[string]config.Backend{"b": {}}, "b")
	f.focused = ntFieldProject
	f.projInput = []rune("p")
	f.projCursorPos = 1
	f.updateProjectAC()
	f.projACIdx = 15
	f.projACScroll = 10
	f.SetRect(0, 0, 100, 30)
	f.Draw(drawSim(t))
}

func TestNewTaskForm_Draw_WithBranchAC(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
	f.SetBranchOptions([]string{"origin/master", "origin/main", "origin/dev"})
	f.focused = ntFieldBranch
	f.branchInput = []rune("o")
	f.branchCursorPos = 1
	f.updateBranchAC()
	f.SetRect(0, 0, 100, 30)
	f.Draw(drawSim(t))
}

func TestNewTaskForm_Draw_WithBranchAC_LongList(t *testing.T) {
	branches := make([]string, 30)
	for i := range branches {
		branches[i] = "origin/branch-" + itoa(i)
	}
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
	f.SetBranchOptions(branches)
	f.focused = ntFieldBranch
	f.branchInput = []rune("o")
	f.branchCursorPos = 1
	f.updateBranchAC()
	f.branchACIdx = 15
	f.branchACScroll = 10
	f.SetRect(0, 0, 100, 30)
	f.Draw(drawSim(t))
}

// --- NewTaskForm: branch AC navigation ---

func TestNewTaskForm_BranchAC_DownAndUp(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b": {}}, "b",
	)
	f.SetBranchOptions([]string{"origin/master", "origin/main", "origin/dev"})
	f.focused = ntFieldBranch
	f.branchInput = []rune("o")
	f.branchCursorPos = 1
	f.updateBranchAC()

	prev := f.branchACIdx
	f.branchACMoveDown()
	if f.branchACIdx == prev {
		t.Error("branchACMoveDown should change idx")
	}
	f.branchACMoveUp()
	testutil.Equal(t, f.branchACIdx, prev)
	// At top, up wraps.
	f.branchACMoveUp()
}

func TestNewTaskForm_ProjectAC_DownAndUp(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"a": {}, "b": {}, "c": {}}, "a",
		map[string]config.Backend{"b": {}}, "b",
	)
	f.focused = ntFieldProject
	f.projInput = nil
	f.projCursorPos = 0
	f.updateProjectAC()

	prev := f.projACIdx
	f.projACMoveDown()
	if f.projACIdx == prev {
		t.Error("projACMoveDown should change idx")
	}
	f.projACMoveUp()
	testutil.Equal(t, f.projACIdx, prev)
	f.projACMoveUp()
}

// --- handleSelectorKey path coverage ---

func TestNewTaskForm_BackendSelector_LeftRight(t *testing.T) {
	f := NewNewTaskForm(
		map[string]config.Project{"p": {}}, "p",
		map[string]config.Backend{"b1": {}, "b2": {}}, "b1",
	)
	f.focused = ntFieldBackend
	prev := f.backendIdx
	handler := f.InputHandler()
	handler(tcell.NewEventKey(tcell.KeyRight, 0, 0), nil)
	if f.backendIdx == prev {
		t.Error("right should change backend")
	}
}

// --- More agent key paths ---

func TestApp_HandleAgentKey_AltLeftRightOnTerminalFocus(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.mode = modeAgent
	app.agentState.Reset("t1", "n")
	app.agentFocus = focusTerminal

	// Cmd+Left when already at terminal — no change.
	app.handleAgentKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModCtrl|tcell.ModAlt))
	testutil.Equal(t, app.agentFocus, focusTerminal)
}

func TestApp_HandleAgentKey_CtrlPOpensPR(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.mode = modeAgent
	app.agentState.Reset("t1", "n")
	app.worktreeDir = t.TempDir()

	var gotDir string
	orig := prOpener
	prOpener = func(dir string) error {
		gotDir = dir
		return nil
	}
	t.Cleanup(func() { prOpener = orig })

	if ev := app.handleAgentKey(tcell.NewEventKey(tcell.KeyCtrlP, 0, 0)); ev != nil {
		t.Fatal("ctrl+p should be consumed")
	}
	testutil.Equal(t, gotDir, app.worktreeDir)
}

func TestApp_HandleAgentKey_CtrlPNoWorktree(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.mode = modeAgent
	app.agentState.Reset("t1", "n")
	// worktreeDir intentionally empty.

	called := false
	orig := prOpener
	prOpener = func(string) error { called = true; return nil }
	t.Cleanup(func() { prOpener = orig })

	if ev := app.handleAgentKey(tcell.NewEventKey(tcell.KeyCtrlP, 0, 0)); ev != nil {
		t.Fatal("ctrl+p should be consumed even with empty worktreeDir")
	}
	if called {
		t.Fatal("prOpener should not run when worktreeDir is empty")
	}
}

// --- handleGlobalKey paths ---
