package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

// seedClaudeSession writes a JSONL session file for worktree under the
// HOME-relative ~/.claude/projects directory and returns the session ID.
func seedClaudeSession(t *testing.T, home, worktree, id, title string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", claudesession.EncodeProjectDir(worktree))
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	line := `{"type":"ai-title","aiTitle":"` + title + `","sessionId":"` + id + `"}` + "\n" +
		`{"type":"user","timestamp":"2026-06-04T10:00:00.000Z","sessionId":"` + id + `"}` + "\n"
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(line), 0o644))
}

func TestSmoke_SessionPickerOpenViaCtrlR(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	worktree := t.TempDir()
	const sid = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	seedClaudeSession(t, home, worktree, sid, "Recover this conversation")

	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	task := &model.Task{
		ID: "sp-1", Name: "switcher smoke", Status: model.StatusInProgress,
		Project: "p", Worktree: worktree, SessionID: "current-session",
		CreatedAt: time.Now(),
	}
	testutil.NoError(t, d.Add(task))
	app.refreshTasks()

	sim, stop := wireApp(t, app)
	defer stop()

	// Enter agent view without auto-starting a session.
	readUI(t, app.tapp, func() {
		app.agentState.Reset(task.ID, task.Name)
		app.mode = modeAgent
	})

	sim.InjectKey(tcell.KeyCtrlR, 0, 0)

	// The picker opens asynchronously (disk scan in a goroutine, then a
	// QueueUpdateDraw). Poll until it lands or time out.
	var mode viewMode
	var titleSeen string
	for i := 0; i < 40; i++ {
		syncUI(t, app.tapp)
		readUI(t, app.tapp, func() {
			mode = app.mode
			if app.sessionPickerModal != nil && len(app.sessionPickerModal.all) > 0 {
				titleSeen = app.sessionPickerModal.all[0].Title
			}
		})
		if mode == modeSessionPicker && titleSeen != "" {
			break
		}
	}
	testutil.Equal(t, mode, modeSessionPicker)
	testutil.Equal(t, titleSeen, "Recover this conversation")

	// Esc closes it and returns to agent view.
	sim.InjectKey(tcell.KeyEscape, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modeAgent)
}

func TestSessionPicker_SelectInvokesSwitch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	task := &model.Task{
		ID: "sp-2", Name: "select", Status: model.StatusInReview,
		Project: "p", Worktree: t.TempDir(), SessionID: "old-session",
		CreatedAt: time.Now(),
	}
	testutil.NoError(t, d.Add(task))

	app.mode = modeAgent
	app.agentState.Reset(task.ID, task.Name)
	// Dead session (agentPane has none): switchSession restarts directly.

	const newID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	app.openSessionPickerModal([]claudesession.Session{
		{ID: newID, Title: "Newer", Branch: "argus/x"},
	}, "old-session")

	app.handleSessionPickerKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	// switchSession persists the chosen session before (re)starting.
	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.SessionID, newID)
	// Modal closed, back in agent view.
	testutil.Equal(t, app.mode, modeAgent)
	testutil.Nil(t, app.sessionPickerModal)
}

func TestSessionPicker_EscClosesModal(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.mode = modeAgent
	app.agentState.Reset("t", "t")
	app.openSessionPickerModal([]claudesession.Session{{ID: "x", Title: "X"}}, "")

	app.handleSessionPickerKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, app.mode, modeAgent)
	testutil.Nil(t, app.sessionPickerModal)
}

func TestSwitchSession_LiveSessionQueuesRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	task := &model.Task{
		ID: "sw-live", Name: "live", Status: model.StatusInProgress,
		Worktree: t.TempDir(), Backend: "test", SessionID: "old-session",
		CreatedAt: time.Now(),
	}
	testutil.NoError(t, d.Add(task))
	cfg := config.DefaultConfig()
	cfg.Backends["test"] = config.Backend{Command: "sleep 30"}
	sess, err := runner.Start(task, cfg, 24, 80, false)
	testutil.NoError(t, err)
	defer runner.Stop(task.ID) //nolint:errcheck

	app.mode = modeAgent
	app.agentState.Reset(task.ID, task.Name)
	app.agentPane.SetSession(sess)
	if !sess.Alive() {
		t.Fatal("expected a live session")
	}

	app.switchSession("cccccccc-cccc-4ccc-8ccc-cccccccccccc", "New convo")

	// Live path: SessionID persisted and an in-place restart is queued for the
	// exit handler to consume — no direct startSession here.
	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.SessionID, "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	testutil.Equal(t, app.pendingRerenderRestart[task.ID], true)
}

func TestSwitchSession_NoopWhenSame(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	task := &model.Task{
		ID: "sw-noop", Name: "noop", Status: model.StatusInProgress,
		Worktree: t.TempDir(), SessionID: "same-session", CreatedAt: time.Now(),
	}
	testutil.NoError(t, d.Add(task))
	app.mode = modeAgent
	app.agentState.Reset(task.ID, task.Name)

	app.switchSession("same-session", "x")

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.SessionID, "same-session")
	testutil.Equal(t, app.pendingRerenderRestart[task.ID], false)
}

func TestOpenSessionPicker_NonClaudeIsNoop(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	task := &model.Task{
		ID: "sp-codex", Name: "codex", Status: model.StatusInProgress,
		Worktree: t.TempDir(), Backend: "codex", SessionID: "s", CreatedAt: time.Now(),
	}
	testutil.NoError(t, d.Add(task))
	app.mode = modeAgent
	app.agentState.Reset(task.ID, task.Name)

	app.openSessionPicker()

	// Codex sessions aren't switchable here — no modal, stays in agent view.
	testutil.Equal(t, app.mode, modeAgent)
	testutil.Nil(t, app.sessionPickerModal)
}

func TestOpenSessionPicker_EmptyTaskIsNoop(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.mode = modeAgent
	app.agentState.Reset("", "")

	app.openSessionPicker() // must not panic with no current task
	testutil.Equal(t, app.mode, modeAgent)
	testutil.Nil(t, app.sessionPickerModal)
}
