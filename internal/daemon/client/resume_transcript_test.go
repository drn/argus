package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestResumeIDWire(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, sock, database := testSetup(t)
	bin := filepath.Join(t.TempDir(), "claude")
	testutil.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > launch-args\nprintf '%s\\n' \"$@\"\nexec sleep 30\n"), 0o700))
	testutil.NoError(t, database.SetBackend("fake-claude", config.Backend{Command: bin}))
	c, err := Connect(sock)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	const old = "11111111-1111-7111-9111-111111111111"
	task := &model.Task{ID: "recover", Name: "recover", Status: model.StatusInProgress, Backend: "fake-claude", Worktree: t.TempDir(), SessionID: old, Prompt: "mission"}
	testutil.NoError(t, database.Add(task))
	sess, err := c.Start(task, database.Config(), 24, 80, true)
	testutil.NoError(t, err)
	testutil.True(t, sess.PID() > 0)
	testutil.False(t, task.SessionID == old || task.SessionID == "")
	assertResumeLaunchID(t, sess, task.SessionID)
	testutil.NoError(t, database.Update(task))
	testutil.NoError(t, c.Stop(task.ID))
	waitStatus(t, database, task.ID, model.StatusInReview)
}

func TestSupResumeID(t *testing.T) {
	d, supervisorClient, database := supE2E(t)
	sock := filepath.Join(t.TempDir(), "d.sock")
	go d.Serve(sock) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitFile(t, sock)
	bin := filepath.Join(t.TempDir(), "claude")
	testutil.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > launch-args\nprintf '%s\\n' \"$@\"\nexec sleep 30\n"), 0o700))
	testutil.NoError(t, database.SetBackend("fixture", config.Backend{Command: bin}))
	c, err := Connect(sock)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	const old = "11111111-1111-7111-9111-111111111111"
	task := &model.Task{ID: "recover", Name: "recover", Status: model.StatusInProgress, Backend: "fixture", Worktree: t.TempDir(), SessionID: old}
	testutil.NoError(t, database.Add(task))
	sess, err := c.Start(task, database.Config(), 24, 80, true)
	testutil.NoError(t, err)
	testutil.False(t, task.SessionID == old || task.SessionID == "")
	assertResumeLaunchID(t, sess, task.SessionID)
	// Explicit live revive must carry opt-in fresh recovery through both hops.
	prior := task.SessionID
	revive := daemon.NewHeraReviveRunner(database, c, database.Config)
	testutil.NoError(t, revive.KickRerender(task.ID))
	recovered, err := database.Get(task.ID)
	testutil.NoError(t, err)
	testutil.False(t, recovered.SessionID == "" || recovered.SessionID == prior)
	task.SessionID = recovered.SessionID
	// Prove the replacement launched with the ID returned through both hops.
	deadline := time.Now().Add(5 * time.Second)
	var launch []byte
	for time.Now().Before(deadline) {
		launch, err = os.ReadFile(filepath.Join(task.Worktree, "launch-args"))
		if err == nil && strings.Contains(string(launch), "--session-id\n"+task.SessionID) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	testutil.NoError(t, err)
	testutil.Contains(t, string(launch), "--session-id\n"+task.SessionID)
	// The old incarnation can remove its exit watcher during a kick. Reacquire
	// the replacement and observe fresh PTY output before stopping it, so the
	// daemon is subscribed to its exit. Use a unique echoed input marker: an
	// existing client's output ring can still contain the old launch arguments.
	testutil.NotNil(t, supervisorClient.Get(task.ID))
	marker := "replacement-ready-" + task.SessionID
	var written daemon.StatusResp
	testutil.NoError(t, supervisorClient.call("Daemon.WriteInput", &daemon.WriteReq{TaskID: task.ID, Data: []byte(marker + "\n")}, &written))
	testutil.Equal(t, written.Error, "")
	deadline = time.Now().Add(5 * time.Second)
	var output []byte
	for time.Now().Before(deadline) {
		if replacement := supervisorClient.Get(task.ID); replacement != nil {
			output = replacement.RecentOutput()
		}
		if strings.Contains(string(output), marker) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	testutil.Contains(t, string(output), marker)
	testutil.NoError(t, database.Update(task))
	testutil.NoError(t, c.Stop(task.ID))
	waitStatus(t, database, task.ID, model.StatusInReview)
}

func assertResumeLaunchID(t *testing.T, sess agent.SessionHandle, id string) {
	t.Helper()
	want := "--session-id\r\n" + id
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(string(sess.RecentOutput()), want) {
		time.Sleep(10 * time.Millisecond)
	}
	testutil.Contains(t, string(sess.RecentOutput()), want)
}
