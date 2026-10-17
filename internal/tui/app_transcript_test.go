package tui

import (
	"os"
	"path/filepath"
	"strings"
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
		testutil.Equal(t, app.committedCols[task.ID], uint16(98))
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

type transcriptProbeRunner struct {
	*agent.Runner
	err   error
	calls atomic.Int32
}

func (r *transcriptProbeRunner) ResumableSessionAvailable(*model.Task, config.Config) (bool, error) {
	r.calls.Add(1)
	return r.err == nil, r.err
}

func TestKickUsesRemoteTranscriptProbe(t *testing.T) {
	for _, tc := range []struct {
		name        string
		fails, busy bool
	}{
		{"available", false, false}, {"inspection error", true, false}, {"busy avoids probe", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fails := tc.fails
			t.Setenv("HOME", t.TempDir())
			r := &transcriptProbeRunner{Runner: agent.NewRunner(nil)}
			if fails {
				r.err = os.ErrPermission
			}
			app := New(testDB(t), r, false)
			_, stop := wireApp(t, app)
			defer stop()
			task := &model.Task{ID: "remote", Backend: "claude", Worktree: t.TempDir(), SessionID: "unseeded"}
			sess := &fakeKickSession{idle: !tc.busy, alive: true, initCols: 80}
			var deferred atomic.Bool
			readUI(t, app.tapp, func() { app.maybeKickRerenderAtWidth(task, sess, 98, func() { deferred.Store(true) }) })
			deadline := time.Now().Add(uiTimeout)
			for time.Now().Before(deadline) && !sess.stopCalled.Load() && !deferred.Load() {
				time.Sleep(10 * time.Millisecond)
			}
			wantCalls := int32(1)
			if tc.busy {
				wantCalls = 0
			}
			testutil.Equal(t, r.calls.Load(), wantCalls)
			testutil.Equal(t, sess.stopCalled.Load(), !fails && !tc.busy)
			testutil.Equal(t, deferred.Load(), fails || tc.busy)
			readUI(t, app.tapp, func() { testutil.Equal(t, app.pendingRerenderRestart[task.ID], !fails && !tc.busy) })
		})
	}
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
	app.committedCols[task.ID] = 100
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
	_, unresolved := app.committedCols[task.ID]
	testutil.False(t, unresolved)
}

func TestReviveLiveMissingTranscript(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	bin := filepath.Join(t.TempDir(), "claude")
	testutil.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\nexec sleep 30\n"), 0o700))
	testutil.NoError(t, d.SetBackend("fixture", config.Backend{Command: bin}))
	r := agent.NewRunner(nil)
	t.Cleanup(r.StopAll)
	app := New(d, r, false)
	_, stop := wireApp(t, app)
	defer stop()
	task := &model.Task{Name: "live", Backend: "fixture", Worktree: t.TempDir(), SessionID: "unsaved", Prompt: "original mission", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	first, err := r.Start(task, d.Config(), 24, 80, false)
	testutil.NoError(t, err)
	readUI(t, app.tapp, func() { app.reviveHeraWorker(task, &fakeKickSession{idle: true, alive: true}) })
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		var id string
		readUI(t, app.tapp, func() { id = task.SessionID })
		next := r.Get(task.ID)
		if id != "unsaved" && id != "" && next != nil && next.PID() != first.PID() && strings.Contains(string(next.RecentOutput()), "original mission") {
			got, err := d.Get(task.ID)
			testutil.NoError(t, err)
			testutil.Equal(t, got.SessionID, id)
			testutil.Contains(t, string(next.RecentOutput()), "--session-id\r\n"+id)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("TUI revive did not persist fresh identity")
}
