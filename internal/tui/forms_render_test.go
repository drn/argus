package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/uxlog"
)

// --- DirAC Draw ---

func TestDirAC_Draw_WithSelection(t *testing.T) {
	root := setupACDirs(t, "alpha", "beta", "gamma", "delta", "epsilon", "zeta")
	var ac dirAC
	ac.Update(root + "/")
	ac.idx = 3 // forces scroll

	sim := drawSim(t)
	rows := ac.Draw(sim, 0, 0, 40, 3)
	testutil.Equal(t, rows, 3)
}

func TestDirAC_Draw_Closed(t *testing.T) {
	var ac dirAC
	rows := ac.Draw(drawSim(t), 0, 0, 40, 8)
	testutil.Equal(t, rows, 0)
}

// --- handleFilePanelKey: 'f', 'o', 'e', 't' branches ---

func TestApp_HandleFilePanelKey_FOAndT(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.mode = modeAgent
	app.agentFocus = focusFiles
	app.filePanel.SetRect(0, 0, 40, 20)
	app.filePanel.SetFiles([]gitutil.ChangedFile{{Status: "M", Path: "a.go"}})
	app.worktreeDir = t.TempDir()

	var systemArgs [][]string
	var editorArgs [][]string
	var termHits int
	oldSystem := systemOpener
	oldEditor := editorOpener
	oldTerm := terminalOpener
	t.Cleanup(func() { systemOpener = oldSystem; editorOpener = oldEditor; terminalOpener = oldTerm })
	systemOpener = func(args ...string) error { systemArgs = append(systemArgs, args); return nil }
	editorOpener = func(wt, path string) error { editorArgs = append(editorArgs, []string{wt, path}); return nil }
	terminalOpener = func(string) error { termHits++; return nil }

	wantFile := app.worktreeDir + "/a.go"

	// 'f' reveals the selected file in Finder (`open -R <path>`).
	got := app.handleFilePanelKey(tcell.NewEventKey(tcell.KeyRune, 'f', 0))
	testutil.Nil(t, got)
	testutil.DeepEqual(t, systemArgs[len(systemArgs)-1], []string{"-R", wantFile})

	// 'o' opens the selected file with its default handler (`open <path>`).
	got = app.handleFilePanelKey(tcell.NewEventKey(tcell.KeyRune, 'o', 0))
	testutil.Nil(t, got)
	testutil.DeepEqual(t, systemArgs[len(systemArgs)-1], []string{wantFile})

	// 'e' opens the selected file in the editor (tmux + $EDITOR).
	got = app.handleFilePanelKey(tcell.NewEventKey(tcell.KeyRune, 'e', 0))
	testutil.Nil(t, got)
	testutil.DeepEqual(t, editorArgs[len(editorArgs)-1], []string{app.worktreeDir, "a.go"})

	// 't' opens a terminal in the worktree.
	got = app.handleFilePanelKey(tcell.NewEventKey(tcell.KeyRune, 't', 0))
	testutil.Nil(t, got)
	testutil.Equal(t, termHits, 1)

	// 'j' navigates down.
	got = app.handleFilePanelKey(tcell.NewEventKey(tcell.KeyRune, 'j', 0))
	testutil.Nil(t, got)

	// 'k' navigates up.
	got = app.handleFilePanelKey(tcell.NewEventKey(tcell.KeyRune, 'k', 0))
	testutil.Nil(t, got)

	// Enter opens diff.
	got = app.handleFilePanelKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Nil(t, got)
}

