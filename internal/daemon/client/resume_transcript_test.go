package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestResumeIDWire(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, sock, database := testSetup(t)
	bin := filepath.Join(t.TempDir(), "claude")
	testutil.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700))
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
	testutil.NoError(t, database.Update(task))
	testutil.NoError(t, c.Stop(task.ID))
	waitStatus(t, database, task.ID, model.StatusInReview)
}

func TestSupResumeID(t *testing.T) {
	d, _, database := supE2E(t)
	sock := filepath.Join(t.TempDir(), "d.sock")
	go d.Serve(sock) //nolint:errcheck
	t.Cleanup(func() { d.Shutdown() })
	waitFile(t, sock)
	bin := filepath.Join(t.TempDir(), "claude")
	testutil.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700))
	testutil.NoError(t, database.SetBackend("fixture", config.Backend{Command: bin}))
	c, err := Connect(sock)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	const old = "11111111-1111-7111-9111-111111111111"
	task := &model.Task{ID: "recover", Name: "recover", Status: model.StatusInProgress, Backend: "fixture", Worktree: t.TempDir(), SessionID: old}
	testutil.NoError(t, database.Add(task))
	_, err = c.Start(task, database.Config(), 24, 80, true)
	testutil.NoError(t, err)
	testutil.False(t, task.SessionID == old || task.SessionID == "")
	testutil.NoError(t, database.Update(task))
	testutil.NoError(t, c.Stop(task.ID))
	waitStatus(t, database, task.ID, model.StatusInReview)
}
