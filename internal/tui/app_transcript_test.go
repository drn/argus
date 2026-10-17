package tui

import (
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

func TestMaybeKickRerenderAtWidth_TranscriptDeferredThenRetry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app := New(testDB(t), agent.NewRunner(nil), false)
	_, stop := wireApp(t, app)
	defer stop()
	task := &model.Task{ID: "unsaved", SessionID: "11111111-1111-7111-9111-111111111111", Worktree: t.TempDir()}
	sess := &fakeKickSession{idle: true, alive: true, initCols: 80}
	var deferred atomic.Bool
	readUI(t, app.tapp, func() {
		app.maybeKickRerenderAtWidth(task, sess, 98, func() { deferred.Store(true) })
	})
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) && !deferred.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	testutil.True(t, deferred.Load())
	testutil.False(t, sess.stopCalled.Load())
	readUI(t, app.tapp, func() {
		testutil.False(t, app.pendingRerenderRestart[task.ID])
	})
	seedTUITranscript(t, home, task.Worktree, task.SessionID, time.Time{})
	readUI(t, app.tapp, func() {
		app.maybeKickRerenderAtWidth(task, sess, 98, nil)
	})
	deadline = time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) && !sess.stopCalled.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	testutil.True(t, sess.stopCalled.Load())
}

func TestStartSessionMissingTranscript(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	bin := filepath.Join(t.TempDir(), "claude")
	testutil.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700))
	testutil.NoError(t, d.SetBackend("fixture", config.Backend{Command: bin}))
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	const old = "11111111-1111-7111-9111-111111111111"
	task := &model.Task{Name: "unsaved", Backend: "fixture", Worktree: t.TempDir(), SessionID: old, Prompt: "original mission"}
	testutil.NoError(t, d.Add(task))
	app.startSession(task)
	sess := runner.Get(task.ID)
	t.Cleanup(func() {
		runner.StopAll()
		if sess != nil {
			<-sess.Done()
		}
	})
	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.False(t, got.SessionID == old || got.SessionID == "")
	testutil.Equal(t, got.SessionID, task.SessionID)
	testutil.Equal(t, got.Status, model.StatusInProgress)
	testutil.NotNil(t, sess)
	testutil.True(t, sess.Alive())
}