// openInEditor logs (does not panic) when the opener fails. Exercises the
// error branch that the success-path key test cannot reach.
func TestApp_OpenInEditor_OpenerError(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ux.log")
	if err := uxlog.Init(logPath); err != nil {
		t.Fatalf("uxlog.Init: %v", err)
	}
	defer uxlog.Close()

	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.filePanel.SetRect(0, 0, 40, 20)
	app.filePanel.SetFiles([]gitutil.ChangedFile{{Status: "M", Path: "a.go"}})
	app.worktreeDir = t.TempDir()

	old := editorOpener
	t.Cleanup(func() { editorOpener = old })
	editorOpener = func(string, string) error { return errors.New("boom") }

	app.openInEditor() // must not panic; logs the failure

	data, err := os.ReadFile(logPath)
	testutil.NoError(t, err)
	testutil.Contains(t, string(data), "open in editor failed")
}

// --- handleDiffKey: scroll with j/k/s/q ---

func TestApp_HandleDiffKey_Scroll(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.mode = modeAgent
	app.agentPane.EnterDiffMode("+1\n-2", "a.go")

	// j scrolls down.
	app.handleDiffKey(tcell.NewEventKey(tcell.KeyRune, 'j', 0))
	// k scrolls up.
	app.handleDiffKey(tcell.NewEventKey(tcell.KeyRune, 'k', 0))
	// s toggles split.
	app.handleDiffKey(tcell.NewEventKey(tcell.KeyRune, 's', 0))
	// q exits diff.
	app.handleDiffKey(tcell.NewEventKey(tcell.KeyRune, 'q', 0))
	testutil.Equal(t, app.agentPane.InDiffMode(), false)
}

// f/o/e/t must still open the selected file while a diff is being displayed
// (BUG: these used to be swallowed by handleDiffKey once Enter entered diff
// mode, since it only resolved CtxDiff, never CtxFilePnl).
func TestApp_HandleDiffKey_FileHotkeysStillWork(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	app.mode = modeAgent
	app.filePanel.SetRect(0, 0, 40, 20)
	app.filePanel.SetFiles([]gitutil.ChangedFile{{Status: "M", Path: "a.go"}})
	app.worktreeDir = t.TempDir()
	app.agentPane.EnterDiffMode("+1\n-2", "a.go")

	var systemArgs [][]string
	var editorArgs [][]string
	var termHits int
	oldSystem := systemOpener
	oldEditor := editorOpener
	oldTerm := terminalOpener
	t.Cleanup(func() { systemOpener = oldSystem; editorOpener = oldEditor; terminalOpener = oldTerm })
	systemOpener = func(args ...string) error { systemArgs = append(systemArgs, args); return nil }
	editorOpener = func(wt, path string) error { editorArgs = append(editorArgs, []string{wt, path}); return nil }
	terminalOpener = func(string) error { termHits++; return nil }

	wantFile := app.worktreeDir + "/a.go"

	got := app.handleDiffKey(tcell.NewEventKey(tcell.KeyRune, 'f', 0))
	testutil.Nil(t, got)
	testutil.DeepEqual(t, systemArgs[len(systemArgs)-1], []string{"-R", wantFile})

	got = app.handleDiffKey(tcell.NewEventKey(tcell.KeyRune, 'o', 0))
	testutil.Nil(t, got)
	testutil.DeepEqual(t, systemArgs[len(systemArgs)-1], []string{wantFile})

	got = app.handleDiffKey(tcell.NewEventKey(tcell.KeyRune, 'e', 0))
	testutil.Nil(t, got)
	testutil.DeepEqual(t, editorArgs[len(editorArgs)-1], []string{app.worktreeDir, "a.go"})

	got = app.handleDiffKey(tcell.NewEventKey(tcell.KeyRune, 't', 0))
	testutil.Nil(t, got)
	testutil.Equal(t, termHits, 1)

	// Still in diff mode — these hotkeys don't exit it.
	testutil.Equal(t, app.agentPane.InDiffMode(), true)
}

func TestApp_HandleDiffKey_PgUpPgDown(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.mode = modeAgent
	app.agentPane.EnterDiffMode("+1\n-2", "a.go")

	app.handleDiffKey(tcell.NewEventKey(tcell.KeyPgUp, 0, 0))
	app.handleDiffKey(tcell.NewEventKey(tcell.KeyPgDn, 0, 0))
}

