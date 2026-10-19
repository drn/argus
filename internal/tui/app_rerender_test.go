package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/uxlog"
)

type blockedPIDProbe struct {
	*agent.Runner
	entered, release chan struct{}
}

func (r *blockedPIDProbe) ResumableSessionAvailable(*model.Task, config.Config) (bool, error) {
	close(r.entered)
	<-r.release
	return true, nil
}

func TestRerender_OldPIDProbeCannotAffectNewBind(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	logPath := filepath.Join(t.TempDir(), "ux.log")
	uxlog.Close()
	testutil.NoError(t, uxlog.Init(logPath))
	defer uxlog.Close()
	r := &blockedPIDProbe{Runner: agent.NewRunner(nil), entered: make(chan struct{}), release: make(chan struct{})}
	app := New(testDB(t), r, false)
	_, stop := wireApp(t, app)
	defer stop()
	var once sync.Once
	unblock := func() { once.Do(func() { close(r.release) }) }
	defer unblock()
	task := &model.Task{ID: "superseded-pid", SessionID: "saved"}
	old := &fakeKickSession{pid: 101, alive: true, idle: true, initCols: 80}
	readUI(t, app.tapp, func() { app.agentPane.SetTaskID(task.ID); app.maybeKickRerenderAtWidth(task, old, 100, nil) })
	select {
	case <-r.entered:
	case <-time.After(uiTimeout):
		t.Fatal("old probe did not start")
	}
	readUI(t, app.tapp, func() {
		app.maybeKickRerenderAtWidth(task, &fakeKickSession{pid: 202, alive: true, idle: true, initCols: 100}, 100, nil)
	})
	unblock()
	waitRerender(t, func() bool {
		data, _ := os.ReadFile(logPath)
		return old.stopCalled.Load() || strings.Contains(string(data), "stale decision skipped task="+task.ID)
	})
	readUI(t, app.tapp, func() {
		testutil.False(t, old.stopCalled.Load())
		_, anchored := app.committedCols[task.ID]
		testutil.False(t, anchored)
		testutil.False(t, app.pendingRerenderRestart[task.ID])
		testutil.Equal(t, app.committedColsPID[task.ID], 202)
	})
}

func TestRerender_ExternalRestartDropsPreviousPIDAnchor(t *testing.T) {
	for _, tc := range []struct {
		name string
		init int
		kick bool
	}{
		{"matching new spawn", 80, false},
		{"new spawn still differs", 120, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			app := New(testDB(t), agent.NewRunner(nil), false)
			_, stop := wireApp(t, app)
			defer stop()
			task := &model.Task{ID: "external-restart", SessionID: "saved", AgentPID: 101, Worktree: t.TempDir()}
			seedTUITranscript(t, os.Getenv("HOME"), task.Worktree, task.SessionID, time.Time{})
			old := &fakeKickSession{pid: 101, alive: true, initCols: 80}
			var deferred atomic.Bool
			readUI(t, app.tapp, func() {
				app.agentPane.SetTaskID(task.ID)
				app.maybeKickRerenderAtWidth(task, old, 100, func() { deferred.Store(true) })
			})
			waitRerender(t, func() bool { return deferred.Load() })
			next := &fakeKickSession{pid: 202, idle: true, alive: true, initCols: tc.init}
			readUI(t, app.tapp, func() {
				testutil.Equal(t, app.committedCols[task.ID], uint16(100))
				app.lastAttachCols[task.ID] = 80           // another viewer cached the old process
				app.pendingRerenderRestart[task.ID] = true // lost old-process exit notification
				app.maybeKickRerenderAtWidth(task, next, 80, nil)
			})
			waitRerender(t, func() bool {
				if tc.kick {
					return next.stopCalled.Load()
				}
				var settled bool
				readUI(t, app.tapp, func() { settled = next.stopCalled.Load() || app.agentPane.RerenderStatus() == "" })
				return settled
			})
			readUI(t, app.tapp, func() {
				_, anchored := app.committedCols[task.ID]
				testutil.False(t, anchored)
				testutil.Equal(t, next.stopCalled.Load(), tc.kick)
				testutil.Equal(t, task.AgentPID, 101) // live PID, not this stale row, owns the anchor
			})
		})
	}
}

func TestRerender_RedundantAttachClearsStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := New(testDB(t), agent.NewRunner(nil), false)
	task := &model.Task{ID: "redundant", SessionID: "saved"}
	app.agentPane.SetTaskID(task.ID)
	app.heraPage.AgentPane().SetTaskID(task.ID)
	app.setRerenderPending(task.ID)
	app.lastAttachCols[task.ID] = 100
	app.committedCols[task.ID] = 100
	app.committedColsPID[task.ID] = 0
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
	app.committedColsPID[task.ID] = 101
	app.agentPane.SetTaskID(task.ID)
	app.setRerendering(task.ID, true)
	app.startSession(task)
	testutil.Equal(t, app.committedCols[task.ID], uint16(100))
	testutil.Equal(t, app.committedColsPID[task.ID], 101)
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

// A zoom toggle re-arms a Hera pane's evaluation while an earlier kick's
// restart is still in flight; the in-flight early return must hand the pane its
// retry callback instead of silently dropping the new width.
func TestRerender_InFlightKickInvokesOnDeferred(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := New(testDB(t), agent.NewRunner(nil), false)
	task := &model.Task{ID: "inflight", Name: "inflight", SessionID: "sid-inflight", AgentPID: 101}
	sess := &fakeKickSession{pid: 101, alive: true, initCols: 88}
	app.committedColsPID[task.ID] = 101
	app.pendingRerenderRestart[task.ID] = true
	called := 0
	app.maybeKickRerenderAtWidth(task, sess, 178, func() { called++ })
	testutil.Equal(t, called, 1)
	app.maybeKickRerenderAtWidth(task, sess, 178, nil) // nil callback is fine
}
