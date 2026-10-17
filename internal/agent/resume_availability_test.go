package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestResumableSessionAvailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := t.TempDir()
	const sid = "11111111-1111-7111-9111-111111111111"
	cfg := config.DefaultConfig()
	task := &model.Task{Backend: "claude", Worktree: wt, SessionID: sid}
	t.Run("missing exact ID despite another transcript", func(t *testing.T) {
		seedClaudeTranscript(t, home, wt, "22222222-2222-7222-9222-222222222222", time.Time{})
		ok, err := ResumableSessionAvailable(task, cfg)
		testutil.NoError(t, err)
		testutil.False(t, ok)
	})
	seedClaudeTranscript(t, home, wt, sid, time.Time{})
	t.Run("exact transcript", func(t *testing.T) {
		ok, err := ResumableSessionAvailable(task, cfg)
		testutil.NoError(t, err)
		testutil.True(t, ok)
	})
	t.Run("account isolated", func(t *testing.T) {
		acct := t.TempDir()
		cfg.Accounts = map[string]config.Account{"work": {ClaudeConfigDir: acct}}
		local := *task
		local.Account = "work"
		ok, err := ResumableSessionAvailable(&local, cfg)
		testutil.NoError(t, err)
		testutil.False(t, ok)
		dir := filepath.Join(acct, "projects", claudesession.EncodeProjectDir(wt))
		testutil.NoError(t, os.MkdirAll(dir, 0o755))
		testutil.NoError(t, os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte("{}\n"), 0o600))
		ok, err = ResumableSessionAvailable(&local, cfg)
		testutil.NoError(t, err)
		testutil.True(t, ok)
		local.Account = "gone"
		_, err = ResumableSessionAvailable(&local, cfg)
		testutil.Error(t, err)
	})
	for _, backend := range []string{"codex", "pi", "opencode", "antigravity", "cursor"} {
		t.Run(backend, func(t *testing.T) {
			local := *task
			local.Backend = backend
			local.SessionID = "unseeded"
			ok, err := ResumableSessionAvailable(&local, cfg)
			testutil.NoError(t, err)
			testutil.True(t, ok)
		})
	}
}

func TestTranscriptLookupLocations(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	wt := t.TempDir()
	physical, err := filepath.EvalSymlinks(wt)
	testutil.NoError(t, err)
	link := filepath.Join(t.TempDir(), "linked")
	testutil.NoError(t, os.Symlink(physical, link))
	const sid = "11111111-1111-7111-9111-111111111111"
	seedClaudeTranscript(t, os.Getenv("HOME"), physical, sid, time.Time{})
	task := &model.Task{Backend: "claude", Worktree: link, SessionID: sid}
	cfg := config.DefaultConfig()
	t.Run("physical worktree", func(t *testing.T) {
		ok, err := ResumableSessionAvailable(task, cfg)
		testutil.NoError(t, err)
		testutil.True(t, ok)
	})
	t.Run("backend environment override", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("TRANSCRIPT_TEST_DIR", dir)
		backend := cfg.Backends["claude"]
		backend.EnvVars = map[string]string{"CLAUDE_CONFIG_DIR": "env://TRANSCRIPT_TEST_DIR"}
		cfg.Backends["claude"] = backend
		cmd, cleanup, err := BuildCmd(task, cfg, true)
		testutil.NoError(t, err)
		if cleanup != nil {
			defer cleanup()
		}
		testutil.Equal(t, claudeConfigDirFromEnv(cmd.Env), dir)
		project, err := claudesession.ProjectDirIn(dir, link)
		testutil.NoError(t, err)
		testutil.NoError(t, os.MkdirAll(project, 0o700))
		testutil.NoError(t, os.WriteFile(filepath.Join(project, sid+".jsonl"), nil, 0o600))
		ok, err := ResumableSessionAvailable(task, cfg)
		testutil.NoError(t, err)
		testutil.True(t, ok)
		testutil.NoError(t, os.Remove(filepath.Join(project, sid+".jsonl")))
		ok, err = ResumableSessionAvailable(task, cfg)
		testutil.NoError(t, err)
		testutil.False(t, ok)
		task.Account = "named"
		defer func() { task.Account = "" }()
		cfg.Accounts = map[string]config.Account{"named": {ClaudeConfigDir: t.TempDir()}}
		got, err := ClaudeConfigDirForTask(task, cfg)
		testutil.NoError(t, err)
		testutil.Equal(t, got, dir)
		t.Setenv("TRANSCRIPT_TEST_DIR", "")
		got, err = ClaudeConfigDirForTask(task, cfg)
		testutil.NoError(t, err)
		testutil.Equal(t, got, "")
	})
}