// --- handleAgentKey: scrollback keys ---

func TestApp_HandleAgentKey_ScrollbackKeys(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.mode = modeAgent
	app.agentState.Reset("t1", "t")

	tests := []tcell.Key{
		tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn, tcell.KeyEnd,
	}
	for _, k := range tests {
		ev := tcell.NewEventKey(k, 0, tcell.ModShift)
		app.handleAgentKey(ev)
	}
}

func TestApp_HandleAgentKey_CmdArrows(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	now := time.Now()
	d.Add(&model.Task{ID: "a", Project: "p", Name: "a", Status: model.StatusPending, CreatedAt: now})
	d.Add(&model.Task{ID: "b", Project: "p", Name: "b", Status: model.StatusPending, CreatedAt: now.Add(time.Second)})
	app.refreshTasks()

	app.mode = modeAgent
	app.agentState.Reset("a", "a")

	// Cmd+Down navigates next.
	app.handleAgentKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModCtrl|tcell.ModAlt))
	testutil.Equal(t, app.agentState.TaskID, "b")

	// Cmd+Up navigates back.
	app.handleAgentKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModCtrl|tcell.ModAlt))
	testutil.Equal(t, app.agentState.TaskID, "a")

	// In split (un-zoomed) mode, Cmd+Left/Right switch panes.
	app.clearAgentZen()
	app.agentFocus = focusFiles
	app.handleAgentKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModCtrl|tcell.ModAlt))
	testutil.Equal(t, app.agentFocus, focusTerminal)
	app.handleAgentKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl|tcell.ModAlt))
	testutil.Equal(t, app.agentFocus, focusFiles)

	// In zoomed (single-pane) mode the side panels are hidden, so Cmd+Left/Right
	// must NOT switch panes — focus stays put and the key is still consumed.
	app.setAgentZen() // snaps focus back to the terminal
	testutil.Equal(t, app.agentFocus, focusTerminal)
	if ev := app.handleAgentKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl|tcell.ModAlt)); ev != nil {
		t.Fatalf("Cmd+Right should be consumed when zoomed, got %v", ev)
	}
	testutil.Equal(t, app.agentFocus, focusTerminal)
	if ev := app.handleAgentKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModCtrl|tcell.ModAlt)); ev != nil {
		t.Fatalf("Cmd+Left should be consumed when zoomed, got %v", ev)
	}
	testutil.Equal(t, app.agentFocus, focusTerminal)
}

func TestApp_HandleAgentKey_EnterRestart(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	task := &model.Task{ID: "t1", Project: "p", Name: "n", Status: model.StatusInReview, CreatedAt: time.Now()}
	d.Add(task)
	app.refreshTasks()

	app.mode = modeAgent
	app.agentState.Reset("t1", "n")

	// Enter on dead session attempts restart (which fails — no worktree).
	app.handleAgentKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
}

func TestApp_HandleAgentKey_ODeadSession(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.mode = modeAgent
	app.agentState.Reset("t1", "n")

	// 'o' on a dead session with terminal focus falls through to the PTY
	// forward path without panicking.
	app.handleAgentKey(tcell.NewEventKey(tcell.KeyRune, 'o', 0))
}

func TestApp_HandleAgentKey_EscapeFromFiles(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.mode = modeAgent
	app.agentFocus = focusFiles
	app.agentState.Reset("t1", "n")

	got := app.handleAgentKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Nil(t, got)
	testutil.Equal(t, app.agentFocus, focusTerminal)
}

func TestApp_HandleAgentKey_EscapeFromDiff(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.mode = modeAgent
	app.agentState.Reset("t1", "n")
	app.agentPane.EnterDiffMode("+a\n-b", "a.go")

	got := app.handleAgentKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Nil(t, got)
	testutil.Equal(t, app.agentPane.InDiffMode(), false)
}

// --- exitAgentView paths via Ctrl+Q ---

