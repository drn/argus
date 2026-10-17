package tui

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestRerender_RedundantAttachClearsStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := New(testDB(t), agent.NewRunner(nil), false)
	task := &model.Task{ID: "redundant", SessionID: "saved"}
	app.agentPane.SetTaskID(task.ID)
	app.heraPage.AgentPane().SetTaskID(task.ID)
	app.setRerenderPending(task.ID)
	app.lastAttachCols[task.ID] = 100
	app.committedCols[task.ID] = 100
	app.maybeKickRerenderAtWidth(task, &fakeKickSession{alive: true}, 100, nil)
	testutil.Equal(t, app.agentPane.RerenderStatus(), "")
	testutil.Equal(t, app.heraPage.AgentPane().RerenderStatus(), "")
	testutil.Equal(t, app.committedCols[task.ID], uint16(100))
}

func TestRerender_PendingReasons(t *testing.T) {
	for _, reason := range []string{"busy", "prompt", "transcript", "stop failure"} {
		t.Run(reason, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			app := New(testDB(t), agent.NewRunner(nil), false)
			_, stop := wireApp(t, app)
			defer stop()
			task := &model.Task{ID: "pending-reason", SessionID: "saved", Worktree: t.TempDir()}
			if reason != "transcript" {
				seedTUITranscript(t, os.Getenv("HOME"), task.Worktree, task.SessionID, time.Time{})
			}
			if reason == "prompt" {
				path := agent.SessionLogPath(task.ID)
				testutil.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
				testutil.NoError(t, os.WriteFile(path, []byte("Do you want to proceed?\n❯ 1. Yes\n  2. No\n"), 0o644))
			}
			sess := &fakeKickSession{idle: reason != "busy", alive: true, initCols: 80}
			if reason == "stop failure" {
				sess.stopErr = errors.New("boom")
			}
			var deferred atomic.Bool
			readUI(t, app.tapp, func() {
				app.agentPane.SetTaskID(task.ID)
				app.maybeKickRerenderAtWidth(task, sess, 100, func() { deferred.Store(true) })
			})
			waitRerender(t, func() bool { return deferred.Load() })
			readUI(t, app.tapp, func() { testutil.Equal(t, app.agentPane.RerenderStatus(), "Re-render pending") })
		})
	}
}

func TestRerender_ActiveAndStaleClear(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := New(testDB(t), agent.NewRunner(nil), false)
	_, stop := wireApp(t, app)
	defer stop()
	task := &model.Task{ID: "active-state", SessionID: "saved", Worktree: t.TempDir()}
	seedTUITranscript(t, os.Getenv("HOME"), task.Worktree, task.SessionID, time.Time{})
	sess := &fakeKickSession{idle: true, alive: true, initCols: 80}
	readUI(t, app.tapp, func() { app.agentPane.SetTaskID(task.ID); app.maybeKickRerenderAtWidth(task, sess, 100, nil) })
	waitRerender(t, func() bool { return sess.stopCalled.Load() })
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.agentPane.RerenderStatus(), "Re-rendering…")
		app.reapStaleRerenderRestart(task.ID, &fakeKickSession{alive: false})
		testutil.Equal(t, app.agentPane.RerenderStatus(), "")
		testutil.False(t, app.pendingRerenderRestart[task.ID])
	})
}

type failedStartRunner struct{ *agent.Runner }

func (*failedStartRunner) Start(*model.Task, config.Config, uint16, uint16, bool) (agent.SessionHandle, error) {
	return nil, errors.New("start failed")
}

func TestRerender_FailedStartPreservesAnchor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	app := New(d, &failedStartRunner{agent.NewRunner(nil)}, false)
	task := &model.Task{Name: "failed", Worktree: t.TempDir()}
	testutil.NoError(t, d.Add(task))
	app.committedCols[task.ID] = 100
	app.agentPane.SetTaskID(task.ID)
	app.setRerendering(task.ID, true)
	app.startSession(task)
	testutil.Equal(t, app.committedCols[task.ID], uint16(100))
	testutil.Equal(t, app.agentPane.RerenderStatus(), "")
	testutil.Contains(t, app.statusbar.Error(), "start failed")
}

func TestRerender_IntermediateSkipPreservesAnchor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := New(testDB(t), agent.NewRunner(nil), false)
	_, stop := wireApp(t, app)
	defer stop()
	task := &model.Task{ID: "skip-anchor", Name: "anchor", SessionID: "sid-anchor", Worktree: t.TempDir(), AgentPID: 101}
	seedTUITranscript(t, os.Getenv("HOME"), task.Worktree, task.SessionID, time.Time{})
	sess := &fakeKickSession{pid: 202, alive: true, initCols: 80}
	var deferred atomic.Bool
	readUI(t, app.tapp, func() {
		app.agentPane.SetTaskID(task.ID)
		app.heraPage.CoordPane().SetTaskID(task.ID)
		app.heraPage.AgentPane().SetTaskID(task.ID)
		app.maybeKickRerenderAtWidth(task, sess, 100, func() { deferred.Store(true) })
	})
	waitRerender(t, func() bool { return deferred.Load() })
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.committedCols[task.ID], uint16(100))
		testutil.True(t, app.heraPage.CoordPane().Rerendering())
		testutil.True(t, app.heraPage.AgentPane().Rerendering())
		testutil.Equal(t, app.agentPane.RerenderStatus(), "Re-render pending")
		app.maybeKickRerenderAtWidth(task, sess, 92, nil)
	})
	waitRerender(t, func() bool {
		var done bool
		readUI(t, app.tapp, func() { done = !app.heraPage.AgentPane().Rerendering() })
		return done
	})
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.committedCols[task.ID], uint16(100))
		testutil.False(t, app.heraPage.CoordPane().Rerendering())
		sess.idle = true
		app.maybeKickRerenderAtWidth(task, sess, 80, nil)
	})
	waitRerender(t, func() bool { return sess.stopCalled.Load() })
}

func TestRerender_ExitClearsHiddenPaneStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	task := &model.Task{ID: "exit-status", Name: "exit", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	app.heraPage.AgentPane().SetTaskID(task.ID)
	app.setRerendering(task.ID, true)
	app.pendingRerenderRestart[task.ID] = true
	app.handleSessionExitUI(task.ID, true, false)
	testutil.False(t, app.heraPage.AgentPane().Rerendering())
}

func waitRerender(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("rerender outcome did not settle")
}