func TestTranscriptInspectionEdges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := t.TempDir()
	task := &model.Task{Backend: "claude", Worktree: wt, SessionID: "saved"}
	cfg := config.DefaultConfig()
	project, err := claudesession.ProjectDirIn("", wt)
	testutil.NoError(t, err)
	testutil.NoError(t, os.MkdirAll(filepath.Join(project, "saved.jsonl"), 0o700))
	t.Run("directory is not transcript", func(t *testing.T) {
		ok, err := ResumableSessionAvailable(task, cfg)
		testutil.NoError(t, err)
		testutil.False(t, ok)
	})
	t.Run("path traversal", func(t *testing.T) {
		local := *task
		local.SessionID = "../x"
		ok, err := ResumableSessionAvailable(&local, cfg)
		testutil.NoError(t, err)
		testutil.False(t, ok)
	})
	t.Run("permission error preserves identity", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root bypasses permissions")
		}
		testutil.NoError(t, os.Chmod(project, 0))
		t.Cleanup(func() {
			//nolint:gosec // G302: this is a directory; restore owner traversal for TempDir cleanup.
			testutil.NoError(t, os.Chmod(project, 0o700))
		})
		_, err := ResumableSessionAvailable(task, cfg)
		testutil.Error(t, err)
		_, err = prepareResume(task, cfg, true)
		testutil.NoError(t, err)
		testutil.Equal(t, task.SessionID, "saved")
	})
}

func TestRunner_ResumeTranscriptRecovery(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "saved"}[saved], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			wt := t.TempDir()
			bin := filepath.Join(t.TempDir(), "claude")
			testutil.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o700))
			cfg := runnerTestConfig()
			cfg.Backends["test"] = config.Backend{Command: bin}
			const old = "11111111-1111-7111-9111-111111111111"
			task := &model.Task{ID: "recover", Worktree: wt, SessionID: old, Prompt: "original mission"}
			if saved {
				seedClaudeTranscript(t, home, wt, old, time.Time{})
			}
			r := NewRunner(nil)
			t.Cleanup(r.StopAll)
			sess, err := r.Start(task, cfg, 24, 80, true)
			testutil.NoError(t, err)
			select {
			case <-sess.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("fixture did not exit")
			}
			out := string(sess.RecentOutput())
			if saved {
				testutil.Equal(t, task.SessionID, old)
				testutil.Contains(t, out, "--resume\r\n"+old)
				testutil.False(t, strings.Contains(out, task.Prompt))
			} else {
				testutil.False(t, task.SessionID == old || task.SessionID == "")
				testutil.Contains(t, out, "--session-id\r\n"+task.SessionID)
				testutil.Contains(t, out, task.Prompt)
				testutil.False(t, strings.Contains(out, "--resume"))
			}
		})
	}
}

func TestRunner_KickWaitsForTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(t.TempDir(), "claude")
	testutil.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700))
	cfg := runnerTestConfig()
	cfg.Backends["test"] = config.Backend{Command: bin}
	task := &model.Task{ID: "wait", Worktree: t.TempDir(), SessionID: "11111111-1111-7111-9111-111111111111"}
	r := NewRunner(nil)
	t.Cleanup(r.StopAll)
	sess, err := r.Start(task, cfg, 24, 80, false)
	testutil.NoError(t, err)
	testutil.ErrorIs(t, r.KickRerender(task, cfg, 24, 98), ErrResumeUnavailable)
	testutil.True(t, sess.Alive())
	testutil.False(t, r.HasPendingRestart(task.ID))
	task.Account = "removed"
	testutil.ErrorIs(t, r.KickRerender(task, cfg, 24, 98), ErrResumeUnavailable)
	testutil.True(t, sess.Alive())
	testutil.False(t, r.HasPendingRestart(task.ID))
	task.Account = ""
	// A live-but-unsaved reattach must not regenerate the pinned identity.
	id := task.SessionID
	attached, healed, err := r.StartOrReattach(task, cfg, 24, 80, true)
	testutil.NoError(t, err)
	testutil.True(t, healed)
	testutil.Equal(t, attached.PID(), sess.PID())
	testutil.Equal(t, task.SessionID, id)
	seedClaudeTranscript(t, home, task.Worktree, id, time.Time{})
	testutil.NoError(t, r.KickRerender(task, cfg, 24, 98))
	// Wait for restart before cleanup, avoiding StopAll racing the pending launch.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if next := r.Get(task.ID); next != nil && next.PID() != sess.PID() {
			testutil.Equal(t, task.SessionID, id)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("saved transcript did not allow restart")
}

func TestPrepareResumeInspectionFailurePreservesID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.DefaultConfig()
	task := &model.Task{Backend: "claude", Account: "removed", SessionID: "saved", Worktree: t.TempDir()}
	resume, err := prepareResume(task, cfg, true)
	testutil.NoError(t, err)
	testutil.True(t, resume)
	testutil.Equal(t, task.SessionID, "saved")
	r := NewRunner(nil)
	t.Cleanup(r.StopAll)
	task.ID = "inspection-error"
	_, err = r.Start(task, cfg, 24, 80, true)
	testutil.Error(t, err)
	testutil.Equal(t, task.SessionID, "saved")
	testutil.False(t, r.HasSession(task.ID))
	testutil.ErrorIs(t, r.KickRerender(task, cfg, 24, 98), ErrSessionNotFound)
	task.Account = ""
	task.Worktree = ""
	_, err = prepareResume(task, cfg, true)
	testutil.NoError(t, err)
	testutil.Equal(t, task.SessionID, "saved")
}