func TestApp_HandleGlobalKey_CtrlQ_ExitsDiffMode(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.mode = modeAgent
	app.agentState.Reset("t1", "n")
	app.agentPane.EnterDiffMode("+a", "a.go")

	got := app.handleGlobalKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, 0))
	testutil.Nil(t, got)
	testutil.Equal(t, app.agentPane.InDiffMode(), false)
}

func TestApp_HandleGlobalKey_CtrlQ_ExitsFilePanel(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.mode = modeAgent
	app.agentState.Reset("t1", "n")
	app.agentFocus = focusFiles

	got := app.handleGlobalKey(tcell.NewEventKey(tcell.KeyCtrlQ, 0, 0))
	testutil.Nil(t, got)
	testutil.Equal(t, app.agentFocus, focusTerminal)
}

func TestApp_HandleGlobalKey_QuitsTaskList(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	// 'q' on task list quits — but app.tapp.Stop is safe even without Run.
	got := app.handleGlobalKey(tcell.NewEventKey(tcell.KeyRune, 'q', 0))
	testutil.Nil(t, got)
}

// --- restoring agent panel scroll on key ---

// --- openFileDiff with file selected ---

func TestApp_OpenFileDiff_NoSelection(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.openFileDiff() // no file, no worktree → no-op
}

// --- fetchGitStatus / fetchTaskGitStatus / fetchDirChildren ---

func TestApp_FetchGitStatus(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	_, stop := wireApp(t, app)
	t.Cleanup(stop)

	dir := t.TempDir()
	// gitutil.FetchGitStatus on a non-repo dir returns whatever — function still needs to QueueUpdateDraw.
	done := make(chan struct{})
	go func() {
		app.fetchGitStatus("nonexistent-task", dir)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(uiTimeout):
		t.Fatal("fetchGitStatus blocked")
	}
}

func TestApp_FetchTaskGitStatus(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	_, stop := wireApp(t, app)
	t.Cleanup(stop)

	dir := t.TempDir()
	done := make(chan struct{})
	go func() {
		app.fetchTaskGitStatus("any-task", dir)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(uiTimeout):
		t.Fatal("fetchTaskGitStatus blocked")
	}
}

func TestApp_FetchDirChildren(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	_, stop := wireApp(t, app)
	t.Cleanup(stop)

	app.worktreeDir = t.TempDir()
	app.agentState.Reset("t1", "n")

	done := make(chan struct{})
	go func() {
		app.fetchDirChildren("any/path")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(uiTimeout):
		t.Fatal("fetchDirChildren blocked")
	}
}

// (Tests for the removed maybeKickNarrowRerender / reapStaleNarrowRestart
// helpers were dropped here when master moved that responsibility into
// the daemon's KickRerender path. See TestRunner_KickRerender_* in
// internal/agent/runner_test.go.)

// --- isTaskRunning ---

func TestApp_IsTaskRunning(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	app.runningIDs = []string{"a", "b"}
	testutil.Equal(t, app.isTaskRunning("a"), true)
	testutil.Equal(t, app.isTaskRunning("c"), false)
}

// --- More renametask tests for completeness ---

func TestRenameTaskForm_HomeEnd(t *testing.T) {
	rf := NewRenameTaskForm("hello")
	rf.HandleKey(tcell.NewEventKey(tcell.KeyHome, 0, 0))
	testutil.Equal(t, rf.cursor, 0)
	rf.HandleKey(tcell.NewEventKey(tcell.KeyEnd, 0, 0))
	testutil.Equal(t, rf.cursor, 5)
}

