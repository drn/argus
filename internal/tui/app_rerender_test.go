package tui

import (
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

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