func TestRenameTaskForm_LeftRightAtBoundaries(t *testing.T) {
	rf := NewRenameTaskForm("ab")
	rf.cursor = 0
	rf.HandleKey(tcell.NewEventKey(tcell.KeyLeft, 0, 0))
	testutil.Equal(t, rf.cursor, 0)
	rf.cursor = 2
	rf.HandleKey(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	testutil.Equal(t, rf.cursor, 2)
}

// --- forkmodal selectedProject case-insensitive ---

func TestForkTaskModal_SelectedProject_CaseInsensitive(t *testing.T) {
	task := &model.Task{Name: "t", Project: "alpha"}
	m := NewForkTaskModal(task, map[string]config.Project{
		"Alpha": {}, "Beta": {},
	})
	// Set input to lowercase.
	m.projInput = []rune("alpha")
	got := m.SelectedProject()
	testutil.Equal(t, got, "Alpha")
}

func TestForkTaskModal_SelectedProject_NotFound(t *testing.T) {
	task := &model.Task{Name: "t", Project: ""}
	m := NewForkTaskModal(task, map[string]config.Project{"a": {}})
	m.projInput = []rune("xyz")
	got := m.SelectedProject()
	testutil.Equal(t, got, "")
}

// --- ReadGitDiff with an actual git repo ---

func TestReadGitDiff_ValidRepo(t *testing.T) {
	// Force HOME to temp so IsWorktreeSubdir lookups work.
	t.Setenv("HOME", t.TempDir())
	worktreeRoot := filepath.Join(os.Getenv("HOME"), ".argus", "worktrees", "p", "task")
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// Not a git repo so git diff fails — expect "".
	got := readGitDiff(worktreeRoot)
	testutil.Equal(t, got, "")
}

// --- ReadSessionLogTail with a real file ---

func TestReadSessionLogTail_RealFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logPath := agent.SessionLogPath("test-task")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("hello world output"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readSessionLogTail("test-task")
	if !strings.Contains(got, "hello world") {
		t.Errorf("got %q", got)
	}
}

func TestReadSessionLogTail_LargeFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logPath := agent.SessionLogPath("big-task")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Write more than maxOutputBytes.
	big := make([]byte, maxOutputBytes+1024)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(logPath, big, 0o644); err != nil {
		t.Fatal(err)
	}
	got := readSessionLogTail("big-task")
	if len(got) == 0 {
		t.Error("expected non-empty result")
	}
}

// --- newtaskform Draw covers drawAutocomplete via project AC open ---

func TestNewTaskForm_Draw_WithACDropdowns(t *testing.T) {
	dir := setupACDirs(t, "alpha-proj", "beta-proj")
	_ = dir

	f := NewNewTaskForm(
		map[string]config.Project{"alpha-proj": {}, "beta-proj": {}},
		"alpha-proj",
		map[string]config.Backend{"b1": {}},
		"b1",
	)
	// Open project AC.
	f.focused = pfFieldName // start on first field; just want Draw to not panic
	f.SetRect(0, 0, 100, 30)
	f.Draw(drawSim(t))
}

// --- LinkPickerModal: scroll past visible ---

func TestLinkPickerModal_LongList_Scroll(t *testing.T) {
	links := make([]Link, 30)
	for i := range links {
		links[i] = Link{Label: "L", URL: "https://x"}
	}
	m := NewLinkPickerModal(links)
	m.cursor = 25 // forces scroll
	m.SetRect(0, 0, 80, 20)
	m.Draw(drawSim(t))
}

// --- FuzzyLinkPickerModal: scroll ---

func TestFuzzyLinkPickerModal_LongList_Scroll(t *testing.T) {
	links := make([]Link, 50)
	for i := range links {
		links[i] = Link{Label: "L", URL: "https://x"}
	}
	m := NewFuzzyLinkPickerModal(links)
	m.cursor = 30
	m.SetRect(0, 0, 80, 12)
	m.Draw(drawSim(t))
}

// --- handleEditSourceKey: Down/Up consumed but no-op ---

func TestSettings_HandleEditSourceKey_DownUp(t *testing.T) {
	sv := makeSettings(t)
	sv.editingSource = true
	got := sv.handleEditSourceKey(tcell.NewEventKey(tcell.KeyDown, 0, 0))
	testutil.Equal(t, got, true)
	got = sv.handleEditSourceKey(tcell.NewEventKey(tcell.KeyUp, 0, 0))
	testutil.Equal(t, got, true)
}

func TestSettings_HandleEditSourceKey_BackspaceAtZero(t *testing.T) {
	sv := makeSettings(t)
	sv.editingSource = true
	sv.editSourceBuf = ""
	got := sv.handleEditSourceKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0))
	testutil.Equal(t, got, true)
	testutil.Equal(t, sv.editSourceBuf, "")
}

func TestSettings_HandleEditSourceKey_Unknown(t *testing.T) {
	sv := makeSettings(t)
	sv.editingSource = true
	got := sv.handleEditSourceKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	testutil.Equal(t, got, false)
}

// --- ProjectForm: branch focus loads ---

func TestProjectForm_Tab_LoadsBranchesEvenWithCallback(t *testing.T) {
	pf := NewProjectForm()
	loaded := false
	pf.OnBranchFocus = func(path string) { loaded = true }
	pf.fields[pfFieldPath] = []rune("/repo")
	pf.focused = pfFieldPath
	pf.HandleKey(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	testutil.Equal(t, loaded, true)
}

// --- ProjectForm: backtab from name in edit mode wraps to sandbox ---

func TestProjectForm_BacktabEditModeFromPath(t *testing.T) {
	pf := NewProjectForm()
	pf.LoadProject("test", config.Project{Path: "/p"})
	// Edit mode focused starts at path. Backtab → name (skipped) → sandbox.
	pf.HandleKey(tcell.NewEventKey(tcell.KeyBacktab, 0, 0))
	testutil.Equal(t, pf.focused, pfFieldSandbox)
}

// --- ProjectForm: editing name field is read-only in edit mode ---

func TestProjectForm_EditModeNameReadOnly(t *testing.T) {
	pf := NewProjectForm()
	pf.LoadProject("locked", config.Project{Path: "/p"})
	// Force focus on name.
	pf.focused = pfFieldName
	pf.HandleKey(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0))
	testutil.Equal(t, string(pf.fields[pfFieldName]), "locked")
	pf.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'X', 0))
	testutil.Equal(t, string(pf.fields[pfFieldName]), "locked")
}

// --- ScheduleForm: edit-mode without ID match in projects ---

func TestScheduleForm_LoadSchedule_UnknownProjectAndBackend(t *testing.T) {
	sf := NewScheduleForm([]string{"a"}, []string{"b1"})
	s := &model.ScheduledTask{
		ID: "id", Name: "n", Project: "unknown", Backend: "unknown-be",
	}
	sf.LoadSchedule(s)
	// Should default to 0 for both.
	testutil.Equal(t, sf.projectIdx, 0)
	testutil.Equal(t, sf.backendIdx, 0)
}

// --- DeleteProject path, runScheduleNow with valid schedule ---

func TestApp_DeleteProject_Direct(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("p", config.Project{Path: t.TempDir()})

	app.deleteProject("p") // opens confirm modal
	testutil.Equal(t, app.mode, modeConfirmDeleteProject)
}

func TestApp_RunScheduleNow_Valid(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	d.SetProject("p", config.Project{Path: t.TempDir()})
	s := &model.ScheduledTask{
		ID: "id", Name: "x", Project: "p", Prompt: "x", Schedule: "@daily", Enabled: true,
	}
	d.AddSchedule(s)

	_, stop := wireApp(t, app)
	t.Cleanup(stop)

	done := make(chan struct{})
	go func() {
		app.runScheduleNow("id")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(uiTimeout):
		t.Fatal("runScheduleNow blocked")
	}
}

// --- onTick: just call to exercise it once with no-op ---

func TestApp_OnTick(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	_, stop := wireApp(t, app)
	t.Cleanup(stop)

	done := make(chan struct{})
	go func() {
		app.onTick()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(uiTimeout):
		t.Fatal("onTick blocked")
	}
}
